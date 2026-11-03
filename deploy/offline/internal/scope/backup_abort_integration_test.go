//go:build offlineintegration

package scope

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestActualScopedBackupAbortCancellationAndInterruptedReserve(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Second)
	defer cancel()
	read := func(name string, out any) {
		raw, e := bundle.ReadProtectedConfiguration(name)
		if e != nil || wire.UniqueJSON(raw, out) != nil {
			t.Fatal("protected actual fixture", e)
		}
	}
	var checkpoint struct {
		Status    string `json:"status"`
		ScopeID   string `json:"scope_id"`
		Config    string `json:"operator_config"`
		ConfigSHA string `json:"operator_config_sha256"`
		Retention string `json:"retention"`
	}
	read(os.Getenv("PROJECT_ONE_TEST_SCOPED_BACKUP_CHECKPOINT"), &checkpoint)
	if checkpoint.Status != "passed_core_application_retained_stopped_next_data_recovery_acceptance" {
		t.Fatal("accepted actual stopped installation required")
	}
	raw, e := bundle.ReadProtectedConfiguration(checkpoint.Config)
	if e != nil || wire.Digest(raw) != checkpoint.ConfigSHA {
		t.Fatal("original installation config changed", e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || !c.Verification.AllowTestFixture {
		t.Fatal("explicit synthetic scope only", e)
	}
	var retention struct {
		ScopeID   string `json:"scope_id"`
		Resources struct {
			Services map[string]struct{ ID string } `json:"services"`
		} `json:"resources"`
	}
	read(checkpoint.Retention, &retention)
	if checkpoint.ScopeID != retention.ScopeID {
		t.Fatal("retained scope changed")
	}
	out := os.Getenv("PROJECT_ONE_TEST_SCOPED_BACKUP_EVIDENCE")
	binary := os.Getenv("PROJECT_ONE_TEST_SCOPED_BACKUP_OPERATOR")
	if !privateDirectory(out) || !canonical(binary) {
		t.Fatal("private evidence/current native operator required")
	}
	operator := func(commandContext context.Context, action string, operation string) ([]byte, error) {
		args := []string{action, "--config", checkpoint.Config}
		if operation != "" {
			args = append(args, "--operation-file", operation)
		}
		return exec.CommandContext(commandContext, binary, args...).CombinedOutput()
	}
	put := func(name string, value any) {
		root, e := os.OpenRoot(out)
		if e != nil {
			t.Fatal(e)
		}
		defer root.Close()
		if e = writeBackupFile(root, name, encode(value)); e != nil {
			t.Fatal(e)
		}
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		t.Fatal(e)
	}
	private := filepath.Join(out, "docker-client")
	if os.Mkdir(private, 0700) != nil {
		t.Fatal("private Docker client")
	}
	if os.WriteFile(filepath.Join(private, "config.json"), []byte("{}\n"), 0600) != nil {
		t.Fatal("private Docker config")
	}
	var model compose.Model
	modelRaw, e := bundle.ReadProtectedConfiguration(c.Compose.ModelFile)
	if e != nil || wire.StrictJSON(modelRaw, &model) != nil {
		t.Fatal("immutable model")
	}
	pg := model.Services["postgres"]
	query := func(statement string) (string, error) {
		cmd := exec.CommandContext(ctx, c.Compose.DockerBinary, "--config", private, "--host", "unix://"+c.Engine.SocketFile, "exec", "--user", pg.User, retention.Resources.Services["postgres"].ID, "psql", "-X", "--set", "ON_ERROR_STOP=1", "-U", pg.Environment["POSTGRES_USER"], "-d", pg.Environment["POSTGRES_DB"], "-At", "-c", statement)
		cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
		raw, e := cmd.CombinedOutput()
		return strings.TrimSpace(string(raw)), e
	}
	inventories := func() map[string]any {
		values := map[string]any{}
		for _, role := range []string{"models", "datasets"} {
			payload := &backupFixtureHashWriter{digest: sha256.New(), maximum: 1 << 20}
			diagnostic := &backupFixtureHashWriter{digest: sha256.New(), maximum: 1 << 20}
			cmd := exec.CommandContext(ctx, c.Compose.DockerBinary, "--config", private, "--host", "unix://"+c.Engine.SocketFile, "cp", retention.Resources.Services["control"].ID+":/var/lib/projectone/"+role, "-")
			cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
			cmd.Stdout = payload
			cmd.Stderr = diagnostic
			if e := cmd.Run(); e != nil {
				t.Fatal("bounded readonly source inventory", role, e)
			}
			values[role] = map[string]any{"bytes": payload.bytes, "sha256": hex.EncodeToString(payload.digest.Sum(nil))}
		}
		return values
	}
	activeRequest := ""
	setActive := func(label, request string) {
		activeRequest = request
		root, e := os.OpenRoot(out)
		if e != nil {
			t.Fatal(e)
		}
		defer root.Close()
		temp := label + ".active.partial"
		if e = writeBackupFile(root, temp, encode(map[string]string{"operation_file": request})); e != nil {
			t.Fatal(e)
		}
		if e = root.Rename(temp, "active-operation.json"); e != nil {
			t.Fatal(e)
		}
		if e = syncBackupDirectory(root, "."); e != nil {
			t.Fatal(e)
		}
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 90*time.Second)
		defer done()
		if t.Failed() && activeRequest != "" {
			_, _ = operator(cleanup, "abort-backup", activeRequest)
		}
		_, err := operator(cleanup, "stop-runtime", "")
		if err != nil {
			t.Error("recorded stop", err)
		}
		put("cleanup.json", map[string]any{"recorded_stop_passed": err == nil, "source_scope_data_and_complete_backups_retained": true, "original_services_and_images_preserved": true})
	})
	if value, e := operator(ctx, "start-database", ""); e != nil {
		t.Fatalf("recorded PG start: %s %v", value, e)
	}
	for deadline := time.Now().Add(30 * time.Second); ; {
		if v, e := query("SELECT 1"); e == nil && v == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("database readiness")
		}
		time.Sleep(200 * time.Millisecond)
	}
	const countsSQL = "SELECT (SELECT count(*) FROM public.datasets)||'|'||(SELECT count(*) FROM public.dataset_versions)||'|'||(SELECT max(version) FROM public.schema_migrations)"
	before, e := query(countsSQL)
	if e != nil || before != "1|2|16" {
		t.Fatal("actual retained history required", e)
	}
	results := []map[string]any{}
	for _, label := range []string{"reserve-collision", "utility-result-loss", "abort-result-loss", "cancel-after-start"} {
		r := configuration.BackupDefaults()
		r.DestinationRoot = filepath.Join(out, label)
		r.Reason = "Isolated backup abort acceptance " + label
		r.MaximumBytes = 64 << 20
		r.MaximumEntries = 10000
		r.StreamSeconds = 90
		r.ObserveSeconds = 90
		r.PollMilliseconds = 100
		request := filepath.Join(out, label+"-operation.json")
		put(label+"-operation.json", r)
		setActive(label, request)
		fault := &backupAbortFaultEngine{backupEngine: client}
		if label == "reserve-collision" {
			fault.raceDestination = r.DestinationRoot
		} else {
			fault.failUtility = true
		}
		beforeFiles := inventories()
		runContext := ctx
		var cancelRun context.CancelFunc
		if label == "cancel-after-start" {
			runContext, cancelRun = context.WithCancel(ctx)
			fault.failUtility = false
			fault.cancelAfterStart = cancelRun
		}
		result, e := runBackup(runContext, c, r, "backup", fault, worker)
		if e == nil {
			t.Fatal("fault became backup success", label)
		}
		if cancelRun != nil {
			cancelRun()
		}
		if label == "cancel-after-start" {
			if result.Pending || result.Status != "backup_aborted_partial_evidence_retained" || fault.starts != 1 {
				t.Fatal("cancelled exact task cleanup", label, result, e)
			}
		} else {
			if !result.Pending {
				t.Fatal("unknown effect not retained", label, result, e)
			}
			journal, e := bundle.ReadProtectedConfiguration(filepath.Join(c.ScopeRoot, journalName))
			if e != nil {
				t.Fatal(e)
			}
			if _, err := operator(ctx, "start-runtime", ""); err == nil {
				t.Fatal("application started during incomplete backup")
			}
			after, e := bundle.ReadProtectedConfiguration(filepath.Join(c.ScopeRoot, journalName))
			if e != nil || wire.Digest(journal) != wire.Digest(after) {
				t.Fatal("refused application mutated journal", e)
			}
			if label == "abort-result-loss" {
				fault.failRemoval = true
				result, e = runBackup(ctx, c, r, "abort-backup", fault, worker)
				if e == nil || !result.Pending || fault.removals != 1 {
					t.Fatal("abort response loss was not durable", result, e)
				}
			}
			value, e := operator(ctx, "abort-backup", request)
			if e != nil || wire.StrictJSON(value, &result) != nil || result.Pending || result.Status != "backup_aborted_partial_evidence_retained" || result.ManifestSHA256 != "" {
				t.Fatalf("native abort %s: %s %v", label, value, e)
			}
		}
		if _, e = os.Lstat(filepath.Join(r.DestinationRoot, backupManifestFile)); !os.IsNotExist(e) {
			t.Fatal("aborted attempt published complete manifest", label, e)
		}
		if label == "reserve-collision" {
			sentinel, e := bundle.ReadProtectedConfiguration(filepath.Join(r.DestinationRoot, "sentinel"))
			if e != nil || string(sentinel) != backupCollisionSentinel {
				t.Fatal("unknown raced destination changed", e)
			}
			if _, e = os.Lstat(filepath.Join(r.DestinationRoot, backupPlanFile)); !os.IsNotExist(e) {
				t.Fatal("unknown destination populated")
			}
		}
		l, e := open(c, true)
		if e != nil {
			t.Fatal(e)
		}
		v, e := l.verifyRuntimeContext(ctx, client, worker, false)
		if e != nil {
			l.close()
			t.Fatal("original runtime changed", label, e)
		}
		plan, _, e := l.makeBackupPlan(v, r, result.AttemptID)
		l.close()
		if e != nil {
			t.Fatal(e)
		}
		for _, volume := range plan.Volumes {
			if _, found, e := client.InspectVolume(ctx, volume.Owner); e != nil || found {
				t.Fatal("aborted disposable volume retained", label, e)
			}
		}
		for role, service := range v.observed.Services {
			if role == "postgres" && service.State != "running" || role != "postgres" && service.State != "exited" {
				t.Fatal("source processes changed", role, service.State)
			}
		}
		if wire.Digest(encode(beforeFiles)) != wire.Digest(encode(inventories())) {
			t.Fatal("source namespace/file bytes changed", label)
		}
		if prior := os.Getenv("PROJECT_ONE_TEST_SCOPED_BACKUP_PRIOR_OPERATION"); prior != "" {
			if _, e := operator(ctx, "inspect-backup", prior); e != nil {
				t.Fatal("complete historical backup lost", e)
			}
		}
		results = append(results, map[string]any{"case": label, "result": result, "actual_export_starts": fault.starts, "lost_abort_volume_removals": fault.removals, "exact_new_objects_removed": true, "complete_manifest_absent": true, "source_runtime_unchanged": true, "unchanged_source_archive_hashes": beforeFiles})
	}
	after, e := query(countsSQL)
	if e != nil || after != before {
		t.Fatal("source business history changed", e)
	}
	put("actual.json", map[string]any{"status": "passed_actual_backup_reservation_abort_cancellation_faults", "scope_id": checkpoint.ScopeID, "cases": results, "source_business_counts_unchanged": before, "qualification": "actual_isolated_backup_failure_recovery_only_not_fresh_restore_upgrade_four_store_or_native_qualification"})
}

