package scope

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit fixed-store SDK effect double; no content readback is simulated as
// actual verification. Existing PKI, private files and journals remain real.
type currentStoresCheckDouble struct{ *liveDatabaseCheckDouble }

func (d *currentStoresCheckDouble) CreateDatabaseTask(ctx context.Context, t engine.DatabaseTask) (engine.RuntimeContainer, error) {
	if t.Kind == engine.DatabaseSchemaCheck || t.Kind == engine.DatabaseRuntimeCheck {
		return d.liveDatabaseCheckDouble.CreateDatabaseTask(ctx, t)
	}
	return (&restoreStoresDouble{restoreDatabaseDouble: d.effects}).CreateDatabaseTask(ctx, t)
}

func TestRestoredCurrentStoresHistoricalPlansSurviveSubsequentSQLRequalification(t *testing.T) {
	c, _, d, w := currentStoresControllerFixture(t)
	out, e := runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w)
	if e != nil || out.Pending {
		t.Fatal(out, e)
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	old, e := l.readCurrentStoresPlan(*currentStoresState(l.events).record)
	l.close()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = runRestoredLiveDatabase(context.Background(), c, "requalify-restored-live-database", d, w); e != nil {
		t.Fatal("new SQL proof after completed stores", e)
	}
	l, e = open(c, false)
	if e != nil {
		t.Fatal("historical store plan blocked new SQL", e)
	}
	retained, e := l.readCurrentStoresPlan(*currentStoresState(l.events).record)
	if e != nil || !reflect.DeepEqual(retained, old) {
		l.close()
		t.Fatal("old SQL lineage rewritten", e)
	}
	current, _, e := l.makeRestoredCurrentStoresPlan(context.Background(), d, w, strings.Repeat("9", 32))
	l.close()
	if e != nil || current.DatabaseProofSHA256 == old.DatabaseProofSHA256 {
		t.Fatal("current SQL proof not refreshed", e)
	}
	again, e := runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w)
	if e != nil || again != out {
		t.Fatal("historical repeat", e)
	}
	next, e := runRestoredCurrentStores(context.Background(), c, "requalify-restored-current-stores", d, w)
	if e != nil || next.Pending || next.GenerationID == out.GenerationID {
		t.Fatal("new store proof", next, e)
	}
}

func TestRestoredCurrentStoresPendingGenerationBlocksNewSQLIntent(t *testing.T) {
	c, _, d, w := currentStoresControllerFixture(t)
	d.effects.fault = "volume-before"
	if _, e := runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w); e == nil {
		t.Fatal("unknown unsubmitted effect required")
	}
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if _, e := runRestoredLiveDatabase(context.Background(), c, "requalify-restored-live-database", d, w); e == nil || e.Error() != "OFFLINE_SCOPE_CURRENT_STORES_CLEANUP_REQUIRED" {
		t.Fatal("new SQL effect admitted before store cleanup", e)
	}
	after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if !bytes.Equal(before, after) {
		t.Fatal("failed concurrent admission changed journal")
	}
	if _, e := runRestoredCurrentStores(context.Background(), c, "abort-restored-current-stores", d, w); e != nil {
		t.Fatal(e)
	}
	if _, e := runRestoredLiveDatabase(context.Background(), c, "requalify-restored-live-database", d, w); e != nil {
		t.Fatal("SQL remained blocked after cleanup", e)
	}
}

func TestRestoredCurrentStoresPublicationWithoutSDKEffectsCanAbortMissingOrPartialMetadata(t *testing.T) {
	for _, fault := range []string{"missing", "partial"} {
		t.Run(fault, func(t *testing.T) {
			c, _, d, w := currentStoresControllerFixture(t)
			l, e := open(c, true)
			if e != nil {
				t.Fatal(e)
			}
			p, _, e := l.makeRestoredCurrentStoresPlan(context.Background(), d, w, strings.Repeat("9", 32))
			if e != nil {
				l.close()
				t.Fatal(e)
			}
			r := currentStoresPlanRecord(p)
			id, e := nonce()
			if e != nil {
				l.close()
				t.Fatal(e)
			}
			if e = l.appendEvent(Event{OperationID: id, Action: "current_stores_effect", Outcome: "started", RestoredCurrentStores: &r}); e != nil {
				l.close()
				t.Fatal(e)
			}
			if fault == "partial" {
				if e = writeNew(l.root, currentStoresPlanFile(p.GenerationID), []byte("{")); e != nil {
					l.close()
					t.Fatal(e)
				}
			}
			l.close()
			out, e := runRestoredCurrentStores(context.Background(), c, "abort-restored-current-stores", d, w)
			if e != nil || out.Pending || out.ProofSHA256 != "" || d.effects.creates != 0 || d.effects.puts != 0 || len(d.effects.starts) != 0 {
				t.Fatal("metadata-only abort submitted SDK effect", out, e)
			}
			if fault == "partial" {
				raw, e := os.ReadFile(c.ScopeRoot + "/" + currentStoresPlanFile(p.GenerationID))
				if e != nil || string(raw) != "{" {
					t.Fatal("partial evidence replaced", e)
				}
			}
			if _, e = Read(context.Background(), c); e != nil {
				t.Fatal("aborted metadata unreadable", e)
			}
			next, e := runRestoredCurrentStores(context.Background(), c, "requalify-restored-current-stores", d, w)
			if e != nil || next.Pending || next.ProofSHA256 == "" || next.GenerationID == p.GenerationID {
				t.Fatal("fresh generation after publication abort", next, e)
			}
		})
	}
}

