package identity

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

const (
	catalogOIDCCodeSeconds  = 90
	catalogOIDCTokenSeconds = 300
)

// CatalogOIDCCandidateApproval is the Portal's explicit approval of one client
// request. Tenant and subject come exclusively from the live Portal session.
type CatalogOIDCCandidateApproval struct {
	ClientID        string `json:"client_id"`
	RedirectURI     string `json:"redirect_uri"`
	State           string `json:"state"`
	Nonce           string `json:"nonce"`
	Challenge       string `json:"code_challenge"`
	ChallengeMethod string `json:"code_challenge_method"`
}

// CatalogOIDCCandidateExchange carries the back-channel client proof and code.
type CatalogOIDCCandidateExchange struct {
	ClientID, ClientSecret, RedirectURI, Code, Verifier string
}

// CatalogOIDCCandidateClaims is the committed authority a token response is
// prepared from. The access token handle is scoped to this catalog client.
type CatalogOIDCCandidateClaims struct {
	Subject, ClientID, Nonce, TokenID string
	AuthTime, ExpiresAt               time.Time
}

func catalogOIDCRedirect(raw string) (*url.URL, bool) {
	if len(raw) > 2048 || strings.ContainsAny(raw, "#\\") {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.ForceQuery {
		return nil, false
	}
	query, err := url.ParseQuery(u.RawQuery)
	return u, err == nil && !query.Has("code") && !query.Has("state") && !query.Has("error") && !query.Has("iss")
}

func catalogOIDCVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}

func catalogOIDCScope(ctx context.Context, tx transaction.Executor, user string) (string, error) {
	var tenant string
	const query = `SELECT i.tenant_id::text FROM installation_state i
		JOIN tenants t ON t.id=i.tenant_id
		JOIN workspaces w ON w.tenant_id=t.id
		JOIN workspace_memberships m ON m.tenant_id=t.id AND m.workspace_id=w.id
		WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' AND w.status='active' AND m.user_id=$1
		ORDER BY (w.id=i.default_workspace_id) DESC,w.created_at,w.id LIMIT 1 FOR SHARE OF i,t,w,m`
	err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, query, user)).Scan(&tenant)
	return tenant, err
}

// ApproveCatalogOIDCCandidate creates a short-lived code and returns the
// registered callback only after the approval and its audit commit.
func (s *Service) ApproveCatalogOIDCCandidate(ctx context.Context, a Auth, input CatalogOIDCCandidateApproval, requestID string) (string, error) {
	if a.Me.Audience != "portal" || !sessionActor(a) {
		return "", failure("ACCESS_DENIED")
	}
	u, ok := catalogOIDCRedirect(input.RedirectURI)
	if !ok || !sessionID(input.ClientID) || !security.ValidToken(input.State) || !security.ValidToken(input.Nonce) ||
		!security.ValidToken(input.Challenge) || input.ChallengeMethod != "S256" {
		return "", failure("VALIDATION_FAILED")
	}
	store, err := s.Repository()
	if err != nil {
		return "", err
	}
	if store.Dialect() != transaction.PostgreSQL {
		return "", failure("STORAGE_UNAVAILABLE")
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	raw := security.Token()
	err = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		if err := s.RecheckWithin(ctx, tx, a); err != nil {
			return err
		}
		tenant, err := catalogOIDCScope(ctx, tx, a.Me.User.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return failure("ACCESS_DENIED")
		}
		if err != nil {
			return err
		}
		var version int64
		err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT version FROM catalog_oidc_candidate_clients
			WHERE id=$1 AND tenant_id=$2 AND redirect_uri=$3 AND enabled FOR SHARE`, input.ClientID, tenant, input.RedirectURI)).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			return failure("ACCESS_DENIED")
		}
		if err != nil {
			return err
		}
		n, err := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO catalog_oidc_candidate_codes
			(code_hash,tenant_id,client_id,client_version,user_id,session_id,auth_epoch,redirect_uri,challenge,nonce,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,clock_timestamp()+make_interval(secs=>$11))`,
			security.Digest(raw), tenant, input.ClientID, version, a.Me.User.ID, a.SessionID, a.Epoch,
			input.RedirectURI, input.Challenge, input.Nonce, catalogOIDCCodeSeconds))
		if err != nil {
			return err
		}
		if n != 1 {
			return failure("IDENTITY_WRITE_UNCONFIRMED")
		}
		return audit.WriteWithin(ctx, tx, a.Me.User.ID, a.Me.User.ID, "portal", "catalog_oidc.authorization_granted", "success", "catalog_client", input.ClientID, "", requestID)
	})
	if err != nil {
		return "", sessionError(err)
	}
	separator := "?"
	if u.RawQuery != "" {
		separator = "&"
	}
	return input.RedirectURI + separator + url.Values{"code": {raw}, "state": {input.State}}.Encode(), nil
}

