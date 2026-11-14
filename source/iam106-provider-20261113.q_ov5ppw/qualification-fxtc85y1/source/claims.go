package boundary

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

var _ op.Storage = (*sdkStore)(nil)

type signingMaterial struct {
	id  string
	key *rsa.PrivateKey
}

func (k signingMaterial) ID() string                                  { return k.id }
func (k signingMaterial) SignatureAlgorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (k signingMaterial) Key() any                                    { return k.key }

type publicMaterial struct {
	id  string
	key *rsa.PublicKey
}

func (k publicMaterial) ID() string                         { return k.id }
func (k publicMaterial) Algorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (k publicMaterial) Use() string                        { return "sig" }
func (k publicMaterial) Key() any                           { return k.key }
func (s *sdkStore) SigningKey(context.Context) (op.SigningKey, error) {
	raw, err := readPrivateFile(s.SigningFile, 32768)
	if err != nil {
		return nil, serverFault()
	}
	defer clear(raw)
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		return nil, serverFault()
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, serverFault()
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() < 2048 || key.N.BitLen() > 4096 || key.E != 65537 || key.Validate() != nil {
		return nil, serverFault()
	}
	return signingMaterial{id: s.signingID, key: key}, nil
}
func (s *sdkStore) SignatureAlgorithms(context.Context) ([]jose.SignatureAlgorithm, error) {
	return []jose.SignatureAlgorithm{jose.RS256}, nil
}
func (s *sdkStore) KeySet(context.Context) ([]op.Key, error) {
	if len(s.publicFiles) < 1 || len(s.publicFiles) > 3 {
		return nil, serverFault()
	}
	var keys []op.Key
	for id, path := range s.publicFiles {
		raw, err := readPrivateFile(path, 8192)
		if err != nil {
			return nil, serverFault()
		}
		block, rest := pem.Decode(raw)
		if block == nil || block.Type != "PUBLIC KEY" || len(rest) != 0 {
			return nil, serverFault()
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, serverFault()
		}
		key, ok := parsed.(*rsa.PublicKey)
		if !ok || key.N.BitLen() < 2048 || key.N.BitLen() > 4096 || key.E != 65537 {
			return nil, serverFault()
		}
		keys = append(keys, publicMaterial{id: id, key: key})
	}
	return keys, nil
}
func (s *sdkStore) SetUserinfoFromScopes(ctx context.Context, info *oidc.UserInfo, subject, client string, scopes []string) error {
	// The deprecated SDK hook supplies no optional profile claims. Its replacement
	// below performs the actual request-bound authority check for this profile.
	state, err := currentTransaction(ctx)
	if err != nil || state.request == nil || subject != state.request.Subject || client != state.client.ID || len(onlyOpenID(scopes)) != 1 {
		return oidc.ErrInvalidGrant()
	}
	return nil
}
func (s *sdkStore) SetUserinfoFromRequest(ctx context.Context, info *oidc.UserInfo, input op.IDTokenRequest, scopes []string) error {
	state, err := currentTransaction(ctx)
	r, ok := input.(*authRecord)
	if err != nil || !ok || r != state.request || len(onlyOpenID(scopes)) != 1 || s.authority(ctx, state.tx, r, state.client) != nil {
		return oidc.ErrInvalidGrant()
	}
	info.Subject = r.Subject
	return nil
}

