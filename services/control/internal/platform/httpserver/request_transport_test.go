package httpserver

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func transportFixture(mode config.TransportMode) config.ServerConfiguration {
	scheme := "https"
	if mode == config.TransportHTTP {
		scheme = "http"
	}
	server := config.ServerConfiguration{TransportMode: mode, ListenAddress: "127.0.0.1:8443", PortalOrigin: scheme + "://portal.test", AdminOrigin: scheme + "://admin.test"}
	if mode == config.TransportTrustedProxyHTTPS {
		server.TrustedProxyCIDRs = []string{"127.0.0.1/32", "::1/128"}
	}
	return server
}

func transportRequest(mode config.TransportMode) *http.Request {
	scheme := "https"
	if mode == config.TransportHTTP || mode == config.TransportTrustedProxyHTTPS {
		scheme = "http"
	}
	r := httptest.NewRequest(http.MethodGet, scheme+"://portal.test/", nil)
	// A real backend receives the origin form; the public proxy authority is
	// carried by Host and the exact forwarding triple, never its dial URL.
	r.URL.Scheme, r.URL.Host = "", ""
	r.RemoteAddr = "127.0.0.1:34567"
	if mode == config.TransportDirectHTTPS || mode == "" {
		r.TLS = &tls.ConnectionState{ServerName: "portal.test"}
	}
	if mode == config.TransportTrustedProxyHTTPS {
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "portal.test")
		r.Header.Set("X-Forwarded-For", "192.0.2.27")
	}
	return r
}

func TestTransportEffectiveConnectionAndCookiePolicy(t *testing.T) {
	for _, mode := range []config.TransportMode{"", config.TransportHTTP, config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		t.Run(string(mode), func(t *testing.T) {
			r, server := transportRequest(mode), transportFixture(mode)
			actualTLS := r.TLS
			value, e := ResolveBrowserRequest(r, server)
			if e != nil || value.Audience != "portal" || value.Origin != server.PortalOrigin || value.Authority != "portal.test" || value.SocketPeerIP != "127.0.0.1" {
				t.Fatal("effective connection", value, e)
			}
			if mode == config.TransportTrustedProxyHTTPS {
				if !value.Forwarded || value.ClientIP != "192.0.2.27" || actualTLS != nil {
					t.Fatal("proxy identity was not independently retained")
				}
			} else if value.Forwarded || value.ClientIP != value.SocketPeerIP {
				t.Fatal("direct client identity was rewritten")
			}
			r = WithRequestTransport(r, value)
			if r.TLS != actualTLS || RequestClientIP(r) != value.ClientIP {
				t.Fatal("effective context changed socket TLS or lost client identity")
			}
			cookies, e := NewCookiePolicy(mode)
			if e != nil {
				t.Fatal(e)
			}
			for _, purpose := range []CookiePurpose{SessionCookie, CSRFContextCookie, SSOFlowCookie} {
				w := httptest.NewRecorder()
				cookies.Set(w, value.Audience, purpose, "synthetic-value", 60)
				values := w.Result().Cookies()
				if len(values) != 1 {
					t.Fatal("cookie not issued")
				}
				cookie := values[0]
				if cookie.Secure != (value.Scheme == "https") || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != 60 || (cookie.SameSite == http.SameSiteStrictMode) != (purpose == CSRFContextCookie) {
					t.Fatal("unsafe cookie attributes", cookie)
				}
				if strings.HasPrefix(cookie.Name, "__Host-") != cookie.Secure || strings.HasPrefix(cookie.Name, "projectone_http_") == cookie.Secure {
					t.Fatal("mode cookie name", cookie.Name)
				}
				read := transportRequest(mode)
				read.AddCookie(cookie)
				if token, ok := cookies.Read(read, "portal", purpose); !ok || token != "synthetic-value" {
					t.Fatal("issued cookie cannot be read")
				}
				if token, ok := cookies.Read(read, "admin", purpose); !ok || token != "" {
					t.Fatal("audience cookie crossed port-independent namespace")
				}
				read.AddCookie(cookie)
				if _, ok := cookies.Read(read, "portal", purpose); ok {
					t.Fatal("duplicate cookie accepted")
				}
			}
			w := httptest.NewRecorder()
			cookies.ClearIdentity(w, "portal")
			for _, cookie := range w.Result().Cookies() {
				if cookie.Value != "" || cookie.MaxAge != -1 || cookie.Secure != (value.Scheme == "https") {
					t.Fatal("cookie clear differs from issue policy")
				}
			}
		})
	}
	secure, _ := NewCookiePolicy(config.TransportDirectHTTPS)
	plain, _ := NewCookiePolicy(config.TransportHTTP)
	r := transportRequest(config.TransportHTTP)
	r.AddCookie(&http.Cookie{Name: secure.Name("portal", SessionCookie), Value: "old-secure-session"})
	if value, ok := plain.Read(r, "portal", SessionCookie); !ok || value != "" {
		t.Fatal("HTTP accepted alternate secure-mode name")
	}
	if _, e := NewCookiePolicy("false"); e == nil {
		t.Fatal("unknown mode cookie policy accepted")
	}
	if name := (CookiePolicy{}).Name("portal", SessionCookie); name != "" {
		t.Fatal("uninitialized cookie policy accepted")
	}
}

