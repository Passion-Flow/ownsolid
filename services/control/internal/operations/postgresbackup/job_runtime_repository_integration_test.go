//go:build repository_integration && postgres_backup_integration && postgres_job_runtime_integration

package postgresbackup

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type actualPostgresJobProfile struct {
	FormatVersion        int    `json:"format_version"`
	SourceManifestSHA256 string `json:"source_manifest_sha256"`
	ControlImageSHA256   string `json:"control_image_sha256"`
	PostgresImageSHA256  string `json:"postgres_image_sha256"`
	BackupClaimUID       string `json:"backup_claim_uid"`
	ArtifactSource       string `json:"artifact_source,omitempty"`
	ClientDistribution   string `json:"client_distribution,omitempty"`
}

func decodeActualPostgresJobProfile(raw []byte) (actualPostgresJobProfile, error) {
	var profile actualPostgresJobProfile
	bad := errors.New("POSTGRES_ACTUAL_JOB_PROFILE_INVALID")
	var fields map[string]json.RawMessage
	if release.StrictJSON(raw, &profile) != nil || json.Unmarshal(raw, &fields) != nil || profile.FormatVersion != 1 || !release.ValidSHA256(profile.SourceManifestSHA256) || !release.ValidSHA256(profile.ControlImageSHA256) || !release.ValidSHA256(profile.PostgresImageSHA256) {
		return actualPostgresJobProfile{}, bad
	}
	for name := range fields {
		switch name {
		case "format_version", "source_manifest_sha256", "control_image_sha256", "postgres_image_sha256", "backup_claim_uid", "artifact_source", "client_distribution":
		default:
			return actualPostgresJobProfile{}, bad
		}
	}
	if _, e := uuid.Parse(profile.BackupClaimUID); e != nil {
		return actualPostgresJobProfile{}, bad
	}
	if profile.ArtifactSource == "" {
		if _, present := fields["artifact_source"]; present || profile.ClientDistribution != "" && profile.ClientDistribution != release.PostgreSQLClientUpstream {
			return actualPostgresJobProfile{}, bad
		}
	} else if profile.ArtifactSource != "current-image" || profile.ClientDistribution != release.PostgreSQLClientPGDGBookworm || profile.ControlImageSHA256 == profile.PostgresImageSHA256 {
		return actualPostgresJobProfile{}, bad
	}
	if _, present := fields["client_distribution"]; present && profile.ClientDistribution == "" {
		return actualPostgresJobProfile{}, bad
	}
	return profile, nil
}

func TestActualPostgresJobProfileRejectsAmbiguousOrContradictoryTools(t *testing.T) {
	legacy := map[string]any{"format_version": 1, "source_manifest_sha256": strings.Repeat("a", 64), "control_image_sha256": strings.Repeat("b", 64), "postgres_image_sha256": strings.Repeat("b", 64), "backup_claim_uid": "11111111-1111-4111-8111-111111111111"}
	raw, _ := json.Marshal(legacy)
	if p, e := decodeActualPostgresJobProfile(raw); e != nil || p.ArtifactSource != "" || p.ClientDistribution != "" {
		t.Fatal("legacy protected profile changed", e)
	}
	current := map[string]any{}
	for key, value := range legacy {
		current[key] = value
	}
	current["artifact_source"], current["client_distribution"], current["postgres_image_sha256"] = "current-image", release.PostgreSQLClientPGDGBookworm, strings.Repeat("c", 64)
	raw, _ = json.Marshal(current)
	if p, e := decodeActualPostgresJobProfile(raw); e != nil || p.ArtifactSource != "current-image" || p.ClientDistribution != release.PostgreSQLClientPGDGBookworm {
		t.Fatal("explicit current packaged profile", e)
	}
	for _, change := range []map[string]any{{"artifact_source": ""}, {"artifact_source": false}, {"artifact_source": nil}, {"artifact_source": "arbitrary"}, {"client_distribution": ""}, {"client_distribution": false}, {"client_distribution": nil}, {"client_distribution": "upstream"}, {"client_distribution": "skip"}, {"postgres_image_sha256": strings.Repeat("b", 64)}, {"extra": "unknown"}, {"ARTIFACT_SOURCE": "current-image"}} {
		value := map[string]any{}
		for key, item := range current {
			value[key] = item
		}
		for key, item := range change {
			value[key] = item
		}
		raw, _ := json.Marshal(value)
		if _, e := decodeActualPostgresJobProfile(raw); e == nil {
			t.Fatal("invalid local tool profile admitted", change)
		}
	}
	raw, _ = json.Marshal(current)
	duplicate := append([]byte(`{"artifact_source":"current-image",`), raw[1:]...)
	if _, e := decodeActualPostgresJobProfile(duplicate); e == nil {
		t.Fatal("duplicate artifact selector admitted")
	}
}

