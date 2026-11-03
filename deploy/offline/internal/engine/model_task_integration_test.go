//go:build offlineintegration

package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestActualCurrentControlModelStoreTask(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_MODEL_TASK_FIXTURE"))
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Status, Archive string
		ArchiveSHA      string `json:"archive_sha256"`
		ArchiveBytes    int64  `json:"archive_bytes"`
		Image           string `json:"image_manifest"`
		Config          string `json:"image_config"`
		UID, GID        int
	}
	if wire.UniqueJSON(raw, &fixture) != nil || fixture.Status != "manufactured_real_control_command_fixture_not_release" || fixture.ArchiveBytes > 64<<20 {
		t.Fatal("current control command fixture required")
	}
	raw, e = os.ReadFile(os.Getenv("PROJECT_ONE_TEST_MODEL_TASK_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	settings, e := configuration.Load(raw, nil)
	if e != nil || settings.Engine == nil || settings.Engine.Utility == nil || settings.Compose == nil || settings.Compose.Process == nil {
		t.Fatal("typed utility/process profile required", e)
	}
	c, e := New(*settings.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	engineObservation, e := c.Preflight(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, found, e := c.Inspect(ctx, fixture.Image); e != nil || found {
		t.Fatal("initially absent owned image required", e)
	}
	output := os.Getenv("PROJECT_ONE_TEST_MODEL_TASK_EVIDENCE")
	intent, e := os.OpenFile(output+".intent.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if json.NewEncoder(intent).Encode(map[string]any{"status": "before_any_fixture_effect", "fixture_sha256": wire.Digest([]byte(fixture.ArchiveSHA)), "image": fixture.Image, "qualification": LocalQualification}) != nil || intent.Sync() != nil || intent.Close() != nil {
		t.Fatal("fixture intent failed")
	}
	imageLoaded := false
	var active *ModelStoreTask
	var utilityActive bool
	var taskID string
	var volumeCreated bool
	cleanup := func() error {
		cleanupContext, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if active != nil {
			value, found, e := c.InspectModelTask(cleanupContext, *active)
			if e != nil {
				return e
			}
			if found {
				if taskID != "" && value.ID != taskID {
					return failure("MODEL_TASK_TEST_CHANGED_CONTAINER_PRESERVED")
				}
				if value.State.Running {
					if e = c.StopModelTask(cleanupContext, *active, value.ID, 2); e != nil {
						return e
					}
				}
				if e = c.RemoveModelTask(cleanupContext, *active, value.ID); e != nil {
					return e
				}
			}
			if utilityActive {
				if e = c.RemoveUtility(cleanupContext, active.VolumeOwner, active.Image, active.UID, active.GID); e != nil {
					return e
				}
				utilityActive = false
			}
			if volumeCreated {
				if e = c.noForeignConsumers(cleanupContext, active.VolumeOwner, ""); e != nil {
					return e
				}
				if _, found, e = c.InspectVolume(cleanupContext, active.VolumeOwner); e != nil || !found {
					return failure("MODEL_TASK_TEST_VOLUME_CONTEXT_CHANGED")
				}
				_, status, e := c.request(cleanupContext, http.MethodDelete, "/v1.49/volumes/"+url.PathEscape(active.VolumeOwner.VolumeName()), url.Values{"force": {"0"}}, nil, c.config.RequestSeconds)
				if e != nil || status != 204 {
					return failure("MODEL_TASK_TEST_VOLUME_CLEANUP_FAILED")
				}
				volumeCreated = false
			}
			active = nil
			taskID = ""
		}
		return nil
	}
	imageCleanup := func() error {
		cleanupContext, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if !imageLoaded {
			return nil
		}
		value, found, e := c.Inspect(cleanupContext, fixture.Image)
		if e != nil || !found || len(value.RepoTags) > 0 {
			return failure("MODEL_TASK_TEST_IMAGE_CHANGED")
		}
		raw, status, e := c.request(cleanupContext, http.MethodGet, "/v1.49/containers/json", url.Values{"all": {"1"}}, nil, c.config.RequestSeconds)
		var containers []struct{ ImageID string }
		if e != nil || status != 200 || wire.UniqueJSON(raw, &containers) != nil {
			return failure("MODEL_TASK_TEST_IMAGE_CONSUMER_CHECK_FAILED")
		}
		for _, container := range containers {
			if container.ImageID == fixture.Image || container.ImageID == fixture.Config {
				return failure("MODEL_TASK_TEST_IMAGE_CONSUMER_PRESERVED")
			}
		}
		_, status, e = c.request(cleanupContext, http.MethodDelete, "/v1.49/images/"+url.PathEscape(fixture.Image), url.Values{"force": {"0"}, "noprune": {"1"}}, nil, c.config.RequestSeconds)
		if e != nil || status != 200 {
			return failure("MODEL_TASK_TEST_IMAGE_CLEANUP_FAILED")
		}
		imageLoaded = false
		return nil
	}
	t.Cleanup(func() {
		if e := cleanup(); e != nil {
			t.Error(e)
			return
		}
		if e := imageCleanup(); e != nil {
			t.Error(e)
		}
	})
	inventory := func() string {
		values := map[string][]string{}
		for _, kind := range []string{"containers", "images", "networks", "volumes"} {
			path := "/v1.49/" + kind
			query := url.Values{}
			if kind == "containers" || kind == "images" {
				path += "/json"
				query.Set("all", "1")
			}
			raw, status, e := c.request(ctx, http.MethodGet, path, query, nil, c.config.RequestSeconds)
			if e != nil || status != 200 {
				t.Fatal("inventory", kind, e)
			}
			var objects []struct {
				ID   string `json:"Id"`
				Name string
			}
			if kind == "volumes" {
				var page struct {
					Volumes []struct {
						ID   string `json:"Id"`
						Name string
					}
				}
				if wire.UniqueJSON(raw, &page) != nil {
					t.Fatal("volume inventory")
				}
				objects = page.Volumes
			} else if wire.UniqueJSON(raw, &objects) != nil {
				t.Fatal("object inventory")
			}
			values[kind] = []string{}
			for _, o := range objects {
				id := o.ID
				if kind == "volumes" {
					id = o.Name
				}
				values[kind] = append(values[kind], id)
			}
			sort.Strings(values[kind])
		}
		b, _ := json.Marshal(values)
		return wire.Digest(b)
	}
	before := inventory()
	asset := wire.Asset{ID: "actual-current-control-command", Path: "control-command-image.tar", Kind: "image_archive", Size: fixture.ArchiveBytes, SHA256: fixture.ArchiveSHA, ImageID: fixture.Image, ImageConfigDigest: fixture.Config, Architecture: "amd64", Licenses: []string{"local-test-only-not-distribution"}}
	verification := bundle.Defaults()
	verification.MountRoot = os.Getenv("PROJECT_ONE_TEST_EXTERNAL_ROOT")
	source, e := bundle.OpenImageSource(ctx, verification, fixture.Archive, asset, settings.Engine.MaximumExpandedImageBytes)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { _, e := source.WriteTagless(ctx, writer); writer.CloseWithError(e); done <- e }()
	e = c.LoadTagless(ctx, reader)
	reader.CloseWithError(e)
	produced := <-done
	_, imageLoaded, _ = c.Inspect(context.Background(), fixture.Image)
	if e != nil || produced != nil || !imageLoaded {
		t.Fatal("real fixture import", e, produced)
	}
	receipt := map[string]any{"engine": engineObservation, "qualification": LocalQualification, "scope": "Real current control command, not full release/application/target qualification"}
	for _, name := range []string{"empty", "nonempty"} {
		var random [48]byte
		if _, e = rand.Read(random[:]); e != nil {
			t.Fatal(e)
		}
		scope := hex.EncodeToString(random[:16])
		task := ModelStoreTask{Owner: Ownership{scope, hex.EncodeToString(random[16:32]), ModelTaskRole}, VolumeOwner: Ownership{scope, hex.EncodeToString(random[32:]), "models"}, Image: fixture.Image, UID: fixture.UID, GID: fixture.GID}
		active = &task
		// Persist exact generated scope/operations before creating their objects.
		f, e := os.OpenFile(output+"."+name+".intent.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		if json.NewEncoder(f).Encode(task) != nil || f.Sync() != nil || f.Close() != nil {
			t.Fatal("task intent")
		}
		if _, e = c.CreateVolume(ctx, task.VolumeOwner); e != nil {
			_, volumeCreated, _ = c.InspectVolume(context.Background(), task.VolumeOwner)
			t.Fatal(e)
		}
		volumeCreated = true
		if _, e = c.CreateUtility(ctx, task.VolumeOwner, task.Image, task.UID, task.GID); e != nil {
			_, utilityActive, _ = c.InspectUtility(context.Background(), task.VolumeOwner, task.Image, task.UID, task.GID)
			t.Fatal(e)
		}
		utilityActive = true
		files := []ConfigurationFile{}
		if name == "nonempty" {
			files = append(files, ConfigurationFile{Name: "preserved.txt", Contents: []byte("Synthetic existing data must remain unchanged\n")})
		}
		if e = c.PutConfiguration(ctx, task.VolumeOwner, task.Image, task.UID, task.GID, files); e != nil {
			t.Fatal(e)
		}
		if e = c.RemoveUtility(ctx, task.VolumeOwner, task.Image, task.UID, task.GID); e != nil {
			t.Fatal(e)
		}
		utilityActive = false
		value, e := c.CreateModelTask(ctx, task)
		taskID = value.ID
		if e != nil {
			t.Fatal(e)
		}
		admission, e := c.StartModelTask(ctx, task, taskID, settings.Compose.Process.ReservePercent)
		if e != nil {
			t.Fatal(e)
		}
		var observed RuntimeContainer
		for deadline := time.Now().Add(15 * time.Second); ; {
			var found bool
			observed, found, e = c.InspectModelTask(ctx, task)
			if e != nil || !found || observed.ID != taskID {
				t.Fatal("task inspection", e)
			}
			if observed.State.Status == "exited" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("real control command did not exit")
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
		if observed.State.OOMKilled || name == "empty" && observed.State.ExitCode != 0 || name == "nonempty" && observed.State.ExitCode == 0 {
			t.Fatal("wrong real control outcome", name, observed.State)
		}
		if _, e = c.StartModelTask(ctx, task, taskID, settings.Compose.Process.ReservePercent); e == nil {
			t.Fatal("real exited task replayed")
		}
		direct, e := c.InspectModelTaskStorage(ctx, task, taskID, files)
		if e != nil || direct.Status != "verified_private_volume_payload" {
			t.Fatal("direct exited result", direct, e)
		}
		if e = c.RemoveModelTask(ctx, task, taskID); e != nil {
			t.Fatal(e)
		}
		taskID = ""
		if _, e = c.CreateUtility(ctx, task.VolumeOwner, task.Image, task.UID, task.GID); e != nil {
			t.Fatal(e)
		}
		utilityActive = true
		contents, e := c.InspectConfiguration(ctx, task.VolumeOwner, task.Image, task.UID, task.GID, files)
		if e != nil || contents.Status != "verified_private_volume_payload" {
			t.Fatal("private data changed", contents, e)
		}
		receipt[name] = map[string]any{"owner": task.Owner, "task_id": observed.ID, "state": observed.State, "admission": admission, "configuration": contents, "direct_task_readback": direct, "replay_refused": true, "readonly_root": observed.HostConfig.ReadonlyRootfs, "network_mode": observed.HostConfig.NetworkMode, "log_driver": observed.HostConfig.LogConfig.Type}
		if e = cleanup(); e != nil {
			t.Fatal(e)
		}
	}
	if e = imageCleanup(); e != nil {
		t.Fatal(e)
	}
	after := inventory()
	if before != after {
		t.Fatal("disposable task changed Docker inventories")
	}
	receipt["same_final_inventory"] = true
	receipt["status"] = "passed_real_control_empty_and_nonempty_model_store_tasks"
	f, e := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if json.NewEncoder(f).Encode(receipt) != nil || f.Sync() != nil {
		t.Fatal("task evidence failed")
	}
}
