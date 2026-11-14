package boundary

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func serverFault() error { return oidc.ErrServerError().WithDescription("operation cannot complete") }
func (s *sdkStore) registered(ctx context.Context, q queryer, id, lock string) (registeredClient, []byte, error) {
	c := registeredClient{Tenant: s.Tenant, Origin: s.Origin, IDLifetime: s.Policy.IDLifetime}
	var secret []byte
	err := q.QueryRow(ctx, `SELECT c.client_id,c.redirect_uri,c.workspace_id::text,c.installation_id::text,c.revision,i.revision,c.secret_digest FROM op_fixture_clients c JOIN op_fixture_installations i ON i.tenant_id=c.tenant_id AND i.workspace_id=c.workspace_id AND i.id=c.installation_id JOIN op_fixture_tenants t ON t.id=c.tenant_id WHERE c.tenant_id=$1 AND c.client_id=$2 AND c.enabled AND i.enabled AND t.enabled`+lock, s.Tenant, id).Scan(&c.ID, &c.Redirect, &c.Workspace, &c.Installation, &c.Revision, &c.InstallationRevision, &secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return registeredClient{}, nil, errAuthority
	}
	if err != nil {
		return registeredClient{}, nil, serverFault()
	}
	return c, secret, nil
}
func (s *sdkStore) GetClientByClientID(ctx context.Context, id string) (op.Client, error) {
	if state, err := currentTransaction(ctx); err == nil {
		if id != state.client.ID {
			return nil, errAuthority
		}
		return state.client, nil
	}
	c, _, err := s.registered(ctx, s.pool, id, "")
	if err != nil {
		return nil, err
	}
	return c, nil
}
func (s *sdkStore) AuthorizeClientIDSecret(ctx context.Context, id, secret string) error {
	var q queryer = s.pool
	if state, err := currentTransaction(ctx); err == nil {
		if id != state.client.ID {
			return errAuthority
		}
		q = state.tx
	}
	_, stored, err := s.registered(ctx, q, id, "")
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(stored, digest(secret)) != 1 {
		return errAuthority
	}
	return nil
}

const authColumns = `id,client_id,redirect_uri,state,nonce,challenge,scope,COALESCE(user_id::text,''),COALESCE(session_id::text,''),COALESCE(subject,''),COALESCE(membership_authority::text,''),client_revision,installation_revision,COALESCE(auth_epoch,0),COALESCE(approved_at,'0001-01-01 UTC'::timestamptz),expires_at,approved_at IS NOT NULL,workspace_id::text,installation_id::text`

