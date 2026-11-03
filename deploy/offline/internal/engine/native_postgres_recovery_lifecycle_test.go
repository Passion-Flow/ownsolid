package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func nativeRecoveryLifecycleFixture(t *testing.T, kind, fault string) (*Client, DatabaseTask, *atomic.Int32, *atomic.Int32, []ConfigurationFile) {
	t.Helper()
	task := nativeRecoveryFixture(kind)
	var request []byte
	if kind == NativePostgreSQLBackup {
		request, _ = json.Marshal(task.NativePostgreSQLRecovery.BackupRequest)
	} else {
		request, _ = json.Marshal(task.NativePostgreSQLRecovery.RestoreRequest)
	}
	files := []ConfigurationFile{{Name: "control.yaml", Contents: []byte("synthetic: true")}, {Name: NativePostgreSQLRecoveryRequestFile, Contents: request}}
	resources := wire.UtilityResources{MemoryBytes: 96 << 20, MilliCPU: 225, PIDs: 23}
	var state, effects atomic.Int32
	var specification atomic.Value
	started := make(chan struct{})
	id := strings.Repeat("e", 64)
	client, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/images/"):
			write(map[string]any{"Id": task.Image, "Os": "linux", "Architecture": "amd64", "RootFS": map[string]any{"Type": "layers", "Layers": []string{}}, "Config": map[string]any{"Entrypoint": []string{modelTaskEntrypoint}, "Env": []string{"IMAGE_SETTING=value"}, "Volumes": map[string]any{}}})
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/volumes/"):
			for _, m := range task.databaseMountProfiles() {
				if path.Base(r.URL.Path) == m.owner.VolumeName() {
					write(Volume{Name: m.owner.VolumeName(), Driver: "local", Scope: "local", Labels: m.owner.labels()})
					return
				}
			}
			t.Error("unexpected volume request")
			w.WriteHeader(404)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/containers/json"):
			if fault == "foreign-consumer" && strings.Contains(r.URL.RawQuery, NativePostgreSQLArtifactRole) {
				write([]RuntimeObject{{ID: strings.Repeat("f", 64)}})
				return
			}
			if state.Load() > 0 && state.Load() < 4 {
				write([]RuntimeObject{{ID: id}})
			} else {
				write([]RuntimeObject{})
			}
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/networks/"):
			labels := map[string]string{"io.projectone.isolated-scope": task.Owner.ScopeID, "io.projectone.purpose": "isolated-validation", "com.docker.compose.project": RuntimeProject(task.Owner.ScopeID), "com.docker.compose.network": "isolated"}
			endpoints := map[string]any{}
			for role, id := range task.Peers {
				endpoints[id] = map[string]any{"Name": RuntimeContainerName(task.Owner.ScopeID, role)}
			}
			if state.Load() > 0 && state.Load() < 4 {
				endpoints[id] = map[string]any{"Name": task.name()}
			}
			write(map[string]any{"ID": task.NetworkID, "Name": RuntimeNetworkName(task.Owner.ScopeID), "Driver": "bridge", "Scope": "local", "Internal": true, "Labels": labels, "Containers": endpoints})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/archive"):
			if (fault == "native-archive" || fault == "native-archive-change") && r.URL.Query().Get("path") == task.NativePostgreSQLRecovery.BackupRequest.BackupRoot {
				raw, _ := nativeArchiveFixture(t, "")
				w.Write(raw)
				if fault == "native-archive-change" {
					state.Store(2)
				}
				return
			}
			if r.URL.Query().Get("path") != DatabaseTaskDestination {
				if fault == "missing-completion" {
					w.WriteHeader(404)
					return
				}
				_, backup, records, _ := task.nativeRecoveryPaths()
				root := backup
				if kind == NativePostgreSQLRestore {
					root = records
				}
				if r.URL.Query().Get("path") != path.Join(root, "complete.json") {
					t.Error("unbounded archive read")
				}
				w.Write(nativeCompletionTar(nativeRecoveryOutputFixture(task), task.UID, task.GID))
				return
			}
			var out bytes.Buffer
			tw := tar.NewWriter(&out)
			tw.WriteHeader(&tar.Header{Name: "operator", Typeflag: tar.TypeDir, Mode: 0755})
			tw.WriteHeader(&tar.Header{Name: "operator/private", Typeflag: tar.TypeDir, Mode: 0700, Uid: task.UID, Gid: task.GID})
			for _, f := range files {
				tw.WriteHeader(&tar.Header{Name: "operator/private/" + f.Name, Typeflag: tar.TypeReg, Mode: 0600, Uid: task.UID, Gid: task.GID, Size: int64(len(f.Contents))})
				tw.Write(f.Contents)
			}
			tw.Close()
			w.Write(out.Bytes())
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/json"):
			for role, peer := range task.Peers {
				if path.Base(strings.TrimSuffix(r.URL.Path, "/json")) == RuntimeContainerName(task.Owner.ScopeID, role) {
					labels := map[string]string{"io.projectone.isolated-scope": task.Owner.ScopeID, "io.projectone.purpose": "isolated-validation", "com.docker.compose.project": RuntimeProject(task.Owner.ScopeID), "com.docker.compose.service": role}
					proc := map[string]any{"Status": "created", "Running": false, "Pid": 0}
					if role == "postgres" {
						proc = map[string]any{"Status": "running", "Running": true, "Pid": 124}
					}
					if fault == "running-control" && role == "control" {
						proc = map[string]any{"Status": "running", "Running": true, "Pid": 125}
					}
					write(map[string]any{"Id": peer, "Name": "/" + RuntimeContainerName(task.Owner.ScopeID, role), "Config": map[string]any{"Labels": labels}, "State": proc})
					return
				}
			}
			if state.Load() == 0 || state.Load() == 4 {
				w.WriteHeader(404)
				return
			}
			b := specification.Load().(map[string]any)
			config := map[string]any{}
			for _, k := range []string{"Image", "User", "Entrypoint", "Cmd", "WorkingDir", "Labels", "Env"} {
				config[k] = b[k]
			}
			config["Volumes"] = map[string]any{}
			host := b["HostConfig"].(map[string]any)
			live := []any{}
			for _, entry := range host["Mounts"].([]any) {
				m := entry.(map[string]any)
				live = append(live, map[string]any{"Type": "volume", "Name": m["Source"], "Destination": m["Target"], "Driver": "local", "RW": !m["ReadOnly"].(bool)})
			}
			process := map[string]any{"Status": "created", "Running": false, "Pid": 0, "StartedAt": neverStarted, "ExitCode": 0}
			if state.Load() == 2 {
				process["Status"], process["Running"], process["Pid"], process["StartedAt"] = "running", true, 123, "2026-11-03T00:00:00Z"
			}
			if state.Load() == 3 {
				process["Status"], process["StartedAt"] = "exited", "2026-11-03T00:00:00Z"
			}
			write(map[string]any{"Id": id, "Name": "/" + task.name(), "Image": task.Image, "Platform": "linux", "Config": config, "HostConfig": host, "State": process, "Mounts": live, "NetworkSettings": map[string]any{"Networks": map[string]any{RuntimeNetworkName(task.Owner.ScopeID): map[string]any{"NetworkID": task.NetworkID}}, "Ports": map[string]any{}}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/containers/create"):
			var b map[string]any
			if json.NewDecoder(r.Body).Decode(&b) != nil {
				t.Error("bad create body")
				w.WriteHeader(500)
				return
			}
			h := b["HostConfig"].(map[string]any)
			if h["NetworkMode"] != task.NetworkID || h["ReadonlyRootfs"] != true || len(h["Mounts"].([]any)) != len(task.databaseMountProfiles()) || r.URL.Query().Get("name") != task.name() {
				t.Error("unsafe native verifier create request")
			}
			if !reflect.DeepEqual(b["Cmd"], []any{kind, "--config", DatabaseTaskDestination + "/private/control.yaml", "--request", DatabaseTaskDestination + "/private/" + NativePostgreSQLRecoveryRequestFile}) {
				t.Error("non-fixed native command")
			}
			specification.Store(b)
			state.Store(1)
			effects.Add(1)
			w.WriteHeader(201)
			write(map[string]any{"Id": id})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/attach"):
			conn, buffer, e := w.(http.Hijacker).Hijack()
			if e != nil {
				return
			}
			defer conn.Close()
			fmt.Fprint(buffer, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			buffer.Flush()
			select {
			case <-started:
			case <-r.Context().Done():
				return
			}
			conn.Write(nativeVerifierFrame(1, nativeRecoveryOutputFixture(task)))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/start"):
			if state.Load() != 1 {
				t.Error("start replay")
			}
			state.Store(2)
			effects.Add(1)
			close(started)
			if fault == "start-lost" {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			w.WriteHeader(204)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/stop"):
			state.Store(3)
			effects.Add(1)
			w.WriteHeader(204)
		case r.Method == "DELETE":
			if r.URL.Query().Get("force") != "0" || r.URL.Query().Get("v") != "0" {
				t.Error("destructive cleanup")
			}
			state.Store(4)
			effects.Add(1)
			w.WriteHeader(204)
		default:
			t.Error("unexpected native verifier route", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	client.config.Utility = &resources
	client.config.ResponseBytes = 65536
	return client, task, &state, &effects, files
}

func TestNativePostgreSQLRecoveryUnixLifecycleAndOriginalCompletionReadback(t *testing.T) {
	for _, kind := range []string{NativePostgreSQLBackup, NativePostgreSQLRestore, NativePostgreSQLRestoreVerify} {
		t.Run(kind, func(t *testing.T) {
			c, task, state, effects, files := nativeRecoveryLifecycleFixture(t, kind, "")
			ctx := context.Background()
			created, e := c.CreateDatabaseTask(ctx, task)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.CreateDatabaseTask(ctx, task); e == nil || effects.Load() != 1 {
				t.Fatal("create replay")
			}
			obs, e := c.ObserveDatabaseTaskOutput(ctx, task, created.ID)
			if e != nil {
				t.Fatal(e)
			}
			defer obs.Close()
			if _, e = c.ReadNativePostgreSQLTaskCompletion(ctx, task, created.ID); e == nil {
				t.Fatal("completion before original effect")
			}
			if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e != nil {
				t.Fatal(e)
			}
			output, e := obs.Wait(ctx)
			if e != nil || output.Redacted {
				t.Fatal("original output missing", e)
			}
			if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || effects.Load() != 2 {
				t.Fatal("start replay")
			}
			if _, e = c.ReadNativePostgreSQLTaskCompletion(ctx, task, created.ID); e == nil {
				t.Fatal("running completion accepted")
			}
			if e = c.RemoveDatabaseTask(ctx, task, created.ID); e == nil {
				t.Fatal("running task removed")
			}
			if e = c.StopDatabaseTask(ctx, task, created.ID, 3); e != nil {
				t.Fatal(e)
			}
			if kind != NativePostgreSQLRestoreVerify {
				retained, e := c.ReadNativePostgreSQLTaskCompletion(ctx, task, created.ID)
				if e != nil {
					t.Fatal("original terminal completion unavailable", e)
				}
				var raw []byte
				if kind == NativePostgreSQLBackup {
					raw = retained.NativePostgreSQLBackupCompletion.Original
				} else {
					raw = retained.NativePostgreSQLRestoreCompletion.Original
				}
				if !bytes.Equal(raw, nativeRecoveryOutputFixture(task)) || effects.Load() != 3 {
					t.Fatal("GET changed original completion or effects")
				}
			}
			if e = c.RemoveDatabaseTask(ctx, task, created.ID); e != nil || state.Load() != 4 || effects.Load() != 4 {
				t.Fatal("normal bounded cleanup", e)
			}
		})
	}
}
func TestNativePostgreSQLRecoveryLostStartNeverReplays(t *testing.T) {
	c, task, state, effects, files := nativeRecoveryLifecycleFixture(t, NativePostgreSQLRestore, "start-lost")
	ctx := context.Background()
	created, e := c.CreateDatabaseTask(ctx, task)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil {
		t.Fatal("lost start accepted")
	}
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || effects.Load() != 2 {
		t.Fatal("uncertain effect replay")
	}
	state.Store(3)
	if _, e = c.ReadNativePostgreSQLTaskCompletion(ctx, task, created.ID); e != nil || effects.Load() != 2 {
		t.Fatal("readonly recovery repeated effect", e)
	}
}
func TestNativePostgreSQLRecoveryMissingCompletionNeverManufactured(t *testing.T) {
	c, task, state, effects, _ := nativeRecoveryLifecycleFixture(t, NativePostgreSQLRestore, "missing-completion")
	ctx := context.Background()
	created, e := c.CreateDatabaseTask(ctx, task)
	if e != nil {
		t.Fatal(e)
	}
	state.Store(3)
	if _, e = c.ReadNativePostgreSQLTaskCompletion(ctx, task, created.ID); e == nil || effects.Load() != 1 {
		t.Fatal("missing completion manufactured")
	}
}
func TestNativePostgreSQLRecoveryForeignConsumerAndRunningPeerBlockEffects(t *testing.T) {
	c, task, _, effects, _ := nativeRecoveryLifecycleFixture(t, NativePostgreSQLRestore, "foreign-consumer")
	if _, e := c.CreateDatabaseTask(context.Background(), task); e == nil || effects.Load() != 0 {
		t.Fatal("foreign consumer allowed mutation")
	}
	c, task, _, effects, files := nativeRecoveryLifecycleFixture(t, NativePostgreSQLRestore, "running-control")
	created, e := c.CreateDatabaseTask(context.Background(), task)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.startDatabaseTask(context.Background(), task, created.ID, files, 30, databaseAdmit); e == nil || effects.Load() != 1 {
		t.Fatal("live Control allowed native effect")
	}
}
