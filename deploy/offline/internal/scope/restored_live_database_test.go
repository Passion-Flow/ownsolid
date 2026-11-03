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

// Explicit SDK effects only; private snapshots, PKI, filesystem and journal
// remain real. Actual SQL checks have a separate excluded integration journey.
type liveDatabaseCheckDouble struct {
	*liveDatabaseAdmissionDouble
	effects *restoreDatabaseDouble
	foreign bool
}

func (d *liveDatabaseCheckDouble) CreateVolume(ctx context.Context, o engine.Ownership) (engine.Volume, error) {
	return d.effects.CreateVolume(ctx, o)
}
func (d *liveDatabaseCheckDouble) InspectVolume(ctx context.Context, o engine.Ownership) (engine.Volume, bool, error) {
	if d.effects.generated(o) {
		return d.effects.InspectVolume(ctx, o)
	}
	return d.liveDatabaseAdmissionDouble.InspectVolume(ctx, o)
}
func (d *liveDatabaseCheckDouble) CreateUtility(ctx context.Context, o engine.Ownership, image string, uid, gid int) (engine.Utility, error) {
	return d.effects.CreateUtility(ctx, o, image, uid, gid)
}
func (d *liveDatabaseCheckDouble) InspectUtility(ctx context.Context, o engine.Ownership, image string, uid, gid int) (engine.Utility, bool, error) {
	if d.effects.generated(o) {
		return d.effects.InspectUtility(ctx, o, image, uid, gid)
	}
	return d.liveDatabaseAdmissionDouble.InspectUtility(ctx, o, image, uid, gid)
}
func (d *liveDatabaseCheckDouble) PutConfiguration(ctx context.Context, o engine.Ownership, image string, uid, gid int, files []engine.ConfigurationFile) error {
	return d.effects.PutConfiguration(ctx, o, image, uid, gid, files)
}
func (d *liveDatabaseCheckDouble) InspectConfiguration(ctx context.Context, o engine.Ownership, image string, uid, gid int, files []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	return d.effects.InspectConfiguration(ctx, o, image, uid, gid, files)
}
func (d *liveDatabaseCheckDouble) RemoveUtility(ctx context.Context, o engine.Ownership, image string, uid, gid int) error {
	return d.effects.RemoveUtility(ctx, o, image, uid, gid)
}
func (d *liveDatabaseCheckDouble) RemoveStoreRestoreVolume(ctx context.Context, o engine.Ownership) error {
	return d.effects.RemoveStoreRestoreVolume(ctx, o)
}
func (d *liveDatabaseCheckDouble) CreateDatabaseTask(ctx context.Context, t engine.DatabaseTask) (engine.RuntimeContainer, error) {
	return d.effects.CreateDatabaseTask(ctx, t)
}
func (d *liveDatabaseCheckDouble) InspectDatabaseTask(ctx context.Context, t engine.DatabaseTask) (engine.RuntimeContainer, bool, error) {
	return d.effects.InspectDatabaseTask(ctx, t)
}
func (d *liveDatabaseCheckDouble) StartDatabaseTask(ctx context.Context, t engine.DatabaseTask, id string, files []engine.ConfigurationFile, reserve int) (engine.ResourceAdmission, error) {
	return d.effects.StartDatabaseTask(ctx, t, id, files, reserve)
}
func (d *liveDatabaseCheckDouble) StopDatabaseTask(ctx context.Context, t engine.DatabaseTask, id string, seconds int) error {
	return d.effects.StopDatabaseTask(ctx, t, id, seconds)
}
func (d *liveDatabaseCheckDouble) RemoveDatabaseTask(ctx context.Context, t engine.DatabaseTask, id string) error {
	return d.effects.RemoveDatabaseTask(ctx, t, id)
}
func (d *liveDatabaseCheckDouble) RuntimeCandidates(ctx context.Context, scope string, networks bool) ([]engine.RuntimeObject, error) {
	// This double owns actual candidate metadata instead of delegating to the
	// older worker-only fixture, whose SDK reader intentionally returns errors.
	objects, e := d.effects.RuntimeCandidates(ctx, scope, networks)
	if d.foreign {
		objects = append(objects, engine.RuntimeObject{ID: strings.Repeat("7", 64)})
	}
	if e != nil {
		return nil, e
	}
	return objects, nil
}
func (d *liveDatabaseCheckDouble) InspectRuntimeNetwork(_ context.Context, scope string) (engine.RuntimeNetwork, bool, error) {
	n := engine.RuntimeNetwork{ID: d.worker.state.NetworkID}
	n.Containers = map[string]struct{ Name string }{}
	for _, service := range d.worker.state.Services {
		if service.State == "running" {
			n.Containers[service.ID] = struct{ Name string }{Name: "peer"}
		}
	}
	for _, r := range d.effects.tasks {
		if r.State.Running {
			n.Containers[r.ID] = struct{ Name string }{Name: strings.TrimPrefix(r.Name, "/")}
		}
	}
	if d.foreign {
		n.Containers[strings.Repeat("7", 64)] = struct{ Name string }{Name: "foreign"}
	}
	return n, scope == d.scope, nil
}