var _ currentStoresEngine = (*currentStoresCheckDouble)(nil)

func currentStoresControllerFixture(t *testing.T) (configuration.Config, configuration.TLSGenerationRequest, *currentStoresCheckDouble, *dependencyWorkerDouble) {
	t.Helper()
	c, request, d, w := liveDatabaseFixture(t)
	if _, e := runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w); e != nil {
		t.Fatal(e)
	}
	d.effects.creates, d.effects.puts, d.effects.removes, d.effects.stops = 0, 0, 0, 0
	d.effects.taskCreates = map[string]int{}
	d.effects.starts = map[string]int{}
	d.effects.tasks = map[string]engine.RuntimeContainer{}
	d.effects.definitions = map[string]engine.DatabaseTask{}
	return c, request, &currentStoresCheckDouble{d}, w
}

func TestRestoredCurrentStoresControllerCompletesAndExplicitRequalificationUsesFreshGeneration(t *testing.T) {
	c, request, d, w := currentStoresControllerFixture(t)
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	out, e := runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w)
	if e != nil || out.Pending || out.ProofSHA256 == "" || out.CompletedSteps != 13 || d.effects.volume || d.effects.utility || len(d.effects.tasks) != 0 || d.effects.starts[engine.ModelFilesVerify] != 1 || d.effects.starts[engine.DatasetFilesVerify] != 1 {
		t.Fatal("checks", out, e)
	}
	if len(d.effects.starts) != 2 || w.databaseStarts != 1 || w.state.Services["control"].State != "created" {
		t.Fatal("mutating/application effect")
	}
	journal, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	again, e := runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w)
	after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if e != nil || again != out || !bytes.Equal(journal, after) || !bytes.HasPrefix(after, before) {
		t.Fatal("terminal replay", e)
	}
	next, e := runRestoredCurrentStores(context.Background(), c, "requalify-restored-current-stores", d, w)
	if e != nil || next.Pending || next.GenerationID == out.GenerationID || next.ProofSHA256 == out.ProofSHA256 || d.effects.starts[engine.ModelFilesVerify] != 2 {
		t.Fatal("fresh qualification", next, e)
	}
	if e = os.Remove(request.PrivateKeyFile); e != nil {
		t.Fatal(e)
	}
	again, e = runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w)
	if e != nil || again != next {
		t.Fatal("historical repeat required", e)
	}
	if _, e = runRestoredCurrentStores(context.Background(), c, "requalify-restored-current-stores", d, w); e == nil {
		t.Fatal("current credential loss ignored")
	}
	s, e := Read(context.Background(), c)
	if e != nil || s.RestoredCurrentStores == nil || s.RestoredCurrentStores.ProofSHA256 != next.ProofSHA256 {
		t.Fatal("reopen", e)
	}
	if _, e = processRuntime(context.Background(), c, d, w, "running", false); e == nil {
		t.Fatal("database check activated application")
	}
}
func TestRestoredCurrentStoresControllerLostCompletedEffectsReconcileWithoutResend(t *testing.T) {
	for _, fault := range []string{"payload-after", "create-after", "start-after", "remove-after"} {
		t.Run(fault, func(t *testing.T) {
			c, _, d, w := currentStoresControllerFixture(t)
			d.effects.fault = fault
			out, e := runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w)
			if e == nil || !out.Pending {
				t.Fatal("unknown effect required", out, e)
			}
			counts := []int{d.effects.creates, d.effects.puts, d.effects.taskCreates[engine.ModelFilesVerify], d.effects.starts[engine.ModelFilesVerify], d.effects.removes}
			if _, e = runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w); e == nil {
				t.Fatal("ordinary resend")
			}
			if !reflect.DeepEqual(counts, []int{d.effects.creates, d.effects.puts, d.effects.taskCreates[engine.ModelFilesVerify], d.effects.starts[engine.ModelFilesVerify], d.effects.removes}) {
				t.Fatal("pending effect resent")
			}
			out, e = runRestoredCurrentStores(context.Background(), c, "reconcile-restored-current-stores", d, w)
			if e != nil || out.Pending || out.ProofSHA256 == "" || d.effects.creates != 1 || d.effects.puts != 1 || d.effects.taskCreates[engine.ModelFilesVerify] != 1 || d.effects.starts[engine.ModelFilesVerify] != 1 || d.effects.removes != 2 {
				t.Fatal("observation recovery", out, e)
			}
		})
	}
}
func TestRestoredCurrentStoresFailedOrUnsubmittedChecksAbortAndCanRequalify(t *testing.T) {
	for _, fault := range []string{"task-failed", "volume-before"} {
		t.Run(fault, func(t *testing.T) {
			c, request, d, w := currentStoresControllerFixture(t)
			d.effects.fault = fault
			out, e := runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w)
			if e == nil || !out.Pending || out.ProofSHA256 != "" {
				t.Fatal("failure required", out, e)
			}
			if _, e = runRestoredCurrentStores(context.Background(), c, "reconcile-restored-current-stores", d, w); e == nil {
				t.Fatal("failed/unsubmitted check accepted")
			}
			key, e := os.ReadFile(request.PrivateKeyFile)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.Remove(request.PrivateKeyFile); e != nil {
				t.Fatal(e)
			}
			out, e = runRestoredCurrentStores(context.Background(), c, "abort-restored-current-stores", d, w)
			if e != nil || out.Pending || out.ProofSHA256 != "" || d.effects.volume || d.effects.utility || len(d.effects.tasks) != 0 {
				t.Fatal("source-independent abort", out, e)
			}
			if w.state.Services["postgres"].State != "running" || w.state.Services["control"].State != "created" || len(d.volumes) != 1 {
				t.Fatal("abort changed retained target")
			}
			if e = os.WriteFile(request.PrivateKeyFile, key, 0600); e != nil {
				t.Fatal(e)
			}
			out, e = runRestoredCurrentStores(context.Background(), c, "requalify-restored-current-stores", d, w)
			if e != nil || out.Pending || out.ProofSHA256 == "" {
				t.Fatal("fresh recovery", out, e)
			}
		})
	}
}
func TestRestoredCurrentStoresCancellationCleansGeneratedActorsOnly(t *testing.T) {
	c, _, d, w := currentStoresControllerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.effects.cancel = cancel
	out, e := runRestoredCurrentStores(ctx, c, "qualify-restored-current-stores", d, w)
	if e == nil || out.Pending || out.ProofSHA256 != "" || d.effects.stops != 1 || d.effects.volume || d.effects.utility || len(d.effects.tasks) != 0 || w.state.Services["postgres"].State != "running" || w.state.Services["control"].State != "created" {
		t.Fatal("cancel cleanup", out, e)
	}
	if _, e = Read(context.Background(), c); e != nil {
		t.Fatal("cancel journal", e)
	}
}
func TestRestoredCurrentStoresRestartDriftRefusesRecoveryButAllowsSafeAbort(t *testing.T) {
	c, _, d, w := currentStoresControllerFixture(t)
	d.effects.fault = "start-after"
	if _, e := runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w); e == nil {
		t.Fatal("response loss required")
	}
	d.process = func(r *engine.RuntimeContainer) { r.State.PID = 456; r.State.StartedAt = "2026-10-17T00:01:01Z" }
	if _, e := runRestoredCurrentStores(context.Background(), c, "reconcile-restored-current-stores", d, w); e == nil {
		t.Fatal("restarted PostgreSQL accepted")
	}
	out, e := runRestoredCurrentStores(context.Background(), c, "abort-restored-current-stores", d, w)
	if e != nil || out.Pending || out.ProofSHA256 != "" {
		t.Fatal("cleanup after drift", out, e)
	}
}

