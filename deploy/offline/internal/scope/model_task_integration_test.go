//go:build offlineintegration

package scope

import (
	"context"
	"encoding/json"
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

type lostModelTaskResponse struct{ *engine.Client }

func (c lostModelTaskResponse) CreateModelTask(ctx context.Context, t engine.ModelStoreTask) (engine.RuntimeContainer, error) {
	r, e := c.Client.CreateModelTask(ctx, t)
	if e == nil {
		os.Exit(94)
	}
	return r, e
}
func (c lostModelTaskResponse) StartModelTask(ctx context.Context, t engine.ModelStoreTask, id string, reserve int) (engine.ResourceAdmission, error) {
	r, e := c.Client.StartModelTask(ctx, t, id, reserve)
	if e == nil {
		os.Exit(93)
	}
	return r, e
}
func (c lostModelTaskResponse) RemoveModelTask(ctx context.Context, t engine.ModelStoreTask, id string) error {
	e := c.Client.RemoveModelTask(ctx, t, id)
	if e == nil {
		os.Exit(92)
	}
	return e
}
func TestActualScopedModelTaskUnfinishedChild(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_SCOPED_MODEL_TASK_CHILD") != "1" {
		return
	}
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_SCOPED_MODEL_TASK_CHILD_CONFIG"))
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	phase := os.Getenv("PROJECT_ONE_TEST_SCOPED_MODEL_TASK_LOSS_PHASE")
	if phase == "execute" || phase == "removed" {
		_, e = modelTaskAction(ctx, c, lostModelTaskResponse{client}, phase, false)
	} else {
		_, e = createModelTask(ctx, c, lostModelTaskResponse{client}, false)
	}
	if e != nil {
		t.Fatal(e)
	}
	t.Fatal("expected controlled real task process loss")
}
func TestActualSignedScopedModelTaskCLIProcessLossAndReuse(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 140*time.Second)
	defer cancel()
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_MODEL_TASK_FIXTURE"))
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Status, Archive string
		SHA             string `json:"archive_sha256"`
		Image           string `json:"image_manifest"`
		Config          string `json:"image_config"`
		UID, GID        int
	}
	if wire.UniqueJSON(raw, &f) != nil || f.Status != "manufactured_real_control_command_fixture_not_release" {
		t.Fatal("explicit real-control fixture required")
	}
	imageBytes, e := os.ReadFile(f.Archive)
	if e != nil || len(imageBytes) > 64<<20 || wire.Digest(imageBytes) != f.SHA {
		t.Fatal("fixture bytes changed", e)
	}
	binary := os.Getenv("PROJECT_ONE_TEST_SCOPED_MODEL_TASK_OPERATOR")
	binaryBytes, e := os.ReadFile(binary)
	if e != nil || wire.Digest(binaryBytes) != os.Getenv("PROJECT_ONE_TEST_SCOPED_MODEL_TASK_OPERATOR_SHA256") {
		t.Fatal("exact current operator required")
	}
	raw, e = os.ReadFile(os.Getenv("PROJECT_ONE_TEST_MODEL_TASK_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	profile, e := configuration.Load(raw, nil)
	if e != nil || profile.Engine == nil || profile.Engine.Utility == nil || profile.Compose == nil {
		t.Fatal(e)
	}
	asset := wire.Asset{ID: "real-control-command", Path: "images/control-command.tar", Kind: "image_archive", Size: int64(len(imageBytes)), SHA256: f.SHA, ImageID: f.Image, ImageConfigDigest: f.Config, Architecture: "amd64", Licenses: []string{"license"}}
	c := fixtureWithAssets(t, []wire.Asset{asset}, [][]byte{imageBytes})
	c.Engine = profile.Engine
	c.Compose = profile.Compose
	parent := filepath.Dir(c.ScopeRoot)
	c.Compose.ModelFile = filepath.Join(parent, "model.json")
	c.Compose.InputsFile = filepath.Join(parent, "sources.json")
	model := compose.Model{Services: map[string]compose.Service{"control": {Image: f.Image, User: "10033:10034", MemoryBytes: 64 << 20, MilliCPU: 100, PIDs: 16, Restart: "no", ReadOnly: true, Mounts: []compose.Mount{{Type: "volume", Source: "models", Target: "/var/lib/projectone/models", ReadOnly: false}}}}, Volumes: []string{"models"}}
	put(t, c.Compose.ModelFile, encode(model))
	put(t, c.Compose.InputsFile, encode(SourceCatalog{FormatVersion: 1, StorageProfile: namedVolumeStorage, ImageAssetID: asset.ID, Roles: []SourceRole{{Key: "models"}}}))
	configurationFile := filepath.Join(parent, "operator.json")
	put(t, configurationFile, encode(c))
	output := os.Getenv("PROJECT_ONE_TEST_SCOPED_MODEL_TASK_EVIDENCE")
	cli := func(command string, success bool) []byte {
		out, e := exec.CommandContext(ctx, binary, command, "--config", configurationFile).CombinedOutput()
		if (e == nil) != success {
			t.Fatalf("current CLI %s %v %s", command, e, out)
		}
		return out
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	if _, found, e := client.Inspect(ctx, f.Image); e != nil || found {
		t.Fatal("exact disposable fixture image must initially be absent", e)
	}
	cli("init-scope", true)
	cleaned := false
	cleanup := func() error {
		cleanupContext, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		l, e := open(c, false)
		if e != nil {
			return e
		}
		events := append([]Event(nil), l.events...)
		state := modelTaskState(events)
		volumes := volumeState(events)
		scopeID := l.marker.ScopeID
		l.close()
		if e = os.WriteFile(output+".journal.json", encodeJournal(appendJournal(events)), 0600); e != nil {
			return e
		}
		if state.record != nil {
			value, found, e := client.InspectModelTask(cleanupContext, state.record.Task)
			if e != nil {
				return e
			}
			if found {
				if !neverStartedModelTask(value) || state.record.ID != "" && state.record.ID != value.ID {
					return failure("SCOPED_MODEL_TASK_CHANGED_RESOURCE_PRESERVED")
				}
				if e = client.RemoveModelTask(cleanupContext, state.record.Task, value.ID); e != nil {
					return e
				}
			}
		}
		private := filepath.Join(parent, "cleanup-client")
		if e = os.Mkdir(private, 0700); e != nil {
			return e
		}
		put(t, filepath.Join(private, "config.json"), []byte("{}\n"))
		docker := func(args ...string) error {
			command := exec.CommandContext(cleanupContext, c.Compose.DockerBinary, append([]string{"--config", private, "--host", "unix://" + c.Engine.SocketFile}, args...)...)
			command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
			if out, e := command.CombinedOutput(); e != nil {
				return failure("SCOPED_MODEL_TASK_EXACT_CLEANUP_FAILED_" + wire.Digest(out)[:12])
			}
			return nil
		}
		for _, v := range volumes.roles {
			o := engine.Ownership{ScopeID: scopeID, OperationID: v.ResourceOperationID, Role: v.Key}
			if _, found, e := client.InspectVolume(cleanupContext, o); e != nil || !found {
				return failure("SCOPED_MODEL_TASK_VOLUME_CHANGED")
			}
			if e = client.VerifyVolumeConsumers(cleanupContext, o, nil); e != nil {
				return e
			}
			if e = docker("volume", "rm", o.VolumeName()); e != nil {
				return e
			}
		}
		image, found, e := client.Inspect(cleanupContext, f.Image)
		if e != nil {
			return e
		}
		if found {
			if len(image.RepoTags) > 0 {
				return failure("SCOPED_MODEL_TASK_NAMED_IMAGE_PRESERVED")
			}
			command := exec.CommandContext(cleanupContext, c.Compose.DockerBinary, "--config", private, "--host", "unix://"+c.Engine.SocketFile, "container", "ls", "--all", "--quiet", "--filter", "ancestor="+f.Image)
			command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
			consumers, e := command.Output()
			if e != nil || len(strings.TrimSpace(string(consumers))) > 0 {
				return failure("SCOPED_MODEL_TASK_IMAGE_CONSUMER_PRESERVED")
			}
			if e = docker("image", "rm", "--no-prune", f.Image); e != nil {
				return e
			}
		}
		cleaned = true
		return nil
	}
	t.Cleanup(func() {
		if !cleaned {
			if e := cleanup(); e != nil {
				t.Error(e)
			}
		}
	})
	for _, command := range []string{"import-images", "prepare-storage-inputs", "initialize-storage"} {
		cli(command, true)
	}
	lost := func(phase string, expected int) {
		child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestActualScopedModelTaskUnfinishedChild$", "-test.timeout=50s")
		child.Env = append(os.Environ(), "PROJECT_ONE_TEST_SCOPED_MODEL_TASK_CHILD=1", "PROJECT_ONE_TEST_SCOPED_MODEL_TASK_CHILD_CONFIG="+configurationFile, "PROJECT_ONE_TEST_SCOPED_MODEL_TASK_LOSS_PHASE="+phase)
		childOutput, e := child.CombinedOutput()
		exit, ok := e.(*exec.ExitError)
		if !ok || exit.ExitCode() != expected {
			t.Fatalf("actual child loss%d: %v %s", expected, e, childOutput)
		}
	}
	lost("create", 94)
	cli("create-model-store-task", false)
	var result ModelTaskResult
	if json.Unmarshal(cli("reconcile-model-store-task", true), &result) != nil || result.Pending || !utilityIDPattern.MatchString(result.ID) {
		t.Fatal("actual readonly creation reconciliation")
	}
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	var repeated ModelTaskResult
	if json.Unmarshal(cli("create-model-store-task", true), &repeated) != nil || repeated.ID != result.ID {
		t.Fatal("completed repeat changed exact task")
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if string(before) != string(after) {
		t.Fatal("completed repeat changed journal")
	}
	var status Status
	if json.Unmarshal(cli("status", true), &status) != nil || status.ModelTaskPending || status.ModelTaskID != result.ID {
		t.Fatal("task status")
	}
	lost("execute", 93)
	cli("start-model-store-task", false)
	l, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	creation := *modelTaskState(l.events).record
	l.close()
	for deadline := time.Now().Add(10 * time.Second); ; {
		value, found, e := client.InspectModelTask(ctx, creation.Task)
		if e != nil || !found || value.ID != creation.ID {
			t.Fatal("actual initializer observation", e)
		}
		if value.State.Status == "exited" {
			if value.State.ExitCode != 0 || value.State.OOMKilled {
				t.Fatal("actual initializer failed", value.State)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual initializer exit deadline")
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	var execution ModelTaskActionResult
	if json.Unmarshal(cli("reconcile-model-store-action", true), &execution) != nil || execution.Pending || !execution.Initialized || execution.Removed {
		t.Fatal("actual execution result reconciliation")
	}
	before, _ = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	cli("start-model-store-task", true)
	after, _ = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if string(before) != string(after) {
		t.Fatal("completed initializer was replayed")
	}
	lost("removed", 92)
	cli("remove-model-store-task", false)
	var removal ModelTaskActionResult
	if json.Unmarshal(cli("reconcile-model-store-action", true), &removal) != nil || removal.Pending || !removal.Initialized || !removal.Removed {
		t.Fatal("actual removal reconciliation")
	}
	before, _ = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	cli("remove-model-store-task", true)
	after, _ = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if string(before) != string(after) {
		t.Fatal("completed removal was replayed")
	}
	if json.Unmarshal(cli("status", true), &status) != nil || status.ModelTaskPending || !status.ModelDirectoryInitialized || !status.ModelTaskRemoved {
		t.Fatal("actual initialized/removed status")
	}
	if e = cleanup(); e != nil {
		t.Fatal(e)
	}
	put(t, output, encode(map[string]any{"status": "passed_current_signed_scope_task_lifecycle_losses94_93_92_reconcile_and_no_replay", "creation": result, "execution": execution, "removal": removal, "observed_status": status, "child_exits": []int{94, 93, 92}, "container_started": true, "real_initializer_exit_zero_and_empty_private_archive": true, "completed_repeat_journal_unchanged": true, "exact_disposable_objects_cleaned": cleaned, "qualification": "signed_model_directory_initializer_only_not_database_full_installation_or_target"}))
}
