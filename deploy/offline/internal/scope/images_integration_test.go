//go:build offlineintegration

package scope

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// The child exits after the actual Engine load, before the product writes its terminal event.
// This controlled process-loss boundary is test-only; all Docker and journal operations are real.
type exitingEngine struct{ *engine.Client }

type exitingStorageEngine struct{ *engine.Client }

func (c exitingStorageEngine) PutConfiguration(ctx context.Context, owner engine.Ownership, image string, uid, gid int, files []engine.ConfigurationFile) error {
	if e := c.Client.PutConfiguration(ctx, owner, image, uid, gid, files); e != nil {
		return e
	}
	os.Exit(96)
	return nil
}

type exitingRuntimeOperator struct{ *compose.Client }

func (c exitingRuntimeOperator) CreateRuntime(ctx context.Context, reader compose.RuntimeReader, scope compose.RenderContext, model compose.Model) (compose.RuntimeStatus, error) {
	status, e := c.Client.CreateRuntime(ctx, reader, scope, model)
	if e != nil {
		return status, e
	}
	os.Exit(95)
	return status, nil
}
func TestActualScopedUnfinishedRuntimeHelper(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_UNFINISHED_RUNTIME_HELPER") != "true" {
		return
	}
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_UNFINISHED_IMPORT_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || c.Engine == nil {
		t.Fatal(e)
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, e = createRuntime(ctx, c, client, exitingRuntimeOperator{worker}, false); e != nil {
		t.Fatal(e)
	}
	t.Fatal("runtime helper did not exit")
}
func TestActualScopedUnfinishedStorageHelper(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_UNFINISHED_STORAGE_HELPER") != "true" {
		return
	}
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_UNFINISHED_IMPORT_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || c.Engine == nil {
		t.Fatal("protected fixture config", e)
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, e = initializeStorage(ctx, c, exitingStorageEngine{client}, false); e != nil {
		t.Fatal(e)
	}
	t.Fatal("storage helper returned without controlled exit")
}