func TestRestoredCurrentStoresRuntimeViewMasksOnlyItsExactCheckingActor(t *testing.T) {
	c, _, d, w := currentStoresControllerFixture(t)
	d.effects.fault = "start-after"
	if _, e := runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w); e == nil {
		t.Fatal("response loss required")
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	p, e := l.readCurrentStoresPlan(*currentStoresState(l.events).record)
	if e != nil {
		t.Fatal(e)
	}
	task := d.effects.tasks[engine.ModelFilesVerify]
	task.State.Running = true
	task.State.Status = "running"
	task.State.PID = 42
	d.effects.tasks[engine.ModelFilesVerify] = task
	d.foreign = true
	view, e := l.currentStoresRuntimeView(context.Background(), d, p)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := view.RuntimeCandidates(context.Background(), p.ScopeID, false)
	if e != nil {
		t.Fatal(e)
	}
	foreign := false
	for _, object := range rows {
		if object.ID == task.ID {
			t.Fatal("owned checking actor leaked into runtime view")
		}
		if object.ID == strings.Repeat("7", 64) {
			foreign = true
		}
	}
	if !foreign {
		t.Fatal("foreign actor hidden")
	}
	n, found, e := view.InspectRuntimeNetwork(context.Background(), p.ScopeID)
	if e != nil || !found {
		t.Fatal(e)
	}
	if _, exists := n.Containers[task.ID]; exists {
		t.Fatal("owned checking endpoint not masked")
	}
	if _, exists := n.Containers[strings.Repeat("7", 64)]; !exists {
		t.Fatal("foreign endpoint hidden")
	}
	original, _, _ := d.InspectRuntimeNetwork(context.Background(), p.ScopeID)
	if _, exists := original.Containers[task.ID]; !exists {
		t.Fatal("underlying observation rewritten")
	}
}