var _ liveDatabaseEngine = (*liveDatabaseCheckDouble)(nil)

func liveDatabaseFixture(t *testing.T) (configuration.Config, configuration.TLSGenerationRequest, *liveDatabaseCheckDouble, *dependencyWorkerDouble) {
	t.Helper()
	c, request, original, w := completedDependencyFixture(t)
	if _, e := startDependencyDouble(t, c, original, w); e != nil {
		t.Fatal(e)
	}
	effects := &restoreDatabaseDouble{databaseEngineDouble: original.databaseEngineDouble, scope: original.scope, taskCreates: map[string]int{}, starts: map[string]int{}, tasks: map[string]engine.RuntimeContainer{}, definitions: map[string]engine.DatabaseTask{}}
	return c, request, &liveDatabaseCheckDouble{liveDatabaseAdmissionDouble: &liveDatabaseAdmissionDouble{replacementEngineDouble: original}, effects: effects}, w
}
func TestRestoredLiveDatabaseControllerCompletesAndExplicitRequalificationUsesFreshGeneration(t *testing.T) {
	c, request, d, w := liveDatabaseFixture(t)
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	out, e := runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w)
	if e != nil || out.Pending || out.ProofSHA256 == "" || out.CompletedSteps != 13 || d.effects.volume || d.effects.utility || len(d.effects.tasks) != 0 || d.effects.starts[engine.DatabaseSchemaCheck] != 1 || d.effects.starts[engine.DatabaseRuntimeCheck] != 1 {
		t.Fatal("checks", out, e)
	}
	if len(d.effects.starts) != 2 || w.databaseStarts != 1 || w.state.Services["control"].State != "created" {
		t.Fatal("mutating/application effect")
	}
	journal, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	again, e := runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w)
	after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if e != nil || again != out || !bytes.Equal(journal, after) || !bytes.HasPrefix(after, before) {
		t.Fatal("terminal replay", e)
	}
	next, e := runRestoredLiveDatabase(context.Background(), c, "requalify-restored-live-database", d, w)
	if e != nil || next.Pending || next.GenerationID == out.GenerationID || next.ProofSHA256 == out.ProofSHA256 || d.effects.starts[engine.DatabaseSchemaCheck] != 2 {
		t.Fatal("fresh qualification", next, e)
	}
	if e = os.Remove(request.PrivateKeyFile); e != nil {
		t.Fatal(e)
	}
	again, e = runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w)
	if e != nil || again != next {
		t.Fatal("historical repeat required", e)
	}
	if _, e = runRestoredLiveDatabase(context.Background(), c, "requalify-restored-live-database", d, w); e == nil {
		t.Fatal("current credential loss ignored")
	}
	s, e := Read(context.Background(), c)
	if e != nil || s.RestoredLiveDatabase == nil || s.RestoredLiveDatabase.ProofSHA256 != next.ProofSHA256 {
		t.Fatal("reopen", e)
	}
	if _, e = processRuntime(context.Background(), c, d, w, "running", false); e == nil {
		t.Fatal("database check activated application")
	}
}
func TestRestoredLiveDatabaseControllerLostCompletedEffectsReconcileWithoutResend(t *testing.T) {
	for _, fault := range []string{"payload-after", "create-after", "start-after", "remove-after"} {
		t.Run(fault, func(t *testing.T) {
			c, _, d, w := liveDatabaseFixture(t)
			d.effects.fault = fault
			out, e := runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w)
			if e == nil || !out.Pending {
				t.Fatal("unknown effect required", out, e)
			}
			counts := []int{d.effects.creates, d.effects.puts, d.effects.taskCreates[engine.DatabaseSchemaCheck], d.effects.starts[engine.DatabaseSchemaCheck], d.effects.removes}
			if _, e = runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w); e == nil {
				t.Fatal("ordinary resend")
			}
			if !reflect.DeepEqual(counts, []int{d.effects.creates, d.effects.puts, d.effects.taskCreates[engine.DatabaseSchemaCheck], d.effects.starts[engine.DatabaseSchemaCheck], d.effects.removes}) {
				t.Fatal("pending effect resent")
			}
			out, e = runRestoredLiveDatabase(context.Background(), c, "reconcile-restored-live-database", d, w)
			if e != nil || out.Pending || out.ProofSHA256 == "" || d.effects.creates != 1 || d.effects.puts != 1 || d.effects.taskCreates[engine.DatabaseSchemaCheck] != 1 || d.effects.starts[engine.DatabaseSchemaCheck] != 1 || d.effects.removes != 2 {
				t.Fatal("observation recovery", out, e)
			}
		})
	}
}
func TestRestoredLiveDatabaseFailedOrUnsubmittedChecksAbortAndCanRequalify(t *testing.T) {
	for _, fault := range []string{"task-failed", "volume-before"} {
		t.Run(fault, func(t *testing.T) {
			c, request, d, w := liveDatabaseFixture(t)
			d.effects.fault = fault
			out, e := runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w)
			if e == nil || !out.Pending || out.ProofSHA256 != "" {
				t.Fatal("failure required", out, e)
			}
			if _, e = runRestoredLiveDatabase(context.Background(), c, "reconcile-restored-live-database", d, w); e == nil {
				t.Fatal("failed/unsubmitted check accepted")
			}
			key, e := os.ReadFile(request.PrivateKeyFile)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.Remove(request.PrivateKeyFile); e != nil {
				t.Fatal(e)
			}
			out, e = runRestoredLiveDatabase(context.Background(), c, "abort-restored-live-database", d, w)
			if e != nil || out.Pending || out.ProofSHA256 != "" || d.effects.volume || d.effects.utility || len(d.effects.tasks) != 0 {
				t.Fatal("source-independent abort", out, e)
			}
			if w.state.Services["postgres"].State != "running" || w.state.Services["control"].State != "created" || len(d.volumes) != 1 {
				t.Fatal("abort changed retained target")
			}
			if e = os.WriteFile(request.PrivateKeyFile, key, 0600); e != nil {
				t.Fatal(e)
			}
			out, e = runRestoredLiveDatabase(context.Background(), c, "requalify-restored-live-database", d, w)
			if e != nil || out.Pending || out.ProofSHA256 == "" {
				t.Fatal("fresh recovery", out, e)
			}
		})
	}
}
func TestRestoredLiveDatabaseCancellationCleansGeneratedActorsOnly(t *testing.T) {
	c, _, d, w := liveDatabaseFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.effects.cancel = cancel
	out, e := runRestoredLiveDatabase(ctx, c, "qualify-restored-live-database", d, w)
	if e == nil || out.Pending || out.ProofSHA256 != "" || d.effects.stops != 1 || d.effects.volume || d.effects.utility || len(d.effects.tasks) != 0 || w.state.Services["postgres"].State != "running" || w.state.Services["control"].State != "created" {
		t.Fatal("cancel cleanup", out, e)
	}
	if _, e = Read(context.Background(), c); e != nil {
		t.Fatal("cancel journal", e)
	}
}
func TestRestoredLiveDatabaseRestartDriftRefusesRecoveryButAllowsSafeAbort(t *testing.T) {
	c, _, d, w := liveDatabaseFixture(t)
	d.effects.fault = "start-after"
	if _, e := runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w); e == nil {
		t.Fatal("response loss required")
	}
	d.process = func(r *engine.RuntimeContainer) { r.State.PID = 456; r.State.StartedAt = "2026-10-17T00:01:01Z" }
	if _, e := runRestoredLiveDatabase(context.Background(), c, "reconcile-restored-live-database", d, w); e == nil {
		t.Fatal("restarted PostgreSQL accepted")
	}
	out, e := runRestoredLiveDatabase(context.Background(), c, "abort-restored-live-database", d, w)
	if e != nil || out.Pending || out.ProofSHA256 != "" {
		t.Fatal("cleanup after drift", out, e)
	}
}

