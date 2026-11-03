package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// Request TLS state below is explicitly synthetic dispatcher input, not an
// actual handshake, private CA, running server, SQL or ready-product fixture.
func machineHealthRequestFixture(h *Handler, path string) *http.Request {
	r := machineIngressTestRequest(h.Service.Config.MachineServer.Origin, path)
	r.Method = http.MethodGet
	return r
}

func machineHealthSuccess(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var envelope map[string]json.RawMessage
	var data map[string]string
	_, requestError := uuid.Parse(w.Header().Get("X-Request-ID"))
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &envelope) != nil || len(envelope) != 1 || json.Unmarshal(envelope["data"], &data) != nil || len(data) != 1 || data["status"] != "ok" || requestError != nil || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" || len(w.Result().Cookies()) != 0 {
		t.Fatal("existing health envelope, no-store or request identity changed", w.Code)
	}
}

func TestMachineHealthDispatcherUsesExistingReadinessOwners(t *testing.T) {
	if machineRoute("/health/live") || machineRoute("/health/ready") {
		t.Fatal("machine protocol ownership captured browser health")
	}
	for _, mode := range []config.TransportMode{config.TransportHTTP, config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		t.Run(string(mode), func(t *testing.T) {
			h := machineIngressTestHandler(mode)
			for _, path := range []string{"/health/live", "/health/ready"} {
				r := machineHealthRequestFixture(h, path)
				w := httptest.NewRecorder()
				h.MachineHandler().ServeHTTP(w, r)
				if path == "/health/live" {
					machineHealthSuccess(t, w)
				} else {
					// The real VerifyStored owner has missing dependencies. Never
					// substitute a successful readiness response for this fixture.
					machineIngressTestProblem(t, w, http.StatusServiceUnavailable, "SERVICE_NOT_READY")
				}
				if len(h.active) != 0 {
					t.Fatal("health response leaked the shared request slot")
				}
			}
			for _, origin := range []string{h.Service.Config.Server.PortalOrigin, h.Service.Config.Server.AdminOrigin} {
				r := machineIngressTestRequest(origin, "/health/live")
				r.Method = http.MethodGet
				if mode == config.TransportTrustedProxyHTTPS {
					r.TLS = nil
					r.URL.Scheme, r.URL.Host = "", ""
					r.Header.Set("X-Forwarded-Proto", "https")
					r.Header.Set("X-Forwarded-Host", r.Host)
					r.Header.Set("X-Forwarded-For", "192.0.2.27")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				machineHealthSuccess(t, w)
			}
		})
	}
}

// A health route must reject the body from metadata without reading it. This
// test-only body turns an accidental read into an immediate regression failure.
type machineHealthUnreadBody struct{}

func (machineHealthUnreadBody) Read([]byte) (int, error) {
	panic("SYNTHETIC_HEALTH_BODY_MUST_NOT_BE_READ")
}
func (machineHealthUnreadBody) Close() error { return nil }

func TestMachineHealthDispatcherRejectsUnsafeRequests(t *testing.T) {
	routeChanges := map[string]func(*http.Request){
		"post":                  func(r *http.Request) { r.Method = http.MethodPost },
		"head":                  func(r *http.Request) { r.Method = http.MethodHead },
		"options":               func(r *http.Request) { r.Method = http.MethodOptions },
		"unknown-health":        func(r *http.Request) { r.URL.Path = "/health/unknown" },
		"trailing-slash":        func(r *http.Request) { r.URL.Path += "/" },
		"query":                 func(r *http.Request) { r.URL.RawQuery = "synthetic=1" },
		"empty-query-marker":    func(r *http.Request) { r.URL.ForceQuery = true },
		"encoded-raw-path":      func(r *http.Request) { r.URL.RawPath = "/health/%6cive" },
		"nonempty-raw-path":     func(r *http.Request) { r.URL.RawPath = r.URL.Path },
		"body":                  func(r *http.Request) { r.Body = machineHealthUnreadBody{}; r.ContentLength = 1 },
		"body-with-zero-length": func(r *http.Request) { r.Body = machineHealthUnreadBody{} },
		"length-without-body":   func(r *http.Request) { r.ContentLength = 1 },
		"unknown-length":        func(r *http.Request) { r.ContentLength = -1 },
		"transfer-encoding":     func(r *http.Request) { r.TransferEncoding = []string{"chunked"} },
		"empty-transfer-header": func(r *http.Request) { r.Header["Transfer-Encoding"] = []string{""} },
	}
	for name, change := range routeChanges {
		t.Run(name, func(t *testing.T) {
			h := machineIngressTestHandler(config.TransportHTTP)
			for _, path := range []string{"/health/live", "/health/ready"} {
				r := machineHealthRequestFixture(h, path)
				change(r)
				w := httptest.NewRecorder()
				h.MachineHandler().ServeHTTP(w, r)
				machineIngressTestProblem(t, w, http.StatusNotFound, "RESOURCE_NOT_FOUND")
				if len(h.active) != 0 {
					t.Fatal("rejected health route retained capacity")
				}
			}
		})
	}
	transportChanges := map[string]struct {
		change func(*http.Request)
		status int
		code   string
	}{
		"plaintext":          {func(r *http.Request) { r.TLS = nil }, http.StatusForbidden, "ORIGIN_REJECTED"},
		"foreign-host":       {func(r *http.Request) { r.Host = "foreign.test:19444" }, http.StatusMisdirectedRequest, "MISDIRECTED_REQUEST"},
		"foreign-url":        {func(r *http.Request) { r.URL.Host = "foreign.test:19444" }, http.StatusMisdirectedRequest, "MISDIRECTED_REQUEST"},
		"foreign-sni":        {func(r *http.Request) { r.TLS.ServerName = "foreign.test" }, http.StatusMisdirectedRequest, "MISDIRECTED_REQUEST"},
		"invalid-peer":       {func(r *http.Request) { r.RemoteAddr = "not-an-address" }, http.StatusForbidden, "ORIGIN_REJECTED"},
		"cookie":             {func(r *http.Request) { r.Header["Cookie"] = []string{""} }, http.StatusForbidden, "ORIGIN_REJECTED"},
		"origin":             {func(r *http.Request) { r.Header["Origin"] = []string{""} }, http.StatusForbidden, "ORIGIN_REJECTED"},
		"forwarded":          {func(r *http.Request) { r.Header["Forwarded"] = []string{""} }, http.StatusForbidden, "ORIGIN_REJECTED"},
		"forwarded-for":      {func(r *http.Request) { r.Header["X-Forwarded-For"] = []string{""} }, http.StatusForbidden, "ORIGIN_REJECTED"},
		"forwarded-protocol": {func(r *http.Request) { r.Header["X-Forwarded-Proto"] = []string{""} }, http.StatusForbidden, "ORIGIN_REJECTED"},
	}
	for name, item := range transportChanges {
		t.Run(name, func(t *testing.T) {
			h := machineIngressTestHandler(config.TransportHTTP)
			r := machineHealthRequestFixture(h, "/health/live")
			item.change(r)
			w := httptest.NewRecorder()
			h.MachineHandler().ServeHTTP(w, r)
			machineIngressTestProblem(t, w, item.status, item.code)
			if len(h.active) != 0 || strings.Contains(w.Body.String(), "foreign.test") {
				t.Fatal("transport failure retained capacity or disclosed authority")
			}
		})
	}
	t.Run("shared-capacity", func(t *testing.T) {
		h := machineIngressTestHandler(config.TransportHTTP)
		h.active <- struct{}{}
		for _, path := range []string{"/health/live", "/health/ready"} {
			w := httptest.NewRecorder()
			h.MachineHandler().ServeHTTP(w, machineHealthRequestFixture(h, path))
			machineIngressTestProblem(t, w, http.StatusServiceUnavailable, "AUTH_CAPACITY_EXCEEDED")
		}
		r := machineIngressTestRequest(h.Service.Config.Server.PortalOrigin, "/health/live")
		r.Method = http.MethodGet
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		machineIngressTestProblem(t, w, http.StatusServiceUnavailable, "AUTH_CAPACITY_EXCEEDED")
		<-h.active
		w = httptest.NewRecorder()
		h.MachineHandler().ServeHTTP(w, machineHealthRequestFixture(h, "/health/live"))
		machineHealthSuccess(t, w)
	})
	t.Run("absent-dedicated-group", func(t *testing.T) {
		h := machineIngressTestHandler(config.TransportHTTP)
		r := machineHealthRequestFixture(h, "/health/live")
		h.Service.Config.MachineServer = nil
		w := httptest.NewRecorder()
		h.MachineHandler().ServeHTTP(w, r)
		machineIngressTestProblem(t, w, http.StatusForbidden, "ORIGIN_REJECTED")
	})
}