func authScan(row pgx.Row) (*authRecord, error) {
	r := new(authRecord)
	err := row.Scan(&r.ID, &r.Client, &r.Redirect, &r.State, &r.Nonce, &r.Challenge, &r.Scope, &r.User, &r.Session, &r.Subject, &r.Membership, &r.ClientRevision, &r.InstallationRevision, &r.Epoch, &r.AuthTime, &r.Expiry, &r.Approved, &r.Workspace, &r.Installation)
	if err != nil {
		return nil, err
	}
	r.AuthTime = r.AuthTime.UTC()
	r.Expiry = r.Expiry.UTC()
	return r, nil
}
func (s *sdkStore) CreateAuthRequest(ctx context.Context, input *oidc.AuthRequest, user string) (op.AuthRequest, error) {
	browserHash, err := browser(ctx)
	if err != nil {
		return nil, oidc.ErrInvalidRequest()
	}
	if input == nil || user != "" || input.ResponseType != oidc.ResponseTypeCode || input.ResponseMode != "" && input.ResponseMode != oidc.ResponseModeQuery || len(input.Scopes) != 1 || input.Scopes[0] != oidc.ScopeOpenID || input.CodeChallengeMethod != oidc.CodeChallengeMethodS256 || len(input.CodeChallenge) != 43 || !printable(input.State, 1, 512, false) || !printable(input.Nonce, 1, 128, false) {
		return nil, oidc.ErrInvalidRequest()
	}
	challenge, err := canonicalURL(input.CodeChallenge)
	if err != nil || len(challenge) != 32 {
		return nil, oidc.ErrInvalidRequest()
	}
	random, err := tokenRandom()
	if err != nil {
		return nil, serverFault()
	}
	id := "ar_" + random
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, serverFault()
	}
	defer rollback(tx, s.Policy.Operation)
	c, _, err := s.registered(ctx, tx, input.ClientID, " FOR UPDATE OF c")
	if err != nil {
		return nil, oidc.ErrInvalidRequest()
	}
	if c.Redirect != input.RedirectURI {
		return nil, oidc.ErrInvalidRequest()
	}
	var pending int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM op_fixture_requests WHERE tenant_id=$1 AND client_id=$2 AND consumed_at IS NULL AND expires_at>clock_timestamp()`, s.Tenant, c.ID).Scan(&pending); err != nil {
		return nil, serverFault()
	}
	if pending >= s.Policy.MaxPending {
		return nil, serverFault()
	}
	r, err := authScan(tx.QueryRow(ctx, `INSERT INTO op_fixture_requests(tenant_id,id,client_id,client_revision,installation_revision,workspace_id,installation_id,browser_digest,redirect_uri,state,nonce,challenge,scope,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'openid',clock_timestamp()+$13::bigint*interval '1 microsecond') RETURNING `+authColumns, s.Tenant, id, c.ID, c.Revision, c.InstallationRevision, c.Workspace, c.Installation, browserHash, input.RedirectURI, input.State, input.Nonce, input.CodeChallenge, s.Policy.RequestLifetime.Microseconds()))
	if err != nil {
		return nil, serverFault()
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, serverFault()
	}
	return r, nil
}
func (s *sdkStore) authority(ctx context.Context, tx pgx.Tx, r *authRecord, c registeredClient) error {
	if r.Workspace != c.Workspace || r.Installation != c.Installation || r.Client != c.ID || r.ClientRevision != c.Revision || r.InstallationRevision != c.InstallationRevision || r.Redirect != c.Redirect || !r.Approved {
		return errAuthority
	}
	var epoch int64
	var sub string
	if err := tx.QueryRow(ctx, `SELECT auth_epoch,subject FROM op_fixture_principals WHERE tenant_id=$1 AND user_id=$2 AND enabled FOR SHARE`, s.Tenant, r.User).Scan(&epoch, &sub); err != nil || epoch != r.Epoch || sub != r.Subject {
		return errAuthority
	}
	var session string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM op_fixture_sessions WHERE tenant_id=$1 AND id=$2 AND user_id=$3 AND auth_epoch=$4 AND revoked_at IS NULL AND expires_at>clock_timestamp() FOR SHARE`, s.Tenant, r.Session, r.User, r.Epoch).Scan(&session); err != nil {
		return errAuthority
	}
	var membership string
	if err := tx.QueryRow(ctx, `SELECT authority_id::text FROM op_fixture_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3 FOR SHARE`, s.Tenant, c.Workspace, r.User).Scan(&membership); err != nil || membership != r.Membership {
		return errAuthority
	}
	return nil
}
func (s *sdkStore) AuthRequestByID(ctx context.Context, id string) (op.AuthRequest, error) {
	browserHash, err := browser(ctx)
	if err != nil || !validRequestID(id) {
		return nil, oidc.ErrInvalidRequest()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, serverFault()
	}
	defer rollback(tx, s.Policy.Operation)
	r, err := authScan(tx.QueryRow(ctx, `SELECT `+authColumns+` FROM op_fixture_requests WHERE tenant_id=$1 AND id=$2 AND browser_digest=$3 AND consumed_at IS NULL AND expires_at>clock_timestamp()`, s.Tenant, id, browserHash))
	if err != nil {
		return nil, oidc.ErrInvalidRequest()
	}
	c, _, err := s.registered(ctx, tx, r.Client, " FOR SHARE OF c,i,t")
	if err != nil {
		return nil, oidc.ErrInvalidRequest()
	}
	if r.Approved && (s.authority(ctx, tx, r, c) != nil || s.callbackSession(ctx, tx, r) != nil) {
		return nil, oidc.ErrInvalidRequest()
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, serverFault()
	}
	return r, nil
}
func (s *sdkStore) SaveAuthCode(ctx context.Context, id, code string) error {
	browserHash, err := browser(ctx)
	if err != nil || !validRequestID(id) || len(code) > s.Policy.Request.MaxCode {
		return oidc.ErrInvalidRequest()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return serverFault()
	}
	defer rollback(tx, s.Policy.Operation)
	r, err := authScan(tx.QueryRow(ctx, `SELECT `+authColumns+` FROM op_fixture_requests WHERE tenant_id=$1 AND id=$2 AND browser_digest=$3 AND expires_at>clock_timestamp() AND code_digest IS NULL`, s.Tenant, id, browserHash))
	if err != nil {
		return oidc.ErrInvalidRequest()
	}
	c, _, err := s.registered(ctx, tx, r.Client, " FOR SHARE OF c,i,t")
	if err != nil || s.authority(ctx, tx, r, c) != nil || s.callbackSession(ctx, tx, r) != nil {
		return oidc.ErrInvalidRequest()
	}
	result, err := tx.Exec(ctx, `UPDATE op_fixture_requests SET code_digest=$3,code_expires_at=LEAST(expires_at,clock_timestamp()+$4::bigint*interval '1 microsecond') WHERE tenant_id=$1 AND id=$2 AND code_digest IS NULL AND consumed_at IS NULL AND expires_at>clock_timestamp()`, s.Tenant, id, digest(code), s.Policy.CodeLifetime.Microseconds())
	if err != nil {
		return serverFault()
	}
	if result.RowsAffected() != 1 {
		return oidc.ErrInvalidRequest()
	}
	if _, err = tx.Exec(ctx, `INSERT INTO op_fixture_events(id,tenant_id,client_id,action,request_id) VALUES($1,$2,$3,'code_issued',$4)`, uuid.NewString(), s.Tenant, c.ID, r.ID); err != nil {
		return serverFault()
	}
	if err = tx.Commit(ctx); err != nil {
		return serverFault()
	}
	return nil
}
func (s *sdkStore) AuthRequestByCode(ctx context.Context, code string) (op.AuthRequest, error) {
	state, err := currentTransaction(ctx)
	if err != nil {
		return nil, oidc.ErrInvalidGrant()
	}
	query := `SELECT ` + authColumns + ` FROM op_fixture_requests WHERE tenant_id=$1 AND client_id=$2 AND code_digest=$3 AND consumed_at IS NULL AND approved_at IS NOT NULL AND code_expires_at>clock_timestamp() AND expires_at>clock_timestamp()`
	r, err := authScan(state.tx.QueryRow(ctx, query, s.Tenant, state.client.ID, digest(code)))
	if err != nil || s.authority(ctx, state.tx, r, state.client) != nil {
		return nil, oidc.ErrInvalidGrant()
	}
	r, err = authScan(state.tx.QueryRow(ctx, query+` FOR UPDATE`, s.Tenant, state.client.ID, digest(code)))
	if err != nil {
		return nil, oidc.ErrInvalidGrant()
	}
	state.request = r
	return r, nil
}
func (s *sdkStore) CreateAccessToken(ctx context.Context, request op.TokenRequest) (string, time.Time, error) {
	state, err := currentTransaction(ctx)
	r, ok := request.(*authRecord)
	if err != nil || !ok || state.request != r {
		return "", time.Time{}, oidc.ErrInvalidGrant()
	}
	id, err := tokenRandom()
	if err != nil {
		return "", time.Time{}, serverFault()
	}
	var expiry time.Time
	err = state.tx.QueryRow(ctx, `INSERT INTO op_fixture_tokens(tenant_id,token_digest,request_id,client_id,client_revision,installation_revision,workspace_id,installation_id,user_id,session_id,auth_epoch,subject,membership_authority,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,clock_timestamp()+$14::bigint*interval '1 microsecond') RETURNING expires_at`, s.Tenant, digest(id), r.ID, r.Client, r.ClientRevision, r.InstallationRevision, state.client.Workspace, state.client.Installation, r.User, r.Session, r.Epoch, r.Subject, r.Membership, s.Policy.TokenLifetime.Microseconds()).Scan(&expiry)
	if err != nil {
		return "", time.Time{}, serverFault()
	}
	return id, expiry.UTC(), nil
}
func (s *sdkStore) DeleteAuthRequest(ctx context.Context, id string) error {
	state, err := currentTransaction(ctx)
	if err != nil || state.request == nil || state.request.ID != id {
		return oidc.ErrInvalidGrant()
	}
	result, err := state.tx.Exec(ctx, `UPDATE op_fixture_requests SET consumed_at=clock_timestamp() WHERE tenant_id=$1 AND client_id=$2 AND id=$3 AND consumed_at IS NULL AND code_expires_at>clock_timestamp()`, s.Tenant, state.client.ID, id)
	if err != nil {
		return serverFault()
	}
	if result.RowsAffected() != 1 {
		return oidc.ErrInvalidGrant()
	}
	if _, err = state.tx.Exec(ctx, `INSERT INTO op_fixture_events(id,tenant_id,client_id,action,request_id) VALUES($1,$2,$3,'code_redeemed',$4)`, uuid.NewString(), s.Tenant, state.client.ID, id); err != nil {
		return serverFault()
	}
	return nil
}
