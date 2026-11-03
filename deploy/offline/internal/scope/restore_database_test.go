package scope

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit in-memory Engine effect double. Private snapshots and durable journal
// are real files; only the tagged acceptance executes actual Docker/backend SQL.
type restoreDatabaseDouble struct {
	*databaseEngineDouble
	scope                         string
	volume, utility, payload      bool
	fault                         string
	fired                         bool
	creates, puts, removes, stops int
	taskCreates, starts           map[string]int
	tasks                         map[string]engine.RuntimeContainer
	definitions                   map[string]engine.DatabaseTask
	cancel                        context.CancelFunc
}

func (d *restoreDatabaseDouble) fail(name string) bool {
	if !d.fired && d.fault == name {
		d.fired = true
		return true
	}
	return false
}
func (d *restoreDatabaseDouble) generated(o engine.Ownership) bool {
	return strings.HasPrefix(o.Role, engine.StoreRestoreConfigRole+"-")
}
func (d *restoreDatabaseDouble) ValidateConfigurationFiles(files []engine.ConfigurationFile, uid, gid int) error {
	if uid < 1 || gid < 1 || len(files) < 3 {
		return errors.New("SYNTHETIC_INVALID_CONFIGURATION")
	}
	return nil
}
func (d *restoreDatabaseDouble) CreateVolume(ctx context.Context, o engine.Ownership) (engine.Volume, error) {
	if !d.generated(o) {
		return d.databaseEngineDouble.CreateVolume(ctx, o)
	}
	d.creates++
	if d.fail("volume-before") {
		return engine.Volume{}, errors.New("SYNTHETIC_VOLUME_UNCERTAIN")
	}
	d.volume = true
	return engine.Volume{Name: o.VolumeName()}, nil
}
func (d *restoreDatabaseDouble) InspectVolume(ctx context.Context, o engine.Ownership) (engine.Volume, bool, error) {
	if !d.generated(o) {
		return d.databaseEngineDouble.InspectVolume(ctx, o)
	}
	return engine.Volume{Name: o.VolumeName()}, d.volume, nil
}
func (d *restoreDatabaseDouble) CreateUtility(_ context.Context, o engine.Ownership, _ string, _, _ int) (engine.Utility, error) {
	if !d.generated(o) {
		panic("unexpected unrelated utility")
	}
	d.utility = true
	return engine.Utility{ID: strings.Repeat("9", 64)}, nil
}
func (d *restoreDatabaseDouble) InspectUtility(_ context.Context, o engine.Ownership, _ string, _, _ int) (engine.Utility, bool, error) {
	if !d.generated(o) {
		return engine.Utility{}, false, nil
	}
	return engine.Utility{ID: strings.Repeat("9", 64)}, d.utility, nil
}
func (d *restoreDatabaseDouble) PutConfiguration(_ context.Context, _ engine.Ownership, _ string, _, _ int, _ []engine.ConfigurationFile) error {
	d.puts++
	d.payload = true
	if d.fail("payload-after") {
		return errors.New("SYNTHETIC_PAYLOAD_RESPONSE_LOST")
	}
	return nil
}
func (d *restoreDatabaseDouble) InspectConfiguration(_ context.Context, _ engine.Ownership, _ string, _, _ int, _ []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	status := "observed_empty_owned_volume"
	if d.payload {
		status = engine.ConfigurationPayloadVerified
	}
	return engine.ConfigurationObservation{Status: status}, nil
}
func (d *restoreDatabaseDouble) RemoveUtility(context.Context, engine.Ownership, string, int, int) error {
	d.utility = false
	return nil
}
func (d *restoreDatabaseDouble) RemoveStoreRestoreVolume(_ context.Context, o engine.Ownership) error {
	if !d.generated(o) || !d.volume || d.utility {
		panic("unsafe generated removal")
	}
	for _, r := range d.tasks {
		if r.ID != "" {
			panic("live generated consumer")
		}
	}
	d.volume = false
	return nil
}
func (d *restoreDatabaseDouble) CreateDatabaseTask(_ context.Context, t engine.DatabaseTask) (engine.RuntimeContainer, error) {
	if t.ValidateRestoredDatabaseTask() != nil {
		panic("invalid generated task")
	}
	d.taskCreates[t.Kind]++
	if d.tasks[t.Kind].ID != "" {
		panic("task resubmitted")
	}
	r := engine.RuntimeContainer{}
	r.ID = wire.Digest([]byte(t.Kind))
	r.Name = "/projectone-" + t.Owner.ScopeID + "-database-" + t.Kind + "-" + t.Owner.OperationID
	r.State.Status = "created"
	d.tasks[t.Kind] = r
	d.definitions[t.Kind] = t
	if d.fail("create-after") {
		return engine.RuntimeContainer{}, errors.New("SYNTHETIC_TASK_CREATE_RESPONSE_LOST")
	}
	return r, nil
}
func (d *restoreDatabaseDouble) InspectDatabaseTask(_ context.Context, t engine.DatabaseTask) (engine.RuntimeContainer, bool, error) {
	r := d.tasks[t.Kind]
	if r.ID != "" && !reflect.DeepEqual(t, d.definitions[t.Kind]) {
		panic("task context changed")
	}
	return r, r.ID != "", nil
}
func (d *restoreDatabaseDouble) StartDatabaseTask(_ context.Context, t engine.DatabaseTask, _ string, _ []engine.ConfigurationFile, _ int) (engine.ResourceAdmission, error) {
	d.starts[t.Kind]++
	r := d.tasks[t.Kind]
	if r.State.Status != "created" {
		panic("process resubmitted")
	}
	r.State.Status = "exited"
	r.State.ExitCode = 0
	if d.fail("task-failed") {
		r.State.ExitCode = 1
	}
	d.tasks[t.Kind] = r
	if d.cancel != nil {
		r.State.Status = "running"
		r.State.Running = true
		r.State.PID = 42
		d.tasks[t.Kind] = r
		d.cancel()
		return engine.ResourceAdmission{}, errors.New("SYNTHETIC_CANCELED_START")
	}
	if d.fail("start-after") {
		return engine.ResourceAdmission{}, errors.New("SYNTHETIC_START_RESPONSE_LOST")
	}
	return engine.ResourceAdmission{}, nil
}
func (d *restoreDatabaseDouble) StopDatabaseTask(_ context.Context, t engine.DatabaseTask, _ string, _ int) error {
	d.stops++
	r := d.tasks[t.Kind]
	r.State.Status = "exited"
	r.State.Running = false
	r.State.PID = 0
	d.tasks[t.Kind] = r
	return nil
}
func (d *restoreDatabaseDouble) RemoveDatabaseTask(_ context.Context, t engine.DatabaseTask, _ string) error {
	d.removes++
	delete(d.tasks, t.Kind)
	if d.fail("remove-after") {
		return errors.New("SYNTHETIC_REMOVE_RESPONSE_LOST")
	}
	return nil
}
func (d *restoreDatabaseDouble) RuntimeCandidates(ctx context.Context, scope string, networks bool) ([]engine.RuntimeObject, error) {
	if networks || scope != d.scope || ctx.Err() != nil {
		return nil, errors.New("SYNTHETIC_CANDIDATE_CONTEXT_INVALID")
	}
	rows := []engine.RuntimeObject{}
	for _, service := range d.worker.state.Services {
		rows = append(rows, engine.RuntimeObject{ID: service.ID})
	}
	if d.utility {
		rows = append(rows, engine.RuntimeObject{ID: strings.Repeat("9", 64)})
	}
	for kind, r := range d.tasks {
		t := d.definitions[kind]
		rows = append(rows, engine.RuntimeObject{ID: r.ID, Labels: map[string]string{"io.projectone.operation": t.Owner.OperationID, "io.projectone.role": t.Owner.Role}})
	}
	return rows, nil
}
func restoreDatabaseControllerFixture(t *testing.T) (configuration.Config, configuration.RestoreRequest, *restoreDatabaseDouble, *databaseWorkerDouble) {
	t.Helper()
	l, plan, base, d, w := restoreSQLSnapshotRuntimeFixture(t)
	for i := range restoreSQLSteps() {
		events := appendSyntheticRestoreSQLStep(t, l.events, base, i, l.marker.ScopeID)
		for _, e := range events[len(events)-2:] {
			if err := l.appendEvent(e); err != nil {
				l.close()
				t.Fatal(err)
			}
		}
	}
	c, scope := l.config, l.marker.ScopeID
	l.close()
	return c, plan.Request, &restoreDatabaseDouble{databaseEngineDouble: d, scope: scope, taskCreates: map[string]int{}, starts: map[string]int{}, tasks: map[string]engine.RuntimeContainer{}, definitions: map[string]engine.DatabaseTask{}}, w
}
func TestRestoreDatabaseControllerCompletesWithoutRepeatedEffects(t *testing.T) {
	c, r, d, w := restoreDatabaseControllerFixture(t)
	out, e := runRestoreDatabase(context.Background(), c, r, "qualify-restored-database", d, w)
	if e != nil || out.ProofSHA256 == "" || out.Pending || out.CompletedSteps != 18 || d.volume || d.utility || len(d.tasks) != 0 {
		t.Fatal("completed fixed controller", out, e)
	}
	for kind, n := range d.starts {
		if n != 1 || d.taskCreates[kind] != 1 {
			t.Fatal("effect replay", kind)
		}
	}
	creates, puts, removes := d.creates, d.puts, d.removes
	if again, e := runRestoreDatabase(context.Background(), c, r, "qualify-restored-database", d, w); e != nil || again != out || d.creates != creates || d.puts != puts || d.removes != removes {
		t.Fatal("terminal repeat changed effects", e)
	}
}
func TestRestoreDatabaseControllerReconcilesLostEffectsWithoutResend(t *testing.T) {
	for _, fault := range []string{"payload-after", "create-after", "start-after"} {
		t.Run(fault, func(t *testing.T) {
			c, r, d, w := restoreDatabaseControllerFixture(t)
			d.fault = fault
			if out, e := runRestoreDatabase(context.Background(), c, r, "qualify-restored-database", d, w); e == nil || !out.Pending {
				t.Fatal("lost effect accepted", e)
			}
			if _, e := runRestoreDatabase(context.Background(), c, r, "qualify-restored-database", d, w); e == nil {
				t.Fatal("uncertain ordinary retry accepted")
			}
			if out, e := runRestoreDatabase(context.Background(), c, r, "reconcile-restored-database", d, w); e != nil || out.ProofSHA256 == "" {
				t.Fatal("independent reconcile", out, e)
			}
			if d.creates != 1 || d.puts != 1 {
				t.Fatal("configuration resubmitted")
			}
			for kind, n := range d.starts {
				if n != 1 || d.taskCreates[kind] != 1 {
					t.Fatal("process resubmitted", kind)
				}
			}
		})
	}
}
func TestRestoreDatabaseControllerPreservesFailedTargetsAndCancellationCleanup(t *testing.T) {
	for _, fault := range []string{"volume-before", "task-failed", "cancel", "cleanup-loss"} {
		t.Run(fault, func(t *testing.T) {
			c, r, d, w := restoreDatabaseControllerFixture(t)
			d.fault = fault
			ctx := context.Background()
			if fault == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				d.cancel = cancel
			}
			if fault == "cleanup-loss" {
				d.fault = "start-after"
			}
			if _, e := runRestoreDatabase(ctx, c, r, "qualify-restored-database", d, w); e == nil {
				t.Fatal("fault accepted")
			}
			if fault == "cleanup-loss" {
				d.fault = "remove-after"
				d.fired = false
				if _, e := runRestoreDatabase(context.Background(), c, r, "abort-restored-database", d, w); e == nil {
					t.Fatal("lost cleanup accepted")
				}
			}
			if out, e := runRestoreDatabase(context.Background(), c, r, "abort-restored-database", d, w); e != nil || out.Pending || out.ProofSHA256 != "" || d.volume || d.utility || len(d.tasks) != 0 {
				t.Fatal("exact abort", out, e)
			}
			if fault == "cancel" && d.stops != 1 {
				t.Fatal("canceled running task was not stopped")
			}
			if _, e := runRestoreDatabase(context.Background(), c, r, "qualify-restored-database", d, w); e == nil {
				t.Fatal("aborted target resumed")
			}
		})
	}
}