type backupFixtureHashWriter struct {
	digest         hash.Hash
	maximum, bytes int64
}

func (w *backupFixtureHashWriter) Write(raw []byte) (int, error) {
	if int64(len(raw)) > w.maximum-w.bytes {
		return 0, io.ErrShortWrite
	}
	n, e := w.digest.Write(raw)
	w.bytes += int64(n)
	return n, e
}

const backupCollisionSentinel = "Keep this raced isolated fixture unchanged\n"

// Test-only faults follow real effects or a controlled owned filesystem race.
// No production environment switch or successful backend stub is introduced.
type backupAbortFaultEngine struct {
	backupEngine
	raceDestination  string
	raced            bool
	failUtility      bool
	failRemoval      bool
	cancelAfterStart context.CancelFunc
	starts           int
	removals         int
}

func (e *backupAbortFaultEngine) Preflight(ctx context.Context) (engine.Observation, error) {
	v, err := e.backupEngine.Preflight(ctx)
	if err == nil && e.raceDestination != "" && !e.raced {
		if err = os.Mkdir(e.raceDestination, 0700); err != nil {
			return v, err
		}
		root, re := os.OpenRoot(e.raceDestination)
		if re != nil {
			return v, re
		}
		err = writeBackupFile(root, "sentinel", []byte(backupCollisionSentinel))
		root.Close()
		e.raced = true
	}
	return v, err
}
func (e *backupAbortFaultEngine) CreateUtility(ctx context.Context, o engine.Ownership, image string, uid, gid int) (engine.Utility, error) {
	v, err := e.backupEngine.CreateUtility(ctx, o, image, uid, gid)
	if err == nil && e.failUtility {
		e.failUtility = false
		return v, errors.New("ACTUAL_BACKUP_UTILITY_RESULT_LOST")
	}
	return v, err
}
func (e *backupAbortFaultEngine) StartDatabaseTask(ctx context.Context, t engine.DatabaseTask, id string, files []engine.ConfigurationFile, reserve int) (engine.ResourceAdmission, error) {
	v, err := e.backupEngine.StartDatabaseTask(ctx, t, id, files, reserve)
	if err == nil {
		e.starts++
		if e.cancelAfterStart != nil {
			e.cancelAfterStart()
		}
	}
	return v, err
}
func (e *backupAbortFaultEngine) RemoveBackupVolume(ctx context.Context, o engine.Ownership) error {
	err := e.backupEngine.RemoveBackupVolume(ctx, o)
	if err == nil && e.failRemoval {
		e.failRemoval = false
		e.removals++
		return errors.New("ACTUAL_BACKUP_ABORT_REMOVE_RESULT_LOST")
	}
	return err
}