func (s *sdkStore) tokenAuthority(ctx context.Context, tx pgx.Tx, id, subject, client string) (*authRecord, registeredClient, error) {
	r := new(authRecord)
	err := tx.QueryRow(ctx, `SELECT request_id,client_id,client_revision,installation_revision,workspace_id::text,installation_id::text,user_id::text,session_id::text,auth_epoch,subject,membership_authority::text,expires_at FROM op_fixture_tokens WHERE tenant_id=$1 AND token_digest=$2 AND subject=$3 AND revoked_at IS NULL AND expires_at>clock_timestamp()`, s.Tenant, digest(id), subject).Scan(&r.ID, &r.Client, &r.ClientRevision, &r.InstallationRevision, &r.Workspace, &r.Installation, &r.User, &r.Session, &r.Epoch, &r.Subject, &r.Membership, &r.Expiry)
	if err != nil || client != "" && r.Client != client {
		return nil, registeredClient{}, errAuthority
	}
	c, _, err := s.registered(ctx, tx, r.Client, " FOR SHARE OF c,i,t")
	if err != nil {
		return nil, registeredClient{}, errAuthority
	}
	r.Redirect, r.Approved = c.Redirect, true
	if s.authority(ctx, tx, r, c) != nil {
		return nil, registeredClient{}, errAuthority
	}
	var exists string
	err = tx.QueryRow(ctx, `SELECT request_id FROM op_fixture_tokens WHERE tenant_id=$1 AND token_digest=$2 AND subject=$3 AND revoked_at IS NULL AND expires_at>clock_timestamp() FOR SHARE`, s.Tenant, digest(id), subject).Scan(&exists)
	if err != nil {
		return nil, registeredClient{}, errAuthority
	}
	return r, c, nil
}
func (s *sdkStore) SetUserinfoFromToken(ctx context.Context, info *oidc.UserInfo, id, subject, origin string) error {
	if origin != "" || !validPayload(id+":"+subject) {
		return oidc.ErrInvalidGrant()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return serverFault()
	}
	defer rollback(tx, s.Policy.Operation)
	r, _, err := s.tokenAuthority(ctx, tx, id, subject, "")
	if err != nil {
		return oidc.ErrInvalidGrant()
	}
	if err = tx.Commit(ctx); err != nil {
		return serverFault()
	}
	info.Subject = r.Subject
	return nil
}
func (s *sdkStore) RevokeToken(ctx context.Context, id, subject, client string) *oidc.Error {
	state, err := currentTransaction(ctx)
	if err != nil || state.client.ID != client || !validPayload(id+":"+subject) {
		return oidc.ErrInvalidClient()
	}
	r, _, err := s.tokenAuthority(ctx, state.tx, id, subject, client)
	if err != nil {
		return nil
	}
	result, err := state.tx.Exec(ctx, `UPDATE op_fixture_tokens SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND client_id=$2 AND token_digest=$3 AND subject=$4 AND revoked_at IS NULL`, s.Tenant, client, digest(id), subject)
	if err != nil {
		return oidc.ErrServerError()
	}
	if result.RowsAffected() == 0 {
		return nil
	}
	if _, err = state.tx.Exec(ctx, `INSERT INTO op_fixture_events(id,tenant_id,client_id,action,request_id) VALUES($1,$2,$3,'token_revoked',$4)`, uuid.NewString(), s.Tenant, client, r.ID); err != nil {
		return oidc.ErrServerError()
	}
	return nil
}
func (s *sdkStore) Health(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return serverFault()
	}
	return nil
}
func (s *sdkStore) CreateAccessAndRefreshTokens(context.Context, op.TokenRequest, string) (string, string, time.Time, error) {
	return "", "", time.Time{}, oidc.ErrUnsupportedGrantType()
}
func (s *sdkStore) TokenRequestByRefreshToken(context.Context, string) (op.RefreshTokenRequest, error) {
	return nil, oidc.ErrUnsupportedGrantType()
}
func (s *sdkStore) GetRefreshTokenInfo(context.Context, string, string) (string, string, error) {
	return "", "", op.ErrInvalidRefreshToken
}
func (s *sdkStore) TerminateSession(context.Context, string, string) error {
	return oidc.ErrRequestNotSupported()
}
func (s *sdkStore) SetIntrospectionFromToken(context.Context, *oidc.IntrospectionResponse, string, string, string) error {
	return oidc.ErrRequestNotSupported()
}
func (s *sdkStore) GetPrivateClaimsFromScopes(context.Context, string, string, []string) (map[string]any, error) {
	return nil, oidc.ErrUnsupportedGrantType()
}
func (s *sdkStore) GetKeyByIDAndClientID(context.Context, string, string) (*jose.JSONWebKey, error) {
	return nil, oidc.ErrRequestNotSupported()
}
func (s *sdkStore) ValidateJWTProfileScopes(context.Context, string, []string) ([]string, error) {
	return nil, oidc.ErrUnsupportedGrantType()
}
