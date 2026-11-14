//go:build repository_integration

package sso

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/crewjam/saml"
	"github.com/jackc/pgx/v5"
)

var identityScopeRefused = errors.New("SAML_IDENTITY_SCOPE_REFUSED")
var identityStorageFailed = errors.New("SAML_IDENTITY_STORAGE_FAILED")

// This experiment borrows the caller's transaction. It neither creates local
// authority nor commits independently of the eventual binding/session owner.
type identityStore struct{ tenant string }
type samlIdentityExecutor interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
type samlIdentityQuery struct{ transaction.Executor }

func (q samlIdentityQuery) QueryRow(ctx context.Context, statement string, args ...any) pgx.Row {
	return q.Executor.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, statement, args...))
}

type storedIdentity struct {
	ID, ContextID, Connector string
	Revision                 int64
	Subject                  subject
	Created                  bool
}

func opaqueNameID(value string) bool {
	return value != "" && len(value) <= 1024 && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 256 && !strings.ContainsRune(value, 0)
}
func identityQueryError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return identityScopeRefused
	}
	return identityStorageFailed
}

type identityAuthority struct {
	trustUntil                                       time.Time
	maximumSessionSeconds                            int
	tenant, connector, contextID, issuer, sp, format string
	revision                                         int64
	digest                                           [32]byte
}
type identityProof struct {
	proof
	source identityAuthority
}
type identityVerifier struct {
	authority identityAuthority
	verifier  *verifier
}

func (s identityStore) loadSource(ctx context.Context, tx samlIdentityExecutor, connector string) (identityAuthority, error) {
	var a identityAuthority
	var digest []byte
	if tx == nil || s.tenant == "" || connector == "" {
		return a, identityScopeRefused
	}
	err := tx.QueryRow(ctx, `SELECT id::text,issuer,sp_entity,name_id_format,revision,metadata_digest,trust_until,maximum_session_seconds FROM sso_saml_profiles WHERE tenant_id=$1 AND connector_id=$2 AND enabled AND trust_until>clock_timestamp() FOR SHARE`, s.tenant, connector).Scan(&a.contextID, &a.issuer, &a.sp, &a.format, &a.revision, &digest, &a.trustUntil, &a.maximumSessionSeconds)
	if err != nil {
		return identityAuthority{}, identityQueryError(err)
	}
	if len(digest) != 32 {
		return identityAuthority{}, identityStorageFailed
	}
	a.trustUntil = a.trustUntil.UTC()
	copy(a.digest[:], digest)
	a.tenant = s.tenant
	a.connector = connector
	return a, nil
}
func newIdentityVerifier(a identityAuthority, c configuration) (identityVerifier, error) {
	if a.tenant == "" || a.connector == "" || a.contextID == "" || a.revision < 1 || a.issuer != c.Issuer || a.sp != c.SP || a.format != string(saml.PersistentNameIDFormat) || a.digest != sha256.Sum256(c.Metadata) || !a.trustUntil.Equal(c.TrustedUntil) {
		return identityVerifier{}, identityScopeRefused
	}
	v, err := newVerifier(c)
	if err != nil {
		return identityVerifier{}, err
	}
	return identityVerifier{authority: a, verifier: v}, nil
}
func (v identityVerifier) verify(encoded, request string) (identityProof, error) {
	if v.verifier == nil {
		return identityProof{}, identityScopeRefused
	}
	p, err := v.verifier.verify(encoded, request)
	if err != nil {
		return identityProof{}, err
	}
	return identityProof{proof: p, source: v.authority}, nil
}

func (s identityStore) resolve(ctx context.Context, tx samlIdentityExecutor, connector string, verified identityProof) (storedIdentity, error) {
	var result storedIdentity
	value := verified.Subject
	if tx == nil || ctx.Err() != nil || s.tenant == "" || connector == "" || !xmlID.MatchString(verified.AssertionID) || verified.AuthnAt.IsZero() || verified.ExpiresAt.IsZero() || !opaqueNameID(value.Value) || !entityIdentifier(value.Issuer) || !entityIdentifier(value.SPNameQualifier) || value.Format != string(saml.PersistentNameIDFormat) {
		return result, identityScopeRefused
	}
	current, err := s.loadSource(ctx, tx, connector)
	if err != nil {
		return storedIdentity{}, err
	}
	if verified.source != current || value.Issuer != current.issuer || value.NameQualifier != current.issuer || value.SPNameQualifier != current.sp || value.Format != current.format {
		return storedIdentity{}, identityScopeRefused
	}
	var live bool
	if tx.QueryRow(ctx, `SELECT clock_timestamp()<$1`, verified.ExpiresAt).Scan(&live) != nil {
		return storedIdentity{}, identityStorageFailed
	}
	if !live {
		return storedIdentity{}, identityScopeRefused
	}
	result.ContextID = current.contextID
	result.Revision = current.revision
	result.Connector = connector
	result.Subject = value
	err = tx.QueryRow(ctx, `INSERT INTO sso_saml_identities(tenant_id,context_id,name_id_value) VALUES($1,$2,$3::text) ON CONFLICT(tenant_id,context_id,name_id_value) DO NOTHING RETURNING id::text,name_id_value`, s.tenant, result.ContextID, value.Value).Scan(&result.ID, &result.Subject.Value)
	if err == nil {
		result.Created = true
		return result, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return storedIdentity{}, identityStorageFailed
	}
	err = tx.QueryRow(ctx, `SELECT id::text,name_id_value FROM sso_saml_identities WHERE tenant_id=$1 AND context_id=$2 AND name_id_value=$3::text FOR KEY SHARE`, s.tenant, result.ContextID, value.Value).Scan(&result.ID, &result.Subject.Value)
	if err != nil {
		return storedIdentity{}, identityQueryError(err)
	}
	return result, nil
}
