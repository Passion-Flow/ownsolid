//go:build telemetry_integration

package components_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	componenthttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/components/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
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

func TestComponentLogReadsBoundAndRecheckThroughARealStore(t *testing.T) {
	address := os.Getenv("OWNSOLID_TEST_LOGS_ADDRESS")
	target, err := url.Parse(address)
	if err != nil || target.Host == "" {
		t.Fatal("OWNSOLID_TEST_LOGS_ADDRESS must name a log store")
	}
	type heldRead struct{ entered, release, disconnected chan struct{} }
	holds := make(chan heldRead, 1)
	proxy := httputil.NewSingleHostReverseProxy(target)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/select/logsql/query" {
			raw, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			fields, parseErr := url.ParseQuery(string(raw))
			if err != nil || parseErr != nil || !strings.HasSuffix(fields.Get("query"), " limit 16") {
				t.Error("component read did not use the bounded VictoriaLogs query")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			select {
			case hold := <-holds:
				close(hold.entered)
				select {
				case <-hold.release:
				case <-r.Context().Done():
					close(hold.disconnected)
					return
				}
			default:
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	f := maintenanceSetupWithIdentity(t, account, password)
	secondSession := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	if secondSession.SessionID == f.admin.SessionID {
		t.Fatal("the test needs two sessions for the same user")
	}
	store, err := telemetrystore.Open(telemetrystore.Settings{LogsAddress: server.URL, RequestTimeout: 10 * time.Second, QueueCapacity: 16}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	f.service.LogStore = store
	storeContext, stopStore := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); _ = store.Run(storeContext) }()
	t.Cleanup(func() { stopStore(); <-done })

	handler := componenthttp.Handler{Service: f.service}
	path := "/api/v1/admin/nodes/" + f.node.NodeID + "/components/node-exporter/logs"
	type result struct {
		snapshot components.LogSnapshot
		err      error
	}
	read := func(ctx context.Context, actor identity.Auth) result {
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		out, _, err := handler.Handle(req, actor, uuid.NewString())
		snapshot, _ := out.(components.LogSnapshot)
		return result{snapshot: snapshot, err: err}
	}
	await := func(ch <-chan struct{}, message string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatal(message)
		}
	}
	startHeld := func() (heldRead, context.CancelFunc, <-chan result) {
		t.Helper()
		hold := heldRead{make(chan struct{}), make(chan struct{}), make(chan struct{})}
		holds <- hold
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		reply := make(chan result, 1)
		go func() { reply <- read(ctx, f.admin) }()
		await(hold.entered, "the component read did not reach the real store proxy")
		return hold, cancel, reply
	}
	requireRefusal := func(reply <-chan result, code string) {
		t.Helper()
		select {
		case got := <-reply:
			expect(t, got.err, code)
			if got.snapshot.NodeID != "" || got.snapshot.Snapshot != nil || got.snapshot.TaskID != nil {
				t.Fatal("a refused read exposed log data")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the component read did not exit")
		}
	}
	revokeDuringRead := func() {
		t.Helper()
		hold, cancel, reply := startHeld()
		defer cancel()
		if _, err := account.DB.Exec(t.Context(), "UPDATE users SET is_platform_admin=false WHERE id=$1", f.admin.Me.User.ID); err != nil {
			t.Fatal(err)
		}
		close(hold.release)
		requireRefusal(reply, "AUTH_SESSION_EXPIRED")
		if _, err := account.DB.Exec(t.Context(), "UPDATE users SET is_platform_admin=true WHERE id=$1", f.admin.Me.User.ID); err != nil {
			t.Fatal(err)
		}
	}

	revokeDuringRead()
	defaults := wire.DefaultComponentConfig()
	maintenanceInstalled(t, f, defaults)
	task, err := f.service.Request(t.Context(), f.admin, f.node.NodeID, "logs", uuid.NewString(), "Synthetic bounded snapshot", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	completeMaintenance(t, f, task, maintenanceObservation("1.12.0", &defaults), &wire.ComponentLogs{
		Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute),
		Entries: []wire.ComponentLogEntry{{Timestamp: now, Priority: 6, Message: "bounded real store snapshot"}},
	})
	waitForLogs(t, f, func(v components.LogSnapshot) bool { return v.Snapshot != nil })

	hold, cancel, reply := startHeld()
	busy := read(t.Context(), secondSession)
	expect(t, busy.err, "RESOURCE_LIMIT_REACHED")
	if busy.snapshot.NodeID != "" || busy.snapshot.Snapshot != nil {
		t.Fatal("concurrent read returned log data")
	}
	cancel()
	await(hold.disconnected, "request cancellation did not reach VictoriaLogs transport")
	requireRefusal(reply, string(transaction.Cancelled))
	if retry := read(t.Context(), secondSession); retry.err != nil || retry.snapshot.Snapshot == nil {
		t.Fatalf("the canceled read did not release its slot: %v", retry.err)
	}
	revokeDuringRead()
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

	first := complete("real store first \u00e9\"{}")
	waitForLogs(t, f, showing(first, "real store first \u00e9\"{}"))
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
