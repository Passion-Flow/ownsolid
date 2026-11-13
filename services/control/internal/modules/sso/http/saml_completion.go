package http

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

func samlCompletionIDs(path string) (string, string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 9 || path != "/"+strings.Join(parts, "/") || parts[0] != "api" || parts[1] != "v1" || parts[2] != "sso" || parts[3] != "saml" || parts[4] != "connectors" || parts[6] != "flows" || parts[8] != "complete" || !validID(parts[5]) || !validID(parts[7]) {
		return "", "", false
	}
	return parts[5], parts[7], true
}

// SAMLCompletionPath matches only the proposed canonical first-party result path.
// It includes no ACS, login, status or metadata route and registers nothing.
func SAMLCompletionPath(path string) bool {
	_, _, ok := samlCompletionIDs(path)
	return ok
}

func samlCompletionBrowserInput(r *http.Request, audience string, cookies httpserver.CookiePolicy) (sso.SAMLCompleteInput, error) {
	connector, flow, ok := samlCompletionIDs(r.URL.Path)
	if !ok || r.Method != http.MethodPost || audience != "portal" {
		return sso.SAMLCompleteInput{}, errors.New("RESOURCE_NOT_FOUND")
	}
	if r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Fragment != "" {
		return sso.SAMLCompleteInput{}, errors.New("VALIDATION_FAILED")
	}
	key, e := creationKey(r)
	if e != nil {
		return sso.SAMLCompleteInput{}, e
	}
	if key != flow {
		return sso.SAMLCompleteInput{}, errors.New("VALIDATION_FAILED")
	}
	var body struct{}
	if e = decode(r, &body); e != nil {
		return sso.SAMLCompleteInput{}, e
	}
	previous, ok := cookies.Read(r, audience, httpserver.SessionCookie)
	if !ok || !security.ValidToken(previous) {
		return sso.SAMLCompleteInput{}, errors.New("AUTH_REQUIRED")
	}
	continuation, ok := cookies.Read(r, audience, httpserver.SSOFlowCookie)
	if !ok || !security.ValidToken(continuation) {
		return sso.SAMLCompleteInput{}, errors.New("SSO_FLOW_INVALID")
	}
	return sso.SAMLCompleteInput{ConnectorID: connector, Audience: audience, FlowID: flow, Continuation: continuation, PreviousSession: previous}, nil
}

// HandleSAMLCompletion runs only after Identity's ordinary validated transport,
// Origin, Fetch Metadata and CSRF boundary. Default composition leaves it closed.
// The result is the ordinary Me projection; an opaque session stays cookie-only.
func (h *Handler) HandleSAMLCompletion(w http.ResponseWriter, r *http.Request, audience, requestID string) (any, int, error) {
	if h == nil || h.Service == nil || h.Service.Identity == nil || !h.SAMLCompletion.MatchesService(h.Service) {
		return nil, 0, errors.New("SERVICE_NOT_READY")
	}
	accounts := h.Service.Identity
	if accounts.Config.Server.EffectiveTransportMode() == config.TransportHTTP {
		return nil, 0, errors.New("SSO_DISABLED")
	}
	cookies, e := httpserver.NewCookiePolicy(accounts.Config.Server.EffectiveTransportMode())
	if e != nil {
		return nil, 0, e
	}
	input, e := samlCompletionBrowserInput(r, audience, cookies)
	if e != nil {
		return nil, 0, e
	}
	if e = h.rate(r, audience, "callback"); e != nil {
		return nil, 0, e
	}
	// License observation requires an authenticated actor before its independent
	// transaction. Completion still rechecks the exact captured actor under fences.
	if _, e = accounts.Authenticate(identity.WithoutSessionTouch(r.Context()), input.PreviousSession, audience); e != nil {
		return nil, 0, e
	}
	// A signed result finishes an already accepted intent. Integrity is required
	// even after license expiry; no global new-business/recovery matcher changes.
	if e = accounts.RequireBusinessIntegrity(r.Context()); e != nil {
		return nil, 0, e
	}
	issued, e := h.SAMLCompletion.Complete(r.Context(), input, requestID)
	if e != nil {
		outcome, classified := transaction.CommitOutcome(e)
		if classified && outcome == transaction.CommitUnknown {
			cookies.Set(w, audience, httpserver.SSOFlowCookie, "", -1)
		}
		return nil, 0, e
	}
	cookies.Set(w, audience, httpserver.SessionCookie, issued.Token, int(accounts.Absolute(audience)/time.Second))
	cookies.Set(w, audience, httpserver.SSOFlowCookie, "", -1)
	cookies.Set(w, audience, httpserver.CSRFContextCookie, "", -1)
	return issued.Me, http.StatusOK, nil
}
