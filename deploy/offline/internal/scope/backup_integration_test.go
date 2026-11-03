//go:build offlineintegration

package scope

import (
	"context"
	"errors"
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

// These wrappers lose a result only after the real bounded Engine effect and
// its SDK readback. They never manufacture a successful container/SQL/archive.
type backupLossEngine struct {
	backupEngine
	lost    map[string]bool
	effects map[string]int
}

func (e *backupLossEngine) loss(kind, key string, err error) error {
	if err != nil {
		return err
	}
	e.effects[kind+":"+key]++
	if e.effects[kind+":"+key] > 1 {
		return errors.New("ACTUAL_BACKUP_PHYSICAL_EFFECT_REPEATED")
	}
	if !e.lost[kind] {
		e.lost[kind] = true
		return errors.New("ACTUAL_BACKUP_RESULT_LOSS_AFTER_REAL_EFFECT")
	}
	return nil
}
func (e *backupLossEngine) CreateVolume(ctx context.Context, o engine.Ownership) (engine.Volume, error) {
	v, err := e.backupEngine.CreateVolume(ctx, o)
	return v, e.loss("volume-create", o.VolumeName(), err)
}
func (e *backupLossEngine) CreateUtility(ctx context.Context, o engine.Ownership, image string, uid, gid int) (engine.Utility, error) {
	v, err := e.backupEngine.CreateUtility(ctx, o, image, uid, gid)
	return v, e.loss("utility-create", o.VolumeName(), err)
}
func (e *backupLossEngine) PutConfiguration(ctx context.Context, o engine.Ownership, image string, uid, gid int, files []engine.ConfigurationFile) error {
	return e.loss("payload-write", o.VolumeName(), e.backupEngine.PutConfiguration(ctx, o, image, uid, gid, files))
}
func (e *backupLossEngine) RemoveUtility(ctx context.Context, o engine.Ownership, image string, uid, gid int) error {
	return e.loss("utility-remove", o.VolumeName(), e.backupEngine.RemoveUtility(ctx, o, image, uid, gid))
}
func (e *backupLossEngine) CreateDatabaseTask(ctx context.Context, t engine.DatabaseTask) (engine.RuntimeContainer, error) {
	v, err := e.backupEngine.CreateDatabaseTask(ctx, t)
	return v, e.loss("task-create", t.Kind, err)
}
func (e *backupLossEngine) StartDatabaseTask(ctx context.Context, t engine.DatabaseTask, id string, f []engine.ConfigurationFile, reserve int) (engine.ResourceAdmission, error) {
	v, err := e.backupEngine.StartDatabaseTask(ctx, t, id, f, reserve)
	return v, e.loss("task-execute", id, err)
}
func (e *backupLossEngine) RemoveDatabaseTask(ctx context.Context, t engine.DatabaseTask, id string) error {
	return e.loss("task-remove", id, e.backupEngine.RemoveDatabaseTask(ctx, t, id))
}
func (e *backupLossEngine) RemoveBackupVolume(ctx context.Context, o engine.Ownership) error {
	return e.loss("volume-remove", o.VolumeName(), e.backupEngine.RemoveBackupVolume(ctx, o))
}
func (e *backupLossEngine) StreamStoreBackupArchive(ctx context.Context, t engine.DatabaseTask, id string, maximum int64, entries, seconds int, sink io.Writer) (engine.StoreBackupArchive, error) {
	a, err := e.backupEngine.StreamStoreBackupArchive(ctx, t, id, maximum, entries, seconds, sink)
	if err == nil && !e.lost["archive-readback"] {
		e.lost["archive-readback"] = true
		return a, errors.New("ACTUAL_BACKUP_RESULT_LOSS_AFTER_REAL_READBACK")
	}
	return a, err
}

func TestActualScopedBackupRetainedDataLossRecoveryAndNativeInspection(t *testing.T) {
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
	r := configuration.BackupDefaults()
	r.DestinationRoot = filepath.Join(out, "backup")
	r.Reason = "Isolated full backup recovery acceptance"
	r.MaximumBytes = 64 << 20
	r.MaximumEntries = 10000
	r.StreamSeconds = 120
	r.ObserveSeconds = 120
	r.PollMilliseconds = 100
	request := filepath.Join(out, "operation.json")
	put("operation.json", r)
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
	completed := false
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 90*time.Second)
		defer done()
		if !completed {
			_, _ = operator(cleanup, "abort-backup", request)
		}
		_, err := operator(cleanup, "stop-runtime", "")
		if err != nil {
			t.Error("recorded stop", err)
		}
		put("cleanup.json", map[string]any{"recorded_stop_passed": err == nil, "original_scope_data_and_backup_artifacts_retained": true, "shared_images_original_services_preserved": true})
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
		t.Fatal("actual installed history required", e)
	}
	wrapper := &backupLossEngine{backupEngine: client, lost: map[string]bool{}, effects: map[string]int{}}
	result, e := runBackup(ctx, c, r, "backup", wrapper, worker)
	historical := false
	for attempt := 0; e != nil && attempt < 9; attempt++ {
		if !result.Pending {
			t.Fatal("loss not durable", result, e)
		}
		lostBefore := len(wrapper.lost)
		effectsBefore := wire.Digest(encode(wrapper.effects))
		if _, repeat := runBackup(ctx, c, r, "backup", wrapper, worker); repeat == nil || wire.Digest(encode(wrapper.effects)) != effectsBefore {
			t.Fatal("unknown backup resent")
		}
		put("loss-"+string(rune('a'+attempt))+".json", map[string]any{"status": result, "lost_real_effect_kinds": wrapper.lost, "effect_counts": wrapper.effects})
		if prior := os.Getenv("PROJECT_ONE_TEST_SCOPED_BACKUP_PRIOR_OPERATION"); prior != "" && !historical {
			value, err := operator(ctx, "inspect-backup", prior)
			var old BackupResult
			if err != nil || wire.StrictJSON(value, &old) != nil || old.Pending || old.AttemptID == result.AttemptID || old.ManifestSHA256 == "" {
				t.Fatalf("historical inspection during pending new attempt: %s %v", value, err)
			}
			historical = true
		}
		if wrapper.lost["volume-remove"] {
			break
		}
		result, e = runBackup(ctx, c, r, "reconcile-backup", wrapper, worker)
		if e != nil && len(wrapper.lost) == lostBefore {
			t.Fatal("reconciliation stopped making progress", e)
		}
	}
	if !wrapper.lost["volume-remove"] || len(wrapper.lost) != 9 {
		t.Fatal("expected exact nine real result-loss boundaries", wrapper.lost, e)
	}
	// A distinct native process reads the persisted pending journal and finishes
	// only the remaining cleanup/manifest steps, without another physical dump.
	value, e := operator(ctx, "reconcile-backup", request)
	if e != nil || wire.StrictJSON(value, &result) != nil || result.Pending || result.Status != "complete_selected_profile_backup_restore_pending" {
		t.Fatalf("native reconcile: %s %v", value, e)
	}
	value, e = operator(ctx, "inspect-backup", request)
	var inspected BackupResult
	if e != nil || wire.StrictJSON(value, &inspected) != nil || inspected.ManifestSHA256 != result.ManifestSHA256 {
		t.Fatalf("native inspection: %s %v", value, e)
	}
	root, e := openBackupDirectory(r.DestinationRoot)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	var plan BackupPlan
	planRaw, e := regular(root, backupPlanFile, c.Verification.MaximumMetadataBytes)
	if e != nil || wire.StrictJSON(planRaw, &plan) != nil {
		t.Fatal("actual plan", e)
	}
	manifest, hash, e := inspectBackupFiles(ctx, root, plan)
	if e != nil || hash.SHA256 != result.ManifestSHA256 || manifest.Source.SchemaVersion != 16 || manifest.Source.VectorVersion != "0.8.7" || len(manifest.Stores) != 2 || manifest.Stores[0] != "datasets" || manifest.Stores[1] != "models" {
		t.Fatal("actual complete declared inventory", manifest.Source, e)
	}
	for _, v := range plan.Volumes {
		if _, found, e := client.InspectVolume(ctx, v.Owner); e != nil || found {
			t.Fatal("backup disposable volume retained", e)
		}
	}
	after, e := query(countsSQL)
	if e != nil || after != before {
		t.Fatal("source business data changed", e)
	}
	// Read-only inspection detects tampering and cannot repair it; restore the
	// same test-owned bytes explicitly before the final successful inspection.
	key := plan.ConfigurationFiles[0]
	name := "configuration/" + key.Role + "/" + key.File.Name
	original, e := regular(root, name, key.File.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	changed := append([]byte{}, original...)
	changed[0] ^= 1
	f, e := root.OpenFile(name, os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteAt(changed, 0)
	f.Sync()
	f.Close()
	if _, e = operator(ctx, "inspect-backup", request); e == nil {
		t.Fatal("tampered backup accepted")
	}
	f, e = root.OpenFile(name, os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteAt(original, 0)
	f.Sync()
	f.Close()
	if _, e = operator(ctx, "inspect-backup", request); e != nil {
		t.Fatal("restored exact fixture bytes", e)
	}
	completed = true
	put("actual.json", map[string]any{"status": "passed_actual_scoped_selected_profile_backup_loss_recovery_native_inspection", "scope_id": checkpoint.ScopeID, "result": result, "manifest": manifest, "nine_result_losses_after_real_effects_or_readback": wrapper.lost, "physical_effect_counts": wrapper.effects, "source_business_counts_unchanged": before, "tamper_refusal_and_exact_test_byte_recovery": true, "historical_completed_backup_inspected_during_new_pending_attempt": historical, "qualification": "complete_two_declared_store_and_later_logical_dump_private_input_backup_only_fresh_restore_four_store_profile_upgrade_and_native_target_pending"})
}
