package scope

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit transport/process doubles; only scope files and journal are real.
// Fake source bytes below are never offered to the real archive/backend SDK.
type restoreStoresDouble struct {
	*restoreDatabaseDouble
	volumes, utilities, payloads map[string]bool
	archives                     map[string]engine.StoreRestoreProfile
	imported                     map[string]bool
	sourcePuts                   int
}

func (d *restoreStoresDouble) generatedStore(o engine.Ownership) bool {
	return strings.HasPrefix(o.Role, engine.StoreRestoreConfigRole+"-") || strings.HasPrefix(o.Role, engine.StoreRestoreArchiveRole+"-")
}
func (d *restoreStoresDouble) CreateVolume(ctx context.Context, o engine.Ownership) (engine.Volume, error) {
	if !d.generatedStore(o) {
		return d.databaseEngineDouble.CreateVolume(ctx, o)
	}
	d.creates++
	if d.fail("volume-before") {
		return engine.Volume{}, errors.New("SYNTHETIC_VOLUME_UNKNOWN")
	}
	d.volumes[o.Role] = true
	return engine.Volume{Name: o.VolumeName()}, nil
}
func (d *restoreStoresDouble) InspectVolume(ctx context.Context, o engine.Ownership) (engine.Volume, bool, error) {
	if !d.generatedStore(o) {
		return d.databaseEngineDouble.InspectVolume(ctx, o)
	}
	return engine.Volume{Name: o.VolumeName()}, d.volumes[o.Role], nil
}
func (d *restoreStoresDouble) CreateUtility(_ context.Context, o engine.Ownership, _ string, _, _ int) (engine.Utility, error) {
	if !d.generatedStore(o) || !d.volumes[o.Role] {
		panic("unsafe utility")
	}
	d.utilities[o.Role] = true
	return engine.Utility{ID: wire.Digest([]byte("utility:" + o.Role))}, nil
}
func (d *restoreStoresDouble) InspectUtility(_ context.Context, o engine.Ownership, _ string, _, _ int) (engine.Utility, bool, error) {
	return engine.Utility{ID: wire.Digest([]byte("utility:" + o.Role))}, d.utilities[o.Role], nil
}
func (d *restoreStoresDouble) RemoveUtility(_ context.Context, o engine.Ownership, _ string, _, _ int) error {
	if !d.generatedStore(o) {
		panic("unrelated utility removal")
	}
	delete(d.utilities, o.Role)
	return nil
}
func (d *restoreStoresDouble) PutConfiguration(_ context.Context, o engine.Ownership, _ string, _, _ int, _ []engine.ConfigurationFile) error {
	if !d.utilities[o.Role] {
		panic("missing utility")
	}
	d.puts++
	d.payloads[o.Role] = true
	if d.fail("payload-after") {
		return errors.New("SYNTHETIC_PAYLOAD_RESPONSE_LOST")
	}
	return nil
}
func (d *restoreStoresDouble) InspectConfiguration(_ context.Context, o engine.Ownership, _ string, _, _ int, _ []engine.ConfigurationFile) (engine.ConfigurationObservation, error) {
	status := "observed_empty_owned_volume"
	if d.payloads[o.Role] {
		status = engine.ConfigurationPayloadVerified
	}
	return engine.ConfigurationObservation{Status: status}, nil
}
func (d *restoreStoresDouble) PutStoreRestoreArchive(_ context.Context, t engine.DatabaseTask, _ string, f *os.File) (engine.StoreRestoreProfile, error) {
	d.sourcePuts++
	raw, e := io.ReadAll(f)
	if e != nil || len(raw) != int(t.StoreRestore.Artifact.Bytes) {
		return engine.StoreRestoreProfile{}, errors.New("SYNTHETIC_SOURCE_BYTES_MISSING")
	}
	d.archives[t.SourceOwner.Role] = *t.StoreRestore
	if d.fail("archive-after") {
		return engine.StoreRestoreProfile{}, errors.New("SYNTHETIC_ARCHIVE_RESPONSE_LOST")
	}
	return *t.StoreRestore, nil
}
func (d *restoreStoresDouble) InspectStoreRestoreArchiveInput(_ context.Context, t engine.DatabaseTask, _ string) (engine.StoreRestoreProfile, error) {
	p, found := d.archives[t.SourceOwner.Role]
	if !found || p != *t.StoreRestore {
		return p, errors.New("SYNTHETIC_ARCHIVE_INCOMPLETE")
	}
	return p, nil
}
func (d *restoreStoresDouble) CreateDatabaseTask(_ context.Context, t engine.DatabaseTask) (engine.RuntimeContainer, error) {
	if t.ValidateRestoredStoreTask() != nil {
		panic("invalid fixed store task")
	}
	d.taskCreates[t.Kind]++
	if d.tasks[t.Kind].ID != "" {
		panic("task replay")
	}
	r := engine.RuntimeContainer{}
	r.ID = wire.Digest([]byte(t.Kind))
	r.Name = "/projectone-" + t.Owner.ScopeID + "-database-" + t.Kind + "-" + t.Owner.OperationID
	r.State.Status = "created"
	d.tasks[t.Kind] = r
	d.definitions[t.Kind] = t
	if d.fail("create-after") {
		return r, errors.New("SYNTHETIC_TASK_CREATE_RESPONSE_LOST")
	}
	return r, nil
}
func (d *restoreStoresDouble) StartDatabaseTask(_ context.Context, t engine.DatabaseTask, _ string, _ []engine.ConfigurationFile, _ int) (engine.ResourceAdmission, error) {
	d.starts[t.Kind]++
	r := d.tasks[t.Kind]
	if r.State.Status != "created" {
		panic("task restarted")
	}
	r.State.Status = "exited"
	r.State.ExitCode = 0
	if t.StoreRestore != nil {
		if d.archives[t.SourceOwner.Role] != *t.StoreRestore || d.imported[t.OutputOwner.Role] {
			panic("unsafe import or overwrite")
		}
		d.imported[t.OutputOwner.Role] = true
		if d.fail("import-exit") {
			r.State.ExitCode = 1
		}
	} else {
		if !d.imported[t.OutputOwner.Role] {
			panic("verification before import")
		}
		if d.fail("verify-exit") {
			r.State.ExitCode = 1
		}
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
func (d *restoreStoresDouble) RemoveStoreRestoreVolume(_ context.Context, o engine.Ownership) error {
	if !d.generatedStore(o) || !d.volumes[o.Role] || d.utilities[o.Role] {
		panic("unsafe volume cleanup")
	}
	for kind := range d.tasks {
		task := d.definitions[kind]
		if task.VolumeOwner == o || task.SourceOwner != nil && *task.SourceOwner == o {
			panic("live generated consumer")
		}
	}
	delete(d.volumes, o.Role)
	delete(d.archives, o.Role)
	delete(d.payloads, o.Role)
	return nil
}
func (d *restoreStoresDouble) RuntimeCandidates(ctx context.Context, scope string, networks bool) ([]engine.RuntimeObject, error) {
	objects, e := d.restoreDatabaseDouble.RuntimeCandidates(ctx, scope, networks)
	if e != nil {
		return nil, e
	}
	for role, active := range d.utilities {
		if active {
			objects = append(objects, engine.RuntimeObject{ID: wire.Digest([]byte("utility:" + role))})
		}
	}
	return objects, nil
}
func restoreStoresControllerFixture(t *testing.T) (configuration.Config, configuration.RestoreRequest, *restoreStoresDouble, *databaseWorkerDouble) {
	t.Helper()
	l, p, d, w := restoreStoresPlanRuntimeFixture(t)
	if e := l.publishRestoreStoresPlan(context.Background(), p, false, func(plan RestoreStorePlan) error { return writeBackupFile(l.root, restoreStoresPlanFile, encode(plan)) }); e != nil {
		l.close()
		t.Fatal(e)
	}
	original, e := l.readBoundRestorePlan()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	for _, job := range p.Jobs {
		if e = os.WriteFile(filepath.Join(original.Request.BackupRoot, job.Role+".tar"), []byte(strings.Repeat("s", int(job.Import.StoreRestore.Artifact.Bytes))), 0600); e != nil {
			l.close()
			t.Fatal(e)
		}
	}
	c, scope := l.config, l.marker.ScopeID
	l.close()
	base := &restoreDatabaseDouble{databaseEngineDouble: d, scope: scope, taskCreates: map[string]int{}, starts: map[string]int{}, tasks: map[string]engine.RuntimeContainer{}, definitions: map[string]engine.DatabaseTask{}}
	return c, original.Request, &restoreStoresDouble{restoreDatabaseDouble: base, volumes: map[string]bool{}, utilities: map[string]bool{}, payloads: map[string]bool{}, archives: map[string]engine.StoreRestoreProfile{}, imported: map[string]bool{}}, w
}
func TestRestoreStoresControllerCompleteAndTerminalNoReplay(t *testing.T) {
	c, r, d, w := restoreStoresControllerFixture(t)
	out, e := runRestoreStores(context.Background(), c, r, "restore-stores", d, w)
	if e != nil || out.ProofSHA256 == "" || out.Pending || out.CompletedSteps != 29 || len(d.volumes) != 0 || len(d.utilities) != 0 || len(d.tasks) != 0 || d.sourcePuts != 2 || d.puts != 1 || d.creates != 3 || len(d.imported) != 2 {
		t.Fatal("complete fixed store controller", out, e)
	}
	for kind, n := range d.starts {
		if n != 1 || d.taskCreates[kind] != 1 {
			t.Fatal("task replay", kind)
		}
	}
	if again, e := runRestoreStores(context.Background(), c, r, "restore-stores", d, w); e != nil || again != out || d.sourcePuts != 2 || d.puts != 1 {
		t.Fatal("terminal call repeated effects", e)
	}
}
func TestRestoreStoresControllerLostResultsObserveWithoutResend(t *testing.T) {
	for _, fault := range []string{"payload-after", "archive-after", "create-after", "start-after"} {
		t.Run(fault, func(t *testing.T) {
			c, r, d, w := restoreStoresControllerFixture(t)
			d.fault = fault
			if out, e := runRestoreStores(context.Background(), c, r, "restore-stores", d, w); e == nil || !out.Pending {
				t.Fatal("unknown effect accepted", out, e)
			}
			if _, e := runRestoreStores(context.Background(), c, r, "restore-stores", d, w); e == nil {
				t.Fatal("unknown ordinary retry accepted")
			}
			if out, e := runRestoreStores(context.Background(), c, r, "reconcile-restored-stores", d, w); e != nil || out.ProofSHA256 == "" {
				t.Fatal("independent reconcile", out, e)
			}
			if d.sourcePuts != 2 || d.puts != 1 {
				t.Fatal("source/configuration resubmitted")
			}
			for kind, n := range d.starts {
				if n != 1 || d.taskCreates[kind] != 1 {
					t.Fatal("task resubmitted", kind)
				}
			}
		})
	}
}
func TestRestoreStoresControllerFailedPartialImportCancellationAndCleanupLoss(t *testing.T) {
	for _, fault := range []string{"volume-before", "import-exit", "verify-exit", "cancel", "cleanup-loss"} {
		t.Run(fault, func(t *testing.T) {
			c, r, d, w := restoreStoresControllerFixture(t)
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
			if _, e := runRestoreStores(ctx, c, r, "restore-stores", d, w); e == nil {
				t.Fatal("failed/canceled effect accepted")
			}
			if fault == "cleanup-loss" {
				d.fault = "remove-after"
				d.fired = false
				if _, e := runRestoreStores(context.Background(), c, r, "abort-restored-stores", d, w); e == nil {
					t.Fatal("unknown cleanup accepted")
				}
			}
			if out, e := runRestoreStores(context.Background(), c, r, "abort-restored-stores", d, w); e != nil || out.Pending || out.ProofSHA256 != "" || len(d.volumes) != 0 || len(d.utilities) != 0 || len(d.tasks) != 0 {
				t.Fatal("exact generated cleanup", out, e)
			}
			if fault != "volume-before" && !d.imported["models"] {
				t.Fatal("partial/completed model target was erased")
			}
			if fault == "cancel" && d.stops != 1 {
				t.Fatal("running task not stopped")
			}
			if _, e := runRestoreStores(context.Background(), c, r, "restore-stores", d, w); e == nil {
				t.Fatal("aborted target resumed")
			}
		})
	}
}
func TestRestoreStoresMetadataOnlyAbortNeedsNoEngineOrOriginalBackup(t *testing.T) {
	l, p := restoreStoresPlanFixture(t)
	if e := l.publishRestoreStoresPlan(context.Background(), p, false, func(RestoreStorePlan) error { return errors.New("SYNTHETIC_HOST_FILE_LOST") }); e == nil {
		l.close()
		t.Fatal("unknown first write accepted")
	}
	original, e := l.readBoundRestorePlan()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	c := l.config
	l.close()
	// No SDK is passed or constructed; original backup has no real archives.
	out, e := RunRestoreStores(context.Background(), c, original.Request, "abort-restored-stores")
	if e != nil || out.Pending || out.ProofSHA256 != "" {
		t.Fatal("metadata-only abort", out, e)
	}
	if again, handled, e := abortRestoreStoresMetadata(context.Background(), c, original.Request); e != nil || !handled || again != out {
		t.Fatal("terminal metadata abort repeat", e)
	}
}

func TestRestoreStoresCannotClaimMetadataCleanupAfterAnyActorIntent(t *testing.T) {
	l, p := restoreStoresPlanFixture(t)
	defer l.close()
	if e := l.publishRestoreStoresPlan(context.Background(), p, false, func(plan RestoreStorePlan) error { return writeBackupFile(l.root, restoreStoresPlanFile, encode(plan)) }); e != nil {
		t.Fatal(e)
	}
	r := restoreStoresPlanRecord(p)
	r.Step = 1
	r.Phase = "volume-create"
	r.Role = "configuration"
	id, _ := nonce()
	if e := l.appendEvent(Event{OperationID: id, Action: "restore_stores_effect", Outcome: "started", RestoreStores: &r}); e != nil {
		t.Fatal(e)
	}
	if _, _, e := l.finishRestoreStoresAbort(context.Background(), r, nil); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORE_STORES_GENERATED_CLEANUP_REQUIRED" {
		t.Fatal("actor uncertainty treated as metadata-only cleanup", e)
	}
}

func TestRestoreStoresPublicAbortCannotSkipSDKAfterActorIntent(t *testing.T) {
	l, p := restoreStoresPlanFixture(t)
	if l.config.Engine.Utility != nil {
		l.close()
		t.Fatal("explicit metadata-only fixture unexpectedly has SDK utility profile")
	}
	if e := l.publishRestoreStoresPlan(context.Background(), p, false, func(plan RestoreStorePlan) error { return writeBackupFile(l.root, restoreStoresPlanFile, encode(plan)) }); e != nil {
		l.close()
		t.Fatal(e)
	}
	r := restoreStoresPlanRecord(p)
	r.Step = 1
	r.Phase = "volume-create"
	r.Role = "configuration"
	id, _ := nonce()
	if e := l.appendEvent(Event{OperationID: id, Action: "restore_stores_effect", Outcome: "started", RestoreStores: &r}); e != nil {
		l.close()
		t.Fatal(e)
	}
	original, e := l.readBoundRestorePlan()
	if e != nil {
		l.close()
		t.Fatal(e)
	}
	c := l.config
	l.close()
	if _, e := RunRestoreStores(context.Background(), c, original.Request, "abort-restored-stores"); e == nil || e.Error() != "OFFLINE_SCOPE_RESTORE_STORES_CONFIGURATION_REQUIRED" {
		t.Fatal("actor uncertainty skipped SDK profile requirement", e)
	}
	reopened, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.close()
	state := restoreStoresState(reopened.events)
	if state.aborted || state.aborting || state.pending == "" {
		t.Fatal("SDK-less public abort changed actor state")
	}
}
