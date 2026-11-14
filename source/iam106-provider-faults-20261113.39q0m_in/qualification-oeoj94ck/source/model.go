package boundary

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

var errAuthority = errors.New("OP_AUTHORITY_REFUSED")

type providerPolicy struct {
	Operation, RequestLifetime, CodeLifetime, TokenLifetime, SessionLifetime, IDLifetime time.Duration
	MaxHeaders, MaxConnections, MaxPending, MaxResponse                                  int
	Request                                                                              requestPolicy
}
type registeredClient struct {
	ID, Redirect, Origin, Tenant, Workspace, Installation string
	Revision, InstallationRevision                        int64
	IDLifetime                                            time.Duration
}

func (c registeredClient) GetID() string                       { return c.ID }
func (c registeredClient) RedirectURIs() []string              { return []string{c.Redirect} }
func (c registeredClient) PostLogoutRedirectURIs() []string    { return nil }
func (c registeredClient) ApplicationType() op.ApplicationType { return op.ApplicationTypeWeb }
func (c registeredClient) AuthMethod() oidc.AuthMethod         { return oidc.AuthMethodBasic }
func (c registeredClient) ResponseTypes() []oidc.ResponseType {
	return []oidc.ResponseType{oidc.ResponseTypeCode}
}
func (c registeredClient) GrantTypes() []oidc.GrantType { return []oidc.GrantType{oidc.GrantTypeCode} }
func (c registeredClient) LoginURL(id string) string {
	return c.Origin + "/login?id=" + url.QueryEscape(id)
}
func (c registeredClient) AccessTokenType() op.AccessTokenType { return op.AccessTokenTypeBearer }
func (c registeredClient) IDTokenLifetime() time.Duration      { return c.IDLifetime }
func (c registeredClient) DevMode() bool                       { return false }
func (c registeredClient) RestrictAdditionalIdTokenScopes() func([]string) []string {
	return onlyOpenID
}
func (c registeredClient) RestrictAdditionalAccessTokenScopes() func([]string) []string {
	return onlyOpenID
}
func (c registeredClient) IsScopeAllowed(string) bool           { return false }
func (c registeredClient) IDTokenUserinfoClaimsAssertion() bool { return true }
func (c registeredClient) ClockSkew() time.Duration             { return 0 }
func onlyOpenID(scopes []string) []string {
	if len(scopes) == 1 && scopes[0] == oidc.ScopeOpenID {
		return []string{oidc.ScopeOpenID}
	}
	return nil
}

type authRecord struct {
	ID, Client, Redirect, State, Nonce, Challenge, Scope, User, Session, Subject, Membership, Workspace, Installation string
	ClientRevision, InstallationRevision, Epoch                                                                       int64
	AuthTime, Expiry                                                                                                  time.Time
	Approved                                                                                                          bool
}

func (r *authRecord) GetID() string  { return r.ID }
func (r *authRecord) GetACR() string { return "" }
func (r *authRecord) GetAMR() []string {
	if r.Approved {
		return []string{"pwd"}
	}
	return nil
}
func (r *authRecord) GetAudience() []string  { return []string{r.Client} }
func (r *authRecord) GetAuthTime() time.Time { return r.AuthTime }
func (r *authRecord) GetClientID() string    { return r.Client }
func (r *authRecord) GetCodeChallenge() *oidc.CodeChallenge {
	return &oidc.CodeChallenge{Challenge: r.Challenge, Method: oidc.CodeChallengeMethodS256}
}
func (r *authRecord) GetNonce() string                   { return r.Nonce }
func (r *authRecord) GetRedirectURI() string             { return r.Redirect }
func (r *authRecord) GetResponseType() oidc.ResponseType { return oidc.ResponseTypeCode }
func (r *authRecord) GetResponseMode() oidc.ResponseMode { return oidc.ResponseModeQuery }
func (r *authRecord) GetScopes() []string                { return []string{oidc.ScopeOpenID} }
func (r *authRecord) GetState() string                   { return r.State }
func (r *authRecord) GetSubject() string                 { return r.Subject }
func (r *authRecord) Done() bool                         { return r.Approved }

type transactionState struct {
	tx      pgx.Tx
	client  registeredClient
	request *authRecord
}
type txContextKey struct{}
type browserContextKey struct{}
type sessionContextKey struct{}
type sdkStore struct {
	pool                        *pgxpool.Pool
	Tenant, Origin, SigningFile string
	Policy                      providerPolicy
	publicFiles                 map[string]string
	signingID                   string
}

func tokenRandom() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func digest(s string) []byte { v := sha256.Sum256([]byte(s)); return v[:] }
func currentTransaction(ctx context.Context) (*transactionState, error) {
	state, ok := ctx.Value(txContextKey{}).(*transactionState)
	if !ok || state.tx == nil {
		return nil, errAuthority
	}
	return state, nil
}
func browser(ctx context.Context) ([]byte, error) {
	v, ok := ctx.Value(browserContextKey{}).([]byte)
	if !ok || len(v) != sha256.Size {
		return nil, errAuthority
	}
	return v, nil
}

func rollback(tx pgx.Tx, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = tx.Rollback(ctx)
}
