package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func mvpUpgradeTaskFixture() DatabaseTask {
	operation := strings.Repeat("b", 32)
	return DatabaseTask{Owner: Ownership{strings.Repeat("a", 32), operation, "database-" + DatabaseCumulativeMVP}, VolumeOwner: Ownership{strings.Repeat("a", 32), operation, CumulativeOperatorRole + "-" + operation}, Kind: DatabaseCumulativeMVP, Image: "sha256:" + strings.Repeat("d", 64), UID: 10001, GID: 10002, NetworkID: strings.Repeat("9", 64), Peers: map[string]string{"postgres": strings.Repeat("8", 64), "control": strings.Repeat("7", 64)}}
}
func TestMVPUpgradeTaskFixedCommandAndLegacyCompatibility(t *testing.T) {
	for _, kind := range []string{DatabaseCumulativeMVP, DatabaseCumulativeReleases} {
		task := mvpUpgradeTaskFixture()
		task.Kind = kind
		task.Owner.Role = "database-" + kind
		expected := []string{kind, "--config", DatabaseTaskDestination + "/private/control.yaml", "--runtime-config", DatabaseTaskDestination + "/private/runtime.yaml"}
		if !task.valid() || !reflect.DeepEqual(task.command(), expected) || len(task.databaseMountProfiles()) != 1 || !task.databaseMountProfiles()[0].readOnly {
			t.Fatal("fixed upgrade command/input custody changed")
		}
	}
	if DatabaseCumulativeMVP != "upgrade-schema" || DatabaseCumulativeReleases != "upgrade-releases" {
		t.Fatal("original CLI command changed")
	}
}
func TestMVPUpgradeTaskRejectsForeignContext(t *testing.T) {
	for name, mutate := range map[string]func(*DatabaseTask){
		"arbitrary command":          func(v *DatabaseTask) { v.Kind = "upgrade-platform" },
		"inspect instead of upgrade": func(v *DatabaseTask) { v.Kind = "inspect-mvp-postgresql-cpu-v1" },
		"legacy input":               func(v *DatabaseTask) { v.VolumeOwner.Role = "operator-config" },
		"different operation":        func(v *DatabaseTask) { v.VolumeOwner.OperationID = strings.Repeat("c", 32) },
		"foreign task role":          func(v *DatabaseTask) { v.Owner.Role = "database-" + DatabaseCumulativeReleases },
		"source volume":              func(v *DatabaseTask) { v.SourceOwner = &v.VolumeOwner },
		"missing original Control":   func(v *DatabaseTask) { v.Peers = map[string]string{"postgres": v.Peers["postgres"]} },
		"native recovery mix": func(v *DatabaseTask) {
			v.NativePostgreSQLRecovery = nativeRecoveryFixture(NativePostgreSQLRestore).NativePostgreSQLRecovery
		},
	} {
		t.Run(name, func(t *testing.T) {
			task := mvpUpgradeTaskFixture()
			mutate(&task)
			if task.valid() {
				t.Fatal("foreign command/owner or source accepted")
			}
		})
	}
}
func TestMVPUpgradeTaskUnixRequiredInputsAndOneStart(t *testing.T) {
	for _, fault := range []string{"", "start-lost", "running-control"} {
		t.Run(fault, func(t *testing.T) {
			task := mvpUpgradeTaskFixture()
			files := []ConfigurationFile{{Name: "control.yaml", Contents: []byte("synthetic operator")}, {Name: "runtime.yaml", Contents: []byte("synthetic runtime")}}
			c, task, _, effects, files := mvpFixedTaskLifecycleFixture(t, task, fault, files, []string{"upgrade-schema", "--config", DatabaseTaskDestination + "/private/control.yaml", "--runtime-config", DatabaseTaskDestination + "/private/runtime.yaml"}, task.NetworkID)
			ctx := context.Background()
			created, e := c.CreateDatabaseTask(ctx, task)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.startDatabaseTask(ctx, task, created.ID, files[:1], 30, databaseAdmit); e == nil || effects.Load() != 1 {
				t.Fatal("missing runtime input authorized a start")
			}
			_, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit)
			if fault == "running-control" {
				if e == nil || effects.Load() != 1 {
					t.Fatal("live original Control authorized upgrade")
				}
				return
			}
			if (e != nil) != (fault == "start-lost") || effects.Load() != 2 {
				t.Fatal("upgrade start state incorrect", e)
			}
			if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || effects.Load() != 2 {
				t.Fatal("upgrade start replayed")
			}
			if _, e = c.CreateDatabaseTask(ctx, task); e == nil || effects.Load() != 2 {
				t.Fatal("existing upgrade actor replaced")
			}
			if e = c.StopDatabaseTask(ctx, task, created.ID, 5); e != nil {
				t.Fatal(e)
			}
			if e = c.RemoveDatabaseTask(ctx, task, created.ID); e != nil || effects.Load() != 4 {
				t.Fatal("normal task cleanup failed", e)
			}
		})
	}
}

// Generic SQL-task double preserves actual Docker image-environment inheritance;
// the independent native-specific lifecycle fixtures remain unchanged.
func mvpFixedTaskLifecycleFixture(t *testing.T, task DatabaseTask, fault string, files []ConfigurationFile, command []string, networkMode string) (*Client, DatabaseTask, *atomic.Int32, *atomic.Int32, []ConfigurationFile) {
	t.Helper()
	expectedCommand := []any{}
	for _, arg := range command {
		expectedCommand = append(expectedCommand, arg)
	}
	resources := wire.UtilityResources{MemoryBytes: 96 << 20, MilliCPU: 225, PIDs: 23}
	var state, effects atomic.Int32
	var specification atomic.Value
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
			// Docker inherits image environment when a generic create omits Env.
			if config["Env"] == nil {
				config["Env"] = []string{"IMAGE_SETTING=value"}
			}
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
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/start"):
			if state.Load() != 1 {
				t.Error("start replay")
			}
			state.Store(2)
			effects.Add(1)
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