// The legacy profile retains its original overlays. The explicit current-image
// profile executes packaged Control/runner/clients without executable overlays.
// Neither profile establishes Kubernetes, CSI, serving/TLS or installed acceptance.
func TestActualNativePostgresBackupThroughFixedJobRuntime(t *testing.T) {
	rawProfile, e := readPrivateBytes("/fixture/job-runtime-profile.json", 64<<10)
	profile, pe := decodeActualPostgresJobProfile(rawProfile)
	if e != nil || pe != nil {
		t.Fatal("protected actual native fixture profile required", e, pe)
	}
	packaged := profile.ArtifactSource == "current-image"
	runner := "/operator/kube-job"
	if packaged {
		runner = "/usr/local/bin/kube-job"
	}
	if os.Getenv("PROJECT_ONE_TEST_POSTGRES_JOB_RUNTIME") != runner {
		t.Fatal("exact isolated current runtime required")
	}
	f := newActualPostgresBackupFixtureWithTools(t, 300*time.Second, 120, 1<<30, 100, 4<<20, actualPostgresFixtureTools{clientDistribution: profile.ClientDistribution, packagedImage: packaged})
	defer f.close()
	r := f.request
	r.SourceManifestSHA256, r.ControlImageSHA256, r.PostgresImageSHA256 = profile.SourceManifestSHA256, profile.ControlImageSHA256, profile.PostgresImageSHA256
	r.RuntimeConfigurationFile = filepath.Join(r.ConfigurationRoot, "control.yaml")
	if e := os.Rename(f.request.RuntimeConfigurationFile, r.RuntimeConfigurationFile); e != nil {
		t.Fatal("exact owned runtime filename", e)
	}
	r.BackupRoot = filepath.Join("/job-backup/private/backups", r.BackupID)
	r.Policy.ScratchRoot = "/job-backup/private/scratch"
	r.Reason = "Actual fixed Job runtime and unchanged native Control backup; no Kubernetes or CSI qualification"
	if packaged {
		r.Reason = "Actual packaged image Control, runner and PGDG clients; no Kubernetes or CSI qualification"
	}
	operatorRoot := "/fixture/job-operator"
	if e := os.Mkdir(operatorRoot, 0700); e != nil {
		t.Fatal(e)
	}
	operatorPath := filepath.Join(operatorRoot, "control.yaml")
	operatorRaw, e := readPrivateBytes(f.operatorPath, 256<<10)
	if e != nil {
		t.Fatal(e)
	}
	f.write(operatorPath, operatorRaw)
	requestPath := filepath.Join(operatorRoot, "postgres-backup-request.json")
	requestRaw, e := canonical(r)
	if e != nil {
		t.Fatal(e)
	}
	f.write(requestPath, requestRaw)
	file, e := os.Open(runner)
	if e != nil {
		t.Fatal(e)
	}
	runnerSHA, _, e := hashStream(f.ctx, nil, file, 128<<20)
	ce := file.Close()
	if e != nil || ce != nil {
		t.Fatal("current runner artifact", e, ce)
	}
	operation := strings.ReplaceAll(uuid.NewString(), "-", "")
	binding := map[string]any{"format_version": 1, "mode": "backup", "operation_id": operation, "installation_id": r.SourceScopeID, "maintenance_sha256": r.MaintenanceSHA256, "request_sha256": digest(requestRaw), "backup_id": r.BackupID, "source_manifest_sha256": r.SourceManifestSHA256, "control_image_sha256": r.ControlImageSHA256, "runner_sha256": runnerSHA, "operator_configuration_file": operatorPath, "operator_environment_file": "", "request_file": requestPath, "runtime_configuration_root": r.ConfigurationRoot, "backup_mount_path": "/job-backup", "backup_claim_uid": profile.BackupClaimUID, "owner_uid": 65534, "owner_gid": 65534, "manifest_sha256": ""}
	bindingRaw, e := canonical(binding)
	if e != nil {
		t.Fatal(e)
	}
	f.write("/fixture/job-binding.json", bindingRaw)
	f.write("/fixture/job-initialization-ready.json", []byte("{\"status\":\"public_binding_ready_no_backup_effect\"}\n"))
	// Parent launches a distinct root initializer mounting only the dedicated
	// claim/current runner/public binding, with no source config, DSN or stores.
	for {
		if f.ctx.Err() != nil {
			t.Fatal("separate root initialization barrier", f.ctx.Err())
		}
		if _, e := os.Lstat("/fixture/job-initialized.json"); e == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	var init map[string]json.RawMessage
	if _, e := readPrivateDocument("/job-backup/private/.initialization.json", 4096, &init); e != nil || len(init) != 7 {
		t.Fatal("actual dedicated private receipt", e)
	}
	if _, e := configurationSnapshot(f.operator, f.runtime, operatorPath, r); e != nil {
		t.Fatal("real native configuration closure remains enabled", e)
	}
	f.releaseLease()
	invoke := func(command string, b map[string]any) ([]byte, error) {
		t.Helper()
		raw, e := canonical(b)
		if e != nil {
			t.Fatal(e)
		}
		cmd := exec.CommandContext(f.ctx, runner, command, "--binding-json", string(raw))
		var stdout limitedBuffer
		stdout.maximum = 64 << 10
		var stderr limitedBuffer
		stderr.maximum = 64 << 10
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		e = cmd.Run()
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatal("actual native runtime did not join")
		}
		if bytes.Contains(stderr.Bytes(), []byte("postgresql://")) {
			t.Fatal("raw credential protocol disclosed")
		}
		return append([]byte(nil), stdout.Bytes()...), e
	}
	response, e := invoke("backup-postgres", binding)
	if e != nil {
		t.Fatal("actual fixed native backup runtime", e)
	}
	var proof map[string]json.RawMessage
	if release.StrictJSON(response, &proof) != nil || len(proof) != 13 {
		t.Fatal("complete native Job proof shape")
	}
	var proofVersion int
	if json.Unmarshal(proof["format_version"], &proofVersion) != nil || proofVersion != 1 {
		t.Fatal("native Job proof version")
	}
	text := func(name string) string {
		var v string
		if json.Unmarshal(proof[name], &v) != nil {
			t.Fatal("typed public proof", name)
		}
		return v
	}
	for name, want := range map[string]string{"mode": "backup", "operation_id": operation, "installation_id": r.SourceScopeID, "maintenance_sha256": r.MaintenanceSHA256, "request_sha256": digest(requestRaw), "backup_id": r.BackupID, "source_manifest_sha256": r.SourceManifestSHA256} {
		if text(name) != want {
			t.Fatal("original native binding proof mismatch", name)
		}
	}
	manifest := text("manifest_sha256")
	for _, name := range []string{"manifest_sha256", "archive_sha256", "archive_directory_sha256", "native_readback_sha256", "native_completion_sha256"} {
		if !release.ValidSHA256(text(name)) {
			t.Fatal("actual proof digest", name)
		}
	}
	var complete Completion
	completeRaw, e := readPrivateDocument(filepath.Join(r.BackupRoot, completeName), r.Policy.MaximumManifestBytes, &complete)
	if e != nil || complete.ManifestSHA256 != manifest || digest(completeRaw) != text("native_completion_sha256") {
		t.Fatal("independent actual native completion", e)
	}
	if _, e = Verify(f.ctx, r, manifest, true); e != nil {
		t.Fatal("independent original native complete readback", e)
	}
	message, e := os.ReadFile("/dev/termination-log")
	if e != nil || !bytes.Equal(message, response) {
		t.Fatal("actual fixed structured termination proof", e)
	}
	f.write("/fixture/job-backup-proof.json", response)
	// Do not reset this native caller's fixed termination file or repeat backup.
	// A separate container/command owns future read-only verification/recovery.
	success := map[string]any{"status": "actual_fixed_runtime_native_backup_and_independent_readback_passed", "scope": r.SourceScopeID, "backup_id": r.BackupID, "manifest_sha256": manifest, "proof_sha256": digest(response), "parent_image_with_current_pinned_control_and_runner_overlays": true, "actual_kubernetes_csi_or_current_image_qualification": false}
	if packaged {
		delete(success, "parent_image_with_current_pinned_control_and_runner_overlays")
		delete(success, "actual_kubernetes_csi_or_current_image_qualification")
		success["current_packaged_image_control_runner_clients"] = true
		success["client_distribution"] = profile.ClientDistribution
		success["actual_kubernetes_or_csi_qualification"] = false
	}
	f.write("/fixture/job-runtime-success.json", canonicalMap(t, success))
}

func canonicalMap(t *testing.T, v map[string]any) []byte {
	t.Helper()
	raw, e := canonical(v)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