func TestTransportAuthorityAndActualTLSRefusals(t *testing.T) {
	for _, item := range []struct {
		name string
		edit func(*http.Request)
		code string
	}{
		{"foreign_host", func(r *http.Request) { r.Host = "foreign.test" }, "MISDIRECTED_REQUEST"},
		{"userinfo", func(r *http.Request) { r.Host = "user@portal.test" }, "MISDIRECTED_REQUEST"},
		{"empty_port", func(r *http.Request) { r.Host = "portal.test:" }, "MISDIRECTED_REQUEST"},
		{"leading_zero_port", func(r *http.Request) { r.Host = "portal.test:0443" }, "MISDIRECTED_REQUEST"},
		{"path_authority", func(r *http.Request) { r.Host = "portal.test/path" }, "MISDIRECTED_REQUEST"},
		{"backslash", func(r *http.Request) { r.Host = "portal.test\\foreign.test" }, "MISDIRECTED_REQUEST"},
		{"no_tls", func(r *http.Request) { r.TLS = nil }, "ORIGIN_REJECTED"},
		{"missing_dns_sni", func(r *http.Request) { r.TLS.ServerName = "" }, "MISDIRECTED_REQUEST"},
		{"cross_host_sni", func(r *http.Request) { r.TLS.ServerName = "admin.test" }, "MISDIRECTED_REQUEST"},
		{"absolute_target_confusion", func(r *http.Request) { r.URL.Scheme, r.URL.Host = "https", "admin.test" }, "MISDIRECTED_REQUEST"},
		{"invalid_socket_peer", func(r *http.Request) { r.RemoteAddr = "caller.test:4000" }, "ORIGIN_REJECTED"},
	} {
		t.Run(item.name, func(t *testing.T) {
			r := transportRequest(config.TransportDirectHTTPS)
			item.edit(r)
			_, e := ResolveBrowserRequest(r, transportFixture(config.TransportDirectHTTPS))
			if e == nil || e.Error() != item.code {
				t.Fatal("refusal", e)
			}
		})
	}
	for _, mode := range []config.TransportMode{config.TransportHTTP, config.TransportDirectHTTPS} {
		for _, header := range []string{"Forwarded", "X-Forwarded", "X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Forwarded-Port", "X-Real-IP", "X-Forwarded-Unrecognized"} {
			r := transportRequest(mode)
			r.Header.Set(header, "spoofed")
			if _, e := ResolveBrowserRequest(r, transportFixture(mode)); e == nil || e.Error() != "ORIGIN_REJECTED" {
				t.Fatal("direct forwarding namespace accepted", mode, header, e)
			}
		}
	}
	r := transportRequest(config.TransportDirectHTTPS)
	r.Host, r.TLS.ServerName = "PORTAL.TEST:443", "portal.test"
	if value, e := ResolveBrowserRequest(r, transportFixture(config.TransportDirectHTTPS)); e != nil || value.Audience != "portal" {
		t.Fatal("case/default port created another origin", value, e)
	}
	for _, address := range []string{"127.0.0.1", "[::1]"} {
		server := transportFixture(config.TransportDirectHTTPS)
		server.PortalOrigin = "https://" + address
		r := transportRequest(config.TransportDirectHTTPS)
		r.Host, r.TLS.ServerName = address, ""
		if _, e := ResolveBrowserRequest(r, server); e != nil {
			t.Fatal("IP authority requires no manufactured SNI", address, e)
		}
		r.TLS.ServerName = "portal.test"
		if _, e := ResolveBrowserRequest(r, server); e == nil {
			t.Fatal("IP request accepted DNS SNI")
		}
	}
}

