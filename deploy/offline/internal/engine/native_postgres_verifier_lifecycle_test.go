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

// This fixture exercises the real client over Unix HTTP, without Docker or SQL.
func nativeVerifierLifecycleFixture(t *testing.T, fault string) (*Client, DatabaseTask, *atomic.Int32, *atomic.Int32, []ConfigurationFile) {
	t.Helper()
	task := nativeBackupVerifierTaskFixture()
	request, _ := json.Marshal(task.NativePostgreSQLBackupVerifier.Request)
	files := []ConfigurationFile{{Name: NativePostgreSQLVerifierRequestFile, Contents: request}}
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
			t.Error("network-free verifier queried live runtime network")
			w.WriteHeader(500)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/archive"):
			if r.URL.Query().Get("path") != DatabaseTaskDestination {
				t.Error("unexpected private archive path")
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
			write(map[string]any{"Id": id, "Name": "/" + task.name(), "Image": task.Image, "Platform": "linux", "Config": config, "HostConfig": host, "State": process, "Mounts": live, "NetworkSettings": map[string]any{"Networks": map[string]any{"none": map[string]any{"NetworkID": "synthetic-none"}}, "Ports": map[string]any{}}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/containers/create"):
			var b map[string]any
			if json.NewDecoder(r.Body).Decode(&b) != nil {
				t.Error("bad create body")
				w.WriteHeader(500)
				return
			}
			h := b["HostConfig"].(map[string]any)
			if h["NetworkMode"] != "none" || h["ReadonlyRootfs"] != true || len(h["Mounts"].([]any)) != 4 || r.URL.Query().Get("name") != task.name() {
				t.Error("unsafe native verifier create request")
			}
			if !reflect.DeepEqual(b["Cmd"], []any{NativePostgreSQLBackupVerify, "--request", DatabaseTaskDestination + "/private/" + NativePostgreSQLVerifierRequestFile, "--manifest-sha256", task.NativePostgreSQLBackupVerifier.CandidateSHA256}) {
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
			conn.Write(nativeVerifierFrame(1, nativeVerifierOutputFixture(task)))
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

func TestNativePostgreSQLBackupVerifierUnixLifecycleAndPrivateAttach(t *testing.T) {
	c, task, state, effects, files := nativeVerifierLifecycleFixture(t, "")
	ctx := context.Background()
	created, e := c.CreateDatabaseTask(ctx, task)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.CreateDatabaseTask(ctx, task); e == nil || effects.Load() != 1 {
		t.Fatal("create replay")
	}
	observer, e := c.ObserveDatabaseTaskOutput(ctx, task, created.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer observer.Close()
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e != nil {
		t.Fatal(e)
	}
	output, e := observer.Wait(ctx)
	if e != nil || output.NativePostgreSQLBackup == nil {
		t.Fatal("native observation not captured privately", e)
	}
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || effects.Load() != 2 {
		t.Fatal("start replay")
	}
	if e = c.RemoveDatabaseTask(ctx, task, created.ID); e == nil {
		t.Fatal("running source verifier removed")
	}
	if e = c.StopDatabaseTask(ctx, task, created.ID, 3); e != nil {
		t.Fatal(e)
	}
	if e = c.RemoveDatabaseTask(ctx, task, created.ID); e != nil || state.Load() != 4 || effects.Load() != 4 {
		t.Fatal("normal exact cleanup", e)
	}
}

func TestNativePostgreSQLBackupVerifierUnknownStartNeverReplays(t *testing.T) {
	c, task, _, effects, files := nativeVerifierLifecycleFixture(t, "start-lost")
	ctx := context.Background()
	created, e := c.CreateDatabaseTask(ctx, task)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil {
		t.Fatal("lost start accepted")
	}
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || effects.Load() != 2 {
		t.Fatal("unknown start replayed")
	}
}

func TestNativePostgreSQLBackupVerifierForeignArtifactHasNoEffect(t *testing.T) {
	c, task, _, effects, _ := nativeVerifierLifecycleFixture(t, "foreign-consumer")
	if _, e := c.CreateDatabaseTask(context.Background(), task); e == nil || effects.Load() != 0 {
		t.Fatal("foreign source consumer permitted effect")
	}
}
