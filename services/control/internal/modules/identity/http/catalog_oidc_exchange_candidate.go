//go:build catalog_oidc_candidate

package http

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/google/uuid"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

const catalogOIDCFormBytes = 8 * 1024

// EnableCatalogOIDCCandidate mounts the private approval route before serving
// the tagged integration fixture.
func (h *Handler) EnableCatalogOIDCCandidate() {
	h.catalogOIDCCandidate = true
}

// CatalogOIDCExchangeCandidate adapts the isolated repository transaction to
// ZITADEL's code request parser and token encoders on a private test listener.
type CatalogOIDCExchangeCandidate struct {
	Identity *identity.Service
	Provider *op.Provider
}

func (h CatalogOIDCExchangeCandidate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
		http.NotFound(w, r)
		return
	}
	switch r.URL.Path {
	case "/.well-known/openid-configuration", "/keys":
		if r.Method == http.MethodGet {
			h.Provider.ServeHTTP(w, r)
			return
		}
	case "/oauth/token":
		if r.Method == http.MethodPost {
			h.exchange(w, r)
			return
		}
	}
	http.NotFound(w, r)
}

func (h CatalogOIDCExchangeCandidate) exchange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if len(r.Header.Values("Authorization")) > 1 || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
		catalogOIDCError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, _, ok := r.BasicAuth(); !ok {
		catalogOIDCError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" {
		catalogOIDCError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, catalogOIDCFormBytes)
	if err := r.ParseForm(); err != nil || !catalogOIDCForm(r.PostForm) {
		catalogOIDCError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.PostForm.Get("grant_type") != string(oidc.GrantTypeCode) {
		catalogOIDCError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	req, err := op.ParseAccessTokenRequest(r, h.Provider.Decoder())
	if err != nil {
		catalogOIDCError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	input := identity.CatalogOIDCCandidateExchange{
		ClientID: req.ClientID, ClientSecret: req.ClientSecret, RedirectURI: req.RedirectURI,
		Code: req.Code, Verifier: req.CodeVerifier,
	}
	var response []byte
	err = h.Identity.ExchangeCatalogOIDCCandidate(r.Context(), input, uuid.NewString(), func(ctx context.Context, claims identity.CatalogOIDCCandidateClaims) error {
		var err error
		response, err = h.prepare(ctx, r, claims, req.Code)
		return err
	})
	if err != nil {
		switch err.Error() {
		case "TOKEN_UNUSABLE":
			catalogOIDCError(w, http.StatusBadRequest, "invalid_grant")
		case "AUTH_INVALID_CREDENTIALS":
			catalogOIDCError(w, http.StatusUnauthorized, "invalid_client")
		default:
			catalogOIDCError(w, http.StatusServiceUnavailable, "server_error")
		}
		return
	}
	_, _ = w.Write(response)
}

func catalogOIDCForm(values map[string][]string) bool {
	if len(values) != 4 {
		return false
	}
	for _, name := range []string{"grant_type", "code", "redirect_uri", "code_verifier"} {
		if len(values[name]) != 1 {
			return false
		}
	}
	return true
}

func catalogOIDCError(w http.ResponseWriter, status int, code string) {
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="catalog"`)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (h CatalogOIDCExchangeCandidate) prepare(ctx context.Context, r *http.Request, claims identity.CatalogOIDCCandidateClaims, raw string) ([]byte, error) {
	storage := h.Provider.Storage()
	client, err := storage.GetClientByClientID(ctx, claims.ClientID)
	if err != nil {
		return nil, err
	}
	lifetime := time.Until(claims.ExpiresAt)
	if lifetime < time.Second {
		return nil, errors.New("CATALOG_OIDC_TOKEN_EXPIRED")
	}
	access, err := op.CreateBearerToken(claims.TokenID, claims.Subject, h.Provider.Crypto())
	if err != nil {
		return nil, err
	}
	req := catalogOIDCSigningRequest{CatalogOIDCCandidateClaims: claims, redirectURI: r.PostForm.Get("redirect_uri")}
	id, err := op.CreateIDToken(ctx, h.Provider.IssuerFromRequest(r), req, lifetime, access, raw, storage, client)
	if err != nil {
		return nil, err
	}
	return json.Marshal(oidc.AccessTokenResponse{
		AccessToken: access, IDToken: id, TokenType: oidc.BearerToken, ExpiresIn: uint64(lifetime / time.Second), Scope: req.GetScopes(),
	})
}

type catalogOIDCSigningRequest struct {
	identity.CatalogOIDCCandidateClaims
	redirectURI string
}

func (r catalogOIDCSigningRequest) GetID() string                       { return r.TokenID }
func (r catalogOIDCSigningRequest) GetSubject() string                  { return r.Subject }
func (r catalogOIDCSigningRequest) GetClientID() string                 { return r.ClientID }
func (r catalogOIDCSigningRequest) GetAudience() []string               { return []string{r.ClientID} }
func (r catalogOIDCSigningRequest) GetScopes() []string                 { return []string{oidc.ScopeOpenID} }
func (r catalogOIDCSigningRequest) GetNonce() string                    { return r.Nonce }
func (r catalogOIDCSigningRequest) GetAuthTime() time.Time              { return r.AuthTime }
func (r catalogOIDCSigningRequest) GetRedirectURI() string              { return r.redirectURI }
func (catalogOIDCSigningRequest) GetResponseType() oidc.ResponseType    { return oidc.ResponseTypeCode }
func (catalogOIDCSigningRequest) GetResponseMode() oidc.ResponseMode    { return oidc.ResponseModeQuery }
func (catalogOIDCSigningRequest) GetAMR() []string                      { return nil }
func (catalogOIDCSigningRequest) GetACR() string                        { return "" }
func (catalogOIDCSigningRequest) GetState() string                      { return "" }
func (catalogOIDCSigningRequest) GetCodeChallenge() *oidc.CodeChallenge { return nil }
func (catalogOIDCSigningRequest) Done() bool                            { return true }
