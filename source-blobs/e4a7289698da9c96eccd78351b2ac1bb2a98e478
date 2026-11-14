package httpserver

import (
	"errors"
	"net/http"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type CookiePurpose string

const (
	SessionCookie     CookiePurpose = "session"
	CSRFContextCookie CookiePurpose = "csrf_context"
	SSOFlowCookie     CookiePurpose = "sso_flow"
)

// CookiePolicy is derived only from the validated advertised transport mode.
// Both identity and SSO use it for read, issue and clear without name fallback.
type CookiePolicy struct {
	secure bool
	prefix string
}

func NewCookiePolicy(mode config.TransportMode) (CookiePolicy, error) {
	switch mode {
	case "", config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS:
		return CookiePolicy{secure: true, prefix: "__Host-"}, nil
	case config.TransportHTTP:
		return CookiePolicy{prefix: "projectone_http_"}, nil
	default:
		return CookiePolicy{}, errors.New("CONFIG_TRANSPORT_INVALID")
	}
}

func (p CookiePolicy) Name(audience string, purpose CookiePurpose) string {
	if p.prefix == "" || audience != "portal" && audience != "admin" || purpose != SessionCookie && purpose != CSRFContextCookie && purpose != SSOFlowCookie {
		return ""
	}
	return p.prefix + audience + "_" + string(purpose)
}

func (p CookiePolicy) Read(r *http.Request, audience string, purpose CookiePurpose) (string, bool) {
	name := p.Name(audience, purpose)
	if name == "" {
		return "", false
	}
	value, count := "", 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == name {
			value, count = cookie.Value, count+1
		}
	}
	return value, count <= 1
}

func (p CookiePolicy) Set(w http.ResponseWriter, audience string, purpose CookiePurpose, value string, age int) {
	name := p.Name(audience, purpose)
	if name == "" {
		return
	}
	sameSite := http.SameSiteLaxMode
	if purpose == CSRFContextCookie {
		sameSite = http.SameSiteStrictMode
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: p.secure, HttpOnly: true, SameSite: sameSite, MaxAge: age})
}

func (p CookiePolicy) ClearIdentity(w http.ResponseWriter, audience string) {
	p.Set(w, audience, SessionCookie, "", -1)
	p.Set(w, audience, CSRFContextCookie, "", -1)
}
