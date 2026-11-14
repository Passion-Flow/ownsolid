package httpserver

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func machineRequestConfiguration(origin string) config.MachineServerConfiguration {
	return config.MachineServerConfiguration{ListenAddress: "127.0.0.1:19443", Origin: origin,
		TLSCertificateFile: "/synthetic-machine/certificate.pem", TLSPrivateKeyFile: "/synthetic-machine/private-key.pem"}
}

func machineRequestFixture() *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://localhost:19443/agent/v1/enroll", nil)
	r.URL.Scheme, r.URL.Host = "", ""
	r.RemoteAddr = "127.0.0.1:34567"
	r.TLS = &tls.ConnectionState{ServerName: "localhost"}
	return r
}

// These controlled request objects test selection only. The listener tests below
// separately obtain actual TLS connection state through normal CA verification.
func TestMachineRequestTLSAuthorityAndSocketContext(t *testing.T) {
	for _, item := range []struct{ name, origin, host, sni, peer, ip string }{
		{"dns", "https://localhost:19443", "LOCALHOST:19443", "localhost", "127.0.0.1:34567", "127.0.0.1"},
		{"ipv4", "https://127.0.0.1:19443", "127.0.0.1:19443", "", "127.0.0.1:34567", "127.0.0.1"},
		{"ipv6", "https://[::1]:19443", "[::1]:19443", "", "[::1]:34567", "::1"},
		{"default_port", "https://localhost", "localhost:443", "LOCALHOST", "127.0.0.1:34567", "127.0.0.1"},
	} {
		t.Run(item.name, func(t *testing.T) {
			r := machineRequestFixture()
			r.Host, r.TLS.ServerName, r.RemoteAddr = item.host, item.sni, item.peer
			actualTLS := r.TLS
			server := machineRequestConfiguration(item.origin)
			value, e := ResolveMachineRequest(r, server)
			if e != nil || value.Mode != config.TransportDirectHTTPS || value.Scheme != "https" || value.Audience != "admin" || value.Origin != item.origin || value.ClientIP != item.ip || value.SocketPeerIP != item.ip || value.Forwarded {
				t.Fatal("machine selection", value, e)
			}
			selected := WithRequestTransport(r, value)
			if selected.TLS != actualTLS || RequestClientIP(selected) != item.ip {
				t.Fatal("machine context changed real connection")
			}
		})
	}
}

func TestMachineRequestRejectsTransportBrowserAndAuthorityConfusion(t *testing.T) {
	for _, item := range []struct {
		name string
		edit func(*http.Request)
		code string
	}{
		{"cleartext", func(r *http.Request) { r.TLS = nil }, "ORIGIN_REJECTED"},
		{"wrong_host", func(r *http.Request) { r.Host = "foreign.test:19443" }, "MISDIRECTED_REQUEST"},
		{"wrong_port", func(r *http.Request) { r.Host = "localhost:19444" }, "MISDIRECTED_REQUEST"},
		{"missing_sni", func(r *http.Request) { r.TLS.ServerName = "" }, "MISDIRECTED_REQUEST"},
		{"wrong_sni", func(r *http.Request) { r.TLS.ServerName = "foreign.test" }, "MISDIRECTED_REQUEST"},
		{"absolute_host", func(r *http.Request) { r.URL.Host, r.URL.Scheme = "foreign.test:19443", "https" }, "MISDIRECTED_REQUEST"},
		{"absolute_scheme", func(r *http.Request) { r.URL.Host, r.URL.Scheme = r.Host, "http" }, "MISDIRECTED_REQUEST"},
		{"scheme_without_authority", func(r *http.Request) { r.URL.Scheme = "https" }, "MISDIRECTED_REQUEST"},
		{"opaque_url", func(r *http.Request) { r.URL.Opaque = "//localhost:19443" }, "MISDIRECTED_REQUEST"},
		{"url_user", func(r *http.Request) { r.URL.User = url.User("synthetic") }, "MISDIRECTED_REQUEST"},
		{"nil_url", func(r *http.Request) { r.URL = nil }, "MISDIRECTED_REQUEST"},
		{"invalid_peer", func(r *http.Request) { r.RemoteAddr = "unknown:34567" }, "ORIGIN_REJECTED"},
		{"no_peer_port", func(r *http.Request) { r.RemoteAddr = "127.0.0.1" }, "ORIGIN_REJECTED"},
		{"forwarded", func(r *http.Request) { r.Header.Set("Forwarded", "proto=https") }, "ORIGIN_REJECTED"},
		{"forwarded_proto", func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }, "ORIGIN_REJECTED"},
		{"forwarded_unknown", func(r *http.Request) { r.Header["x-forwarded-unknown"] = nil }, "ORIGIN_REJECTED"},
		{"real_ip", func(r *http.Request) { r.Header.Set("X-Real-IP", "192.0.2.1") }, "ORIGIN_REJECTED"},
		{"origin", func(r *http.Request) { r.Header.Set("Origin", "https://localhost:19443") }, "ORIGIN_REJECTED"},
		{"empty_origin", func(r *http.Request) { r.Header["oRiGiN"] = nil }, "ORIGIN_REJECTED"},
		{"cookie", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "synthetic", Value: "unused"}) }, "ORIGIN_REJECTED"},
		{"empty_cookie", func(r *http.Request) { r.Header["cOoKiE"] = []string{""} }, "ORIGIN_REJECTED"},
	} {
		t.Run(item.name, func(t *testing.T) {
			r := machineRequestFixture()
			item.edit(r)
			if _, e := ResolveMachineRequest(r, machineRequestConfiguration("https://localhost:19443")); e == nil || e.Error() != item.code {
				t.Fatal("machine request refusal", e)
			}
		})
	}
	if _, e := ResolveMachineRequest(nil, machineRequestConfiguration("https://localhost:19443")); e == nil {
		t.Fatal("nil request accepted")
	}
	r := machineRequestFixture()
	r.Host = "127.0.0.1:19443"
	if _, e := ResolveMachineRequest(r, machineRequestConfiguration("https://127.0.0.1:19443")); e == nil {
		t.Fatal("IP authority accepted DNS SNI")
	}
	if _, e := ResolveMachineRequest(machineRequestFixture(), machineRequestConfiguration("http://localhost:19443")); e == nil {
		t.Fatal("invalid machine configuration accepted")
	}
}
