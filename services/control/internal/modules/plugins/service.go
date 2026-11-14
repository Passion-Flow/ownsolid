package plugins

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"database/sql"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func failure(code string) error                { return errors.New(code) }
func (s *Service) policy() config.PluginPolicy { return s.Identity.Config.Policy().Plugins }
func admin(a identity.Auth) bool               { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }
func validReason(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= 500 && !strings.ContainsFunc(value, unicode.IsControl)
}
func validID(value string) bool {
	id, e := uuid.Parse(value)
	return e == nil && id != uuid.Nil && id.String() == value
}

const versionFields = `id::text,tenant_id::text,plugin_id,release_version,abi,executor,capsule_sha256,manifest_sha256,key_id,state,revision,imported_by::text,created_at,installed_at,uninstalled_at,capsule,manifest`

func scanVersion(row transaction.Row) (versionRecord, error) {
	var v versionRecord
	e := row.Scan(&v.ID, &v.tenant, &v.PluginID, &v.ReleaseVersion, &v.ABI, &v.Executor, &v.CapsuleSHA256, &v.ManifestSHA256, &v.KeyID, &v.State, &v.Revision, &v.ImportedBy, &v.CreatedAt, &v.InstalledAt, &v.UninstalledAt, &v.capsule, &v.manifest)
	if errors.Is(e, sql.ErrNoRows) {
		return v, failure("PLUGIN_VERSION_NOT_FOUND")
	}
	if e != nil {
		return versionRecord{}, pluginError(e)
	}
	v.CreatedAt = v.CreatedAt.UTC()
	if v.InstalledAt != nil {
		t := v.InstalledAt.UTC()
		v.InstalledAt = &t
	}
	if v.UninstalledAt != nil {
		t := v.UninstalledAt.UTC()
		v.UninstalledAt = &t
	}
	return v, nil
}
func (s *Service) verified(v versionRecord) (wire.Verified, error) {
	keys, e := s.Identity.Config.Plugins.PublicKeys()
	if e != nil {
		return wire.Verified{}, failure("PLUGIN_TRUST_KEY_INVALID")
	}
	verified, e := wire.Verify(v.capsule, v.CapsuleSHA256, keys, s.policy().PackageLimits())
	if e != nil {
		return verified, e
	}
	var manifest wire.Manifest
	if strict.DecodeStrict(v.manifest, &manifest) != nil {
		return verified, failure("PLUGIN_PACKAGE_INVALID")
	}
	canonical, _ := json.Marshal(manifest)
	if wire.Digest(canonical) != verified.ManifestSHA256 || v.ManifestSHA256 != verified.ManifestSHA256 || v.PluginID != verified.Manifest.PluginID || v.ReleaseVersion != verified.Manifest.Version || v.ABI != verified.Manifest.ABI || v.Executor != verified.Manifest.Executor || v.KeyID != verified.KeyID {
		return verified, failure("PLUGIN_PACKAGE_INVALID")
	}
	return verified, nil
}
func (s *Service) present(v versionRecord) Version {
	out := v.Version
	out.Capabilities = []string{}
	verified, e := s.verified(v)
	if e != nil {
		code := "PLUGIN_PACKAGE_INVALID"
		if e.Error() == "PLUGIN_TRUST_KEY_INVALID" {
			code = e.Error()
		}
		out.UnavailableCode = &code
		return out
	}
	out.Trusted = true
	out.Capabilities = append([]string(nil), verified.Manifest.Capabilities...)
	out.License = verified.Manifest.License
	out.Available = s.available() && out.State == InstalledState
	if !out.Available {
		code := "PLUGIN_NOT_INSTALLED"
		if !s.available() {
			code = "PLUGIN_UNAVAILABLE"
		}
		out.UnavailableCode = &code
	}
	return out
}
func (s *Service) audit(ctx context.Context, tx transaction.Executor, a identity.Auth, tenant, workspace, action, target, id, reason, request string) error {
	e := pluginExecOne(ctx, tx, pluginSQL(tx, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,NULLIF($4,'')::uuid,$5,$6,'success',$7,$8,$9,$10)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,NULLIF(?,''),?,?,'success',?,?,?,?)`, nativePluginAudit, uuid.NewString(), a.Me.User.ID, tenant, workspace, a.Me.Audience, action, target, id, reason, request))
	return e
}
func (s *Service) intent(ctx context.Context, tx transaction.Executor, a identity.Auth, tenant, purpose, key string, input any) (string, []byte, error) {
	if !validID(key) {
		return "", nil, failure("VALIDATION_FAILED")
	}
	raw, e := json.Marshal(struct {
		Actor, Audience, Purpose string
		Input                    any
	}{a.Me.User.ID, a.Me.Audience, purpose, input})
	if e != nil {
		return "", nil, failure("VALIDATION_FAILED")
	}
	digest := security.MAC(s.Identity.HMACKey, "plugin-mutation", "v1", string(raw))
	var entity, previousPurpose string
	var prior []byte
	e = tx.QueryRow(ctx, pluginSQL(tx, `SELECT entity_id::text,purpose,request_hash FROM plugin_mutation_intents WHERE tenant_id=$1 AND actor_id=$2 AND idempotency_key=$3 FOR UPDATE`, `SELECT entity_id,purpose,request_hash FROM plugin_mutation_intents WHERE tenant_id=? AND actor_id=? AND idempotency_key=? FOR UPDATE`, nativePluginIntentRead, tenant, a.Me.User.ID, key)).Scan(&entity, &previousPurpose, &prior)
	if errors.Is(e, sql.ErrNoRows) {
		return "", digest, nil
	}
	if e != nil {
		return "", nil, e
	}
	if previousPurpose != purpose || subtle.ConstantTimeCompare(prior, digest) != 1 {
		return "", nil, failure("IDEMPOTENCY_CONFLICT")
	}
	return entity, digest, nil
}
func saveIntent(ctx context.Context, tx transaction.Executor, a identity.Auth, tenant, purpose, key, entity string, digest []byte) error {
	e := pluginExecOne(ctx, tx, pluginSQL(tx, `INSERT INTO plugin_mutation_intents(tenant_id,actor_id,idempotency_key,purpose,request_hash,entity_id) VALUES($1,$2,$3,$4,$5,$6)`, `INSERT INTO plugin_mutation_intents(tenant_id,actor_id,idempotency_key,purpose,request_hash,entity_id) VALUES(?,?,?,?,?,?)`, nativePluginIntentCreate, tenant, a.Me.User.ID, key, purpose, digest, entity))
	return e
}
