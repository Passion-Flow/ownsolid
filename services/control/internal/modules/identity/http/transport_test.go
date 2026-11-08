package http

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// These dispatcher refusal/projection tests open no database and issue no
// successful authentication. SQL, real login and browser journeys have their
// separate executable acceptance gate.
func TestTransportDispatcherProjectionAndRefusalBeforeSQL(t *testing.T) {
	for _, profile := range []struct {
		name, expected string
	}{
		{name: "single-node", expected: "single-node"},
	} {
		for _, mode := range []config.TransportMode{config.TransportHTTP, config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
			t.Run(profile.name+"/"+string(mode), func(t *testing.T) {
				var c config.Config
				c.Server.TransportMode = mode
				scheme := "https"
				if mode == config.TransportHTTP {
					scheme = "http"
				}
				c.Server.PortalOrigin, c.Server.AdminOrigin = scheme+"://portal.test", scheme+"://admin.test"
				if mode == config.TransportTrustedProxyHTTPS {
					c.Server.TrustedProxyCIDRs = []string{"127.0.0.1/32"}
				}
				h := &Handler{Service: &identity.Service{Config: c}, active: make(chan struct{}, 1)}
				request := func(audience, method, path string) *http.Request {
					r := httptest.NewRequest(method, scheme+"://"+audience+".test"+path, strings.NewReader("{}"))
					r.RemoteAddr = "127.0.0.1:45678"
					r.Header.Set("Content-Type", "application/json")
					if mode == config.TransportDirectHTTPS {
						r.TLS = &tls.ConnectionState{ServerName: audience + ".test"}
					} else if mode == config.TransportTrustedProxyHTTPS {
						r.TLS = nil
						r.URL.Scheme, r.URL.Host = "", ""
						r.Header.Set("X-Forwarded-Proto", "https")
						r.Header.Set("X-Forwarded-Host", audience+".test")
						r.Header.Set("X-Forwarded-For", "192.0.2.27")
					}
					return r
				}
				for _, audience := range []string{"portal", "admin"} {
					w := httptest.NewRecorder()
					h.ServeHTTP(w, request(audience, http.MethodGet, "/runtime-config.json"))
					var response struct {
						Data map[string]json.RawMessage `json:"data"`
					}
					publicFields := []string{
						"topology", "default_locale", "sso_callback_origins", "transport", "private_clusters", "monitoring",
						"middleware", "task_events", "image_registry", "artifact_storage",
						"workspaces", "ui", "gateway_keys", "agent_gateways", "request_timeout_milliseconds", "component_default_version",
						"component_max_scrapes", "component_default_config",
					}
					if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Data) != len(publicFields) || w.Header().Get("Cache-Control") != "no-store" {
						t.Fatal("runtime projection", w.Code, w.Body.String())
					}
					for _, field := range publicFields {
						if _, present := response.Data[field]; !present {
							t.Fatal("missing public runtime field", field)
						}
					}
					var topology struct {
						Size string `json:"size"`
					}
					if json.Unmarshal(response.Data["topology"], &topology) != nil || topology.Size != profile.expected {
						t.Fatal("public topology projection", topology.Size, profile.expected)
					}
					callbacks := []string{}
					expectedCallbacks := []string{}
					if mode != config.TransportHTTP {
						expectedCallbacks = []string{"https://portal.test", "https://admin.test"}
					}
					if json.Unmarshal(response.Data["sso_callback_origins"], &callbacks) != nil || !reflect.DeepEqual(callbacks, expectedCallbacks) {
						t.Fatal("exact deployment-selected HTTPS callback origins", callbacks, expectedCallbacks)
					}
					var public struct {
						Mode      config.TransportMode `json:"mode"`
						Plaintext bool                 `json:"plaintext"`
					}
					if json.Unmarshal(response.Data["transport"], &public) != nil || public.Mode != mode || public.Plaintext != (mode == config.TransportHTTP) || len(w.Result().Cookies()) != 0 {
						t.Fatal("public mode/cookie projection")
					}
					for _, forbidden := range []string{"tls_certificate_file", "tls_private_key_file", "trusted_proxy_cidrs", "dsn_file", "listen_address"} {
						if strings.Contains(w.Body.String(), forbidden) {
							t.Fatal("private setting exposed", forbidden)
						}
					}
					for _, item := range []struct{ origin, csrf, code string }{
						{"http://foreign.test", "", "ORIGIN_REJECTED"},
						{scheme + "://" + audience + ".test", "", "CSRF_INVALID"},
					} {
						r := request(audience, http.MethodPost, "/api/v1/auth/login")
						r.Header.Set("Origin", item.origin)
						r.Header.Set("X-CSRF-Token", item.csrf)
						w := httptest.NewRecorder()
						h.ServeHTTP(w, r)
						var problem struct {
							Code string `json:"code"`
						}
						if w.Code != 403 || json.Unmarshal(w.Body.Bytes(), &problem) != nil || problem.Code != item.code || len(w.Result().Cookies()) != 0 {
							t.Fatal("mutation reached SQL before original refusal", w.Code, problem.Code)
						}
					}
				}
				if mode == config.TransportHTTP {
					status, code := http.StatusForbidden, "ORIGIN_REJECTED"
					for _, route := range []string{"/agent/v1/sites/default/cells/default/nodes/unknown", "/gateway/v1/admissions"} {
						w := httptest.NewRecorder()
						h.ServeHTTP(w, request("admin", http.MethodPost, route))
						var problem struct {
							Code string `json:"code"`
						}
						if w.Code != status || json.Unmarshal(w.Body.Bytes(), &problem) != nil || problem.Code != code || w.Header().Get("Cache-Control") != "no-store" || len(w.Result().Cookies()) != 0 {
							t.Fatal("machine route refusal changed", w.Code, problem.Code)
						}
					}
				}
			})
		}
	}
}
