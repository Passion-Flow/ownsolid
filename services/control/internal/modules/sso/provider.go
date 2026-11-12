package sso

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const maximumProviderFields = 128
const maximumAuthorizationCodeBytes = 4096
const maximumProviderCertificateBytes = 1048576

type providerIdentity struct{ subject, login, name string }

// The provider receives only operator-approved network policy and exact server-generated callback URIs.
type giteaProvider struct {
	configuration config.SSOProvider
	policy        config.SSOPolicy
	callbacks     map[string]bool
	client        *http.Client
	transport     *http.Transport
	slots         chan struct{}
}

func newGiteaProvider(c config.SSOProvider, policy config.SSOPolicy, callbacks []string, slots chan struct{}) (*giteaProvider, error) {
	bad := errors.New("SSO_PROVIDER_CONFIGURATION_INVALID")
	if (config.SSOConfiguration{Providers: []config.SSOProvider{c}}).Validate() != nil || policy.Validate() != nil || len(callbacks) < 1 || len(callbacks) > 2*config.SSOMaximumConnectors {
		return nil, bad
	}
	allowed := map[string]bool{}
	for _, value := range callbacks {
		u, e := url.Parse(value)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(value) > 2048 || strings.ContainsFunc(value, unicode.IsControl) || allowed[value] {
			return nil, bad
		}
		allowed[value] = true
	}
	var roots *x509.CertPool
	if c.CAFile != "" {
		raw, e := readPrivateFile(c.CAFile, maximumProviderCertificateBytes)
		roots = x509.NewCertPool()
		if e != nil || !roots.AppendCertsFromPEM(raw) {
			return nil, bad
		}
	} else {
		var e error
		roots, e = x509.SystemCertPool()
		if e != nil {
			return nil, bad
		}
	}
	origin, _ := url.Parse(c.Origin)
	port := origin.Port()
	if port == "" {
		port = "443"
	}
	expectedAddress := net.JoinHostPort(origin.Hostname(), port)
	addresses := append([]string(nil), c.DialAddresses...)
	dialer := &net.Dialer{Timeout: config.Seconds(policy.ConnectSeconds)}
	protocols := &http.Protocols{}
	protocols.SetHTTP1(true)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, TLSHandshakeTimeout: config.Seconds(policy.ConnectSeconds), ResponseHeaderTimeout: config.Seconds(policy.OperationSeconds), DisableCompression: true, MaxConnsPerHost: policy.MaxConcurrentExchanges, MaxIdleConns: policy.MaxConcurrentExchanges, MaxIdleConnsPerHost: policy.MaxConcurrentExchanges, IdleConnTimeout: config.Seconds(policy.OperationSeconds), Protocols: protocols}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != expectedAddress {
			return nil, errors.New("SSO_PROVIDER_DESTINATION_REJECTED")
		}
		for _, ip := range addresses {
			conn, e := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
			if e == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				break
			}
		}
		return nil, errors.New("SSO_PROVIDER_UNAVAILABLE")
	}
	if slots == nil {
		slots = make(chan struct{}, policy.MaxConcurrentExchanges)
	}
	if cap(slots) != policy.MaxConcurrentExchanges {
		transport.CloseIdleConnections()
		return nil, bad
	}
	p := &giteaProvider{configuration: c, policy: policy, callbacks: allowed, transport: transport, slots: slots}
	p.client = &http.Client{Transport: transport, Timeout: config.Seconds(policy.OperationSeconds), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return p, nil
}

func (p *giteaProvider) close() { p.transport.CloseIdleConnections() }

func (p *giteaProvider) authorizationURL(clientID, callback, state, verifier string) (string, error) {
	if !canonicalID(clientID) || !p.callbacks[callback] || !security.ValidToken(state) || !security.ValidToken(verifier) {
		return "", errors.New("SSO_AUTHORIZATION_INPUT_INVALID")
	}
	sum := sha256.Sum256([]byte(verifier))
	query := url.Values{"client_id": {clientID}, "redirect_uri": {callback}, "response_type": {"code"}, "scope": {GiteaScope}, "state": {state}, "code_challenge_method": {GiteaPKCEMethod}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}
	return p.configuration.Origin + GiteaAuthorizationPath + "?" + query.Encode(), nil
}

func safeOpaque(value string, maximum int) bool {
	return len(value) > 0 && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsFunc(value, func(c rune) bool { return unicode.IsControl(c) || unicode.IsSpace(c) })
}

func caseAliases(fields map[string]json.RawMessage, names ...string) bool {
	for key := range fields {
		for _, name := range names {
			if key != name && strings.EqualFold(key, name) {
				return true
			}
		}
	}
	return false
}

func requiredString(fields map[string]json.RawMessage, key string) (string, error) {
	var value string
	if json.Unmarshal(fields[key], &value) != nil || value == "" {
		return "", errors.New("SSO_PROVIDER_RESPONSE_INVALID")
	}
	return value, nil
}

