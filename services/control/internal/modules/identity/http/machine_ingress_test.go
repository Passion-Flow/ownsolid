package http

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	nodehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentadmission"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

// No database or credential acceptance is simulated here. Nil domain owners
// make accidental browser/machine fallback fail rather than return fake success.
func machineIngressTestHandler(mode config.TransportMode) *Handler {
	scheme := "https"
	if mode == config.TransportHTTP {
		scheme = "http"
	}
	c := config.Config{Server: config.ServerConfiguration{
		TransportMode: mode, ListenAddress: "127.0.0.1:19443",
		PortalOrigin: scheme + "://portal.test", AdminOrigin: scheme + "://admin.test",
	}, MachineServer: &config.MachineServerConfiguration{
		ListenAddress: "127.0.0.1:19444", Origin: "https://machine.test:19444",
		TLSCertificateFile: "/synthetic/certificate.pem", TLSPrivateKeyFile: "/synthetic/private-key.pem",
	}}
	if mode == config.TransportTrustedProxyHTTPS {
		c.Server.TrustedProxyCIDRs = []string{"127.0.0.1/32"}
	}
	return &Handler{Service: &identity.Service{Config: c}, active: make(chan struct{}, 1)}
}

func machineIngressTestRequest(origin, path string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, origin+path, nil)
	r.RemoteAddr = "127.0.0.1:45678"
	if r.URL.Scheme == "https" {
		r.TLS = &tls.ConnectionState{ServerName: r.URL.Hostname()}
	}
	return r
}

func machineIngressTestProblem(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var p struct{ Code, RequestID string }
	var raw map[string]json.RawMessage
	if json.Unmarshal(w.Body.Bytes(), &raw) != nil || json.Unmarshal(raw["code"], &p.Code) != nil || json.Unmarshal(raw["request_id"], &p.RequestID) != nil || w.Code != status || p.Code != code || p.RequestID == "" || p.RequestID != w.Header().Get("X-Request-ID") || w.Header().Get("Cache-Control") != "no-store" || len(w.Result().Cookies()) != 0 {
		t.Fatal("closed entrance or redacted correlated problem changed", w.Code, p.Code)
	}
}

