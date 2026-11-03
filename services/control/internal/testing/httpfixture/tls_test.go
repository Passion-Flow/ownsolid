package httpfixture

import (
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit fixture probes below serve synthetic values only. They verify real
// TLS/CA/SNI/dial and client behavior, not successful product authentication/SQL.
func fixtureProbe(seen *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", "synthetic-fixture-request")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.TLS == nil || r.TLS.ServerName != r.Host {
			w.WriteHeader(http.StatusMisdirectedRequest)
			return
		}
		if r.URL.Path == "/cross" {
			http.Redirect(w, r, AdminOrigin+"/received", http.StatusSeeOther)
			return
		}
		if r.URL.Path == "/received" || r.URL.Path == "/unrequested" {
			seen.Add(1)
		}
		if r.URL.Path == "/set" || r.URL.Path == "/clear" {
			audience := "portal"
			if r.Host == fixtureAdminHostname {
				audience = "admin"
			}
			value, age := "synthetic-fixture-cookie", 60
			if r.URL.Path == "/clear" {
				value, age = "", -1
			}
			http.SetCookie(w, &http.Cookie{Name: "__Host-" + audience + "_session", Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: age})
			http.Redirect(w, r, "https://"+r.Host+"/unrequested", http.StatusSeeOther)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"host": r.Host, "sni": r.TLS.ServerName, "socket_tls": true}})
	})
}

func closeFixtureResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
	}
}

func TestTLSFixtureActualDNSAuthorityAndPrivateCA(t *testing.T) {
	var seen atomic.Int32
	server := NewTLSServer(t, fixtureProbe(&seen))
	if len(server.Certificate().DNSNames) != 2 || server.Certificate().VerifyHostname(fixturePortalHostname) != nil || server.Certificate().VerifyHostname(fixtureAdminHostname) != nil || server.Certificate().VerifyHostname("127.0.0.1") == nil {
		t.Fatal("fixture certificate did not retain its exact DNS identities")
	}
	for _, audience := range []string{"portal", "admin"} {
		t.Run(audience, func(t *testing.T) {
			client := New(t, server, audience, nil)
			transport, ok := client.client.Transport.(*http.Transport)
			if !ok || transport.Proxy != nil || transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil || transport.TLSClientConfig.ServerName != "" {
				t.Fatal("client did not retain private trust and URL-derived SNI")
			}
			data := client.Call(http.MethodGet, "/", nil, 200, "")
			if data["host"] != audience+".example.invalid" || data["sni"] != data["host"] || data["socket_tls"] != true {
				t.Fatal("actual DNS Host or native TLS SNI did not match selected audience")
			}
		})
	}
}

func TestTLSFixtureExactDialAndAudienceRedirectRefusals(t *testing.T) {
	var seen atomic.Int32
	server := NewTLSServer(t, fixtureProbe(&seen))
	for _, item := range []struct{ name, target string }{
		{"foreign_dns", "https://foreign.example.invalid/"},
		{"loopback_url", server.URL + "/"},
		{"wrong_port", PortalOrigin + ":444/"},
		{"plaintext", "http://portal.example.invalid/"},
		{"cross_audience_redirect", PortalOrigin + "/cross"},
	} {
		t.Run(item.name, func(t *testing.T) {
			client := New(t, server, "portal", nil)
			response, e := client.client.Get(item.target)
			closeFixtureResponse(response)
			if e == nil || seen.Load() != 0 {
				t.Fatal("unapproved target or cross-audience redirect was requested")
			}
		})
	}
	t.Run("host_sni_confusion", func(t *testing.T) {
		request, e := http.NewRequest(http.MethodGet, PortalOrigin+"/", nil)
		if e != nil {
			t.Fatal("synthetic request construction failed")
		}
		request.Host = fixtureAdminHostname
		response, e := server.Client().Do(request)
		if e != nil {
			t.Fatal("actual TLS probe failed before authority observation")
		}
		defer closeFixtureResponse(response)
		if response.StatusCode != http.StatusMisdirectedRequest {
			t.Fatal("fixture hid URL SNI versus Host mismatch")
		}
	})
}

func TestTLSFixtureNormalTrustAndPeerVerificationFailures(t *testing.T) {
	var seen atomic.Int32
	server := NewTLSServer(t, fixtureProbe(&seen))
	for _, kind := range []string{"untrusted_ca", "wrong_peer_name"} {
		t.Run(kind, func(t *testing.T) {
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.TLSClientConfig = transport.TLSClientConfig.Clone()
			if kind == "untrusted_ca" {
				transport.TLSClientConfig.RootCAs = x509.NewCertPool()
			} else {
				transport.TLSClientConfig.ServerName = "foreign.example.invalid"
			}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: fixtureRequestTimeout}
			response, e := client.Get(PortalOrigin + "/")
			closeFixtureResponse(response)
			if e == nil {
				t.Fatal("normal CA or peer hostname verification was bypassed")
			}
		})
	}
}

func TestTLSFixtureRedirectReadsClearsCookiesWithoutFollowing(t *testing.T) {
	var seen atomic.Int32
	server := NewTLSServer(t, fixtureProbe(&seen))
	for _, audience := range []string{"portal", "admin"} {
		t.Run(audience, func(t *testing.T) {
			client := New(t, server, audience, nil)
			status, location, cookies := client.Redirect("/set")
			name := "__Host-" + audience + "_session"
			if status != http.StatusSeeOther || location != client.origin+"/unrequested" || len(cookies) != 1 || client.cookies[name] == nil || client.cookies[name].Value != "synthetic-fixture-cookie" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Path != "/" || seen.Load() != 0 {
				t.Fatal("first redirect cookie or selected origin was lost")
			}
			status, location, cookies = client.Redirect("/clear")
			if status != http.StatusSeeOther || location != client.origin+"/unrequested" || len(cookies) != 1 || cookies[0].MaxAge != -1 || client.cookies[name] != nil || seen.Load() != 0 {
				t.Fatal("clear followed navigation or retained the old fixture cookie")
			}
		})
	}
}

func TestTLSFixtureConfigurationAndOwnedListenerClosure(t *testing.T) {
	t.Run("nil_server", func(t *testing.T) {
		if client, e := fixtureTLSClient(nil, []string{PortalOrigin}); client != nil || e == nil {
			t.Fatal("missing owned listener accepted")
		}
	})
	t.Run("unknown_audience", func(t *testing.T) {
		if origin, e := fixtureOrigin("foreign"); origin != "" || e == nil {
			t.Fatal("unknown audience accepted")
		}
	})
	t.Run("unknown_origin", func(t *testing.T) {
		var seen atomic.Int32
		server := NewTLSServer(t, fixtureProbe(&seen))
		if client, e := fixtureTLSClient(server, []string{"https://foreign.example.invalid"}); client != nil || e == nil || strings.Contains(e.Error(), "foreign") {
			t.Fatal("unknown client origin accepted or disclosed")
		}
	})
	t.Run("listener_close", func(t *testing.T) {
		var seen atomic.Int32
		server := NewTLSServer(t, fixtureProbe(&seen))
		response, e := server.Client().Get(AdminOrigin + "/")
		closeFixtureResponse(response)
		if e != nil {
			t.Fatal("owned listener did not serve real TLS")
		}
		address := server.Listener.Addr().String()
		server.Close()
		connection, e := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if e == nil {
			connection.Close()
			t.Fatal("owned TLS listener remained after close")
		}
	})
}
