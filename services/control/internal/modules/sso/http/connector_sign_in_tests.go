package http

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

// connectorSignInTest handles only explicitly composed diagnostic routes. The
// surrounding identity transport retains audience, Origin, CSRF and License.
func (h *Handler) connectorSignInTest(w http.ResponseWriter, r *http.Request, parts []string, query url.Values, audience, session, requestID string) (any, int, error) {
	bad, missing := errors.New("VALIDATION_FAILED"), errors.New("RESOURCE_NOT_FOUND")
	if audience != "admin" || !validID(parts[5]) {
		return nil, 0, missing
	}
	accounts := h.Service.Identity
	actor, err := accounts.Authenticate(r.Context(), session, audience)
	callback := len(parts) == 8 && parts[7] == "callback" && r.Method == http.MethodGet
	if err != nil {
		if callback {
			http.Redirect(w, r, "/sso?test_error="+resultError(err), http.StatusSeeOther)
			return nil, 0, nil
		}
		return nil, 0, err
	}
	if !actor.Me.User.IsPlatformAdmin {
		return nil, 0, missing
	}
	if callback {
		if len(r.URL.RawQuery) > httpserver.MaxJSONBytes || !only(query, "state", "code", "error", "error_description") || !security.ValidToken(query.Get("state")) {
			return nil, 0, bad
		}
		_, hasCode := query["code"]
		_, hasError := query["error"]
		_, hasDescription := query["error_description"]
		if hasCode == hasError || hasDescription && !hasError || hasCode && (query.Get("code") == "" || len(query.Get("code")) > maximumCallbackCodeBytes) || hasError && (query.Get("error") == "" || len(query.Get("error")) > maximumCallbackErrorBytes || len(query.Get("error_description")) > maximumCallbackDescriptionBytes) {
			return nil, 0, bad
		}
		if err = h.rate(r, "admin", "callback"); err != nil {
			return nil, 0, err
		}
		cookies, e := httpserver.NewCookiePolicy(accounts.Config.Server.EffectiveTransportMode())
		if e != nil {
			return nil, 0, e
		}
		browser, ok := cookies.Read(r, "admin", httpserver.SSOFlowCookie)
		if !ok {
			return nil, 0, bad
		}
		result, e := h.ConnectorTests.Complete(r.Context(), actor, parts[5], query.Get("state"), browser, query.Get("code"), hasError, requestID)
		if result.ID != "" {
			cookies.Set(w, "admin", httpserver.SSOFlowCookie, "", -1)
		}
		target := "/sso?test_id=" + result.ID
		if e != nil {
			target = "/sso?test_error=" + resultError(e)
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return nil, 0, nil
	}
	if len(query) != 0 {
		return nil, 0, bad
	}
	if len(parts) == 7 && r.Method == http.MethodPost {
		key, e := creationKey(r)
		if e != nil {
			return nil, 0, e
		}
		var input sso.ConnectorSignInTestInput
		if e = decode(r, &input); e != nil {
			return nil, 0, e
		}
		if e = h.rate(r, "admin", "start"); e != nil {
			return nil, 0, e
		}
		out, e := h.ConnectorTests.Start(r.Context(), actor, parts[5], input, key, requestID)
		if e != nil {
			return nil, 0, e
		}
		cookies, e := httpserver.NewCookiePolicy(accounts.Config.Server.EffectiveTransportMode())
		if e != nil {
			return nil, 0, e
		}
		cookies.Set(w, "admin", httpserver.SSOFlowCookie, out.BrowserToken, accounts.Config.Policy().SSO.FlowSeconds)
		return out, 200, nil
	}
	if len(parts) == 8 && r.Method == http.MethodGet && validID(parts[7]) {
		out, e := h.ConnectorTests.Get(r.Context(), actor, parts[5], parts[7])
		return out, 200, e
	}
	return nil, 0, missing
}