// ExchangeCatalogOIDCCandidate rechecks authority, prepares a response and
// commits single-use code consumption, token state and audit together.
// prepare may build a response in memory but must not release it to the caller.
func (s *Service) ExchangeCatalogOIDCCandidate(ctx context.Context, input CatalogOIDCCandidateExchange, requestID string, prepare func(context.Context, CatalogOIDCCandidateClaims) error) error {
	if !security.ValidToken(input.ClientSecret) || !sessionID(input.ClientID) {
		return failure("AUTH_INVALID_CREDENTIALS")
	}
	if !security.ValidToken(input.Code) || !catalogOIDCVerifier(input.Verifier) {
		return failure("TOKEN_UNUSABLE")
	}
	if _, ok := catalogOIDCRedirect(input.RedirectURI); !ok || prepare == nil {
		return failure("TOKEN_UNUSABLE")
	}
	store, err := s.Repository()
	if err != nil {
		return err
	}
	if store.Dialect() != transaction.PostgreSQL {
		return failure("STORAGE_UNAVAILABLE")
	}
	ctx, cancel := s.sessionContext(ctx)
	defer cancel()
	digest := security.Digest(input.Code)
	secret := security.Digest(input.ClientSecret)
	proof := base64.RawURLEncoding.EncodeToString(security.Digest(input.Verifier))
	return sessionError(store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		var credential []byte
		err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT secret_digest FROM catalog_oidc_candidate_clients
			WHERE id=$1 AND enabled`, input.ClientID)).Scan(&credential)
		if errors.Is(err, sql.ErrNoRows) || err == nil && subtle.ConstantTimeCompare(secret, credential) != 1 {
			return failure("AUTH_INVALID_CREDENTIALS")
		}
		if err != nil {
			return err
		}
		var a Auth
		a.Me.Audience = "portal"
		var tenant, challenge, nonce string
		var version int64
		var authTime time.Time
		const query = `SELECT g.tenant_id::text,g.client_version,g.user_id::text,g.session_id::text,
			g.auth_epoch,g.challenge,g.nonce,se.token_hash,se.created_at
			FROM catalog_oidc_candidate_codes g JOIN sessions se ON se.id=g.session_id
			JOIN catalog_oidc_candidate_clients c ON c.id=g.client_id AND c.tenant_id=g.tenant_id
			WHERE g.code_hash=$1 AND g.client_id=$2 AND g.redirect_uri=$3 AND g.used_at IS NULL AND g.expires_at>clock_timestamp()
			AND c.enabled AND c.version=g.client_version AND c.redirect_uri=g.redirect_uri`
		err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, query, digest, input.ClientID, input.RedirectURI)).
			Scan(&tenant, &version, &a.Me.User.ID, &a.SessionID, &a.Epoch, &challenge, &nonce, &a.TokenHash, &authTime)
		if errors.Is(err, sql.ErrNoRows) || err == nil && subtle.ConstantTimeCompare([]byte(proof), []byte(challenge)) != 1 {
			return failure("TOKEN_UNUSABLE")
		}
		if err != nil {
			return err
		}
		if err := s.RecheckWithin(ctx, tx, a); err != nil {
			if code(err) == "AUTH_SESSION_EXPIRED" {
				return failure("TOKEN_UNUSABLE")
			}
			return err
		}
		currentTenant, err := catalogOIDCScope(ctx, tx, a.Me.User.ID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && tenant != currentTenant {
			return failure("TOKEN_UNUSABLE")
		}
		if err != nil {
			return err
		}
		var present string
		err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT id::text FROM catalog_oidc_candidate_clients
			WHERE id=$1 AND tenant_id=$2 AND version=$3 AND redirect_uri=$4 AND secret_digest=$5 AND enabled FOR SHARE`,
			input.ClientID, tenant, version, input.RedirectURI, secret)).Scan(&present)
		if errors.Is(err, sql.ErrNoRows) {
			return failure("TOKEN_UNUSABLE")
		}
		if err != nil {
			return err
		}
		n, err := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE catalog_oidc_candidate_codes SET used_at=clock_timestamp()
			WHERE code_hash=$1 AND used_at IS NULL AND expires_at>clock_timestamp()`, digest))
		if err != nil {
			return err
		}
		if n != 1 {
			return failure("TOKEN_UNUSABLE")
		}
		claims := CatalogOIDCCandidateClaims{Subject: a.Me.User.ID, ClientID: input.ClientID, Nonce: nonce, AuthTime: authTime, TokenID: uuid.NewString()}
		err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO catalog_oidc_candidate_tokens
			(id,code_hash,expires_at) VALUES($1,$2,clock_timestamp()+make_interval(secs=>$3)) RETURNING expires_at`,
			claims.TokenID, digest, catalogOIDCTokenSeconds)).Scan(&claims.ExpiresAt)
		if err != nil {
			return err
		}
		if err := prepare(ctx, claims); err != nil {
			return err
		}
		return audit.WriteWithin(ctx, tx, claims.Subject, claims.Subject, "portal", "catalog_oidc.code_exchanged", "success", "catalog_client", claims.ClientID, "", requestID)
	}))
}