func TestTransportSingleHopProxyRefusals(t *testing.T) {
	for _, item := range []struct {
		name string
		edit func(*http.Request, *config.ServerConfiguration)
	}{
		{"untrusted_peer", func(r *http.Request, s *config.ServerConfiguration) { r.RemoteAddr = "192.0.2.1:4000" }},
		{"missing_peer_policy", func(r *http.Request, s *config.ServerConfiguration) { s.TrustedProxyCIDRs = nil }},
		{"trust_all", func(r *http.Request, s *config.ServerConfiguration) { s.TrustedProxyCIDRs = []string{"0.0.0.0/0"} }},
		{"tls_backhaul", func(r *http.Request, s *config.ServerConfiguration) { r.TLS = &tls.ConnectionState{} }},
		{"missing_proto", func(r *http.Request, s *config.ServerConfiguration) { r.Header.Del("X-Forwarded-Proto") }},
		{"duplicate_proto", func(r *http.Request, s *config.ServerConfiguration) { r.Header.Add("X-Forwarded-Proto", "https") }},
		{"duplicate_case", func(r *http.Request, s *config.ServerConfiguration) {
			r.Header["x-forwarded-proto"] = []string{"https"}
		}},
		{"plaintext_proto", func(r *http.Request, s *config.ServerConfiguration) { r.Header.Set("X-Forwarded-Proto", "http") }},
		{"host_mismatch", func(r *http.Request, s *config.ServerConfiguration) { r.Header.Set("X-Forwarded-Host", "admin.test") }},
		{"host_chain", func(r *http.Request, s *config.ServerConfiguration) {
			r.Header.Set("X-Forwarded-Host", "portal.test,portal.test")
		}},
		{"client_chain", func(r *http.Request, s *config.ServerConfiguration) {
			r.Header.Set("X-Forwarded-For", "192.0.2.1,127.0.0.1")
		}},
		{"client_port", func(r *http.Request, s *config.ServerConfiguration) {
			r.Header.Set("X-Forwarded-For", "192.0.2.1:4000")
		}},
		{"client_zone", func(r *http.Request, s *config.ServerConfiguration) { r.Header.Set("X-Forwarded-For", "fe80::1%en0") }},
		{"client_noncanonical", func(r *http.Request, s *config.ServerConfiguration) { r.Header.Set("X-Forwarded-For", "2001:DB8::1") }},
		{"client_unspecified", func(r *http.Request, s *config.ServerConfiguration) { r.Header.Set("X-Forwarded-For", "0.0.0.0") }},
		{"client_mapped", func(r *http.Request, s *config.ServerConfiguration) {
			r.Header.Set("X-Forwarded-For", "::ffff:192.0.2.1")
		}},
		{"standard_forwarded", func(r *http.Request, s *config.ServerConfiguration) { r.Header.Set("Forwarded", "proto=https") }},
		{"legacy_client_ip", func(r *http.Request, s *config.ServerConfiguration) { r.Header.Set("X-Real-IP", "192.0.2.1") }},
		{"extra_forwarded", func(r *http.Request, s *config.ServerConfiguration) { r.Header.Set("X-Forwarded-Port", "443") }},
	} {
		t.Run(item.name, func(t *testing.T) {
			r, server := transportRequest(config.TransportTrustedProxyHTTPS), transportFixture(config.TransportTrustedProxyHTTPS)
			item.edit(r, &server)
			if _, e := ResolveBrowserRequest(r, server); e == nil || e.Error() != "ORIGIN_REJECTED" {
				t.Fatal("invalid single-hop proxy admitted", e)
			}
		})
	}
	r := transportRequest(config.TransportTrustedProxyHTTPS)
	r.RemoteAddr = "[::1]:34567"
	r.Header.Set("X-Forwarded-For", "2001:db8::27")
	if value, e := ResolveBrowserRequest(r, transportFixture(config.TransportTrustedProxyHTTPS)); e != nil || value.ClientIP != "2001:db8::27" || value.SocketPeerIP != "::1" || r.TLS != nil {
		t.Fatal("canonical IPv6 peer/client or actual backend TLS lost", value, e)
	}
}
