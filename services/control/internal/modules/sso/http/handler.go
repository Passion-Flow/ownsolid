package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/google/uuid"
)

type Handler struct {
	Service *sso.Service
	// ConnectorTests selects the unpublished diagnostic adapter after explicit storage admission.
	ConnectorTests *sso.ConnectorSignInTests
}

const (
	maximumCallbackCodeBytes        = 4096
	maximumCallbackErrorBytes       = 128
	maximumCallbackDescriptionBytes = 4096
)

func Owns(path string) bool {
	return strings.HasPrefix(path, "/api/v1/auth/sso/") || strings.HasPrefix(path, "/api/v1/admin/sso/") || path == "/api/v1/account/sso/bindings" || strings.HasPrefix(path, "/api/v1/account/sso/")
}

func validID(value string) bool {
	id, e := uuid.Parse(value)
	return e == nil && id != uuid.Nil && id.String() == value
}

// decode enforces exact, required property names, including explicit nullable
// fields. Go's ordinary struct decoder alone accepts case aliases and absent fields.
func decode(r *http.Request, out any) error {
	media, parameters, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 || len(parameters) > 1 || len(parameters) == 1 && parameters["charset"] != "utf-8" {
		return errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, httpserver.MaxJSONBytes+1))
	if e != nil || !utf8.Valid(raw) {
		return errors.New("VALIDATION_FAILED")
	}
	if len(raw) > httpserver.MaxJSONBytes {
		return errors.New("PAYLOAD_TOO_LARGE")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, e := decoder.Token()
	if e != nil || token != json.Delim('{') {
		return errors.New("VALIDATION_FAILED")
	}
	typ := reflect.TypeOf(out).Elem()
	expected := map[string]reflect.Kind{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		expected[field.Tag.Get("json")] = field.Type.Kind()
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, e = decoder.Token()
		if e != nil {
			return errors.New("VALIDATION_FAILED")
		}
		name, ok := token.(string)
		kind, exists := expected[name]
		if !ok || !exists || seen[name] {
			return errors.New("VALIDATION_FAILED")
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) && kind != reflect.Pointer {
			return errors.New("VALIDATION_FAILED")
		}
	}
	if token, e = decoder.Token(); e != nil || token != json.Delim('}') || len(seen) != len(expected) {
		return errors.New("VALIDATION_FAILED")
	}
	if _, e = decoder.Token(); e != io.EOF {
		return errors.New("VALIDATION_FAILED")
	}
	if json.Unmarshal(raw, out) != nil {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}

func creationKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 || !validID(values[0]) {
		return "", errors.New("VALIDATION_FAILED")
	}
	return values[0], nil
}
func only(query url.Values, allowed ...string) bool {
	for key, values := range query {
		if len(values) != 1 {
			return false
		}
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func remote(r *http.Request) string { return httpserver.RequestClientIP(r) }

func (h *Handler) rate(r *http.Request, audience, kind string) error {
	p := h.Service.Identity.Config.Policy().SSO
	count, window := p.StartIPCount, p.StartWindowSeconds
	if kind == "callback" {
		count, window = p.CallbackIPCount, p.CallbackWindowSeconds
	} else if kind == "password" {
		count, window = p.PasswordIPCount, p.PasswordWindowSeconds
	}
	_, e := h.Service.Identity.Admit(r.Context(), identity.Limit{Purpose: "sso." + kind + ".ip." + audience, Identifier: remote(r), Count: count, Window: config.Seconds(window)})
	return e
}

// Handle runs behind the existing validated effective audience, Origin and CSRF
// boundary. The callback is a GET protected by single-use state/browser/PKCE intent.
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, audience, session, requestID string) (any, int, error) {
	bad, missing := errors.New("VALIDATION_FAILED"), errors.New("RESOURCE_NOT_FOUND")
	if h.Service == nil {
		return nil, 0, errors.New("SERVICE_NOT_READY")
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.URL.RawPath != "" || r.URL.Path != "/"+strings.Join(parts, "/") || !Owns(r.URL.Path) {
		return nil, 0, missing
	}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, 0, bad
	}
	if h.ConnectorTests != nil && len(parts) >= 7 && len(parts) <= 8 && parts[2] == "admin" && parts[3] == "sso" && parts[4] == "connectors" && parts[6] == "sign-in-tests" {
		return h.connectorSignInTest(w, r, parts, query, audience, session, requestID)
	}
	ctx := r.Context()
	accounts := h.Service.Identity
	cookies, e := httpserver.NewCookiePolicy(accounts.Config.Server.EffectiveTransportMode())
	if e != nil {
		return nil, 0, e
	}
	if len(parts) == 6 && parts[2] == "auth" && parts[3] == "sso" && parts[5] == "callback" && r.Method == http.MethodGet {
		if !validID(parts[4]) || len(r.URL.RawQuery) > httpserver.MaxJSONBytes || !only(query, "state", "code", "error", "error_description") || !security.ValidToken(query.Get("state")) {
			return nil, 0, bad
		}
		_, hasCode := query["code"]
		_, hasError := query["error"]
		_, hasDescription := query["error_description"]
		if hasCode == hasError || hasDescription && !hasError || hasCode && (query.Get("code") == "" || len(query.Get("code")) > maximumCallbackCodeBytes) || hasError && (query.Get("error") == "" || len(query.Get("error")) > maximumCallbackErrorBytes || len(query.Get("error_description")) > maximumCallbackDescriptionBytes) {
			return nil, 0, bad
		}
		if e = h.rate(r, audience, "callback"); e != nil {
			return nil, 0, e
		}
		browser, ok := cookies.Read(r, audience, httpserver.SSOFlowCookie)
		if !ok {
			return nil, 0, bad
		}
		// Callback finishes accepted work. License integrity is observed outside
		// the business transaction;expiry alone does not abandon an accepted flow.
		if e := accounts.RequireBusinessIntegrity(ctx); e != nil {
			return nil, 0, e
		}
		issued, mode, e := h.Service.Complete(ctx, parts[4], audience, query.Get("state"), browser, session, query.Get("code"), hasError, requestID)
		if mode == "" {
			if e != nil && e.Error() == string(transaction.CommitUnknown) {
				// Claim returned no credentials or mode. End this browser binding
				// and show the safe recovery page without retaining code/state URL.
				cookies.Set(w, audience, httpserver.SSOFlowCookie, "", -1)
				http.Redirect(w, r, "/login/sso-result?error="+string(transaction.CommitUnknown), http.StatusSeeOther)
				return nil, 0, nil
			}
			// A rejected pending/terminal flow can still select its fixed
			// presentation when every immutable browser/callback proof matches.
			// Never repeat claim/exchange, including after an uncertain commit.
			if e != nil {
				mode = h.Service.BoundResultMode(ctx, parts[4], audience, query.Get("state"), browser, session)
			}
			if mode == "" {
				return nil, 0, e
			}
		}
		cookies.Set(w, audience, httpserver.SSOFlowCookie, "", -1)
		path := "/login/sso-result"
		result := "authenticated"
		if mode == sso.ModeLink {
			path, result = "/account/sso-result", "linked"
		}
		if e != nil {
			// Complete returns only stable first-party codes after a claim. Keep
			// the redirect allowlist explicit even if a future service error changes.
			code := resultError(e)
			http.Redirect(w, r, path+"?error="+code, http.StatusSeeOther)
			return nil, 0, nil
		}
		cookies.Set(w, audience, httpserver.SessionCookie, issued.Token, int(accounts.Absolute(audience)/time.Second))
		cookies.Set(w, audience, httpserver.CSRFContextCookie, "", -1)
		http.Redirect(w, r, path+"?result="+result, http.StatusSeeOther)
		return nil, 0, nil
	}
	if r.URL.Path == "/api/v1/auth/sso/connectors" && r.Method == http.MethodGet {
		if len(query) != 0 {
			return nil, 0, bad
		}
		items, e := h.Service.PublicConnectors(ctx)
		return map[string]any{"items": items}, 200, e
	}
	if len(parts) == 6 && parts[2] == "auth" && parts[3] == "sso" && parts[5] == "starts" && r.Method == http.MethodPost {
		if len(query) != 0 || !validID(parts[4]) {
			return nil, 0, bad
		}
		var input sso.StartInput
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		if e = h.rate(r, audience, "start"); e != nil {
			return nil, 0, e
		}
		var actor *identity.Auth
		if input.Mode == sso.ModeLink {
			if e = h.rate(r, audience, "password"); e != nil {
				return nil, 0, e
			}
			a, ae := accounts.Authenticate(ctx, session, audience)
			if ae != nil {
				return nil, 0, ae
			}
			if e = accounts.RequireNewBusiness(ctx); e != nil {
				return nil, 0, e
			}
			actor = &a
		}
		out, e := h.Service.Start(ctx, parts[4], audience, session, actor, input, requestID)
		if e != nil {
			return nil, 0, e
		}
		cookies.Set(w, audience, httpserver.SSOFlowCookie, out.BrowserToken, accounts.Config.Policy().SSO.FlowSeconds)
		return out, 200, nil
	}
	a, e := accounts.Authenticate(ctx, session, audience)
	if e != nil {
		return nil, 0, e
	}
	if len(parts) >= 4 && parts[2] == "admin" && (audience != "admin" || !a.Me.User.IsPlatformAdmin) {
		return nil, 0, missing
	}
	if r.URL.Path == "/api/v1/admin/sso/connectors" && r.Method == http.MethodGet {
		if !only(query, "limit", "cursor") {
			return nil, 0, bad
		}
		limit := accounts.Config.Policy().UI.PageSize
		if raw, exists := query["limit"]; exists {
			limit, e = strconv.Atoi(raw[0])
			if e != nil || strconv.Itoa(limit) != raw[0] {
				return nil, 0, bad
			}
		}
		out, e := h.Service.ListConnectors(ctx, a, limit, query.Get("cursor"))
		return out, 200, e
	}
	if r.URL.Path == "/api/v1/admin/sso/connectors" && r.Method == http.MethodPost {
		if len(query) != 0 {
			return nil, 0, bad
		}
		key, e := creationKey(r)
		if e != nil {
			return nil, 0, e
		}
		var input sso.CreateConnector
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.CreateConnector(ctx, a, input, key, requestID)
		return out, 201, e
	}
	if len(parts) == 6 && parts[2] == "admin" && parts[3] == "sso" && parts[4] == "connectors" && r.Method == http.MethodPatch {
		if len(query) != 0 || !validID(parts[5]) {
			return nil, 0, bad
		}
		key, e := creationKey(r)
		if e != nil {
			return nil, 0, e
		}
		var input sso.UpdateConnector
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.UpdateConnector(ctx, a, parts[5], input, key, requestID)
		return out, 200, e
	}
	if r.URL.Path == "/api/v1/admin/sso/configuration" && r.Method == http.MethodGet {
		if len(query) != 0 {
			return nil, 0, bad
		}
		out, e := h.Service.Configuration(a)
		return out, 200, e
	}
	// A check changes nothing and may be repeated, so it takes neither a body nor a key.
	if len(parts) == 7 && parts[2] == "admin" && parts[3] == "sso" && parts[4] == "connectors" && parts[6] == "tests" && r.Method == http.MethodPost {
		if len(query) != 0 || !validID(parts[5]) || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
			return nil, 0, bad
		}
		out, e := h.Service.TestConnector(ctx, a, parts[5], requestID)
		return out, 200, e
	}
	if r.URL.Path == "/api/v1/account/sso/bindings" && r.Method == http.MethodGet {
		if len(query) != 0 {
			return nil, 0, bad
		}
		items, e := h.Service.ListBindings(ctx, a)
		return map[string]any{"items": items}, 200, e
	}
	if len(parts) == 7 && parts[2] == "account" && parts[3] == "sso" && parts[4] == "bindings" && parts[6] == "unlink" && r.Method == http.MethodPost {
		if len(query) != 0 || !validID(parts[5]) {
			return nil, 0, bad
		}
		key, e := creationKey(r)
		if e != nil {
			return nil, 0, e
		}
		var input sso.UnlinkBinding
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		if e = h.rate(r, audience, "password"); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Unlink(ctx, a, parts[5], input, key, requestID)
		if e == nil {
			if _, ae := accounts.Authenticate(ctx, session, audience); ae != nil && ae.Error() == "AUTH_SESSION_EXPIRED" {
				cookies.Set(w, audience, httpserver.SessionCookie, "", -1)
				cookies.Set(w, audience, httpserver.CSRFContextCookie, "", -1)
			}
		}
		return out, 200, e
	}
	return nil, 0, missing
}

func resultError(e error) string {
	switch e.Error() {
	case "STORAGE_COMMIT_OUTCOME_UNKNOWN", "SSO_PROVIDER_DENIED", "SSO_PROVIDER_UNAVAILABLE", "SSO_PROVIDER_REJECTED", "SSO_PROVIDER_RESPONSE_INVALID", "SSO_SCOPE_REJECTED", "SSO_IDENTITY_REJECTED", "SSO_CAPACITY_EXCEEDED", "SSO_BINDING_REQUIRED", "SSO_BINDING_CONFLICT", "SSO_BINDING_LIMIT", "SSO_BROWSER_CHANGED", "SSO_PASSWORD_CHANGED", "SSO_FLOW_UNUSABLE", "SSO_FLOW_EXPIRED", "AUTH_SESSION_EXPIRED", "AUTH_INVALID_CREDENTIALS", "STORAGE_UNAVAILABLE":
		return e.Error()
	default:
		return "SSO_FLOW_UNUSABLE"
	}
}
