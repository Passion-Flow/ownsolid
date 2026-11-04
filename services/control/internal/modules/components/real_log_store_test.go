//go:build telemetry_integration

package components_test

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

// waitForLogs polls until the store shows what accept wants: a new record is readable
// about a second after it is written.
func waitForLogs(t *testing.T, f maintenanceScenario, accept func(components.LogSnapshot) bool) components.LogSnapshot {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		view, e := f.service.Logs(context.Background(), f.admin, f.node.NodeID)
		if e == nil && accept(view) {
			return view
		}
		if time.Now().After(deadline) {
			t.Fatalf("the log store never showed the expected snapshot: %+v %v", view, e)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestComponentLogsThroughARealStore runs the whole control-plane path against real
// PostgreSQL and the real log store at OWNSOLID_TEST_LOGS_ADDRESS: a settled task
// writes through the queue, the reader confirms it against node_tasks, a newer snapshot
// wins, and removal leaves a tombstone that hides everything before it.
func TestComponentLogsThroughARealStore(t *testing.T) {
	address := os.Getenv("OWNSOLID_TEST_LOGS_ADDRESS")
	if address == "" {
		t.Fatal("OWNSOLID_TEST_LOGS_ADDRESS must name a log store")
	}
	store, e := telemetrystore.Open(telemetrystore.Settings{LogsAddress: address, RequestTimeout: 5 * time.Second, QueueCapacity: 16}, slog.New(slog.DiscardHandler))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); store.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	f := maintenanceSetup(t)
	f.service.LogStore = store
	defaults := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	maintenanceInstalled(t, f, defaults)
	observation := maintenanceObservation("1.12.0", &defaults)

	complete := func(message string) string {
		t.Helper()
		task, e := f.service.Request(ctx, f.admin, f.node.NodeID, "logs", uuid.NewString(), "Synthetic log read", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		now := time.Now().UTC()
		logs := wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute), Entries: []wire.ComponentLogEntry{{Timestamp: now, Priority: 6, Message: message}}}
		completeMaintenance(t, f, task, observation, &logs)
		return task.ID
	}
	showing := func(taskID, message string) func(components.LogSnapshot) bool {
		return func(v components.LogSnapshot) bool {
			return v.TaskID != nil && *v.TaskID == taskID && v.Snapshot != nil && v.Snapshot.Entries[0].Message == message
		}
	}

	first := complete("real store first é\"{}")
	waitForLogs(t, f, showing(first, "real store first é\"{}"))
	second := complete("real store second")
	waitForLogs(t, f, showing(second, "real store second"))

	remove, e := f.service.Request(ctx, f.admin, f.node.NodeID, "remove", uuid.NewString(), "Synthetic explicit retention", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	absent := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed", ObservedAt: time.Now().UTC()}
	completeMaintenance(t, f, remove, absent, nil)
	waitForLogs(t, f, func(v components.LogSnapshot) bool { return v.Snapshot == nil })
	if store.Dropped() != 0 || store.Failed() != 0 {
		t.Fatalf("dropped %d, failed %d", store.Dropped(), store.Failed())
	}
}