func (c exitingEngine) LoadTagless(ctx context.Context, r io.Reader) error {
	if e := c.Client.LoadTagless(ctx, r); e != nil {
		return e
	}
	os.Exit(97)
	return nil
}
func TestActualScopedUnfinishedImportHelper(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_UNFINISHED_IMPORT_HELPER") != "true" {
		return
	}
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_UNFINISHED_IMPORT_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || c.Engine == nil {
		t.Fatal("protected fixture config", e)
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, e = images(ctx, c, exitingEngine{client}, false); e != nil {
		t.Fatal(e)
	}
	t.Fatal("helper returned without controlled exit")
}
func TestActualScopedCLIImageImportCrashReconcileAndReuse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	archive := os.Getenv("PROJECT_ONE_TEST_IMAGE_ARCHIVE")
	raw, e := os.ReadFile(archive)
	if e != nil || len(raw) > 32<<20 {
		t.Fatal("bounded retained actual OCI fixture required", e)
	}
	if wire.Digest(raw) != os.Getenv("PROJECT_ONE_TEST_IMAGE_ARCHIVE_SHA256") {
		t.Fatal("wrong real fixture")
	}
	a := wire.Asset{ID: "actual-synthetic-image", Path: "images/actual.tar", Kind: "image_archive", Size: int64(len(raw)), SHA256: wire.Digest(raw), ImageID: os.Getenv("PROJECT_ONE_TEST_IMAGE_MANIFEST"), ImageConfigDigest: os.Getenv("PROJECT_ONE_TEST_IMAGE_CONFIG"), Architecture: "amd64", Licenses: []string{"license"}}
	c := fixtureWithAssets(t, []wire.Asset{a}, [][]byte{raw})
	c.Engine = &configuration.Engine{SocketFile: os.Getenv("PROJECT_ONE_TEST_ENGINE_SOCKET_FILE"), Profile: "local-macos-amd64-emulation", DialSeconds: 2, RequestSeconds: 5, LoadSeconds: 30, ResponseBytes: 1048576, MaximumExpandedImageBytes: 64 << 20}
	c.Engine.Utility = &wire.UtilityResources{MemoryBytes: 24 << 20, MilliCPU: 50, PIDs: 12}
	c.Compose = &configuration.Compose{DockerBinary: os.Getenv("PROJECT_ONE_TEST_DOCKER_BINARY"), PluginDirectory: os.Getenv("PROJECT_ONE_TEST_COMPOSE_PLUGIN_DIRECTORY"), CommandSeconds: 7, OutputBytes: 8192, ModelFile: filepath.Join(filepath.Dir(c.ScopeRoot), "compose-model.json")}
	c.Compose.InputsFile = filepath.Join(filepath.Dir(c.ScopeRoot), "storage-inputs.json")
	model := compose.Model{Services: map[string]compose.Service{"control": {Image: a.ImageID, User: "10033:10034", MemoryBytes: 64 << 20, MilliCPU: 250, PIDs: 32, Restart: "no", ReadOnly: true, Command: []string{"$UNSET", "${UNSET}", "$$"}, Tmpfs: []compose.Tmpfs{{Target: "/tmp", SizeBytes: 1048576, UID: 10033, GID: 10034}}, Mounts: []compose.Mount{{Type: "volume", Source: "control-config", Target: "/run/projectone", ReadOnly: true}}}}, Volumes: []string{"control-config", "operator-config"}}
	modelRaw := encode(model)
	put(t, c.Compose.ModelFile, modelRaw)
	runtimeSource := filepath.Join(filepath.Dir(c.ScopeRoot), "runtime-source.yaml")
	operatorSource := filepath.Join(filepath.Dir(c.ScopeRoot), "operator-source.yaml")
	put(t, runtimeSource, []byte("Explicit runtime-only synthetic configuration\n"))
	put(t, operatorSource, []byte("Explicit privileged-only synthetic configuration\n"))
	put(t, c.Compose.InputsFile, encode(SourceCatalog{FormatVersion: 1, StorageProfile: namedVolumeStorage, ImageAssetID: a.ID, Roles: []SourceRole{{Key: "control-config", Files: []SourceFile{{Name: "control.yaml", SourceFile: runtimeSource}}}, {Key: "operator-config", Files: []SourceFile{{Name: "operator.yaml", SourceFile: operatorSource}}}}}))
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	observation, e := client.Preflight(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, found, e := client.Inspect(ctx, a.ImageID); e != nil || found {
		t.Fatal("exact disposable image must initially be absent", e, found)
	}
	contextName := os.Getenv("PROJECT_ONE_TEST_DOCKER_CONTEXT")
	if contextName == "" {
		t.Fatal("explicit test Docker context required")
	}
	docker := func(args ...string) []byte {
		commandContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(commandContext, "docker", append([]string{"--context", contextName}, args...)...)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("Docker test command failed: %v", e)
		}
		return out
	}
	tags := func() map[string]string {
		out := docker("image", "ls", "--all", "--no-trunc", "--format", `{{.Repository}}:{{.Tag}} {{.ID}}`)
		result := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && !strings.Contains(fields[0], "<none>") {
				result[fields[0]] = fields[1]
			}
		}
		return result
	}
	before, _ := json.Marshal(tags())
	cleaned := false
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		image, found, e := client.Inspect(ctx, a.ImageID)
		if e != nil {
			t.Error("cleanup inspection failed", e)
			return
		}
		if !found {
			cleaned = true
			return
		}
		if len(image.RepoTags) > 0 {
			t.Error("tagged synthetic result preserved")
			return
		}
		ids := strings.Fields(string(docker("container", "ls", "--all", "--quiet")))
		if len(ids) > 0 {
			images := strings.Fields(string(docker(append([]string{"container", "inspect", "--format", "{{.Image}}"}, ids...)...)))
			for _, id := range images {
				if id == a.ImageID || id == a.ImageConfigDigest {
					t.Error("synthetic image has a consumer; preserved")
					return
				}
			}
		}
		docker("image", "rm", "--no-prune", a.ImageID)
		if _, found, e = client.Inspect(ctx, a.ImageID); e != nil || found {
			t.Error("cleanup not confirmed", e)
			return
		}
		cleaned = true
	}
	t.Cleanup(func() {
		if !cleaned {
			cleanup()
		}
	})
	configFile := filepath.Join(filepath.Dir(c.ScopeRoot), "operator-engine.json")
	put(t, configFile, encode(c))
	originalScopeRoot := c.ScopeRoot
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		raw, e := os.ReadFile(filepath.Join(originalScopeRoot, journalName))
		if e != nil {
			t.Error("failed test journal unavailable", e)
			return
		}
		file, e := os.OpenFile(os.Getenv("PROJECT_ONE_TEST_SCOPE_ENGINE_EVIDENCE")+"-failed-journal.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Error("failed test evidence unavailable", e)
			return
		}
		defer file.Close()
		if _, e = file.Write(encodeJournal(raw)); e != nil || file.Sync() != nil {
			t.Error("failed test evidence not durable", e)
		}
	})
	binary := os.Getenv("PROJECT_ONE_TEST_OPERATOR_BINARY")
	if binary == "" {
		t.Fatal("actual current operator binary required")
	}
	cli := func(command string, success bool) []byte {
		out, e := exec.CommandContext(ctx, binary, command, "--config", configFile).CombinedOutput()
		if success && e != nil || !success && e == nil {
			t.Fatalf("current CLI %s: %v %s", command, e, out)
		}
		return out
	}
	cli("init-scope", true)
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	child := exec.CommandContext(ctx, self, "-test.run=^TestActualScopedUnfinishedImportHelper$", "-test.count=1")
	child.Env = append(os.Environ(), "PROJECT_ONE_TEST_UNFINISHED_IMPORT_HELPER=true", "PROJECT_ONE_TEST_UNFINISHED_IMPORT_CONFIG="+configFile)
	out, e := child.CombinedOutput()
	var exit *exec.ExitError
	if !errorAsExit(e, &exit) || exit.ExitCode() != 97 {
		t.Fatalf("controlled child outcome %v %s", e, out)
	}
	var pending Status
	if json.Unmarshal(cli("status", true), &pending) != nil || pending.ImagesPending != 1 || pending.Events != 5 {
		t.Fatal("real process loss not persisted", pending)
	}
	journalBefore, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Contains(cli("import-images", false), []byte("OFFLINE_SCOPE_IMAGE_RECONCILIATION_REQUIRED")) {
		t.Fatal("uncertain resend not refused")
	}
	journalAfter, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(journalBefore, journalAfter) {
		t.Fatal("refusal changed journal")
	}
	var reconciled Status
	if json.Unmarshal(cli("reconcile-images", true), &reconciled) != nil || reconciled.ImagesRecorded != 1 || reconciled.ImagesPending != 0 || reconciled.Events != 6 {
		t.Fatal("real outcome not reconciled", reconciled)
	}
	committed, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	var plan ComposePlan
	planOutput := cli("plan-compose", true)
	if json.Unmarshal(planOutput, &plan) != nil || plan.ScopeID != reconciled.ScopeID || plan.ModelSHA256 != wire.Digest(modelRaw) || plan.Rendered.Services != 1 || !wire.ValidSHA256(plan.Rendered.DocumentSHA256) || bytes.Contains(planOutput, []byte(`"document":`)) {
		t.Fatal("actual scope plan invalid", plan)
	}
	unsafe := model.Services["control"]
	unsafe.Environment = map[string]string{"API_KEY": "explicit-test-secret"}
	model.Services["control"] = unsafe
	put(t, c.Compose.ModelFile, encode(model))
	if !bytes.Contains(cli("plan-compose", false), []byte("OFFLINE_COMPOSE_CONSTRAINED_MODEL_INVALID")) {
		t.Fatal("inline credential model accepted")
	}
	put(t, c.Compose.ModelFile, modelRaw)
	model, e = compose.DecodeModel(modelRaw, c.Verification.MaximumMetadataBytes)
	if e != nil {
		t.Fatal(e)
	}
	var inputSnapshot StorageInputsStatus
	if json.Unmarshal(cli("prepare-storage-inputs", true), &inputSnapshot) != nil || inputSnapshot.Roles != 2 || inputSnapshot.ScopeID != reconciled.ScopeID {
		t.Fatal("actual input snapshot failed", inputSnapshot)
	}
	committed, _ = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	for _, source := range []string{runtimeSource, operatorSource, c.Compose.InputsFile, c.Compose.ModelFile} {
		if e = os.Remove(source); e != nil {
			t.Fatal(e)
		}
	}
	cli("prepare-storage-inputs", true)
	cli("plan-compose", true)
	cli("import-images", true)
	cli("status", true)
	cli("reconcile-images", true)
	unchanged, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(committed, unchanged) {
		t.Fatal("successful reuse/reconcile changed journal")
	}
	storageConfig := c
	volumesCleaned := false
	cleanVolumes := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		l, e := open(storageConfig, false)
		if e != nil {
			t.Error("test volume ownership journal unavailable", e)
			return
		}
		state := volumeState(l.events)
		l.close()
		for _, record := range state.roles {
			owner := engine.Ownership{ScopeID: inputSnapshot.ScopeID, OperationID: record.ResourceOperationID, Role: record.Key}
			if _, found, e := client.InspectUtility(ctx, owner, record.ImageID, record.UID, record.GID); e != nil {
				t.Error("test utility identity changed; preserved", e)
				return
			} else if found {
				if e = client.RemoveUtility(ctx, owner, record.ImageID, record.UID, record.GID); e != nil {
					t.Error("test utility cleanup failed", e)
					return
				}
			}
			if _, found, e := client.InspectVolume(ctx, owner); e != nil {
				t.Error("test volume identity changed; preserved", e)
				return
			} else if !found {
				continue
			}
			if len(strings.Fields(string(docker("container", "ls", "--all", "--quiet", "--filter", "volume="+owner.VolumeName())))) != 0 {
				t.Error("test volume has consumers; preserved")
				return
			}
			docker("volume", "rm", owner.VolumeName())
			if _, found, e := client.InspectVolume(ctx, owner); e != nil || found {
				t.Error("test volume removal unconfirmed", e)
				return
			}
		}
		volumesCleaned = true
	}
	t.Cleanup(func() {
		if !volumesCleaned {
			cleanVolumes()
		}
	})
	storageChild := exec.CommandContext(ctx, self, "-test.run=^TestActualScopedUnfinishedStorageHelper$", "-test.count=1")
	storageChild.Env = append(os.Environ(), "PROJECT_ONE_TEST_UNFINISHED_STORAGE_HELPER=true", "PROJECT_ONE_TEST_UNFINISHED_IMPORT_CONFIG="+configFile)
	out, e = storageChild.CombinedOutput()
	if !errorAsExit(e, &exit) || exit.ExitCode() != 96 {
		t.Fatalf("controlled storage child outcome %v %s", e, out)
	}
	var storagePending Status
	if json.Unmarshal(cli("status", true), &storagePending) != nil || storagePending.VolumesPending != 1 || storagePending.VolumesPrepared != 0 {
		t.Fatal("storage process loss not recorded", storagePending)
	}
	interruptedJournal, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Contains(cli("initialize-storage", false), []byte("OFFLINE_SCOPE_STORAGE_RECONCILIATION_REQUIRED")) {
		t.Fatal("uncertain storage write resend accepted")
	}
	refusedJournal, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(interruptedJournal, refusedJournal) {
		t.Fatal("storage refusal changed journal")
	}
	cli("reconcile-storage", true)
	var prepared StorageStatus
	if json.Unmarshal(cli("initialize-storage", true), &prepared) != nil || prepared.RolesPrepared != 2 || prepared.RolesPending != 0 {
		t.Fatal("actual role preparation failed", prepared)
	}
	committed, _ = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	cli("initialize-storage", true)
	cli("reconcile-storage", true)
	var storageStatus Status
	if json.Unmarshal(cli("status", true), &storageStatus) != nil || storageStatus.Status != "isolated_scope_storage_payloads_prepared" || storageStatus.VolumesPrepared != 2 {
		t.Fatal("actual prepared status", storageStatus)
	}
	unchanged, _ = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(committed, unchanged) {
		t.Fatal("prepared storage repeated effects")
	}
	// This low-level interface test has its own durable synthetic intent. The
	// product scope lifecycle journal and public mutation CLI are still pending.
	runtimeScope := compose.RenderContext{ScopeID: inputSnapshot.ScopeID, ScopeRoot: c.ScopeRoot, AllowedImages: map[string]bool{a.ImageID: true}}
	composeClient, e := compose.New(c)
	if e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(filepath.Dir(c.ScopeRoot), "synthetic-runtime-intent.json"), encode(map[string]any{"scope_id": inputSnapshot.ScopeID, "engine": observation, "model_sha256": wire.Digest(modelRaw), "action": "create_only_actual_interface_test"}))
	runtimeCleaned := false
	cleanRuntime := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		state, e := composeClient.ObserveRuntime(ctx, client, runtimeScope, model)
		if e != nil {
			t.Error("runtime identity changed; preserved", e)
			return
		}
		for _, service := range state.Services {
			if service.State != "created" {
				t.Error("unexpected started test process; preserved")
				return
			}
			docker("container", "rm", service.ID)
		}
		state, e = composeClient.ObserveRuntime(ctx, client, runtimeScope, model)
		if e != nil || len(state.Services) != 0 {
			t.Error("test container removal unconfirmed", e)
			return
		}
		if state.NetworkID != "" {
			docker("network", "rm", state.NetworkID)
		}
		state, e = composeClient.ObserveRuntime(ctx, client, runtimeScope, model)
		if e != nil || state.Status != "observed_absent_compose_resources" {
			t.Error("test runtime removal unconfirmed", e)
			return
		}
		runtimeCleaned = true
	}
	t.Cleanup(func() {
		if !runtimeCleaned {
			cleanRuntime()
		}
	})
	runtimeChild := exec.CommandContext(ctx, self, "-test.run=^TestActualScopedUnfinishedRuntimeHelper$", "-test.count=1")
	runtimeChild.Env = append(os.Environ(), "PROJECT_ONE_TEST_UNFINISHED_RUNTIME_HELPER=true", "PROJECT_ONE_TEST_UNFINISHED_IMPORT_CONFIG="+configFile)
	out, e = runtimeChild.CombinedOutput()
	if !errorAsExit(e, &exit) || exit.ExitCode() != 95 {
		t.Fatalf("controlled runtime child outcome %v %s", e, out)
	}
	pendingRuntimeJournal, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Contains(cli("create-runtime", false), []byte("OFFLINE_SCOPE_RUNTIME_RECONCILIATION_REQUIRED")) {
		t.Fatal("uncertain runtime create resent")
	}
	refusedRuntimeJournal, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(pendingRuntimeJournal, refusedRuntimeJournal) {
		t.Fatal("runtime refusal changed journal")
	}
	var runtimeResult RuntimeResult
	if json.Unmarshal(cli("reconcile-runtime", true), &runtimeResult) != nil || runtimeResult.Pending || runtimeResult.Resources.Status != "observed_complete_compose_resources" {
		t.Fatal("actual runtime reconciliation", runtimeResult)
	}
	created := runtimeResult.Resources
	committed, _ = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	cli("create-runtime", true)
	cli("reconcile-runtime", true)
	var runtimeStatus Status
	if json.Unmarshal(cli("status", true), &runtimeStatus) != nil || runtimeStatus.Status != "isolated_scope_runtime_resources_recorded" {
		t.Fatal(runtimeStatus)
	}
	unchanged, _ = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(committed, unchanged) {
		t.Fatal("completed runtime recreation/reconcile changed journal")
	}
	cleanRuntime()
	if !runtimeCleaned {
		t.Fatal("owned runtime retained")
	}
	cleanVolumes()
	if !volumesCleaned {
		t.Fatal("owned synthetic volumes retained")
	}
	image, found, e := client.Inspect(ctx, a.ImageID)
	if e != nil || !found || len(image.RepoTags) > 0 {
		t.Fatal("real imported image mismatch", e)
	}
	// A distinct scope must reuse the exact existing immutable image without a load intent.
	c.ScopeRoot = filepath.Join(filepath.Dir(c.ScopeRoot), "reuse-scope")
	put(t, configFile, encode(c))
	cli("init-scope", true)
	cli("import-images", true)
	reuseJournal, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Contains(reuseJournal, []byte(`"mode":"reuse"`)) || bytes.Contains(reuseJournal, []byte(`"mode":"load"`)) {
		t.Fatal("existing content not reused")
	}
	after, _ := json.Marshal(tags())
	if !bytes.Equal(before, after) {
		t.Fatal("named image mappings changed")
	}
	cleanup()
	if !cleaned {
		t.Fatal("owned image retained")
	}
	finalTags, _ := json.Marshal(tags())
	if !bytes.Equal(before, finalTags) {
		t.Fatal("cleanup changed named images")
	}
	evidence := map[string]any{"status": "passed_actual_CLI_storage_process_loss_reconcile_and_preparation", "qualification": engine.LocalQualification, "engine": observation, "pending": pending, "reconciled": reconciled, "compose_plan": plan, "input_snapshot": inputSnapshot, "storage_pending": storagePending, "storage_prepared": prepared, "storage_status": storageStatus, "runtime_create": created, "controlled_runtime_exit": 95, "runtime_result": runtimeResult, "runtime_cleaned": runtimeCleaned, "exact_test_volumes_cleaned": volumesCleaned, "original_sources_deleted_and_snapshot_reused": true, "inline_credential_model_refused": true, "controlled_child_exit": 97, "controlled_storage_exit": 96, "original_scope_journal": json.RawMessage(encodeJournal(committed)), "reuse_scope_journal": json.RawMessage(encodeJournal(reuseJournal)), "named_tags_before_sha256": wire.Digest(before), "named_tags_after_sha256": wire.Digest(after), "removed_exact_new_untagged_image": a.ImageID, "only_never_started_utility_containers_created": true, "production_database_mutated": false}
	file, e := os.OpenFile(os.Getenv("PROJECT_ONE_TEST_SCOPE_ENGINE_EVIDENCE"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if json.NewEncoder(file).Encode(evidence) != nil || file.Sync() != nil {
		t.Fatal("evidence write failed")
	}
}
func errorAsExit(e error, out **exec.ExitError) bool {
	v, ok := e.(*exec.ExitError)
	*out = v
	return ok
}
func encodeJournal(raw []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	return append(append([]byte("["), bytes.Join(lines, []byte(","))...), ']')
}
