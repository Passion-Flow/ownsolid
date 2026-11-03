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

func nativeTargetInitializationLifecycleFixture(t *testing.T, kind, fault string) (*Client, DatabaseTask, *atomic.Int32, *atomic.Int32, []ConfigurationFile) {
	t.Helper()
	task := nativeTargetInitializationFixture(kind)
	if fault == "discovery" {
		task.NativePostgreSQLTargetInitialization.Request.ExpectedSystemIdentifier = ""
		task.NativePostgreSQLTargetInitialization.Request.ExpectedDatabaseOID = 0
	}
	request, _ := json.Marshal(task.NativePostgreSQLTargetInitialization.Request)
	files := []ConfigurationFile{{Name: "control.yaml", Contents: []byte("synthetic: true")}, {Name: NativePostgreSQLTargetRequestFile, Contents: request}}
	return nativeFixedNativeTaskLifecycleFixture(t, task, fault, files, nativeTargetInitializationOutputFixture(task), []string{kind, "--config", DatabaseTaskDestination + "/private/control.yaml", "--request", DatabaseTaskDestination + "/private/" + NativePostgreSQLTargetRequestFile}, task.NetworkID)
}

// Share only the Docker/Unix protocol double; each native command retains its
// independent fixed input/output assertions and authority rejection tests.
func nativeFixedNativeTaskLifecycleFixture(t *testing.T, task DatabaseTask, fault string, files []ConfigurationFile, original []byte, command []string, networkMode string) (*Client, DatabaseTask, *atomic.Int32, *atomic.Int32, []ConfigurationFile) {
	t.Helper()
	expectedCommand := []any{}
	for _, arg := range command {
		expectedCommand = append(expectedCommand, arg)
	}
	resources := wire.UtilityResources{MemoryBytes: 96 << 20, MilliCPU: 225, PIDs: 23}
	var state, effects atomic.Int32
	var specification atomic.Value
	started := make(chan struct{})
	var physicalID atomic.Value
	physicalID.Store(strings.Repeat("e", 64))
	var creations atomic.Int32
	currentID := func() string { return physicalID.Load().(string) }
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
			if (fault == "foreign-consumer" && strings.Contains(r.URL.RawQuery, NativePostgreSQLTargetRecordsRole)) || (fault == "foreign-store" && strings.Contains(r.URL.RawQuery, "models")) || (fault == "foreign-projection" && strings.Contains(r.URL.RawQuery, NativePostgreSQLConfigurationRole)) {
				write([]RuntimeObject{{ID: strings.Repeat("f", 64)}})
				return
			}
			if state.Load() > 0 && state.Load() < 4 {
				write([]RuntimeObject{{ID: currentID()}})
			} else {
				write([]RuntimeObject{})
			}
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/networks/"):
			labels := map[string]string{"io.projectone.isolated-scope": task.Owner.ScopeID, "io.projectone.purpose": "isolated-validation", "com.docker.compose.project": RuntimeProject(task.Owner.ScopeID), "com.docker.compose.network": "isolated"}
			endpoints := map[string]any{}
			for role, id := range task.Peers {
				endpoints[id] = map[string]any{"Name": RuntimeContainerName(task.Owner.ScopeID, role)}
			}
			if state.Load() > 0 && state.Load() < 4 && (networkMode != "none" || fault == "bridge-actor") {
				endpoints[currentID()] = map[string]any{"Name": task.name()}
			}
			write(map[string]any{"ID": task.NetworkID, "Name": RuntimeNetworkName(task.Owner.ScopeID), "Driver": "bridge", "Scope": "local", "Internal": true, "Labels": labels, "Containers": endpoints})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/archive"):
			if r.URL.Query().Get("path") != DatabaseTaskDestination {
				t.Error("unexpected target input path")
				w.WriteHeader(404)
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
			networkName, networkID := RuntimeNetworkName(task.Owner.ScopeID), task.NetworkID
			if networkMode == "none" {
				networkName, networkID = "none", ""
			}
			write(map[string]any{"Id": currentID(), "Name": "/" + task.name(), "Image": task.Image, "Platform": "linux", "Config": config, "HostConfig": host, "State": process, "Mounts": live, "NetworkSettings": map[string]any{"Networks": map[string]any{networkName: map[string]any{"NetworkID": networkID}}, "Ports": map[string]any{}}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/containers/create"):
			var b map[string]any
			if json.NewDecoder(r.Body).Decode(&b) != nil {
				t.Error("bad create body")
				w.WriteHeader(500)
				return
			}
			h := b["HostConfig"].(map[string]any)
			if h["NetworkMode"] != networkMode || h["ReadonlyRootfs"] != true || len(h["Mounts"].([]any)) != len(task.databaseMountProfiles()) || r.URL.Query().Get("name") != task.name() {
				t.Error("unsafe native verifier create request")
			}
			if !reflect.DeepEqual(b["Cmd"], expectedCommand) {
				t.Error("non-fixed native command")
			}
			if creations.Add(1) > 1 {
				physicalID.Store(strings.Repeat("f", 64))
			}
			specification.Store(b)
			state.Store(1)
			effects.Add(1)
			w.WriteHeader(201)
			write(map[string]any{"Id": currentID()})
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
			conn.Write(nativeVerifierFrame(1, original))
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

func TestNativePostgreSQLTargetInitializationUnixOriginalLifecycleAndPrivateOutput(t *testing.T) {
	for _, test := range []struct{ kind, fault string }{{wire.PostgreSQLRestoreTargetInitializeCommand, ""}, {wire.PostgreSQLRestoreTargetInspectCommand, ""}, {wire.PostgreSQLRestoreTargetInspectCommand, "discovery"}} {
		t.Run(test.kind+test.fault, func(t *testing.T) {
			c, task, state, effects, files := nativeTargetInitializationLifecycleFixture(t, test.kind, test.fault)
			ctx := context.Background()
			created, e := c.CreateDatabaseTask(ctx, task)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.CreateDatabaseTask(ctx, task); e == nil || effects.Load() != 1 {
				t.Fatal("existing target actor replaced")
			}
			out, e := c.ObserveDatabaseTaskOutput(ctx, task, created.ID)
			if e != nil {
				t.Fatal(e)
			}
			defer out.Close()
			if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e != nil {
				t.Fatal(e)
			}
			observed, e := out.Wait(ctx)
			if e != nil || observed.NativePostgreSQLTargetInitialization == nil || observed.Redacted {
				t.Fatal("original target result lost", e)
			}
			if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || effects.Load() != 2 {
				t.Fatal("original target effect replayed")
			}
			if e = c.RemoveDatabaseTask(ctx, task, created.ID); e == nil {
				t.Fatal("running target actor removed")
			}
			if e = c.StopDatabaseTask(ctx, task, created.ID, 3); e != nil {
				t.Fatal(e)
			}
			if e = c.RemoveDatabaseTask(ctx, task, created.ID); e != nil || state.Load() != 4 || effects.Load() != 4 {
				t.Fatal("exact normal cleanup", e)
			}
		})
	}
}

func TestNativePostgreSQLTargetInitializationUnixUnknownStartCannotBeRecreated(t *testing.T) {
	c, task, _, effects, files := nativeTargetInitializationLifecycleFixture(t, wire.PostgreSQLRestoreTargetInitializeCommand, "start-lost")
	ctx := context.Background()
	created, e := c.CreateDatabaseTask(ctx, task)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil {
		t.Fatal("unknown effect accepted")
	}
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil {
		t.Fatal("unknown start replayed")
	}
	if _, e = c.CreateDatabaseTask(ctx, task); e == nil || effects.Load() != 2 {
		t.Fatal("unknown original actor replaced")
	}
}

func TestNativePostgreSQLTargetInitializationUnixReadonlyFreshPhysicalActorKeepsStrictOwner(t *testing.T) {
	c, task, state, effects, _ := nativeTargetInitializationLifecycleFixture(t, wire.PostgreSQLRestoreTargetInspectCommand, "")
	ctx := context.Background()
	original, e := c.CreateDatabaseTask(ctx, task)
	if e != nil {
		t.Fatal(e)
	}
	// The Scope separately owns output/terminal proof durability. This fixture
	// sets terminal metadata to test the exact library cleanup/absence fence.
	state.Store(3)
	if e = c.RemoveDatabaseTask(ctx, task, original.ID); e != nil {
		t.Fatal(e)
	}
	fresh, e := c.CreateDatabaseTask(ctx, task)
	if e != nil || fresh.ID == original.ID {
		t.Fatal("fresh exact actor not created after absence", e)
	}
	before := effects.Load()
	if _, e = c.exactDatabaseTask(ctx, task, original.ID); e == nil || effects.Load() != before {
		t.Fatal("old actor ID adopted")
	}
	if e = c.RemoveDatabaseTask(ctx, task, original.ID); e == nil || effects.Load() != before {
		t.Fatal("stale actor ID removed current actor")
	}
	if e = c.RemoveDatabaseTask(ctx, task, fresh.ID); e != nil {
		t.Fatal(e)
	}
}

func TestNativePostgreSQLTargetInitializationUnixForeignConsumersBlockEffects(t *testing.T) {
	c, task, _, effects, _ := nativeTargetInitializationLifecycleFixture(t, wire.PostgreSQLRestoreTargetInitializeCommand, "foreign-consumer")
	if _, e := c.CreateDatabaseTask(context.Background(), task); e == nil || effects.Load() != 0 {
		t.Fatal("foreign record consumer permitted effect")
	}
	c, task, _, effects, files := nativeTargetInitializationLifecycleFixture(t, wire.PostgreSQLRestoreTargetInitializeCommand, "running-control")
	created, e := c.CreateDatabaseTask(context.Background(), task)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.startDatabaseTask(context.Background(), task, created.ID, files, 30, databaseAdmit); e == nil || effects.Load() != 1 {
		t.Fatal("live target writer permitted effect")
	}
}
