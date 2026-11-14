package postgresbackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// MVPSourceBinding distinguishes the original native database and runtime SQL
// principal from independent fresh-target/operator/configuration origins.
// Only a hash of the already salted SCRAM verifier is retained, never a secret.
// External artifact bytes remain owned by their actual provider/registry; the
// full SQL archive preserves their identifiers, claims, receipts and uncertainty.
type MVPSourceBinding struct {
	ApplicationProfile      string                         `json:"application_profile"`
	SchemaVersion           int                            `json:"schema_version"`
	SystemIdentifier        string                         `json:"system_identifier"`
	DatabaseOID             uint32                         `json:"database_oid"`
	BackendPID              int                            `json:"backend_pid"`
	RuntimePrincipal        string                         `json:"runtime_principal"`
	PrincipalOID            uint32                         `json:"principal_oid"`
	PrincipalVerifierSHA256 string                         `json:"principal_verifier_sha256"`
	ExternalArtifacts       []MVPExternalArtifactOwnership `json:"external_artifacts"`
	RegistryConfigured      bool                           `json:"registry_configured"`
	Registry                *MVPRegistryPayload            `json:"registry,omitempty"`
	SourceMetadata          *MVPSourceMetadata             `json:"source_metadata,omitempty"`
}

// These are actual facts from the same original exported-snapshot transaction,
// never values inferred from a client binary, image or requested policy.
type MVPSourceMetadata struct {
	ServerVersionNumber int    `json:"server_version_number"`
	VectorVersion       string `json:"vector_version"`
}

func (m MVPSourceMetadata) Valid() bool {
	return m.ServerVersionNumber == 180006 && m.VectorVersion == "0.8.7"
}

func (b *MVPSourceBinding) UnmarshalJSON(raw []byte) error {
	type plain MVPSourceBinding
	var value plain
	var fields map[string]json.RawMessage
	if b == nil || datawire.DecodeStrict(raw, &value) != nil || json.Unmarshal(raw, &fields) != nil {
		return errors.New("POSTGRES_BACKUP_MVP_SOURCE_BINDING_INVALID")
	}
	if v, present := fields["source_metadata"]; present && strings.TrimSpace(string(v)) == "null" {
		return errors.New("POSTGRES_BACKUP_MVP_SOURCE_METADATA_INVALID")
	}
	*b = MVPSourceBinding(value)
	return nil
}

type MVPExternalArtifactOwnership struct {
	Owner            string `json:"owner"`
	Records          int64  `json:"records"`
	ContentSHA256    string `json:"content_sha256"`
	PayloadAuthority string `json:"payload_authority"`
}

func validMVPSourceBinding(m Manifest) error {
	bad := errors.New("POSTGRES_BACKUP_MVP_SOURCE_BINDING_INVALID")
	if m.Request.EffectiveApplicationProfile() != database.LineageName {
		if m.MVPSource != nil {
			return bad
		}
		return nil
	}
	b := m.MVPSource
	if b == nil || b.ApplicationProfile != database.LineageName || b.SchemaVersion != database.SchemaVersion || b.SystemIdentifier == "" || b.DatabaseOID == 0 || b.BackendPID != m.Database.BackendPID || b.BackendPID < 1 || b.PrincipalOID == 0 || b.RuntimePrincipal == m.Request.Operator || b.RuntimePrincipal == "" || !release.ValidSHA256(b.PrincipalVerifierSHA256) || len(b.ExternalArtifacts) != 2 {
		return bad
	}
	if b.SourceMetadata != nil && !b.SourceMetadata.Valid() {
		return errors.New("POSTGRES_BACKUP_MVP_SOURCE_METADATA_INVALID")
	}
	sourceID, e := strconv.ParseUint(b.SystemIdentifier, 10, 64)
	if e != nil || sourceID == 0 || strconv.FormatUint(sourceID, 10) != b.SystemIdentifier {
		return bad
	}
	principalRequest := m.Request
	principalRequest.Operator = b.RuntimePrincipal
	if !principalRequest.Valid() {
		return bad
	}
	for i, kind := range []string{"artifactstores", "imageregistry"} {
		v := b.ExternalArtifacts[i]
		if v.Owner != kind || v.Records < 0 || !release.ValidSHA256(v.ContentSHA256) || v.PayloadAuthority != "original-owner-external-payload-and-reconciliation-required" {
			return bad
		}
	}
	if (b.ExternalArtifacts[1].Records != 0 || b.Registry != nil) && !b.RegistryConfigured {
		return errors.New("POSTGRES_BACKUP_MVP_REGISTRY_PAYLOAD_OWNER_REQUIRED")
	}
	return nil
}

