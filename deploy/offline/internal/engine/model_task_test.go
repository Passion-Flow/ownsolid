package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// modelTaskFixture is an explicit synthetic Engine HTTP double, never product behavior.
func modelTaskFixture(t *testing.T, fault string) (*Client, ModelStoreTask, *atomic.Int32) {
	t.Helper()
	task := ModelStoreTask{Owner: Ownership{strings.Repeat("a", 32), strings.Repeat("b", 32), ModelTaskRole}, VolumeOwner: Ownership{strings.Repeat("a", 32), strings.Repeat("c", 32), "models"}, Image: "sha256:" + strings.Repeat("d", 64), UID: 10033, GID: 10034}
	resources := wire.UtilityResources{MemoryBytes: 64 << 20, MilliCPU: 100, PIDs: 16}
	var created atomic.Bool
	var posts atomic.Int32
	var removed atomic.Bool
	var started atomic.Bool
	var stopped atomic.Bool
	var archiveReads atomic.Int32
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		write := func(value any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(value) }
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/archive"):
			archiveReads.Add(1)
			if r.URL.Query().Get("path") != ModelTaskDestination {
				t.Error("arbitrary archive path")
			}
			if fault == "readback-response" {
				w.WriteHeader(500)
				return
			}
			var data bytes.Buffer
			tw := tar.NewWriter(&data)
			tw.WriteHeader(&tar.Header{Name: "model-init", Typeflag: tar.TypeDir, Mode: 0755})
			if fault != "readback-missing-private" {
				mode, uid := int64(0700), task.UID
				if fault == "readback-mode" {
					mode = 0755
				}
				if fault == "readback-owner" {
					uid++
				}
				tw.WriteHeader(&tar.Header{Name: "model-init/private", Typeflag: tar.TypeDir, Mode: mode, Uid: uid, Gid: task.GID})
			}
			if fault == "readback-extra" {
				tw.WriteHeader(&tar.Header{Name: "model-init/private/extra", Typeflag: tar.TypeReg, Mode: 0600, Uid: task.UID, Gid: task.GID, Size: 1})
				tw.Write([]byte("x"))
			}
			tw.Close()
			raw := data.Bytes()
			if fault == "readback-trailer" {
				raw = append(raw, bytes.Repeat([]byte{1}, 512)...)
			}
			w.Write(raw)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1.49/images/"):
			entry := []string{modelTaskEntrypoint}
			volumes := map[string]any{}
			if fault == "image-entrypoint" {
				entry = []string{"/bin/sh"}
			}
			if fault == "implicit-volume" {
				volumes["/foreign"] = map[string]any{}
			}
			write(map[string]any{"Id": task.Image, "Os": "linux", "Architecture": "amd64", "RootFS": map[string]any{"Type": "layers", "Layers": []string{}}, "Config": map[string]any{"Entrypoint": entry, "Env": []string{"IMAGE_SETTING=value"}, "Volumes": volumes}})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1.49/volumes/"):
			labels := task.VolumeOwner.labels()
			if fault == "volume-owner" {
				labels["io.projectone.operation"] = "foreign"
			}
			write(Volume{Name: task.VolumeOwner.VolumeName(), Driver: "local", Scope: "local", Labels: labels})
		case r.Method == "GET" && r.URL.Path == "/v1.49/containers/json":
			if fault == "foreign-consumer" {
				write([]any{map[string]string{"Id": strings.Repeat("f", 64)}})
			} else if created.Load() && !removed.Load() {
				write([]any{map[string]string{"Id": strings.Repeat("e", 64)}})
			} else {
				write([]any{})
			}
		case r.Method == "POST" && r.URL.Path == "/v1.49/containers/create":
			posts.Add(1)
			created.Store(true)
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid body")
			}
			if body["Image"] != task.Image || body["User"] != "10033:10034" || body["WorkingDir"] != "/" || body["Env"] != nil || r.URL.Query().Get("platform") != "linux/amd64" || r.URL.Query().Get("name") != task.name() {
				t.Error("unbound task body")
			}
			if fault == "create-disconnect" {
				conn, _, e := w.(http.Hijacker).Hijack()
				if e == nil {
					conn.Close()
				}
				return
			}
			w.WriteHeader(201)
			write(map[string]any{"Id": strings.Repeat("e", 64), "Warnings": []string{}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/start"):
			posts.Add(1)
			started.Store(true)
			if fault == "start-disconnect" {
				conn, _, e := w.(http.Hijacker).Hijack()
				if e == nil {
					conn.Close()
				}
				return
			}
			w.WriteHeader(204)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/stop"):
			posts.Add(1)
			stopped.Store(true)
			if r.URL.Query().Get("t") != "2" {
				t.Error("unbounded stop")
			}
			if fault == "stop-disconnect" {
				conn, _, e := w.(http.Hijacker).Hijack()
				if e == nil {
					conn.Close()
				}
				return
			}
			w.WriteHeader(204)
		case r.Method == "DELETE":
			if r.URL.Query().Get("force") != "0" || r.URL.Query().Get("v") != "0" {
				t.Error("destructive removal")
			}
			removed.Store(true)
			posts.Add(1)
			w.WriteHeader(204)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/json"):
			if !created.Load() || removed.Load() {
				w.WriteHeader(404)
				return
			}
			object := modelTaskObject(task, resources)
			switch fault {
			case "task-image":
				object.Image = "sha256:" + strings.Repeat("f", 64)
			case "task-user":
				object.Config.User = "0:0"
			case "task-command":
				object.Config.Cmd = []string{"serve"}
			case "task-environment":
				object.Config.Env = append(object.Config.Env, "SECRET=foreign")
			case "task-role":
				object.Config.Labels["io.projectone.role"] = "foreign"
			case "task-capability":
				object.HostConfig.CapAdd = []string{"SYS_ADMIN"}
			case "task-network":
				object.HostConfig.NetworkMode = "host"
			case "task-log":
				object.HostConfig.LogConfig.Type = "json-file"
			case "task-swap":
				object.HostConfig.MemorySwap = -1
			case "task-memory":
				object.HostConfig.Memory++
			case "task-mount":
				object.Mounts[0].Name = "foreign"
			case "task-copy":
				object.HostConfig.Mounts[0].VolumeOptions.NoCopy = false
			case "task-namespace":
				object.HostConfig.PidMode = "host"
			case "task-stale-created":
				object.State.StartedAt = "2026-10-17T00:00:00Z"
			case "task-running", "stop-disconnect":
				object.State.Status = "running"
				object.State.Running = true
				object.State.PID = 123
				object.State.StartedAt = "2026-10-17T00:00:00Z"
			case "task-exited":
				object.State.Status = "exited"
				object.State.StartedAt = "2026-10-17T00:00:00Z"
			}
			if started.Load() || stopped.Load() {
				object.State.Status = "exited"
				object.State.PID = 0
				object.State.Running = false
				object.State.StartedAt = "2026-10-17T00:00:00Z"
			}
			if strings.HasPrefix(fault, "readback-") && fault != "readback-created" {
				object.State.Status = "exited"
				object.State.PID = 0
				object.State.Running = false
				object.State.StartedAt = "2026-10-17T00:00:00Z"
			}
			if fault == "readback-changed" && archiveReads.Load() > 0 {
				object.State.StartedAt = "2026-10-17T00:00:01Z"
			}
			write(object)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	c.config.Utility = &resources
	c.config.ResponseBytes = 64 << 10
	return c, task, &posts
}

func TestModelTaskDirectExitedStorageReadback(t *testing.T) {
	for _, fault := range []string{"readback-good", "readback-response", "readback-missing-private", "readback-mode", "readback-owner", "readback-extra", "readback-trailer", "readback-changed", "readback-created"} {
		t.Run(fault, func(t *testing.T) {
			c, task, posts := modelTaskFixture(t, fault)
			ctx := context.Background()
			// This synthetic fixture exposes a chosen pre-existing process state after its create response.
			c.CreateModelTask(ctx, task)
			before := posts.Load()
			result, e := c.InspectModelTaskStorage(ctx, task, strings.Repeat("e", 64), []ConfigurationFile{})
			if fault == "readback-good" {
				if e != nil || result.Status != "verified_private_volume_payload" || result.Files != 0 {
					t.Fatal(result, e)
				}
			} else if e == nil {
				t.Fatal("unsafe direct archive accepted")
			}
			if posts.Load() != before {
				t.Fatal("readonly result reader changed Engine objects")
			}
		})
	}
}

func TestModelTaskStartStopLossAndCancellation(t *testing.T) {
	for _, fault := range []string{"start-good", "start-disconnect"} {
		t.Run(fault, func(t *testing.T) {
			c, task, posts := modelTaskFixture(t, fault)
			ctx := context.Background()
			created, e := c.CreateModelTask(ctx, task)
			if e != nil {
				t.Fatal(e)
			}
			// Explicit admission double: actual host/Engine admission is tested by the tagged journey.
			admit := func(_ context.Context, r ResourceRequirement, reserve int) (ResourceAdmission, error) {
				if r.MemoryBytes != 64<<20 || r.MilliCPU != 100 || reserve != 30 {
					t.Error("changed resource requirement")
				}
				return ResourceAdmission{Status: "explicit_test_admission"}, nil
			}
			_, e = c.startModelTask(ctx, task, created.ID, 30, admit)
			if fault == "start-good" && e != nil || fault == "start-disconnect" && e == nil {
				t.Fatal(e)
			}
			observed, found, e := c.InspectModelTask(ctx, task)
			if e != nil || !found || observed.ID != created.ID || observed.State.Status != "exited" || posts.Load() != 2 {
				t.Fatal("lost start observation", e)
			}
			if _, e = c.startModelTask(ctx, task, created.ID, 30, admit); e == nil || posts.Load() != 2 {
				t.Fatal("lost start resent")
			}
		})
	}
	c, task, posts := modelTaskFixture(t, "stop-disconnect")
	ctx := context.Background()
	c.CreateModelTask(ctx, task)
	value, found, e := c.InspectModelTask(ctx, task)
	if e != nil || !found {
		t.Fatal(e)
	}
	if e = c.StopModelTask(ctx, task, value.ID, 2); e == nil {
		t.Fatal("lost stop reported success")
	}
	value, found, e = c.InspectModelTask(ctx, task)
	if e != nil || !found || value.State.Status != "exited" || posts.Load() != 2 {
		t.Fatal("lost stop observation", e)
	}
	if e = c.StopModelTask(ctx, task, value.ID, 2); e == nil || posts.Load() != 2 {
		t.Fatal("lost stop resent")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = c.CreateModelTask(cancelled, task); e == nil || posts.Load() != 2 {
		t.Fatal("canceled operation submitted")
	}
}
func modelTaskObject(t ModelStoreTask, u wire.UtilityResources) RuntimeContainer {
	r := RuntimeContainer{ID: strings.Repeat("e", 64), Name: "/" + t.name(), Image: t.Image, Platform: "linux"}
	r.Config.Image = t.Image
	r.Config.User = "10033:10034"
	r.Config.Cmd = t.command()
	r.Config.Entrypoint = []string{modelTaskEntrypoint}
	r.Config.Env = []string{"IMAGE_SETTING=value"}
	r.Config.Labels = t.Owner.labels()
	r.Config.WorkingDir = "/"
	r.State.Status = "created"
	r.State.StartedAt = neverStarted
	h := &r.HostConfig
	h.NetworkMode = "none"
	h.IpcMode = "private"
	h.CgroupnsMode = "private"
	h.Runtime = "runc"
	h.ReadonlyRootfs = true
	h.Memory = u.MemoryBytes
	h.MemorySwap = u.MemoryBytes
	h.NanoCPUs = u.MilliCPU * nanosecondsPerMillicore
	h.PIDs = u.PIDs
	h.CapDrop = []string{"ALL"}
	h.SecurityOpt = []string{"no-new-privileges:true"}
	h.RestartPolicy.Name = "no"
	h.LogConfig.Type = "none"
	// Roundtrip only constructs anonymous metadata types in this explicit test double.
	b, _ := json.Marshal(map[string]any{"HostConfig": map[string]any{"Mounts": []any{map[string]any{"Type": "volume", "Source": t.VolumeOwner.VolumeName(), "Target": ModelTaskDestination, "ReadOnly": false, "VolumeOptions": map[string]any{"NoCopy": true}}}}, "Mounts": []any{map[string]any{"Type": "volume", "Name": t.VolumeOwner.VolumeName(), "Destination": ModelTaskDestination, "Driver": "local", "RW": true}}, "NetworkSettings": map[string]any{"Networks": map[string]any{"none": map[string]any{"NetworkID": ""}}}})
	json.Unmarshal(b, &r)
	return r
}
func TestModelTaskCreateInspectRemoveAndUncertain(t *testing.T) {
	for _, fault := range []string{"", "create-disconnect"} {
		t.Run(fault, func(t *testing.T) {
			c, task, posts := modelTaskFixture(t, fault)
			ctx := context.Background()
			value, e := c.CreateModelTask(ctx, task)
			if fault == "create-disconnect" && e == nil || fault == "" && e != nil {
				t.Fatal(e)
			}
			if posts.Load() != 1 {
				t.Fatal("create effect count")
			}
			observed, found, e := c.InspectModelTask(ctx, task)
			if e != nil || !found || observed.State.Status != "created" {
				t.Fatal(e)
			}
			if fault == "" && value.ID != observed.ID {
				t.Fatal("changed id")
			}
			if _, e = c.CreateModelTask(ctx, task); e == nil || posts.Load() != 1 {
				t.Fatal("existing task replay")
			}
			if _, e = c.StartModelTask(ctx, task, observed.ID, 0); e == nil || posts.Load() != 1 {
				t.Fatal("invalid admission started task")
			}
			if _, e = c.StartModelTask(ctx, task, strings.Repeat("f", 64), 30); e == nil {
				t.Fatal("foreign id accepted")
			}
			if e = c.RemoveModelTask(ctx, task, observed.ID); e != nil || posts.Load() != 2 {
				t.Fatal(e)
			}
		})
	}
}
func TestModelTaskContextAndObservedSecurity(t *testing.T) {
	for _, fault := range []string{"image-entrypoint", "implicit-volume", "volume-owner", "foreign-consumer", "task-image", "task-user", "task-command", "task-environment", "task-role", "task-capability", "task-network", "task-log", "task-swap", "task-memory", "task-mount", "task-copy", "task-namespace", "task-stale-created"} {
		t.Run(fault, func(t *testing.T) {
			c, task, posts := modelTaskFixture(t, fault)
			_, e := c.CreateModelTask(context.Background(), task)
			if e == nil {
				t.Fatal("fault accepted")
			}
			if !strings.HasPrefix(fault, "task-") && posts.Load() != 0 {
				t.Fatal("effect before context refusal")
			}
		})
	}
	for _, mutate := range []func(*ModelStoreTask){func(t *ModelStoreTask) { t.UID = 0 }, func(t *ModelStoreTask) { t.GID = -1 }, func(t *ModelStoreTask) { t.Image = "image:latest" }, func(t *ModelStoreTask) { t.Owner.Role = "foreign" }, func(t *ModelStoreTask) { t.VolumeOwner.Role = "operator-config" }, func(t *ModelStoreTask) { t.VolumeOwner.ScopeID = strings.Repeat("f", 32) }} {
		c, task, posts := modelTaskFixture(t, "")
		mutate(&task)
		if _, e := c.CreateModelTask(context.Background(), task); e == nil || posts.Load() != 0 {
			t.Fatal("invalid task context")
		}
	}
}
func TestModelTaskExecutionReplayAndRunningCleanupRefusal(t *testing.T) {
	for _, fault := range []string{"task-running", "task-exited"} {
		t.Run(fault, func(t *testing.T) {
			c, task, posts := modelTaskFixture(t, fault)
			ctx := context.Background()
			c.CreateModelTask(ctx, task)
			value, found, e := c.InspectModelTask(ctx, task)
			if e != nil || !found {
				t.Fatal(e)
			}
			before := posts.Load()
			if _, e = c.StartModelTask(ctx, task, value.ID, 30); e == nil || posts.Load() != before {
				t.Fatal("execution replay")
			}
			if e = c.StopModelTask(ctx, task, value.ID, 0); e == nil {
				t.Fatal("unbounded stop")
			}
			if fault == "task-running" {
				if e = c.RemoveModelTask(ctx, task, value.ID); e == nil || posts.Load() != before {
					t.Fatal("removed running task")
				}
			} else if e = c.RemoveModelTask(ctx, task, value.ID); e != nil {
				t.Fatal(e)
			}
		})
	}
}
