package monitoringmetrics_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/health"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
)

func TestActualHealthRoutesKeepBoundedClassesOverTLS(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		name := "http1"
		if h2 {
			name = "http2"
		}
		t.Run(name, func(t *testing.T) {
			recorder, err := monitoringmetrics.New(monitoringmetrics.Defaults(), nil)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(recorder.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestID := uuid.NewString()
				w.Header().Set("X-Request-ID", requestID)
				out, status, err := health.CheckSelected(r.Context(), nil, nil, r.Method, r.URL.Path)
				if err != nil {
					httpserver.Problem(w, requestID, err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if err := json.NewEncoder(w).Encode(map[string]any{"data": out}); err != nil {
					t.Error(err)
				}
			})))
			server.EnableHTTP2 = h2
			server.StartTLS()
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			defer client.CloseIdleConnections()
			for _, test := range []struct {
				path   string
				status int
			}{
				{"/health/live?caller=must-not-be-a-label", 200},
				{"/health/ready", 503},
				{"/healthz", 404},
				{"/readyz", 404},
				{"/health/live/private-one", 404},
				{"/health/ready/private-two", 404},
				{"/health/private-three", 404},
				{"/readyz/private-four", 404},
			} {
				response, err := client.Get(server.URL + test.path)
				if err != nil {
					t.Fatal(err)
				}
				_, readError := io.Copy(io.Discard, response.Body)
				closeError := response.Body.Close()
				if readError != nil || closeError != nil || response.StatusCode != test.status ||
					(response.ProtoMajor == 2) != h2 || response.TLS == nil || len(response.TLS.VerifiedChains) == 0 {
					t.Fatalf("%s changed the actual health response or TLS protocol: status=%d read=%v close=%v", test.path, response.StatusCode, readError, closeError)
				}
			}
			snapshot := recorder.Snapshot()
			if snapshot.Inflight != 0 || len(snapshot.Series) != 3 {
				t.Fatalf("health traffic created extra route series or leaked inflight state: %+v", snapshot)
			}
			expected := map[string]uint64{"health/succeeded": 1, "health/http_error": 3, "other/http_error": 4}
			for _, series := range snapshot.Series {
				key := series.Route + "/" + series.Outcome
				if series.Method != http.MethodGet || series.Count != expected[key] {
					t.Fatalf("incorrect bounded route observation: %+v", series)
				}
				delete(expected, key)
			}
			if len(expected) != 0 {
				t.Fatal("health traffic lost a required route or outcome", expected)
			}
		})
	}
}

// Family classification observes refused transport traffic without granting a
// request authority. Protocol owners retain all credential and payload checks.
func TestCanonicalMachineFamiliesKeepIdentityOutOfLabels(t *testing.T) {
	recorder, err := monitoringmetrics.New(monitoringmetrics.Defaults(), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := recorder.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	node, instance := uuid.NewString(), uuid.NewString()
	scope := wire.Scope{SiteID: "private-site", CellID: "private-cell"}
	paths := []string{
		wire.SessionPath,
		"/agent/v1/nodes/" + node + "/heartbeat",
		cellroute.Public("/agent/v1/nodes/"+node+"/heartbeat", scope),
		"/api/v1/agent/legacy",
		"/gateway/v1/instances/" + instance + "/requests",
		cellroute.Public("/gateway/v1/instances/"+instance+"/requests", scope),
		"/api/v1/gateway/legacy",
		"/agent/v10/private-node",
		"/gateway/v1-private/private-instance",
		"/ownsolid.agent.v1.AgentSession/Unknown",
	}
	for _, path := range paths {
		request := httptest.NewRequest(http.MethodPost, "http://fixture.invalid"+path+"?credential=must-not-be-a-label", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatal("recorder changed admission refusal")
		}
	}
	snapshot := recorder.Snapshot()
	if snapshot.Inflight != 0 || len(snapshot.Series) != 3 {
		t.Fatal("machine traffic created extra route families or leaked inflight state")
	}
	expected := map[string]uint64{"agent": 4, "gateway": 3, "other": 3}
	for _, series := range snapshot.Series {
		if series.Method != http.MethodPost || series.Outcome != "http_error" || series.Count != expected[series.Route] {
			t.Fatalf("incorrect canonical machine family: %+v", series)
		}
		delete(expected, series.Route)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || len(expected) != 0 || strings.Contains(string(encoded), node) || strings.Contains(string(encoded), instance) ||
		strings.Contains(string(encoded), scope.SiteID) || strings.Contains(string(encoded), scope.CellID) ||
		strings.Contains(string(encoded), "must-not-be-a-label") {
		t.Fatal("machine route observations exposed an identity or lost a family")
	}
}