// Requests never forward cookies/proxy configuration or provider-selected URLs. POST has no retry authority.
func (p *giteaProvider) request(ctx context.Context, method, path, clientID, secret, bearer string, form url.Values) (map[string]json.RawMessage, error) {
	if !(method == http.MethodPost && (path == GiteaTokenPath || path == GiteaIntrospectionPath) || method == http.MethodGet && path == GiteaUserPath) {
		return nil, errors.New("SSO_PROVIDER_DESTINATION_REJECTED")
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, e := http.NewRequestWithContext(ctx, method, p.configuration.Origin+path, body)
	if e != nil {
		return nil, errors.New("SSO_PROVIDER_UNAVAILABLE")
	}
	req.GetBody = nil
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	} else {
		req.SetBasicAuth(clientID, secret)
	}
	response, e := p.client.Do(req)
	if e != nil {
		return nil, errors.New("SSO_PROVIDER_UNAVAILABLE")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("SSO_PROVIDER_REJECTED")
	}
	media, parameters, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(response.Header.Values("Content-Type")) != 1 || len(parameters) > 1 || len(parameters) == 1 && !strings.EqualFold(parameters["charset"], "utf-8") || response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity" {
		return nil, errors.New("SSO_PROVIDER_RESPONSE_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, int64(p.policy.ResponseBytes)+1))
	if e != nil {
		return nil, errors.New("SSO_PROVIDER_UNAVAILABLE")
	}
	if len(raw) > p.policy.ResponseBytes {
		return nil, errors.New("SSO_PROVIDER_RESPONSE_INVALID")
	}
	fields, e := objectFields(raw, maximumProviderFields)
	if e != nil {
		return nil, errors.New("SSO_PROVIDER_RESPONSE_INVALID")
	}
	return fields, nil
}

func (p *giteaProvider) exchange(ctx context.Context, clientID, secret, callback, code, verifier string) (providerIdentity, error) {
	var out providerIdentity
	bad := errors.New("SSO_PROVIDER_RESPONSE_INVALID")
	if !canonicalID(clientID) || !p.callbacks[callback] || !safeOpaque(secret, maximumSecretBytes) || !safeOpaque(code, maximumAuthorizationCodeBytes) || !security.ValidToken(verifier) {
		return out, errors.New("SSO_AUTHORIZATION_INPUT_INVALID")
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		return out, errors.New("SSO_CAPACITY_EXCEEDED")
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(p.policy.OperationSeconds))
	defer cancel()
	token, e := p.request(ctx, http.MethodPost, GiteaTokenPath, clientID, secret, "", url.Values{"grant_type": {"authorization_code"}, "redirect_uri": {callback}, "code": {code}, "code_verifier": {verifier}})
	if e != nil {
		return out, e
	}
	if caseAliases(token, "access_token", "token_type", "expires_in") {
		return out, bad
	}
	access, e := requiredString(token, "access_token")
	if e != nil || !safeOpaque(access, p.policy.ResponseBytes) {
		return out, bad
	}
	kind, e := requiredString(token, "token_type")
	if e != nil || !strings.EqualFold(kind, "bearer") {
		return out, bad
	}
	var expires int64
	if json.Unmarshal(token["expires_in"], &expires) != nil || expires < 1 {
		return out, bad
	}
	introspection, e := p.request(ctx, http.MethodPost, GiteaIntrospectionPath, clientID, secret, "", url.Values{"token": {access}})
	if e != nil {
		return out, e
	}
	if caseAliases(introspection, "active", "scope", "iss", "aud", "sub") {
		return out, bad
	}
	var active bool
	if json.Unmarshal(introspection["active"], &active) != nil || !active {
		return out, errors.New("SSO_IDENTITY_REJECTED")
	}
	scope, e := requiredString(introspection, "scope")
	if e != nil || scope != GiteaScope {
		return out, errors.New("SSO_SCOPE_REJECTED")
	}
	issuer, e := requiredString(introspection, "iss")
	if e != nil || issuer != p.configuration.Issuer {
		return out, errors.New("SSO_IDENTITY_REJECTED")
	}
	var audiences []string
	if json.Unmarshal(introspection["aud"], &audiences) != nil {
		var single string
		if json.Unmarshal(introspection["aud"], &single) != nil {
			return out, bad
		}
		audiences = []string{single}
	}
	if len(audiences) != 1 || audiences[0] != clientID {
		return out, errors.New("SSO_IDENTITY_REJECTED")
	}
	subject, e := requiredString(introspection, "sub")
	if e != nil {
		return out, bad
	}
	id, e := strconv.ParseInt(subject, 10, 64)
	if e != nil || id < 1 || strconv.FormatInt(id, 10) != subject {
		return out, errors.New("SSO_IDENTITY_REJECTED")
	}
	user, e := p.request(ctx, http.MethodGet, GiteaUserPath, "", "", access, nil)
	if e != nil {
		return out, e
	}
	if caseAliases(user, "id", "active", "prohibit_login", "type") {
		return out, bad
	}
	var currentID int64
	var currentActive bool
	var prohibited *bool
	if json.Unmarshal(user["id"], &currentID) != nil || currentID != id || json.Unmarshal(user["active"], &currentActive) != nil || !currentActive || json.Unmarshal(user["prohibit_login"], &prohibited) != nil || prohibited == nil || *prohibited {
		return out, errors.New("SSO_IDENTITY_REJECTED")
	}
	userType, e := requiredString(user, "type")
	if e != nil || userType != "User" {
		return out, errors.New("SSO_IDENTITY_REJECTED")
	}
	// Email, name, avatar, groups and provider administrator status confer no local identity/authority.
	out.subject = subject
	// Display fields never confer identity or authority. A malformed optional
	// profile cannot change ordinary sign-in admission.
	for key, target := range map[string]*string{"login": &out.login, "full_name": &out.name} {
		var value string
		if json.Unmarshal(user[key], &value) == nil && len(value) <= 256 && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl) {
			*target = value
		}
	}
	return out, nil
}
