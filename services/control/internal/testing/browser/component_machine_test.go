package browser_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"net/url"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"sync"
	"testing"
	"time"
)

// This machine is a test-only observation fixture for the real browser/control path.
// It does not execute a service manager or establish actual component/systemd qualification.
func startSyntheticComponentMachine(t *testing.T, server *httptest.Server, s *nodes.Service, a identity.Auth) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h := wire.Handshake{ProtocolVersion: wire.ManagementVersion, ExecutionProfile: wire.SystemdComponentsProfile, Capabilities: wire.Capabilities(wire.SystemdComponentsProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-browser-component", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	join, e := s.MintForProfile(ctx, a, a.Me.Workspace.ID, "Synthetic management fixture", wire.SystemdComponentsProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	enrolled, e := s.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	client := server.Client()
	client.Timeout = 5 * time.Second
	origin, _ := url.Parse(s.Identity.Config.Server.AdminOrigin)
	post := func(path string, input, out any) bool {
		body, _ := json.Marshal(input)
		r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+path, bytes.NewReader(body))
		r.Host = origin.Host
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+enrolled.Credential)
		response, e := client.Do(r)
		if e != nil {
			return false
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return false
		}
		var envelope struct{ Data json.RawMessage }
		if json.NewDecoder(response.Body).Decode(&envelope) != nil {
			return false
		}
		return out == nil || json.Unmarshal(envelope.Data, out) == nil
	}
	done := make(chan struct{})
	var mu sync.Mutex
	failed := false
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		lastHeartbeat := time.Time{}
		firstInstall := true
		observation := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed", ObservedAt: time.Now().UTC()}
		for {
			base := "/agent/v1/nodes/" + enrolled.NodeID
			good := true
			if time.Since(lastHeartbeat) >= 10*time.Second {
				h.Facts.ObservedAt = time.Now().UTC()
				observation.ObservedAt = time.Now().UTC()
				h.Component = &observation
				good = post(base+"/heartbeat", h, nil)
				lastHeartbeat = time.Now()
			}
			var claim wire.Claim
			if good {
				good = post(base+"/tasks/claims", struct{}{}, &claim)
			}
			if good && claim.Lease != nil {
				lease := claim.Lease
				if lease.Component == nil || lease.Component.Validate(lease.Operation) != nil {
					good = false
				} else {
					observation.ErrorCode = ""
					observation.Metrics = nil
					observation.ObservedAt = time.Now().UTC()
					observation.Managed = true
					switch lease.Component.Action {
					case "install":
						if firstInstall {
							firstInstall = false
							observation.State = "unknown"
							observation.Managed = false
							observation.ErrorCode = "COMPONENT_SYSTEMD_UNAVAILABLE"
						} else {
							observation.State = "running"
						}
					case "start":
						observation.State = "running"
					case "stop":
						observation.State = "stopped"
					case "remove":
						observation.State = "not_installed"
						observation.Managed = false
					}
					if observation.State == "running" {
						observation.Metrics = &wire.ComponentMetrics{CPUSecondsTotal: 100, CPUIdleSecondsTotal: 80, MemoryTotalBytes: 1024, MemoryAvailableBytes: 512, FilesystemTotalBytes: 4096, FilesystemAvailableBytes: 2048}
					}
					result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Component: &observation}
					if observation.ErrorCode != "" {
						result.Outcome = "failure"
						result.ErrorCode = observation.ErrorCode
					}
					good = post(base+"/tasks/"+lease.TaskID+"/results", result, nil)
				}
			}
			if !good {
				if ctx.Err() == nil {
					mu.Lock()
					failed = true
					mu.Unlock()
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return enrolled.NodeID, func() {
		cancel()
		<-done
		mu.Lock()
		defer mu.Unlock()
		if failed {
			t.Error("SYNTHETIC_COMPONENT_MACHINE_FIXTURE_FAILED")
		}
	}
}
