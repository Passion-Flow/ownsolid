package boundary

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

const browserCookie = "__Host-OPBrowser"
const sessionCookie = "__Host-OPSession"

type fixtureProvider struct {
	store                      *sdkStore
	sdk                        *op.Provider
	crypto                     *providerCrypto
	host, loginUser, loginName string
}

func newFixtureProvider(s *sdkStore, c *providerCrypto, user, name string) (*fixtureProvider, error) {
	if s == nil || s.pool == nil || !s.Policy.Request.valid() || c == nil || c.code == nil || c.bearer == nil || user == "" || name == "" || s.Policy.Operation <= 0 || s.Policy.Operation > time.Minute || s.Policy.RequestLifetime <= 0 || s.Policy.RequestLifetime > 10*time.Minute || s.Policy.CodeLifetime <= 0 || s.Policy.CodeLifetime > s.Policy.RequestLifetime || s.Policy.TokenLifetime <= 0 || s.Policy.TokenLifetime > time.Hour || s.Policy.SessionLifetime < s.Policy.TokenLifetime || s.Policy.IDLifetime <= 0 || s.Policy.IDLifetime > s.Policy.TokenLifetime || s.Policy.MaxHeaders < 1024 || s.Policy.MaxHeaders > 65536 || s.Policy.MaxConnections < 2 || s.Policy.MaxConnections > 8 || s.Policy.MaxPending < 1 || s.Policy.MaxPending > 128 || s.Policy.MaxResponse < 4096 || s.Policy.MaxResponse > 65536 {
		return nil, errConfig
	}
	u, err := url.Parse(s.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errConfig
	}
	if _, err = s.SigningKey(context.Background()); err != nil {
		return nil, err
	}
	if _, err = s.KeySet(context.Background()); err != nil {
		return nil, err
	}
	provider, err := op.NewProvider(&op.Config{CodeMethodS256: true, SupportedScopes: []string{oidc.ScopeOpenID}, SupportedClaims: []string{"sub", "iss", "aud", "exp", "iat", "auth_time", "nonce", "at_hash", "c_hash", "azp", "amr"}}, s, op.StaticIssuer(s.Origin), op.WithCrypto(c), op.WithCORSOptions(nil))
	if err != nil {
		return nil, errConfig
	}
	return &fixtureProvider{store: s, sdk: provider, crypto: c, host: u.Host, loginUser: user, loginName: name}, nil
}

type responseBuffer struct {
	headers         http.Header
	body            []byte
	status, maximum int
	overflow        bool
}