func readMVPSourceBinding(ctx context.Context, tx pgx.Tx, r release.PostgreSQLBackupRequest) (*MVPSourceBinding, error) {
	if r.EffectiveApplicationProfile() != database.LineageName {
		return nil, nil
	}
	bad := errors.New("POSTGRES_BACKUP_MVP_PRINCIPAL_BINDING_INVALID")
	runtime, e := config.LoadProtectedWithEnvironment(r.RuntimeConfigurationFile, r.RuntimeEnvironmentFile)
	if e != nil || r.EffectiveApplicationProfile() != database.LineageName {
		return nil, bad
	}
	raw, e := readPrivateBytes(runtime.Database.DSNFile, 16<<10)
	if e != nil {
		return nil, bad
	}
	pc, e := pgx.ParseConfig(strings.TrimSpace(string(raw)))
	if e != nil || pc.User == r.Operator || pc.Database != r.Database {
		return nil, bad
	}
	b := &MVPSourceBinding{ApplicationProfile: r.EffectiveApplicationProfile(), SchemaVersion: database.SchemaVersion, RuntimePrincipal: pc.User, RegistryConfigured: runtime.ImageRegistry != nil}
	var metadata MVPSourceMetadata
	var valid bool
	if tx.QueryRow(ctx, `SELECT (SELECT system_identifier::text FROM pg_catalog.pg_control_system()),d.oid,pg_catalog.pg_backend_pid(),a.oid,pg_catalog.encode(pg_catalog.sha256(pg_catalog.convert_to(a.rolpassword,'UTF8')),'hex'),a.rolcanlogin AND NOT a.rolsuper AND NOT a.rolcreatedb AND NOT a.rolcreaterole AND NOT a.rolinherit AND NOT a.rolreplication AND NOT a.rolbypassrls AND a.rolpassword LIKE 'SCRAM-SHA-256$%',pg_catalog.current_setting('server_version_num')::integer,(SELECT e.extversion FROM pg_catalog.pg_extension e WHERE e.extname='vector') FROM pg_catalog.pg_database d CROSS JOIN pg_catalog.pg_authid a WHERE d.datname=pg_catalog.current_database() AND a.rolname=$1`, pc.User).Scan(&b.SystemIdentifier, &b.DatabaseOID, &b.BackendPID, &b.PrincipalOID, &b.PrincipalVerifierSHA256, &valid, &metadata.ServerVersionNumber, &metadata.VectorVersion) != nil || !valid || !metadata.Valid() {
		return nil, bad
	}
	b.SourceMetadata = &metadata
	for _, v := range []struct{ owner, table string }{{"artifactstores", "artifact_objects"}, {"imageregistry", "image_registry_pins"}} {
		out, e := readMVPExternalOwnership(ctx, tx, v.owner, v.table, r.Policy.MaximumSQLBytes)
		if e != nil {
			return nil, e
		}
		b.ExternalArtifacts = append(b.ExternalArtifacts, out)
	}

	return b, nil
}

func recheckMVPSourceBinding(ctx context.Context, lease *pgxpool.Conn, r release.PostgreSQLBackupRequest, expected *MVPSourceBinding) (result error) {
	if r.EffectiveApplicationProfile() != database.LineageName {
		return nil
	}
	tx, e := lease.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return errors.New("POSTGRES_BACKUP_MVP_BINDING_READ_UNAVAILABLE")
	}
	defer func() {
		result = errors.Join(result, closeRestoreTransaction(ctx, tx, r.Policy.TerminationGraceSeconds))
	}()
	current, e := readMVPSourceBinding(ctx, tx, r)
	if e == nil && current != nil && expected != nil {
		current.Registry = expected.Registry
	}
	if e != nil || !reflect.DeepEqual(current, expected) {
		return errors.New("POSTGRES_BACKUP_MVP_SOURCE_BINDING_CHANGED")
	}
	return nil
}

func distinctMVPSourceTarget(m Manifest, t RestoreTarget) error {
	if m.Request.EffectiveApplicationProfile() != database.LineageName {
		return nil
	}
	if e := validMVPSourceBinding(m); e != nil {
		return e
	}
	if m.MVPSource.SystemIdentifier == t.SystemIdentifier && m.MVPSource.DatabaseOID == t.DatabaseOID {
		return errors.New("POSTGRES_RESTORE_MVP_SOURCE_TARGET_ALIAS")
	}
	return nil
}

func readMVPExternalOwnership(ctx context.Context, tx pgx.Tx, owner, table string, maximum int64) (MVPExternalArtifactOwnership, error) {
	out := MVPExternalArtifactOwnership{Owner: owner, PayloadAuthority: "original-owner-external-payload-and-reconciliation-required"}
	bad := errors.New("POSTGRES_BACKUP_EXTERNAL_ARTIFACT_OWNERSHIP_UNAVAILABLE")
	rows, e := tx.Query(ctx, `SELECT measured.row_sha FROM (SELECT pg_catalog.encode(pg_catalog.sha256(pg_catalog.convert_to(pg_catalog.to_jsonb(t)::text,'UTF8')),'hex') AS row_sha FROM `+pgx.Identifier{"public", table}.Sanitize()+` t) measured ORDER BY measured.row_sha COLLATE "C"`)
	if e != nil {
		return out, bad
	}
	defer rows.Close()
	h := sha256.New()
	var bytes int64
	for rows.Next() {
		var sha string
		if rows.Scan(&sha) != nil || !release.ValidSHA256(sha) || maximum-bytes < 65 {
			return out, bad
		}
		bytes += 65
		h.Write([]byte(sha + "\n"))
		out.Records++
	}
	if rows.Err() != nil {
		return out, bad
	}
	out.ContentSHA256 = hex.EncodeToString(h.Sum(nil))
	return out, nil
}