func TestRestoredCurrentStoresAbsentFirstPublicationReconstructsOnlyExactPlan(t *testing.T) {
	c, _, d, w := currentStoresControllerFixture(t)
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	plan, _, e := l.makeRestoredCurrentStoresPlan(context.Background(), d, w, strings.Repeat("9", 32))
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	r := currentStoresPlanRecord(plan)
	id, e := nonce()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	if e = l.appendEvent(Event{OperationID: id, Action: "current_stores_effect", Outcome: "started", RestoredCurrentStores: &r}); e != nil {
		l.close()
		t.Fatal(e)
	}
	l.close()
	out, e := runRestoredCurrentStores(context.Background(), c, "reconcile-restored-current-stores", d, w)
	if e != nil || out.Pending || out.ProofSHA256 == "" || d.effects.creates != 1 {
		t.Fatal("exact publication recovery", out, e)
	}
	if e = os.Remove(c.ScopeRoot + "/" + currentStoresPlanFile(plan.GenerationID)); e != nil {
		t.Fatal(e)
	}
	if _, e = Read(context.Background(), c); e == nil {
		t.Fatal("missing completed plan silently accepted")
	}
}
func TestRestoredCurrentStoresJournalRejectsForgedOrderLineageAndActions(t *testing.T) {
	c, _, d, w := currentStoresControllerFixture(t)
	d.effects.fault = "volume-before"
	if _, e := runRestoredCurrentStores(context.Background(), c, "qualify-restored-current-stores", d, w); e == nil {
		t.Fatal("unsubmitted required")
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	first := 0
	for i, event := range l.events {
		if event.RestoredCurrentStores != nil {
			first = i
			break
		}
	}
	event := l.events[first]
	prefix := l.events[:first]
	if validateCurrentStoresEvent(prefix, event) != nil {
		t.Fatal("valid intent")
	}
	for _, fault := range []string{"step", "generation", "proof", "action", "combined", "object", "outcome", "reference"} {
		t.Run(fault, func(t *testing.T) {
			var altered Event
			if wire.StrictJSON(encode(event), &altered) != nil {
				t.Fatal("decode")
			}
			switch fault {
			case "step":
				altered.RestoredCurrentStores.Step = 1
			case "generation":
				altered.RestoredCurrentStores.GenerationID = prefix[0].OperationID
			case "proof":
				altered.RestoredCurrentStores.Admission.TLSProofSHA256 = strings.Repeat("a", 64)
			case "action":
				altered.Action = "process_start"
			case "combined":
				altered.Process = &ProcessRecord{}
			case "object":
				altered.RestoredCurrentStores.ObjectID = strings.Repeat("a", 64)
			case "outcome":
				altered.Outcome = "completed"
			case "reference":
				altered.ReferenceOperationID = prefix[0].OperationID
			}
			if validateImageEvent(prefix, altered) == nil {
				t.Fatal("forgery admitted")
			}
		})
	}
	p, e := l.readCurrentStoresPlan(*event.RestoredCurrentStores)
	if e != nil {
		t.Fatal(e)
	}
	for _, fault := range []string{"mutating-task", "stale-peer", "file", "process"} {
		t.Run(fault, func(t *testing.T) {
			var altered RestoredCurrentStoresPlan
			if wire.StrictJSON(encode(p), &altered) != nil {
				t.Fatal("decode")
			}
			switch fault {
			case "mutating-task":
				altered.Tasks[0].Kind = engine.DatabaseMigrate
			case "stale-peer":
				altered.Tasks[0].Peers["control"] = runtimeState(l.events).record.Services["control"].ID
			case "file":
				altered.Files[0].SHA256 = strings.Repeat("a", 64)
			case "process":
				altered.PostgresPID = 0
			}
			if l.validateCurrentStoresPlan(altered) == nil {
				t.Fatal("invalid plan admitted")
			}
		})
	}
}
