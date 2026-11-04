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

// This observation source exists only in tests. Real systemd qualification is separate.
func startSyntheticMaintenanceMachine(t *testing.T, server *httptest.Server, s *nodes.Service, a identity.Auth) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h := wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-browser-maintenance", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	join, e := s.MintForProfile(ctx, a, a.Me.Workspace.ID, "Synthetic maintenance fixture", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	node, e := s.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	client := server.Client()
	client.Timeout = 5 * time.Second
	origin, _ := url.Parse(s.Identity.Config.Server.AdminOrigin)
	post := func(path string, input, out any) bool {
		body, _ := json.Marshal(input)
		request, _ := http.NewRequestWithContext(ctx, "POST", server.URL+path, bytes.NewReader(body))
		request.Host = origin.Host
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+node.Credential)
		response, e := client.Do(request)
		if e != nil {
			return false
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return false
		}
		var envelope struct{ Data json.RawMessage }
		return json.NewDecoder(response.Body).Decode(&envelope) == nil && (out == nil || json.Unmarshal(envelope.Data, out) == nil)
	}
	done := make(chan struct{})
	var mu sync.Mutex
	failed := false
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		lastHeartbeat := time.Time{}
		version := wire.NodeExporterVersion
		configuration := wire.DefaultComponentConfig()
		previous := ""
		previousConfig := configuration
		managed := false
		state := "not_installed"
		firstUpgrade := true
		retainedError := ""
		retainedCause := ""
		observation := func() wire.ComponentObservation {
			o := wire.ComponentObservation{Component: wire.NodeExporter, Version: version, State: state, Managed: managed, ObservedAt: time.Now().UTC(), PreviousVersion: previous, MaintenanceError: retainedError, MaintenanceCause: retainedCause}
			if managed {
				copy := configuration
				o.Config = &copy
			}
			if state == "running" {
				o.Metrics = &wire.ComponentMetrics{CPUSecondsTotal: 100, CPUIdleSecondsTotal: 80, MemoryTotalBytes: 1024, MemoryAvailableBytes: 512, FilesystemTotalBytes: 4096, FilesystemAvailableBytes: 2048}
			}
			return o
		}
		for {
			base := "/agent/v1/sites/default/cells/default/nodes/" + node.NodeID
			good := true
			if time.Since(lastHeartbeat) >= 10*time.Second {
				o := observation()
				h.Component = &o
				h.Facts.ObservedAt = time.Now().UTC()
				good = post(base+"/heartbeat", h, nil)
				lastHeartbeat = time.Now()
			}
			var claim wire.Claim
			if good {
				good = post(base+"/tasks/claims", struct{}{}, &claim)
			}
			if good && claim.Lease != nil {
				lease := claim.Lease
				command := lease.Component
				if command == nil || command.ValidateMaintenance(lease.Operation) != nil {
					good = false
				} else {
					errorCode := ""
					var logs *wire.ComponentLogs
					if command.ExpectedVersion != "" && command.ExpectedVersion != version {
						errorCode = "COMPONENT_VERSION_CONFLICT"
					} else {
						switch command.Action {
						case "install":
							version = command.Version
							configuration = wire.DefaultComponentConfig()
							if command.Config != nil {
								configuration = *command.Config
							}
							managed = true
							state = "running"
						case "configure":
							previous = version
							previousConfig = configuration
							configuration = *command.Config
							retainedError = ""
							retainedCause = ""
						case "restart", "start":
							state = "running"
						case "stop":
							state = "stopped"
						case "upgrade":
							if firstUpgrade {
								firstUpgrade = false
								errorCode = "COMPONENT_UPDATE_ROLLED_BACK"
								retainedError = errorCode
								retainedCause = "COMPONENT_SERVICE_FAILED"
							} else {
								previous = version
								previousConfig = configuration
								version = command.Version
								if command.Config != nil {
									configuration = *command.Config
								}
								retainedError = ""
								retainedCause = ""
							}
						case "rollback":
							oldVersion, oldConfig := version, configuration
							version, configuration = previous, previousConfig
							previous, previousConfig = oldVersion, oldConfig
							retainedError = ""
							retainedCause = ""
						case "logs":
							now := time.Now().UTC()
							logs = &wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute), Entries: []wire.ComponentLogEntry{{Timestamp: now, Priority: 6, Message: "SYNTHETIC_BROWSER_LOG <img src='https://unexpected.invalid/image'> <script>window.__unsafeLog=true</script>"}}}
						case "remove":
							managed = false
							state = "not_installed"
							previous = ""
							retainedError = ""
							retainedCause = ""
						}
					}
					o := observation()
					o.ErrorCode = errorCode
					result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Component: &o, Logs: logs}
					if errorCode != "" {
						result.Outcome = "failure"
						result.ErrorCode = errorCode
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
	return node.NodeID, func() {
		cancel()
		<-done
		mu.Lock()
		defer mu.Unlock()
		if failed {
			t.Error("SYNTHETIC_MAINTENANCE_MACHINE_FIXTURE_FAILED")
		}
	}
}
