package http_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestLaneDExporterTLS(t *testing.T) {
	catalog := os.Getenv("LANE_D_COMPONENT_CATALOG")
	if catalog == "" {
		t.Skip("provide the pinned node exporter archives via LANE_D_COMPONENT_STATE")
	}
	address := os.Getenv("LANE_D_LOG_STORE")
	if address == "" {
		t.Fatal("the exporter fixture needs its isolated log store")
	}
	logStore, err := telemetrystore.Open(telemetrystore.Settings{LogsAddress: address, RequestTimeout: 5 * time.Second, QueueCapacity: 16}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		logStore.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })
	var lose, committed atomic.Bool
	c := newWorkflowCLI(t, func(service *identity.Service) {
		service.Config.Components.NodeExporterCatalogDirectory = catalog
	}, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/components/node-exporter/actions") && lose.CompareAndSwap(true, false) {
				reply := httptest.NewRecorder()
				next.ServeHTTP(reply, r)
				committed.Store(reply.Code == http.StatusAccepted)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	c.handler.Components.Service.LogStore = logStore
	node := enrollWorkflowNode(c, "Exporter maintenance fixture", wire.SystemdMaintenanceProfile)
	readOnly := enrollWorkflowNode(c, "Read-only exporter fixture", wire.ReadOnlyProfile)
	nodeService := c.handler.Nodes.Service
	c.ok(`"artifact_available": true`, "node", "exporter", "status", node.NodeID, "-o", "json")
	badKey := uuid.NewString()
	c.refused("COMPONENT_PROFILE_UNAVAILABLE", "node", "exporter", "install", readOnly.NodeID, "--reason", "Expected refusal", "--idempotency-key", badKey)
	c.count(0, "SELECT count(*) FROM node_tasks WHERE idempotency_key=$1", badKey)

	action := func(command, key string, flags ...string) []string {
		return append([]string{"node", "exporter", command, node.NodeID, "--reason", "Exporter maintenance fixture", "--idempotency-key", key, "-o", "json"}, flags...)
	}
	task := func(args []string) string {
		t.Helper()
		var value cliRow
		if err := json.Unmarshal([]byte(c.ok("", args...)), &value); err != nil || value.ID == "" {
			t.Fatalf("invalid component task %v", err)
		}
		return value.ID
	}
	settings := wire.DefaultComponentConfig()
	settle := func(id, version, state, previous string, logs *wire.ComponentLogs) {
		t.Helper()
		lease := claimTask(t, nodeService, node, id)
		if lease.Component == nil || lease.Component.Version != version {
			t.Fatal("component command did not retain the chosen recipe")
		}
		switch lease.Component.Action {
		case "install", "upgrade":
			err := nodeService.WithLease(ctx, node.NodeID, node.Credential, id, lease.LeaseToken, func(tx pgx.Tx, n nodes.Node, task nodes.Task) error {
				download, err := c.handler.Components.Service.TaskArtifact(ctx, tx, n, task)
				if err != nil {
					return err
				}
				defer download.File.Close()
				hash := sha256.New()
				size, err := io.Copy(hash, download.File)
				if err != nil || size != lease.Component.ArchiveBytes || fmt.Sprintf("%x", hash.Sum(nil)) != lease.Component.ArtifactSHA256 {
					return errors.New("leased archive did not match the pinned recipe")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		observation := wire.ComponentObservation{Component: wire.NodeExporter, Version: version, State: state,
			Managed: state != "not_installed", ObservedAt: time.Now().UTC(), PreviousVersion: previous}
		if observation.Managed {
			observation.Config = &settings
		}
		if state == "running" {
			observation.Metrics = &wire.ComponentMetrics{CPUSecondsTotal: 100, CPUIdleSecondsTotal: 80,
				MemoryTotalBytes: 1024, MemoryAvailableBytes: 512, FilesystemTotalBytes: 4096, FilesystemAvailableBytes: 2048}
		}
		result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Component: &observation, Logs: logs}
		if outcome, err := nodeService.Complete(ctx, node.NodeID, node.Credential, id, result, uuid.NewString()); err != nil || outcome != "succeeded" {
			t.Fatalf("settle component %s: %s %v", lease.Component.Action, outcome, err)
		}
	}
	const older, newer = "1.12.0", "1.12.1"
	installKey := uuid.NewString()
	install := action("install", installKey, "--version", older)
	lose.Store(true)
	c.refused("retry with the same --idempotency-key "+installKey, install...)
	if !committed.Load() {
		t.Fatal("component task was not accepted before losing the reply")
	}
	installID := task(install)
	c.count(1, "SELECT count(*) FROM node_tasks WHERE idempotency_key=$1", installKey)
	settle(installID, older, "running", "", nil)
	settings = wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 4}
	settle(task(action("configure", uuid.NewString(), "--log-level", "warn", "--max-scrape-requests", "4")), older, "running", "", nil)
	c.ok(`"max_scrape_requests": 4`, "node", "exporter", "status", node.NodeID, "-o", "json")
	settle(task(action("restart", uuid.NewString())), older, "running", "", nil)
	settle(task(action("upgrade", uuid.NewString(), "--version", newer)), newer, "running", older, nil)
	c.ok(`"previous_version": "1.12.0"`, "node", "exporter", "status", node.NodeID, "-o", "json")
	settle(task(action("rollback", uuid.NewString())), older, "running", "", nil)
	settle(task(action("stop", uuid.NewString())), older, "stopped", "", nil)
	settle(task(action("start", uuid.NewString())), older, "running", "", nil)
	settle(task(action("observe", uuid.NewString())), older, "running", "", nil)
	c.ok("running", "node", "exporter", "status", node.NodeID)

	now := time.Now().UTC()
	const message = "Lane D verified journal snapshot"
	logs := &wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute),
		Entries: []wire.ComponentLogEntry{{Timestamp: now, Priority: 6, Message: message}}}
	logID := task(action("collect-logs", uuid.NewString()))
	settle(logID, older, "running", "", logs)
	deadline := time.Now().Add(20 * time.Second)
	for {
		out, stderr, err := c.run("node", "exporter", "logs", node.NodeID, "-o", "json")
		if err == nil && strings.Contains(out, message) && strings.Contains(out, logID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("journal snapshot was not readable through the real log store: %v %s", err, stderr)
		}
		time.Sleep(200 * time.Millisecond)
	}
	c.ok(message, "node", "exporter", "logs", node.NodeID)
	c.refused("--confirm", action("remove", uuid.NewString())...)
	settle(task(action("remove", uuid.NewString(), "--confirm")), older, "not_installed", "", nil)
	c.ok("not_installed", "node", "exporter", "status", node.NodeID)
}

func enrollWorkflowNode(c *workflowCLI, name, profile string) wire.Enrolled {
	c.t.Helper()
	ctx := c.t.Context()
	service := c.handler.Nodes.Service
	join, err := service.MintForProfile(ctx, c.auth, c.auth.Me.Workspace.ID, name, profile, uuid.NewString())
	if err != nil {
		c.t.Fatal(err)
	}
	h := wire.Handshake{
		ProtocolVersion: wire.ProfileVersion(profile), ExecutionProfile: profile, Capabilities: wire.Capabilities(profile),
		Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "lane-d-node", LogicalCPUs: 4,
			MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion},
	}
	node, err := service.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if err != nil {
		c.t.Fatal(err)
	}
	return node
}