func TestMachineDispatcherClosesHumanRoutesAndBrowserMachineFallback(t *testing.T) {
	for _, mode := range []config.TransportMode{config.TransportHTTP, config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		t.Run(string(mode), func(t *testing.T) {
			h := machineIngressTestHandler(mode)
			for _, path := range []string{"/", "/api/v1/auth/login", "/runtime-config.json", "/health/live", "/api/v1/localization/inventory", "/assets/application.js"} {
				w := httptest.NewRecorder()
				h.MachineHandler().ServeHTTP(w, machineIngressTestRequest(h.Service.Config.MachineServer.Origin, path))
				machineIngressTestProblem(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
			}
			for _, origin := range []string{h.Service.Config.Server.PortalOrigin, h.Service.Config.Server.AdminOrigin} {
				for _, path := range []string{"/agent/v1/enroll", "/gateway/v1/admissions", "/agent/v1/sites/default/cells/default/nodes/node/tasks/task/image-registry/manifest"} {
					r := machineIngressTestRequest(origin, path)
					if mode == config.TransportTrustedProxyHTTPS {
						r.TLS = nil
						r.URL.Scheme, r.URL.Host = "", ""
						r.Header.Set("X-Forwarded-Proto", "https")
						r.Header.Set("X-Forwarded-Host", r.Host)
						r.Header.Set("X-Forwarded-For", "192.0.2.27")
					}
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					machineIngressTestProblem(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
				}
			}
		})
	}
}

func TestMachineDispatcherSeparatesAgentSeatsFromRequestSlots(t *testing.T) {
	h := machineIngressTestHandler(config.TransportHTTP)
	policy := config.DefaultAgentAdmissionPolicy()
	policy.Bulk.MaxSeats, policy.Bulk.WaitMilliseconds = 1, 0
	admission, e := agentadmission.New(policy, slog.Default())
	if e != nil {
		t.Fatal(e)
	}
	h.Nodes = &nodehttp.Handler{Admission: admission}
	heartbeat := "/agent/v1/sites/default/cells/default/nodes/node/heartbeat"
	registry := "/agent/v1/sites/default/cells/default/nodes/node/tasks/task/image-registry/manifest"
	// Full request slots refuse the model gateway and the console, not agent calls (ADR 0017).
	h.active <- struct{}{}
	w := httptest.NewRecorder()
	h.MachineHandler().ServeHTTP(w, machineIngressTestRequest(h.Service.Config.MachineServer.Origin, "/gateway/v1/admissions"))
	machineIngressTestProblem(t, w, http.StatusServiceUnavailable, "AUTH_CAPACITY_EXCEEDED")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, machineIngressTestRequest(h.Service.Config.Server.PortalOrigin, "/runtime-config.json"))
	machineIngressTestProblem(t, w, http.StatusServiceUnavailable, "AUTH_CAPACITY_EXCEEDED")
	w = httptest.NewRecorder()
	h.MachineHandler().ServeHTTP(w, machineIngressTestRequest(h.Service.Config.MachineServer.Origin, heartbeat))
	machineIngressTestProblem(t, w, http.StatusBadRequest, "VALIDATION_FAILED")
	<-h.active
	// Full bulk seats refuse transfers with the bulk retry hint and leave heartbeats alone.
	release, e := admission.Enter(context.Background(), agentwire.PriorityBulk)
	if e != nil {
		t.Fatal(e)
	}
	w = httptest.NewRecorder()
	h.MachineHandler().ServeHTTP(w, machineIngressTestRequest(h.Service.Config.MachineServer.Origin, registry))
	machineIngressTestProblem(t, w, http.StatusServiceUnavailable, "AGENT_OVERLOADED")
	if w.Header().Get("Retry-After") != strconv.Itoa(policy.Bulk.RetrySeconds) {
		t.Fatalf("Retry-After %q", w.Header().Get("Retry-After"))
	}
	w = httptest.NewRecorder()
	h.MachineHandler().ServeHTTP(w, machineIngressTestRequest(h.Service.Config.MachineServer.Origin, heartbeat))
	machineIngressTestProblem(t, w, http.StatusBadRequest, "VALIDATION_FAILED")
	release()
	w = httptest.NewRecorder()
	h.MachineHandler().ServeHTTP(w, machineIngressTestRequest(h.Service.Config.MachineServer.Origin, registry))
	machineIngressTestProblem(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	// A path that is no agent call is not found before admission.
	w = httptest.NewRecorder()
	h.MachineHandler().ServeHTTP(w, machineIngressTestRequest(h.Service.Config.MachineServer.Origin, "/agent/v1/sites/default/cells/default/nodes/node/unknown"))
	machineIngressTestProblem(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
}

func TestMachineDispatcherRejectsClientIngressSelection(t *testing.T) {
	h := machineIngressTestHandler(config.TransportHTTP)
	for _, name := range []string{"Origin", "Cookie", "Forwarded", "X-Forwarded-Proto"} {
		r := machineIngressTestRequest(h.Service.Config.MachineServer.Origin, "/agent/v1/enroll")
		r.Header[name] = []string{""}
		w := httptest.NewRecorder()
		h.MachineHandler().ServeHTTP(w, r)
		machineIngressTestProblem(t, w, http.StatusForbidden, "ORIGIN_REJECTED")
		if len(h.active) != 0 {
			t.Fatal("transport refusal leaked shared request slot")
		}
	}
	// A private-looking header cannot move an HTTP browser request onto machine policy.
	r := machineIngressTestRequest(h.Service.Config.Server.AdminOrigin, "/agent/v1/enroll")
	r.Header.Set("X-Projectone-Machine-Ingress", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	machineIngressTestProblem(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
	h.Service.Config.MachineServer = nil
	w = httptest.NewRecorder()
	h.MachineHandler().ServeHTTP(w, machineIngressTestRequest("https://machine.test:19444", "/agent/v1/enroll"))
	machineIngressTestProblem(t, w, http.StatusForbidden, "ORIGIN_REJECTED")
}

func TestMachineTransportUnavailablePublicProblemIsExactAndRedacted(t *testing.T) {
	for _, item := range []struct{ input, expected string }{
		{"CONFIG_MACHINE_TRANSPORT_REQUIRED", "CONFIG_MACHINE_TRANSPORT_REQUIRED"},
		{"CONFIG_MACHINE_TRANSPORT_REQUIRED: /private/operator", "STORAGE_UNAVAILABLE"},
		{"CONFIG_MACHINE_SERVER_INVALID", "STORAGE_UNAVAILABLE"},
	} {
		w := httptest.NewRecorder()
		httpserver.Problem(w, "synthetic-request", errors.New(item.input))
		var p struct{ Code string }
		if json.Unmarshal(w.Body.Bytes(), &p) != nil || w.Code != http.StatusServiceUnavailable || p.Code != item.expected {
			t.Fatal("machine configuration problem escaped exact public registry")
		}
	}
}