func TestRestoredLiveDatabaseRuntimeViewMasksOnlyItsExactCheckingActor(t *testing.T) {
	c, _, d, w := liveDatabaseFixture(t)
	d.effects.fault = "start-after"
	if _, e := runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w); e == nil {
		t.Fatal("response loss required")
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	p, e := l.readLiveDatabasePlan(*liveDatabaseState(l.events).record)
	if e != nil {
		t.Fatal(e)
	}
	task := d.effects.tasks[engine.DatabaseSchemaCheck]
	task.State.Running = true
	task.State.Status = "running"
	task.State.PID = 42
	d.effects.tasks[engine.DatabaseSchemaCheck] = task
	d.foreign = true
	view, e := l.liveDatabaseRuntimeView(context.Background(), d, p)
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

func TestRestoredLiveDatabaseAbsentFirstPublicationReconstructsOnlyExactPlan(t *testing.T) {
	c, _, d, w := liveDatabaseFixture(t)
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	plan, _, e := l.makeRestoredLiveDatabasePlan(context.Background(), d, w, strings.Repeat("9", 32))
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	r := liveDatabasePlanRecord(plan)
	id, e := nonce()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	if e = l.appendEvent(Event{OperationID: id, Action: "live_database_effect", Outcome: "started", RestoredLiveDatabase: &r}); e != nil {
		l.close()
		t.Fatal(e)
	}
	l.close()
	out, e := runRestoredLiveDatabase(context.Background(), c, "reconcile-restored-live-database", d, w)
	if e != nil || out.Pending || out.ProofSHA256 == "" || d.effects.creates != 1 {
		t.Fatal("exact publication recovery", out, e)
	}
	if e = os.Remove(c.ScopeRoot + "/" + liveDatabasePlanFile(plan.GenerationID)); e != nil {
		t.Fatal(e)
	}
	if _, e = Read(context.Background(), c); e == nil {
		t.Fatal("missing completed plan silently accepted")
	}
}
func TestRestoredLiveDatabaseJournalRejectsForgedOrderLineageAndActions(t *testing.T) {
	c, _, d, w := liveDatabaseFixture(t)
	d.effects.fault = "volume-before"
	if _, e := runRestoredLiveDatabase(context.Background(), c, "qualify-restored-live-database", d, w); e == nil {
		t.Fatal("unsubmitted required")
	}
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	first := 0
	for i, event := range l.events {
		if event.RestoredLiveDatabase != nil {
			first = i
			break
		}
	}
	event := l.events[first]
	prefix := l.events[:first]
	if validateLiveDatabaseEvent(prefix, event) != nil {
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
				altered.RestoredLiveDatabase.Step = 1
			case "generation":
				altered.RestoredLiveDatabase.GenerationID = prefix[0].OperationID
			case "proof":
				altered.RestoredLiveDatabase.Admission.TLSProofSHA256 = strings.Repeat("a", 64)
			case "action":
				altered.Action = "process_start"
			case "combined":
				altered.Process = &ProcessRecord{}
			case "object":
				altered.RestoredLiveDatabase.ObjectID = strings.Repeat("a", 64)
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
	p, e := l.readLiveDatabasePlan(*event.RestoredLiveDatabase)
	if e != nil {
		t.Fatal(e)
	}
	for _, fault := range []string{"mutating-task", "stale-peer", "file", "process"} {
		t.Run(fault, func(t *testing.T) {
			var altered RestoredLiveDatabasePlan
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
			if l.validateLiveDatabasePlan(altered) == nil {
				t.Fatal("invalid plan admitted")
			}
		})
	}
}

func TestRestoredLiveDatabasePublicationWithoutSDKEffectsCanAbortMissingOrPartialMetadata(t *testing.T) {
	for _, fault := range []string{"missing", "partial", "empty", "changed", "wrong-mode", "symlink", "directory"} {
		t.Run(fault, func(t *testing.T) {
			c, _, d, w := liveDatabaseFixture(t)
			l, e := open(c, true)
			if e != nil {
				t.Fatal(e)
			}
			p, _, e := l.makeRestoredLiveDatabasePlan(context.Background(), d, w, strings.Repeat("9", 32))
			if e != nil {
				l.close()
				t.Fatal(e)
			}
			r := liveDatabasePlanRecord(p)
			id, e := nonce()
			if e != nil {
				l.close()
				t.Fatal(e)
			}
			if e = l.appendEvent(Event{OperationID: id, Action: "live_database_effect", Outcome: "started", RestoredLiveDatabase: &r}); e != nil {
				l.close()
				t.Fatal(e)
			}
			name := c.ScopeRoot + "/" + liveDatabasePlanFile(p.GenerationID)
			contents := []byte("{")
			if fault == "empty" {
				contents = nil
			}
			if fault == "changed" {
				contents = encode(map[string]string{"foreign": "preserve"})
			}
			foreign := ""
			if fault == "symlink" {
				foreign = t.TempDir() + "/foreign.txt"
				if e = os.WriteFile(foreign, []byte("FOREIGN_UNCHANGED"), 0600); e != nil {
					t.Fatal(e)
				}
				if e = os.Symlink(foreign, name); e != nil {
					t.Fatal(e)
				}
			} else if fault == "directory" {
				if e = os.Mkdir(name, 0700); e != nil {
					t.Fatal(e)
				}
			} else if fault != "missing" {
				if e = writeNew(l.root, liveDatabasePlanFile(p.GenerationID), contents); e != nil {
					l.close()
					t.Fatal(e)
				}
				if fault == "wrong-mode" {
					if e = os.Chmod(name, 0644); e != nil {
						t.Fatal(e)
					}
				}
			}

			l.close()
			if fault != "missing" {
				if _, re := runRestoredLiveDatabase(context.Background(), c, "reconcile-restored-live-database", d, w); re == nil {
					t.Fatal("present unusable publication authorized execution")
				}
				if d.effects.creates != 0 || d.effects.puts != 0 || len(d.effects.starts) != 0 {
					t.Fatal("publication reconciliation submitted SDK effect")
				}
			}

			out, e := runRestoredLiveDatabase(context.Background(), c, "abort-restored-live-database", d, w)
			unsafe := fault == "wrong-mode" || fault == "symlink" || fault == "directory"
			if unsafe {
				if e == nil || e.Error() != "OFFLINE_SCOPE_ROOT_CONTENT_UNSAFE" {
					t.Fatal("unsafe root not preserved/refused", e)
				}
			} else if e != nil || out.Pending || out.ProofSHA256 != "" {
				t.Fatal("metadata-only abort", out, e)
			}
			if d.effects.creates != 0 || d.effects.puts != 0 || len(d.effects.starts) != 0 {
				t.Fatal("metadata-only abort submitted SDK effect")
			}
			if fault == "symlink" {
				target, re := os.Readlink(name)
				raw, fe := os.ReadFile(foreign)
				if re != nil || fe != nil || target != foreign || string(raw) != "FOREIGN_UNCHANGED" {
					t.Fatal("foreign link/target changed", re, fe)
				}
			} else if fault == "directory" {
				info, se := os.Lstat(name)
				if se != nil || !info.IsDir() {
					t.Fatal("foreign directory changed", se)
				}
			} else if fault != "missing" {
				raw, re := os.ReadFile(name)
				if re != nil || !bytes.Equal(raw, contents) {
					t.Fatal("partial evidence replaced", re)
				}
			}

			if unsafe {
				if fault == "wrong-mode" {
					info, se := os.Lstat(name)
					if se != nil || info.Mode().Perm() != 0644 {
						t.Fatal("unsafe permission changed", se)
					}
				}
				if _, re := Read(context.Background(), c); re == nil || re.Error() != "OFFLINE_SCOPE_ROOT_CONTENT_UNSAFE" {
					t.Fatal("unsafe root accepted on read", re)
				}
				return
			}

			if _, e = Read(context.Background(), c); e != nil {
				t.Fatal("aborted metadata unreadable", e)
			}
			next, e := runRestoredLiveDatabase(context.Background(), c, "requalify-restored-live-database", d, w)
			if e != nil || next.Pending || next.ProofSHA256 == "" || next.GenerationID == p.GenerationID {
				t.Fatal("fresh generation after publication abort", next, e)
			}
		})
	}
}
