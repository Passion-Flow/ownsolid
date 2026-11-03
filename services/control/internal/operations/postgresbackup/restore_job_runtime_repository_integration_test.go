//go:build repository_integration && postgres_backup_integration && postgres_job_runtime_integration

package postgresbackup

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type actualRestoreJobProfile struct {
	FormatVersion            int      `json:"format_version"`
	ArtifactSource           string   `json:"artifact_source"`
	ClientDistribution       string   `json:"client_distribution"`
	SourceBackupID           string   `json:"source_backup_id"`
	SourceScopeID            string   `json:"source_scope_id"`
	SourceManifestSHA256     string   `json:"source_manifest_sha256"`
	SourceControlImageSHA256 string   `json:"source_control_image_sha256"`
	TargetControlImageSHA256 string   `json:"target_control_image_sha256"`
	PostgresImageSHA256      string   `json:"postgres_image_sha256"`
	PostgresSystemIdentifier string   `json:"postgres_system_identifier"`
	ControlSHA256            string   `json:"control_sha256"`
	RunnerSHA256             string   `json:"runner_sha256"`
	DumpSHA256               string   `json:"dump_sha256"`
	RestoreSHA256            string   `json:"restore_sha256"`
	TargetScopeID            string   `json:"target_scope_id"`
	RestoreID                string   `json:"restore_id"`
	OperationID              string   `json:"operation_id"`
	MaintenanceSHA256        string   `json:"maintenance_sha256"`
	BackupClaimUID           string   `json:"backup_claim_uid"`
	OperationsClaimUID       string   `json:"operations_claim_uid"`
	StoreClaimUIDs           []string `json:"store_claim_uids"`
	PortalOrigin             string   `json:"portal_origin"`
	AdminOrigin              string   `json:"admin_origin"`
}

func decodeActualRestoreJobProfile(raw []byte) (actualRestoreJobProfile, bool) {
	var p actualRestoreJobProfile
	var fields map[string]json.RawMessage
	if release.StrictJSON(raw, &p) != nil || json.Unmarshal(raw, &fields) != nil {
		return p, false
	}
	shape := reflect.TypeOf(p)
	if len(fields) != shape.NumField() {
		return p, false
	}
	for i := 0; i < shape.NumField(); i++ {
		if _, ok := fields[shape.Field(i).Tag.Get("json")]; !ok {
			return p, false
		}
	}
	if p.FormatVersion != 1 || p.ArtifactSource != "current-image" || p.ClientDistribution != release.PostgreSQLClientPGDGBookworm || len(p.StoreClaimUIDs) != 4 || p.PostgresSystemIdentifier != "7702686790272344104" || p.TargetControlImageSHA256 == p.SourceControlImageSHA256 || p.PostgresImageSHA256 == p.TargetControlImageSHA256 {
		return p, false
	}
	for _, sha := range []string{p.SourceManifestSHA256, p.SourceControlImageSHA256, p.TargetControlImageSHA256, p.PostgresImageSHA256, p.ControlSHA256, p.RunnerSHA256, p.DumpSHA256, p.RestoreSHA256, p.MaintenanceSHA256} {
		if !release.ValidSHA256(sha) {
			return p, false
		}
	}
	seen := map[string]bool{}
	for _, id := range []string{p.SourceBackupID, p.SourceScopeID, p.TargetScopeID, p.RestoreID, p.OperationID} {
		if len(id) != 32 || !release.ValidSHA256(id+id) || seen[id] || id == strings.Repeat("0", 32) {
			return p, false
		}
		seen[id] = true
	}
	for _, id := range append([]string{p.BackupClaimUID, p.OperationsClaimUID}, p.StoreClaimUIDs...) {
		u, e := uuid.Parse(id)
		if e != nil || u == uuid.Nil || u.String() != id || seen[id] {
			return p, false
		}
		seen[id] = true
	}
	for _, origin := range []string{p.PortalOrigin, p.AdminOrigin} {
		u, e := url.Parse(origin)
		if e != nil || u.Scheme != "https" || u.Hostname() != "localhost" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
			return p, false
		}
	}
	return p, p.PortalOrigin != p.AdminOrigin
}

