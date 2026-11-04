//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestComponentCycleRecoversBeforeHeartbeatAndDisabledClaimGate(t *testing.T) {
	var mu sync.Mutex
	events := []string{}
	record := func(event string) { mu.Lock(); defer mu.Unlock(); events = append(events, event) }
	status := "disabled"
	identity := state.Identity{SiteID: "default", CellID: "default", NodeID: "00000000-0000-0000-0000-000000000001", Credential: strings.Repeat("B", 43)}
	base := "/agent/v1/sites/default/cells/default/nodes/" + identity.NodeID
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+identity.Credential {
			t.Error("cycle lost machine identity")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case base + "/heartbeat":
			record("heartbeat")
			var h wire.Handshake
			if json.NewDecoder(r.Body).Decode(&h) != nil || h.Component == nil || h.Component.State != "stopped" || h.Profile() != wire.SystemdMaintenanceProfile {
				t.Error("heartbeat did not use recovered observation")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			current := status
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": wire.Disposition{Status: current, ServerTime: time.Now().UTC()}})
		case base + "/tasks/claims":
			record("claim")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": wire.Claim{}})
		default:
			t.Error("unexpected cycle endpoint", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	connection := &client.Client{Origin: server.URL, HTTP: server.Client(), Credential: identity.Credential}
	defer connection.Close()
	failure := errors.New("COMPONENT_ROLLBACK_FAILED")
	calls := 0
	m := &componentRecoveryDouble{
		recover: func(context.Context) error {
			record("recover")
			calls++
			if calls == 1 {
				return failure
			}
			return nil
		},
		observe: func(context.Context) wire.ComponentObservation {
			record("observe")
			return wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "stopped", Managed: true, ObservedAt: time.Now().UTC()}
		},
	}
	c := Config{DiskPath: t.TempDir(), ExecutionProfile: wire.SystemdMaintenanceProfile}
	if e := cycle(t.Context(), c, connection, identity, m, nil, nil, nil, nil); !errors.Is(e, failure) {
		t.Fatal("failed recovery reached heartbeat", e)
	}
	if e := cycle(t.Context(), c, connection, identity, m, nil, nil, nil, nil); e != nil {
		t.Fatal("same-process recovery did not resume disabled heartbeat", e)
	}
	mu.Lock()
	first := append([]string(nil), events...)
	status = "active"
	mu.Unlock()
	if !reflect.DeepEqual(first, []string{"recover", "recover", "observe", "heartbeat"}) {
		t.Fatal("disabled cycle claimed work or observed before recovery", first)
	}
	if e := cycle(t.Context(), c, connection, identity, m, nil, nil, nil, nil); e != nil {
		t.Fatal("active recovered cycle failed", e)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(events, []string{"recover", "recover", "observe", "heartbeat", "recover", "observe", "heartbeat", "claim"}) {
		t.Fatal("recovery/observation/claim order changed", events)
	}
}
