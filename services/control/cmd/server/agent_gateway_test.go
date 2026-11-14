package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

type gatewayProbe struct {
	mu    sync.Mutex
	ready error
}

func (p *gatewayProbe) Ready() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ready
}

// gatewayServer serves agentGatewayHandler on a TLS listener whose origin is its own address.
func gatewayServer(t *testing.T, probe *gatewayProbe) (*httptest.Server, string) {
	t.Helper()
	// No session stream is opened here, so the handler needs no gateway.
	handler := &agentGatewayHandler{ready: probe.Ready}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	origin := server.URL
	handler.machine = config.MachineServerConfiguration{ListenAddress: strings.TrimPrefix(origin, "https://"), Origin: origin, TLSCertificateFile: "/synthetic/certificate.pem", TLSPrivateKeyFile: "/synthetic/key.pem"}
	return server, origin
}

func gatewayGet(t *testing.T, server *httptest.Server, url string, host string) (int, string) {
	t.Helper()
	request, e := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if e != nil {
		t.Fatal(e)
	}
	if host != "" {
		request.Host = host
	}
	response, e := server.Client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(body)
}

func TestAgentGatewayServesOnlyTheSessionAndHealth(t *testing.T) {
	probe := &gatewayProbe{}
	server, origin := gatewayServer(t, probe)
	if status, body := gatewayGet(t, server, origin+"/health/live", ""); status != http.StatusOK || !strings.Contains(body, `"live"`) {
		t.Fatalf("live: %d %s", status, body)
	}
	if status, body := gatewayGet(t, server, origin+"/health/ready", ""); status != http.StatusOK || !strings.Contains(body, `"ready"`) {
		t.Fatalf("ready: %d %s", status, body)
	}
	probe.mu.Lock()
	probe.ready = errors.New("SERVICE_NOT_READY")
	probe.mu.Unlock()
	status, body := gatewayGet(t, server, origin+"/health/ready", "")
	var problem struct {
		Code string `json:"code"`
	}
	if status != http.StatusServiceUnavailable || json.Unmarshal([]byte(body), &problem) != nil || problem.Code != "SERVICE_NOT_READY" {
		t.Fatalf("fenced ready: %d %s", status, body)
	}
	// Results, renewals, transfers and enrollment stay on control's agent entrance.
	for _, path := range []string{"/agent/v1/enroll", "/agent/v1/sites/default/cells/default/nodes/x/heartbeat", "/api/v1/admin/license", "/"} {
		if status, _ := gatewayGet(t, server, origin+path, ""); status != http.StatusNotFound {
			t.Fatalf("%s: %d", path, status)
		}
	}
}

func TestAgentGatewayRefusesAnotherOrigin(t *testing.T) {
	server, origin := gatewayServer(t, &gatewayProbe{})
	if status, _ := gatewayGet(t, server, origin+"/health/live", "admin.example.invalid"); status == http.StatusOK {
		t.Fatal("a request for another origin was served")
	}
}