func newResponseBuffer(max int) *responseBuffer {
	return &responseBuffer{headers: http.Header{}, maximum: max}
}
func (b *responseBuffer) Header() http.Header { return b.headers }
func (b *responseBuffer) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *responseBuffer) Write(data []byte) (int, error) {
	if b.status == 0 {
		b.status = 200
	}
	if len(data) > b.maximum-len(b.body) {
		b.overflow = true
		return 0, errors.New("OP_RESPONSE_BUDGET_EXCEEDED")
	}
	b.body = append(b.body, data...)
	return len(data), nil
}
func protocolError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code})
}
func release(w http.ResponseWriter, b *responseBuffer) {
	for key, values := range b.headers {
		if strings.EqualFold(key, "Content-Type") || strings.EqualFold(key, "WWW-Authenticate") || strings.EqualFold(key, "Location") {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
	}
	status := b.status
	if status == 0 {
		status = 200
	}
	w.WriteHeader(status)
	w.Write(b.body)
}
func cookieValue(r *http.Request, name string) (string, bool) {
	var found string
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == name {
			found = c.Value
			count++
		}
	}
	if count != 1 || len(found) != 43 {
		return "", false
	}
	raw, err := canonicalURL(found)
	return found, err == nil && len(raw) == 32
}
func (f *fixtureProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if r.Host != f.host || r.URL.EscapedPath() != r.URL.Path {
		protocolError(w, 400, "invalid_request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), f.store.Policy.Operation)
	defer cancel()
	r = r.WithContext(ctx)
	if err := http.NewResponseController(w).SetReadDeadline(time.Now().Add(f.store.Policy.Operation)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		protocolError(w, 500, "server_error")
		return
	}
	if token, ok := cookieValue(r, browserCookie); ok {
		r = r.WithContext(context.WithValue(r.Context(), browserContextKey{}, digest(token)))
	}
	if token, ok := cookieValue(r, sessionCookie); ok {
		r = r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, digest(token)))
	}
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		if r.Method != "GET" || r.URL.RawQuery != "" {
			protocolError(w, 400, "invalid_request")
			return
		}
		f.discovery(w, r)
	case "/keys":
		if r.Method != "GET" || r.URL.RawQuery != "" {
			protocolError(w, 400, "invalid_request")
			return
		}
		f.boundedSDK(w, r)
	case "/authorize":
		if r.Method != "GET" || len(r.URL.RawQuery) > f.store.Policy.Request.MaxBody {
			protocolError(w, 400, "invalid_request")
			return
		}
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values) != 8 {
			protocolError(w, 400, "invalid_request")
			return
		}
		for name, parts := range values {
			if len(parts) != 1 {
				protocolError(w, 400, "invalid_request")
				return
			}
			switch name {
			case "client_id", "redirect_uri", "response_type", "scope", "state", "nonce", "code_challenge", "code_challenge_method":
			default:
				protocolError(w, 400, "invalid_request")
				return
			}
		}
		if values.Get("scope") != "openid" || values.Get("response_type") != "code" || values.Get("code_challenge_method") != "S256" {
			protocolError(w, 400, "invalid_request")
			return
		}
		client, _, err := f.store.registered(r.Context(), f.store.pool, values.Get("client_id"), "")
		if err != nil || client.Redirect != values.Get("redirect_uri") {
			protocolError(w, 400, "invalid_request")
			return
		}
		if _, err = browser(r.Context()); err != nil {
			token, e := tokenRandom()
			if e != nil {
				protocolError(w, 500, "server_error")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: browserCookie, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(f.store.Policy.SessionLifetime.Seconds())})
			r = r.WithContext(context.WithValue(r.Context(), browserContextKey{}, digest(token)))
		}
		f.authorize(w, r, client)
	case "/authorize/callback":
		if r.Method != "GET" {
			protocolError(w, 400, "invalid_request")
			return
		}
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values) != 1 || len(values["id"]) != 1 || !validRequestID(values.Get("id")) {
			protocolError(w, 400, "invalid_request")
			return
		}
		f.callback(w, r)
	case "/login":
		f.login(w, r)
	case "/oauth/token":
		f.atomicToken(w, r, false)
	case "/userinfo":
		if _, err := userInfoToken(r, f.crypto.bearer); err != nil {
			protocolError(w, 401, "invalid_token")
			return
		}
		f.boundedSDK(w, r)
	case "/revoke":
		f.atomicToken(w, r, true)
	default:
		protocolError(w, 404, "invalid_request")
	}
}
func (f *fixtureProvider) boundedSDK(w http.ResponseWriter, r *http.Request) {
	b := newResponseBuffer(f.store.Policy.MaxResponse)
	defer clear(b.body)
	f.sdk.ServeHTTP(b, r)
	if b.overflow || r.Context().Err() != nil {
		protocolError(w, 503, "server_error")
		return
	}
	release(w, b)
}
func (f *fixtureProvider) discovery(w http.ResponseWriter, r *http.Request) {
	b := newResponseBuffer(f.store.Policy.MaxResponse)
	defer clear(b.body)
	f.sdk.ServeHTTP(b, r)
	var document map[string]any
	if b.status != 200 || b.overflow || json.Unmarshal(b.body, &document) != nil {
		protocolError(w, 500, "server_error")
		return
	}
	document["response_types_supported"] = []string{"code"}
	document["response_modes_supported"] = []string{"query"}
	document["grant_types_supported"] = []string{"authorization_code"}
	document["token_endpoint_auth_methods_supported"] = []string{"client_secret_basic"}
	document["revocation_endpoint_auth_methods_supported"] = []string{"client_secret_basic"}
	document["code_challenge_methods_supported"] = []string{"S256"}
	for _, name := range []string{"introspection_endpoint", "introspection_endpoint_auth_methods_supported", "introspection_endpoint_auth_signing_alg_values_supported", "end_session_endpoint", "device_authorization_endpoint", "check_session_iframe", "request_object_signing_alg_values_supported", "registration_endpoint"} {
		delete(document, name)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(document)
}
func (f *fixtureProvider) atomicToken(w http.ResponseWriter, r *http.Request, revocation bool) {
	var input tokenInput
	var err error
	if revocation {
		input, err = parseRevocation(r, f.store.Policy.Request)
	} else {
		input, err = parseToken(r, f.store.Policy.Request)
	}
	if err != nil {
		protocolError(w, 400, "invalid_request")
		return
	}
	client, secret, err := f.store.registered(r.Context(), f.store.pool, input.Client, "")
	if err != nil || subtle.ConstantTimeCompare(secret, digest(input.Secret)) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
		protocolError(w, 401, "invalid_client")
		return
	}
	tx, err := f.store.pool.Begin(r.Context())
	if err != nil {
		protocolError(w, 503, "server_error")
		return
	}
	defer rollback(tx, f.store.Policy.Operation)
	lock := " FOR SHARE OF c,i,t"
	if revocation {
		lock = " FOR UPDATE OF c,i,t"
	}
	current, currentSecret, err := f.store.registered(r.Context(), tx, client.ID, lock)
	if err != nil || subtle.ConstantTimeCompare(currentSecret, digest(input.Secret)) != 1 || current.Revision != client.Revision || current.InstallationRevision != client.InstallationRevision || current.Installation != client.Installation || current.Workspace != client.Workspace {
		protocolError(w, 401, "invalid_client")
		return
	}
	state := &transactionState{tx: tx, client: current}
	r = r.WithContext(context.WithValue(r.Context(), txContextKey{}, state))
	b := newResponseBuffer(f.store.Policy.MaxResponse)
	defer clear(b.body)
	f.sdk.ServeHTTP(b, r)
	if b.overflow || r.Context().Err() != nil {
		protocolError(w, 503, "server_error")
		return
	}
	if b.status != 200 {
		var refusal struct {
			Error string `json:"error"`
		}
		json.Unmarshal(b.body, &refusal)
		code := "server_error"
		switch refusal.Error {
		case "invalid_request", "invalid_client", "invalid_grant", "unsupported_grant_type", "unauthorized_client":
			code = refusal.Error
		}
		status := b.status
		if status < 400 || status > 599 {
			status = 500
		}
		protocolError(w, status, code)
		return
	}
	if !revocation {
		var tokens struct{ Access, ID string }
		var fields map[string]json.RawMessage
		if json.Unmarshal(b.body, &fields) != nil || json.Unmarshal(fields["access_token"], &tokens.Access) != nil || json.Unmarshal(fields["id_token"], &tokens.ID) != nil || tokens.Access == "" || tokens.ID == "" {
			protocolError(w, 500, "server_error")
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		protocolError(w, 503, "server_error")
		return
	}
	release(w, b)
}