func TestActualRestoreJobProfileRejectsAmbiguousOrUnboundInputs(t *testing.T) {
	p := actualRestoreJobProfile{FormatVersion: 1, ArtifactSource: "current-image", ClientDistribution: release.PostgreSQLClientPGDGBookworm, SourceBackupID: strings.Repeat("e", 32), SourceScopeID: strings.Repeat("a", 32), SourceManifestSHA256: strings.Repeat("9", 64), SourceControlImageSHA256: strings.Repeat("1", 64), TargetControlImageSHA256: strings.Repeat("2", 64), PostgresImageSHA256: strings.Repeat("3", 64), PostgresSystemIdentifier: "7702686790272344104", ControlSHA256: strings.Repeat("4", 64), RunnerSHA256: strings.Repeat("5", 64), DumpSHA256: strings.Repeat("6", 64), RestoreSHA256: strings.Repeat("7", 64), TargetScopeID: strings.Repeat("b", 32), RestoreID: strings.Repeat("c", 32), OperationID: strings.Repeat("d", 32), MaintenanceSHA256: strings.Repeat("8", 64), BackupClaimUID: "11111111-1111-4111-8111-111111111111", OperationsClaimUID: "22222222-2222-4222-8222-222222222222", StoreClaimUIDs: []string{"33333333-3333-4333-8333-333333333333", "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555", "66666666-6666-4666-8666-666666666666"}, PortalOrigin: "https://localhost:18543", AdminOrigin: "https://localhost:19543"}
	raw, _ := json.Marshal(p)
	if _, ok := decodeActualRestoreJobProfile(raw); !ok {
		t.Fatal("exact protected profile refused")
	}
	for _, change := range []map[string]any{{"artifact_source": ""}, {"artifact_source": false}, {"client_distribution": "upstream"}, {"client_distribution": nil}, {"target_control_image_sha256": p.SourceControlImageSHA256}, {"postgres_system_identifier": "other"}, {"store_claim_uids": []string{p.BackupClaimUID, p.BackupClaimUID, p.BackupClaimUID, p.BackupClaimUID}}, {"target_scope_id": p.SourceScopeID}, {"portal_origin": "http://localhost:18543"}, {"admin_origin": p.PortalOrigin}, {"extra": true}} {
		var value map[string]any
		_ = json.Unmarshal(raw, &value)
		for k, v := range change {
			value[k] = v
		}
		bad, _ := json.Marshal(value)
		if _, ok := decodeActualRestoreJobProfile(bad); ok {
			t.Fatal("unbound profile admitted")
		}
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"artifact_source"`), []byte(`"ARTIFACT_SOURCE"`), 1), append([]byte(`{"artifact_source":"current-image",`), raw[1:]...)} {
		if _, ok := decodeActualRestoreJobProfile(bad); ok {
			t.Fatal("ambiguous profile admitted")
		}
	}
}

// A continuation of the retained actual packaged-image backup. No live source
// volume or source SQL connection is needed or admitted by this fixture.
func TestActualPackagedPostgresFreshRestoreThroughFixedJobRuntime(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" || os.Geteuid() != 65534 || os.Getegid() != 65534 {
		t.Fatal("exact isolated native ARM64 runtime-owner fixture required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	fixture, e := openPrivateRoot("/fixture")
	if e != nil {
		t.Fatal("private owned fixture required")
	}
	defer fixture.Close()
	profileRaw, e := readPrivateBytes("/fixture/restore-job-profile.json", 64<<10)
	p, ok := decodeActualRestoreJobProfile(profileRaw)
	if e != nil || !ok {
		t.Fatal("protected current-image restore inputs required")
	}
	write := func(name string, value any) {
		t.Helper()
		if e := writeDocument(fixture, name, value, 1<<20); e != nil {
			t.Fatal("exclusive durable fixture record", e)
		}
	}
	backupRoot := filepath.Join("/job-backup/private/backups", p.SourceBackupID)
	var m Manifest
	manifestRaw, e := readPrivateDocument(filepath.Join(backupRoot, candidateName), 16<<20, &m)
	if e != nil || digest(manifestRaw) != p.SourceManifestSHA256 || m.Request.BackupRoot != backupRoot || m.Request.BackupID != p.SourceBackupID || m.Request.SourceScopeID != p.SourceScopeID || m.Request.ControlImageSHA256 != p.SourceControlImageSHA256 || m.Database.SchemaVersion != 22 || m.Database.Tables != 86 {
		t.Fatal("exact retained native source required")
	}
	for _, absent := range []string{"/fixture/source-dsn", "/fixture/operator.yaml", "/fixture/job-operator", "/fixture/models"} {
		if _, e = os.Lstat(absent); !os.IsNotExist(e) {
			t.Fatal("live source inputs must not be mounted")
		}
	}
	r := release.PostgreSQLRestoreRequest{FormatVersion: 1, RestoreID: p.RestoreID, TargetScopeID: p.TargetScopeID, SourceManifestSHA256: p.SourceManifestSHA256, BackupRoot: backupRoot, RestoreRoot: filepath.Join("/restore-operations/private/restores", p.RestoreID), TargetDatabase: "m18_restore_" + p.TargetScopeID + "_test", TargetOperator: m.Request.Operator, ConfigurationRoot: "/fixture/target-configuration", Reason: "Actual current packaged fixed restore Job from retained readonly backup; no source access or Kubernetes/CSI qualification", Policy: m.Request.Policy}
	r.Policy.ControlFile = "/usr/local/bin/control"
	r.Policy.ControlSHA256 = p.ControlSHA256
	r.Policy.DumpFile = "/usr/local/bin/pg_dump"
	r.Policy.DumpSHA256 = p.DumpSHA256
	r.Policy.RestoreFile = "/usr/local/bin/pg_restore"
	r.Policy.RestoreSHA256 = p.RestoreSHA256
	r.Policy.ClientDistribution = p.ClientDistribution
	r.Policy.ScratchRoot = "/restore-operations/private/scratch"
	r.Policy.OperationSeconds = 180
	r.RuntimeConfigurationFile = filepath.Join(r.ConfigurationRoot, "control.yaml")
	if !r.Valid() {
		t.Fatal("target native request invalid")
	}
	if e = trustedTool(ctx, r.Policy.ControlFile, p.ControlSHA256); e != nil {
		t.Fatal("packaged current Control required", e)
	}
	runnerFile, e := os.Open("/usr/local/bin/kube-job")
	if e != nil {
		t.Fatal("packaged current runner required")
	}
	runnerSHA, _, he := hashStream(ctx, nil, runnerFile, 128<<20)
	ce := runnerFile.Close()
	if he != nil || ce != nil || runnerSHA != p.RunnerSHA256 {
		t.Fatal("packaged runner artifact mismatch")
	}
	if _, e = copyTree(ctx, filepath.Join(backupRoot, "configuration"), r.ConfigurationRoot, r.Policy.MaximumConfigurationBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes, r.Policy.MinimumFreeBytes); e != nil {
		t.Fatal("retained configuration copy", e)
	}
	effective, e := readPrivateBytes(filepath.Join(backupRoot, effectiveName), r.Policy.MaximumConfigurationBytes)
	if e != nil {
		t.Fatal(e)
	}
	targetOperator, e := config.Decode([]byte(strings.ReplaceAll(string(effective), m.Request.ConfigurationRoot, r.ConfigurationRoot)))
	if e != nil {
		t.Fatal("owning typed target configuration refused", e)
	}
	targetOperator.Database.DSNFile = "/fixture/target-operator-dsn"
	targetOperator.Models.ArtifactRoot = "/restore-models"
	targetOperator.Datasets.ArtifactRoot = "/restore-datasets"
	targetOperator.Training.ArtifactRoot = "/restore-training"
	targetOperator.ModelPreparation.ArtifactRoot = "/restore-preparations"
	targetOperator.Server.PortalOrigin = p.PortalOrigin
	targetOperator.Server.AdminOrigin = p.AdminOrigin
	operatorDSN, e := readPrivateBytes(targetOperator.Database.DSNFile, 16<<10)
	if e != nil {
		t.Fatal("protected target operator credential required")
	}
	u, e := url.Parse(strings.TrimSpace(string(operatorDSN)))
	if e != nil || u.User == nil || u.User.Username() != r.TargetOperator || u.Host != "postgres:5432" || u.Path != "/"+r.TargetDatabase || u.Query().Get("sslmode") != "disable" {
		t.Fatal("exact local target operator endpoint required")
	}
	targetRuntime := targetOperator
	targetRuntime.Database.DSNFile = "/fixture/target-runtime-dsn"
	credential, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal("existing runtime credential producer")
	}
	u.User = url.UserPassword("m18_restored_"+p.TargetScopeID, credential)
	if e = writeNew(fixture, "target-runtime-dsn", []byte(u.String())); e != nil {
		t.Fatal("private runtime credential write")
	}
	if e = os.Mkdir("/fixture/restore-operator", 0700); e != nil {
		t.Fatal(e)
	}
	operatorRoot, e := openPrivateRoot("/fixture/restore-operator")
	if e != nil {
		t.Fatal(e)
	}
	defer operatorRoot.Close()
	opRaw, e := yaml.Marshal(targetOperator)
	if e != nil || writeNew(operatorRoot, "control.yaml", opRaw) != nil {
		t.Fatal("target operator projection")
	}
	runtimeRaw, e := yaml.Marshal(targetRuntime)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(r.RuntimeConfigurationFile, runtimeRaw, 0600); e != nil {
		t.Fatal("owned target runtime projection")
	}
	runtimeFile, e := os.OpenFile(r.RuntimeConfigurationFile, os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	se, ce := runtimeFile.Sync(), runtimeFile.Close()
	if se != nil || ce != nil {
		t.Fatal("target runtime sync")
	}
	if e = writeDocument(operatorRoot, "postgres-restore-request.json", r, 1<<20); e != nil {
		t.Fatal(e)
	}
	requestRaw, _ := canonical(r)
	stores := []map[string]string{}
	for i, name := range []string{"models", "datasets", "training", "preparations"} {
		stores = append(stores, map[string]string{"name": name, "mount_path": storeRoots(targetRuntime)[i], "claim_uid": p.StoreClaimUIDs[i]})
	}
	binding := map[string]any{"format_version": 1, "mode": "restore", "operation_id": p.OperationID, "target_scope_id": p.TargetScopeID, "maintenance_sha256": p.MaintenanceSHA256, "request_sha256": digest(requestRaw), "restore_id": p.RestoreID, "source_scope_id": p.SourceScopeID, "source_manifest_sha256": p.SourceManifestSHA256, "source_control_image_sha256": p.SourceControlImageSHA256, "target_control_image_sha256": p.TargetControlImageSHA256, "runner_sha256": p.RunnerSHA256, "operator_configuration_file": "/fixture/restore-operator/control.yaml", "operator_environment_file": "", "request_file": "/fixture/restore-operator/postgres-restore-request.json", "runtime_configuration_root": r.ConfigurationRoot, "backup_mount_path": "/job-backup", "backup_claim_uid": p.BackupClaimUID, "operations_mount_path": "/restore-operations", "operations_claim_uid": p.OperationsClaimUID, "stores": stores, "owner_uid": 65534, "owner_gid": 65534, "completion_sha256": ""}
	write("restore-job-binding.json", binding)
	write("restore-preparation-ready.json", map[string]string{"status": "target_inputs_ready_no_SQL_or_restore_effect", "target_database": r.TargetDatabase})
	for {
		if ctx.Err() != nil {
			t.Fatal("separate narrow initializer barrier", ctx.Err())
		}
		if _, e = os.Lstat("/fixture/restore-initialized.json"); e == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	for _, path := range storeRoots(targetRuntime) {
		info, e := os.Lstat(path)
		if e != nil || !owned(info, true) {
			t.Fatal("separately initialized target root", e)
		}
		entries, e := os.ReadDir(path)
		if e != nil || len(entries) != 0 {
			t.Fatal("target stores must remain empty before native restore", e)
		}
	}
	targetOperator, e = config.LoadProtected("/fixture/restore-operator/control.yaml")
	if e != nil {
		t.Fatal("typed target operator load", e)
	}
	targetRuntime, e = config.LoadProtected(r.RuntimeConfigurationFile)
	if e != nil {
		t.Fatal("typed target runtime load", e)
	}
	if e = AdmitRestore(ctx, targetOperator, targetRuntime, "/fixture/restore-operator/control.yaml", r, true); e != nil {
		t.Fatal("native target input closure before SQL", e)
	}
	if _, e = databaseaccess.LoadRuntime(r.RuntimeConfigurationFile); e != nil {
		t.Fatal("canonical target runtime credential input", e)
	}
	pc, e := pgxpool.ParseConfig(string(operatorDSN))
	if e != nil {
		t.Fatal("protected target DSN parse")
	}
	pc.MaxConns = 2
	pc.MinConns = 0
	pc.ConnConfig.RuntimeParams["search_path"] = "public"
	adminPC := pc.Copy()
	adminPC.ConnConfig.Database = "postgres"
	adminPC.MaxConns = 1
	admin, e := pgxpool.NewWithConfig(ctx, adminPC)
	if e != nil {
		t.Fatal("owned isolated server connection")
	}
	var systemID string
	if admin.QueryRow(ctx, "SELECT system_identifier::text FROM pg_catalog.pg_control_system()").Scan(&systemID) != nil || systemID != p.PostgresSystemIdentifier {
		admin.Close()
		t.Fatal("original owned PostgreSQL server identity required")
	}
	write("create-target-database-intent.json", map[string]string{"database": r.TargetDatabase, "target_scope_id": r.TargetScopeID, "system_identifier": systemID, "marker": "projectone:kubernetes-restore:" + r.TargetScopeID})
	if _, e = admin.Exec(ctx, `CREATE DATABASE "`+r.TargetDatabase+`" TEMPLATE template0`); e != nil {
		admin.Close()
		t.Fatal("fresh target creation failed or uncertain; never resend")
	}
	if _, e = admin.Exec(ctx, `COMMENT ON DATABASE "`+r.TargetDatabase+`" IS 'projectone:kubernetes-restore:`+r.TargetScopeID+`'`); e != nil {
		admin.Close()
		t.Fatal("fresh target marker failed or uncertain; never resend")
	}
	admin.Close()
	write("target-database-created.json", map[string]string{"database": r.TargetDatabase, "status": "created_once_empty_template0_marked"})
	invoke := func(mode string, b map[string]any) ([]byte, []byte, error) {
		raw, _ := canonical(b)
		cmd := exec.CommandContext(ctx, "/usr/local/bin/kube-job", mode, "--binding-json", string(raw))
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.WaitDelay = 3 * time.Second
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return os.ErrProcessDone
			}
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		var stdout, stderr limitedBuffer
		stdout.maximum = 64 << 10
		stderr.maximum = 64 << 10
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}
	output, diagnostic, e := invoke("restore-postgres", binding)
	if e != nil {
		_ = writeNew(fixture, "restore-runtime-failure-diagnostic", diagnostic)
		t.Fatal("actual fixed native restore failed; private diagnostic retained, never resend")
	}
	var proof map[string]json.RawMessage
	if release.StrictJSON(output, &proof) != nil || len(proof) != 18 {
		t.Fatal("strict actual restore process proof")
	}
	var complete RestoreCompletion
	completeRaw, e := readPrivateDocument(filepath.Join(r.RestoreRoot, restoreCompleteName), r.Policy.MaximumManifestBytes, &complete)
	var proofCompletion string
	_ = json.Unmarshal(proof["native_completion_sha256"], &proofCompletion)
	if e != nil || complete.RestoreID != r.RestoreID || complete.ManifestSHA256 != p.SourceManifestSHA256 || complete.CurrentLockHeld || digest(completeRaw) != proofCompletion {
		t.Fatal("actual native and process completion mismatch")
	}
	var intent RestoreIntent
	intentRaw, e := readPrivateDocument(filepath.Join(r.RestoreRoot, restoreIntentName), r.Policy.MaximumManifestBytes, &intent)
	if e != nil || len(intent.Request.TargetRoots) != 4 || digest(intentRaw) != complete.IntentSHA256 || intent.Request.RestoreID != r.RestoreID || intent.Request.TargetScopeID != r.TargetScopeID {
		t.Fatal("native measured request intent mismatch")
	}
	effectiveRequestRaw, _ := canonical(intent.Request)
	var proofEffective string
	_ = json.Unmarshal(proof["effective_request_sha256"], &proofEffective)
	if digest(effectiveRequestRaw) != proofEffective {
		t.Fatal("native request differs from fixed original effective request")
	}
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal("fresh target verification connection")
	}
	defer pool.Close()
	lease, e := pool.Acquire(ctx)
	if e != nil {
		t.Fatal("target verification session")
	}
	defer lease.Release()
	var held bool
	if lease.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&held) != nil || !held {
		t.Fatal("target verification installation lock")
	}
	defer func() {
		var unlocked bool
		if lease.QueryRow(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID).Scan(&unlocked) != nil || !unlocked {
			t.Error("target verification unlock")
		}
	}()
	current, e := readRestoreTarget(ctx, lease, intent.Request, false)
	if e != nil || current.SystemIdentifier != systemID || current.Database == m.Database.Database || targetBinding(current) != complete.TargetBindingSHA256 {
		t.Fatal("fresh target system/OID/name/marker binding", e)
	}
	intent.Target.BackendPID = current.BackendPID
	post, e := readRestoreAuthority(ctx, lease, intent, complete.IntentSHA256, complete.FilesSHA256)
	if e != nil || post != complete.PostDataSHA256 {
		t.Fatal("actual schema22/86 authority post-image", e)
	}
	var files RestoreFiles
	filesRaw, e := readPrivateDocument(filepath.Join(r.RestoreRoot, restoreFilesName), r.Policy.MaximumManifestBytes, &files)
	if e != nil || digest(filesRaw) != complete.FilesSHA256 || len(files.TargetTrees) != 4 || !reflect.DeepEqual(files.Roundtrip, m.Stores) {
		t.Fatal("native independent four-owner roundtrip binding", e)
	}
	var storesFiles int
	var storesBytes int64
	for i, path := range storeRoots(targetRuntime) {
		got, e := TreeDigest(ctx, path, r.Policy.MaximumStoreBytes, r.Policy.MaximumStoreFiles, r.Policy.MaximumManifestBytes)
		if e != nil || got != files.TargetTrees[i] {
			t.Fatal("actual restored original target tree bytes/modes differ", e)
		}
		storesFiles += got.Files
		storesBytes += got.Bytes
	}
	write("restore-job-result.json", map[string]any{"status": "passed_actual_packaged_native_fresh_restore_fixed_job", "source_manifest_sha256": p.SourceManifestSHA256, "target_scope_id": r.TargetScopeID, "restore_id": r.RestoreID, "target_database": r.TargetDatabase, "target_database_oid": current.DatabaseOID, "system_identifier": current.SystemIdentifier, "native_completion_sha256": digest(completeRaw), "native_intent_sha256": complete.IntentSHA256, "native_files_sha256": complete.FilesSHA256, "post_data_sha256": post, "process_proof_sha256": digest(output), "schema": 22, "tables": 86, "store_files": storesFiles, "store_bytes": storesBytes, "no_live_source_volume_or_SQL": true, "qualification": "Actual packaged ARM64 native SQL/four-store restore only; separate kernel-readonly observer, role/serving/Kubernetes/CSI/populated/physical qualification remain."})
	write("restore-job-proof.json", json.RawMessage(output))
}
