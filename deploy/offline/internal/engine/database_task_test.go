package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// databaseTaskFixture is an explicit synthetic Unix Engine HTTP double.
func databaseTaskFixture(t *testing.T, fault string, kinds ...string) (*Client, DatabaseTask, *atomic.Int32, *atomic.Int32, []ConfigurationFile) {
	t.Helper()
	task := DatabaseTask{Owner: Ownership{strings.Repeat("a", 32), strings.Repeat("b", 32), "database-migrate"}, VolumeOwner: Ownership{strings.Repeat("a", 32), strings.Repeat("c", 32), "operator-config"}, Kind: DatabaseMigrate, Image: "sha256:" + strings.Repeat("d", 64), UID: 11033, GID: 11034, NetworkID: strings.Repeat("9", 64), Peers: map[string]string{"postgres": strings.Repeat("8", 64)}}
	if len(kinds) == 1 {
		task.Kind = kinds[0]
		task.Owner.Role = "database-" + task.Kind
	}
	if task.bootstrap() {
		task.OutputOwner = &Ownership{task.Owner.ScopeID, strings.Repeat("f", 32), BootstrapStateRole}
	}
	if p, ok := storeTask(task.Kind); ok {
		task.OutputOwner = &Ownership{task.Owner.ScopeID, strings.Repeat("f", 32), p.role}
		task.Peers["control"] = strings.Repeat("7", 64)
		if p.mode != "check" {
			task.Reason = "Synthetic isolated namespace initialization"
		}
	}
	if p, ok := storeBackupTask(task.Kind); ok {
		task.VolumeOwner = Ownership{task.Owner.ScopeID, task.Owner.OperationID, StoreBackupInputRole + "-" + task.Owner.OperationID}
		task.OutputOwner = &Ownership{task.Owner.ScopeID, task.Owner.OperationID, "backup-" + p.role + "-" + task.Owner.OperationID}
		task.SourceOwner = &Ownership{task.Owner.ScopeID, strings.Repeat("f", 32), p.role}
		task.Peers["control"] = strings.Repeat("7", 64)
		task.Reason = "Synthetic isolated store snapshot"
	}
	if fault == "control-running" {
		task.Peers["control"] = strings.Repeat("7", 64)
	}
	if strings.HasPrefix(fault, "locale-") {
		task.LocalizationOwner = &Ownership{task.Owner.ScopeID, strings.Repeat("2", 32), LocalizationConfigurationRole}
		task.Peers["control"] = strings.Repeat("7", 64)
	}
	resources := wire.UtilityResources{MemoryBytes: 96 << 20, MilliCPU: 225, PIDs: 23}
	var state, posts, reads atomic.Int32
	var changedPeer atomic.Bool
	files := []ConfigurationFile{{Name: "control.yaml", Contents: []byte("synthetic-config")}, {Name: "operator-dsn", Contents: []byte("synthetic-private-input")}}
	if task.Kind == DatabaseRuntimePrepare || task.Kind == DatabaseRuntimeCheck {
		files = append(files, ConfigurationFile{Name: "runtime.yaml", Contents: []byte("synthetic-runtime-config")})
	}
	if task.bootstrap() {
		files = append(files, ConfigurationFile{Name: "bootstrap-request.json", Contents: []byte("synthetic-request")})
	}
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
		disconnect := func() {
			conn, _, e := w.(http.Hijacker).Hijack()
			if e == nil {
				conn.Close()
			}
		}
		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/images/"):
			entry := []string{modelTaskEntrypoint}
			volumes := map[string]any{}
			if fault == "image-entrypoint" {
				entry = []string{"/bin/sh"}
			}
			if fault == "implicit-volume" {
				volumes["/foreign"] = map[string]any{}
			}
			write(map[string]any{"Id": task.Image, "Os": "linux", "Architecture": "amd64", "RootFS": map[string]any{"Type": "layers", "Layers": []string{}}, "Config": map[string]any{"Entrypoint": entry, "Env": []string{"IMAGE_SETTING=value"}, "Volumes": volumes}})
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/volumes/"):
			owner := task.VolumeOwner
			if task.OutputOwner != nil && strings.Contains(r.URL.Path, task.OutputOwner.VolumeName()) {
				owner = *task.OutputOwner
			}
			if task.SourceOwner != nil && strings.Contains(r.URL.Path, task.SourceOwner.VolumeName()) {
				owner = *task.SourceOwner
			}
			if task.LocalizationOwner != nil && strings.Contains(r.URL.Path, task.LocalizationOwner.VolumeName()) {
				owner = *task.LocalizationOwner
				if fault == "locale-missing" {
					w.WriteHeader(404)
					return
				}
			}
			labels := owner.labels()
			if fault == "volume-owner" {
				labels["io.projectone.operation"] = "foreign"
			}
			write(Volume{Name: owner.VolumeName(), Driver: "local", Scope: "local", Labels: labels})
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/networks/"):
			n := RuntimeNetwork{ID: task.NetworkID, Name: RuntimeNetworkName(task.Owner.ScopeID), Driver: "bridge", Scope: "local", Internal: true, Labels: map[string]string{"io.projectone.isolated-scope": task.Owner.ScopeID, "io.projectone.purpose": "isolated-validation", "com.docker.compose.project": RuntimeProject(task.Owner.ScopeID), "com.docker.compose.network": "isolated"}, Containers: map[string]struct{ Name string }{task.Peers["postgres"]: {Name: RuntimeContainerName(task.Owner.ScopeID, "postgres")}}}
			if state.Load() == 2 {
				n.Containers[strings.Repeat("e", 64)] = struct{ Name string }{task.name()}
			}
			switch fault {
			case "network-external":
				n.Internal = false
			case "network-id":
				n.ID = strings.Repeat("6", 64)
			case "network-label":
				n.Labels["com.docker.compose.project"] = "foreign"
			case "network-options":
				n.Options = map[string]string{"foreign": "true"}
			case "foreign-endpoint":
				n.Containers[strings.Repeat("f", 64)] = struct{ Name string }{"foreign"}
			case "endpoint-name":
				n.Containers[task.Peers["postgres"]] = struct{ Name string }{"foreign"}
			}
			write(n)
		case r.Method == "GET" && r.URL.Path == "/v1.49/containers/json":
			if task.LocalizationOwner != nil && strings.Contains(r.URL.Query().Get("filters"), task.LocalizationOwner.VolumeName()) {
				objects := []RuntimeObject{{ID: task.Peers["control"]}}
				if fault == "locale-foreign-consumer" {
					objects = append(objects, RuntimeObject{ID: strings.Repeat("6", 64)})
				}
				if state.Load() > 0 && state.Load() < 4 {
					objects = append(objects, RuntimeObject{ID: strings.Repeat("e", 64)})
				}
				write(objects)
				return
			}
			if task.SourceOwner != nil && strings.Contains(r.URL.Query().Get("filters"), task.SourceOwner.VolumeName()) {
				if fault == "backup-foreign-source-consumer" {
					write([]RuntimeObject{{ID: strings.Repeat("6", 64)}})
					return
				}
				objects := []RuntimeObject{{ID: task.Peers["control"]}}
				if state.Load() > 0 && state.Load() < 4 {
					objects = append(objects, RuntimeObject{ID: strings.Repeat("e", 64)})
				}
				write(objects)
				return
			}
			if p, ok := storeTask(task.Kind); ok && strings.Contains(r.URL.Query().Get("filters"), task.OutputOwner.VolumeName()) && fault != "foreign-consumer" {
				_ = p
				if fault == "store-foreign-consumer" {
					write([]RuntimeObject{{ID: strings.Repeat("6", 64)}})
					return
				}
				objects := []RuntimeObject{{ID: task.Peers["control"]}}
				if state.Load() > 0 && state.Load() < 4 {
					objects = append(objects, RuntimeObject{ID: strings.Repeat("e", 64)})
				}
				write(objects)
				return
			}

			if fault == "foreign-consumer" {
				write([]RuntimeObject{{ID: strings.Repeat("f", 64)}})
			} else if state.Load() > 0 && state.Load() < 4 {
				write([]RuntimeObject{{ID: strings.Repeat("e", 64)}})
			} else {
				write([]RuntimeObject{})
			}
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/archive"):
			reads.Add(1)
			if _, ok := storeBackupTask(task.Kind); ok && r.URL.Query().Get("path") == StoreBackupDestination {
				payload := []ConfigurationFile{}
				if fault == "backup-nonempty" {
					payload = append(payload, ConfigurationFile{Name: "existing", Contents: []byte("preserve")})
				}
				var out bytes.Buffer
				tw := tar.NewWriter(&out)
				tw.WriteHeader(&tar.Header{Name: "files-backup", Typeflag: tar.TypeDir, Mode: 0755})
				tw.WriteHeader(&tar.Header{Name: "files-backup/private", Typeflag: tar.TypeDir, Mode: 0700, Uid: task.UID, Gid: task.GID})
				for _, f := range payload {
					tw.WriteHeader(&tar.Header{Name: "files-backup/private/" + f.Name, Typeflag: tar.TypeReg, Mode: 0600, Uid: task.UID, Gid: task.GID, Size: int64(len(f.Contents))})
					io.Copy(tw, bytes.NewReader(f.Contents))
				}
				tw.Close()
				w.Write(out.Bytes())
				return
			}
			if p, ok := storeTask(task.Kind); ok && r.URL.Query().Get("path") == p.destination() {
				w.Write(storeArchiveTestBytes(t, p, task.UID, task.GID, fault))
				if fault == "store-state-change" {
					state.Store(2)
				}
				if fault == "store-peer-change" {
					changedPeer.Store(true)
				}
				return
			}
			if r.URL.Query().Get("path") == BootstrapTaskDestination && task.bootstrap() {
				b := bootstrapArchiveTestBytes(t, task.UID, task.GID, fault)
				w.Write(b)
				if fault == "output-state-change" {
					state.Store(2)
				}
				return
			}
			if r.URL.Query().Get("path") != DatabaseTaskDestination {
				t.Error("arbitrary input path")
			}
			if fault == "archive-response" {
				w.WriteHeader(500)
				return
			}
			var data bytes.Buffer
			tw := tar.NewWriter(&data)
			tw.WriteHeader(&tar.Header{Name: "operator", Typeflag: tar.TypeDir, Mode: 0755})
			mode, uid := int64(0700), task.UID
			if fault == "input-mode" {
				mode = 0755
			}
			if fault == "input-owner" {
				uid++
			}
			tw.WriteHeader(&tar.Header{Name: "operator/private", Typeflag: tar.TypeDir, Mode: mode, Uid: uid, Gid: task.GID})
			for _, file := range files {
				b := file.Contents
				if fault == "input-bytes" {
					b = []byte("changed")
				}
				tw.WriteHeader(&tar.Header{Name: "operator/private/" + file.Name, Typeflag: tar.TypeReg, Mode: 0600, Uid: task.UID, Gid: task.GID, Size: int64(len(b))})
				tw.Write(b)
			}
			if fault == "input-extra" {
				tw.WriteHeader(&tar.Header{Name: "operator/private/extra", Typeflag: tar.TypeReg, Mode: 0600, Uid: task.UID, Gid: task.GID})
			}
			tw.Close()
			w.Write(data.Bytes())
			if fault == "input-state-change" {
				state.Store(3)
			}
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/json"):
			for role, id := range task.Peers {
				if strings.Contains(r.URL.Path, RuntimeContainerName(task.Owner.ScopeID, role)+"/json") {
					if fault == "missing-peer" {
						w.WriteHeader(404)
						return
					}
					peer := RuntimeContainer{ID: id, Name: "/" + RuntimeContainerName(task.Owner.ScopeID, role)}
					peer.Config.Labels = map[string]string{"io.projectone.isolated-scope": task.Owner.ScopeID, "io.projectone.purpose": "isolated-validation", "com.docker.compose.project": RuntimeProject(task.Owner.ScopeID), "com.docker.compose.service": role}
					peer.State.Status = "running"
					peer.State.Running = true
					peer.State.PID = 42
					if fault == "peer-id" {
						peer.ID = strings.Repeat("6", 64)
					}
					if fault == "peer-label" {
						peer.Config.Labels["io.projectone.isolated-scope"] = "foreign"
					}
					if fault == "postgres-stopped" {
						peer.State.Status = "exited"
						peer.State.Running = false
						peer.State.PID = 0
					}
					if role != "postgres" && fault != "control-running" {
						peer.State.Status = "created"
						peer.State.Running = false
						peer.State.PID = 0
						peer.State.StartedAt = neverStarted
					}
					if role == "control" && changedPeer.Load() {
						peer.State.Status = "running"
						peer.State.Running = true
						peer.State.PID = 43
					}
					write(peer)
					return
				}
			}
			if state.Load() == 0 || state.Load() == 4 {
				w.WriteHeader(404)
				return
			}
			raw, _ := json.Marshal(map[string]any{"Id": strings.Repeat("e", 64), "Name": "/" + task.name(), "Image": task.Image, "Platform": "linux", "Config": map[string]any{"Image": task.Image, "User": "11033:11034", "Entrypoint": []string{modelTaskEntrypoint}, "Cmd": task.command(), "WorkingDir": "/", "Labels": task.Owner.labels(), "Env": []string{"IMAGE_SETTING=value"}}, "State": map[string]any{"Status": "created", "StartedAt": neverStarted}, "HostConfig": map[string]any{"NetworkMode": task.NetworkID, "IpcMode": "private", "CgroupnsMode": "private", "Runtime": "runc", "ReadonlyRootfs": true, "Memory": resources.MemoryBytes, "MemorySwap": resources.MemoryBytes, "NanoCpus": resources.MilliCPU * nanosecondsPerMillicore, "PidsLimit": resources.PIDs, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "RestartPolicy": map[string]string{"Name": "no"}, "LogConfig": map[string]any{"Type": "none"}, "Mounts": []any{map[string]any{"Type": "volume", "Source": task.VolumeOwner.VolumeName(), "Target": DatabaseTaskDestination, "ReadOnly": true, "VolumeOptions": map[string]bool{"NoCopy": true}}}}, "Mounts": []any{map[string]any{"Type": "volume", "Name": task.VolumeOwner.VolumeName(), "Destination": DatabaseTaskDestination, "Driver": "local", "RW": false}}, "NetworkSettings": map[string]any{"Networks": map[string]any{RuntimeNetworkName(task.Owner.ScopeID): map[string]string{"NetworkID": task.NetworkID}}}})
			var value RuntimeContainer
			json.Unmarshal(raw, &value)
			if task.LocalizationOwner != nil {
				mount := value.HostConfig.Mounts[0]
				actual := value.Mounts[0]
				mount.Source, mount.Target = task.LocalizationOwner.VolumeName(), LocalizationTaskDestination
				actual.Name, actual.Destination = mount.Source, mount.Target
				if fault == "locale-request-rw" {
					mount.ReadOnly = false
				}
				if fault == "locale-actual-rw" {
					actual.RW = true
				}
				if fault == "locale-nocopy" {
					options := *mount.VolumeOptions
					options.NoCopy = false
					mount.VolumeOptions = &options
				}
				if fault == "locale-target" {
					mount.Target = "/foreign"
				}
				value.HostConfig.Mounts = append(value.HostConfig.Mounts, mount)
				value.Mounts = append(value.Mounts, actual)
			}
			if task.OutputOwner != nil {
				destination, readOnly := task.outputProfile()
				mount := value.HostConfig.Mounts[0]
				mount.Source = task.OutputOwner.VolumeName()
				mount.Target = destination
				mount.ReadOnly = readOnly
				actual := value.Mounts[0]
				actual.Name = task.OutputOwner.VolumeName()
				actual.Destination = destination
				actual.RW = !mount.ReadOnly
				if fault == "output-mount-rw" {
					mount.ReadOnly = !mount.ReadOnly
				}
				if fault == "output-actual-rw" {
					actual.RW = !actual.RW
				}
				if fault == "output-nocopy" {
					mount.VolumeOptions.NoCopy = false
				}
				if fault == "output-mount-name" {
					mount.Source = "foreign"
				}
				value.HostConfig.Mounts = append(value.HostConfig.Mounts, mount)
				value.Mounts = append(value.Mounts, actual)
			}
			if task.SourceOwner != nil {
				mount := value.HostConfig.Mounts[0]
				actual := value.Mounts[0]
				mount.Source, mount.Target, mount.ReadOnly = task.SourceOwner.VolumeName(), "/var/lib/projectone/"+task.SourceOwner.Role, false
				actual.Name, actual.Destination, actual.RW = mount.Source, mount.Target, true
				if fault == "backup-source-mount" {
					mount.Source = "foreign"
				}
				value.HostConfig.Mounts = append(value.HostConfig.Mounts, mount)
				value.Mounts = append(value.Mounts, actual)
			}
			if state.Load() == 2 {
				value.State.Status = "running"
				value.State.Running = true
				value.State.PID = 99
				value.State.StartedAt = "2026-10-17T01:00:00Z"
			}
			if state.Load() == 3 {
				value.State.Status = "exited"
				value.State.StartedAt = "2026-10-17T01:00:00Z"
			}
			switch fault {
			case "task-id":
				value.ID = strings.Repeat("5", 64)
			case "task-command":
				value.Config.Cmd = []string{"serve"}
			case "task-env":
				value.Config.Env = append(value.Config.Env, "SECRET=foreign")
			case "task-port":
				value.HostConfig.PublishAllPorts = true
			case "task-root":
				value.HostConfig.ReadonlyRootfs = false
			case "task-privilege":
				value.HostConfig.Privileged = true
			case "task-capability":
				value.HostConfig.CapAdd = []string{"SYS_ADMIN"}
			case "task-resource":
				value.HostConfig.Memory++
			case "task-swap":
				value.HostConfig.MemorySwap = -1
			case "task-log":
				value.HostConfig.LogConfig.Type = "json-file"
			case "task-mount-rw":
				value.HostConfig.Mounts[0].ReadOnly = false
			case "task-actual-rw":
				value.Mounts[0].RW = true
			case "task-nocopy":
				value.HostConfig.Mounts[0].VolumeOptions.NoCopy = false
			case "task-extra-network":
				value.NetworkSettings.Networks["bridge"] = struct{ NetworkID string }{"foreign"}
			case "task-pid":
				value.HostConfig.PidMode = "host"
			}
			write(value)
		case r.Method == "POST" && r.URL.Path == "/v1.49/containers/create":
			posts.Add(1)
			state.Store(1)
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			expected := []any{task.Kind, "--config", "/run/projectone/operator/private/control.yaml"}
			if task.Kind == DatabaseRuntimePrepare || task.Kind == DatabaseRuntimeCheck {
				expected = append(expected, "--runtime-config", "/run/projectone/operator/private/runtime.yaml")
			}
			if task.bootstrap() {
				for _, v := range []string{"--bootstrap-input", DatabaseTaskDestination + "/private/bootstrap-request.json", "--credential-output", BootstrapTaskDestination + "/private/" + BootstrapCredentialFile} {
					expected = append(expected, v)
				}
			}
			for _, v := range task.storeArguments() {
				expected = append(expected, v)
			}
			for _, v := range task.storeBackupArguments() {
				expected = append(expected, v)
			}
			if body["Image"] != task.Image || body["User"] != "11033:11034" || body["Env"] != nil || !reflect.DeepEqual(body["Cmd"], expected) || r.URL.Query().Get("platform") != "linux/amd64" || r.URL.Query().Get("name") != task.name() {
				t.Error("arbitrary command or unbound request")
			}
			if fault == "create-disconnect" {
				disconnect()
				return
			}
			w.WriteHeader(201)
			write(map[string]any{"Id": strings.Repeat("e", 64), "Warnings": []string{}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/start"):
			posts.Add(1)
			state.Store(2)
			if fault == "start-disconnect" {
				disconnect()
				return
			}
			w.WriteHeader(204)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/stop"):
			posts.Add(1)
			state.Store(3)
			if r.URL.Query().Get("t") != "3" {
				t.Error("stop grace changed")
			}
			if fault == "stop-disconnect" {
				disconnect()
				return
			}
			w.WriteHeader(204)
		case r.Method == "DELETE":
			posts.Add(1)
			state.Store(4)
			if r.URL.Query().Get("force") != "0" || r.URL.Query().Get("v") != "0" {
				t.Error("destructive removal")
			}
			if fault == "remove-disconnect" {
				disconnect()
				return
			}
			w.WriteHeader(204)
		default:
			t.Error("unexpected database task route", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	c.config.Utility = &resources
	c.config.ResponseBytes = 65536
	return c, task, &state, &posts, files
}
func databaseAdmit(_ context.Context, r ResourceRequirement, reserve int) (ResourceAdmission, error) {
	if r.MemoryBytes != 96<<20 || r.MilliCPU != 225 || reserve != 30 {
		return ResourceAdmission{}, errors.New("BAD_TEST_ADMISSION")
	}
	return ResourceAdmission{}, nil
}
func TestDatabaseTaskFixedLifecycleAndNondefaultBounds(t *testing.T) {
	for _, kind := range []string{DatabaseMigrate, DatabaseSchemaCheck, DatabaseRuntimePrepare, DatabaseRuntimeCheck, DatabaseBootstrap, DatabaseBootstrapReconcile, DatabaseBootstrapResume} {
		t.Run(kind, func(t *testing.T) {
			c, task, state, posts, files := databaseTaskFixture(t, "", kind)
			ctx := context.Background()
			created, e := c.CreateDatabaseTask(ctx, task)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.CreateDatabaseTask(ctx, task); e == nil || posts.Load() != 1 {
				t.Fatal("creation replay", e)
			}
			if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e != nil {
				t.Fatal(e)
			}
			if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || posts.Load() != 2 {
				t.Fatal("start replay", e)
			}
			if e = c.RemoveDatabaseTask(ctx, task, created.ID); e == nil {
				t.Fatal("running task removed")
			}
			if e = c.StopDatabaseTask(ctx, task, created.ID, 3); e != nil {
				t.Fatal(e)
			}
			if _, e = c.InspectDatabaseTaskInput(ctx, task, created.ID, files); e != nil {
				t.Fatal(e)
			}
			if e = c.RemoveDatabaseTask(ctx, task, created.ID); e != nil || state.Load() != 4 || posts.Load() != 4 {
				t.Fatal(e, state.Load(), posts.Load())
			}
		})
	}
}
func TestDatabaseTaskRejectsContextAndForeignResources(t *testing.T) {
	for _, fault := range []string{"image-entrypoint", "implicit-volume", "volume-owner", "network-external", "network-id", "network-label", "network-options", "foreign-endpoint", "endpoint-name", "missing-peer", "peer-id", "peer-label", "foreign-consumer"} {
		t.Run(fault, func(t *testing.T) {
			c, task, _, posts, _ := databaseTaskFixture(t, fault)
			if _, e := c.CreateDatabaseTask(context.Background(), task); e == nil || posts.Load() != 0 {
				t.Fatal("unsafe creation", e, posts.Load())
			}
		})
	}
	for _, change := range []func(*DatabaseTask){func(t *DatabaseTask) { t.Kind = "serve" }, func(t *DatabaseTask) { t.Owner.Role = "models" }, func(t *DatabaseTask) { t.VolumeOwner.Role = "control-config" }, func(t *DatabaseTask) { t.Peers["unknown"] = strings.Repeat("6", 64) }, func(t *DatabaseTask) { delete(t.Peers, "postgres") }, func(t *DatabaseTask) { t.Peers["control"] = t.Peers["postgres"] }, func(t *DatabaseTask) { t.NetworkID = "foreign" }} {
		c, task, _, posts, _ := databaseTaskFixture(t, "")
		change(&task)
		if _, e := c.CreateDatabaseTask(context.Background(), task); e == nil || posts.Load() != 0 {
			t.Fatal("invalid context mutation", e)
		}
	}
}
func TestDatabaseTaskRejectsChangedContainerWithoutFurtherMutation(t *testing.T) {
	for _, fault := range []string{"task-id", "task-command", "task-env", "task-port", "task-root", "task-privilege", "task-capability", "task-resource", "task-swap", "task-log", "task-mount-rw", "task-actual-rw", "task-nocopy", "task-extra-network", "task-pid"} {
		t.Run(fault, func(t *testing.T) {
			c, task, _, posts, files := databaseTaskFixture(t, fault)
			if _, e := c.CreateDatabaseTask(context.Background(), task); e == nil || posts.Load() != 1 {
				t.Fatal("changed creation accepted", e)
			}
			if _, e := c.startDatabaseTask(context.Background(), task, strings.Repeat("e", 64), files, 30, databaseAdmit); e == nil || posts.Load() != 1 {
				t.Fatal("changed task started", e)
			}
		})
	}
}
func TestDatabaseTaskInputPeerAdmissionAndExactIDBeforeStart(t *testing.T) {
	for _, fault := range []string{"input-mode", "input-owner", "input-bytes", "input-extra", "input-state-change", "archive-response", "postgres-stopped", "control-running"} {
		t.Run(fault, func(t *testing.T) {
			c, task, _, posts, files := databaseTaskFixture(t, fault)
			created, e := c.CreateDatabaseTask(context.Background(), task)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.startDatabaseTask(context.Background(), task, strings.Repeat("e", 64), files, 30, databaseAdmit); e == nil || posts.Load() != 1 {
				t.Fatal("unsafe start", created.ID, e)
			}
		})
	}
	c, task, _, posts, files := databaseTaskFixture(t, "")
	created, e := c.CreateDatabaseTask(context.Background(), task)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.startDatabaseTask(context.Background(), task, created.ID, files, 30, func(context.Context, ResourceRequirement, int) (ResourceAdmission, error) {
		return ResourceAdmission{}, errors.New("TEST_HEADROOM_REFUSED")
	}); e == nil || posts.Load() != 1 {
		t.Fatal("headroom refusal ignored")
	}
	if _, e = c.startDatabaseTask(context.Background(), task, created.ID, files[1:], 30, databaseAdmit); e == nil || posts.Load() != 1 {
		t.Fatal("missing control configuration accepted")
	}
	if e = c.StopDatabaseTask(context.Background(), task, created.ID, 0); e == nil || posts.Load() != 1 {
		t.Fatal("invalid grace accepted")
	}
}
func TestDatabaseTaskUncertainEffectsNeverRetry(t *testing.T) {
	for _, fault := range []string{"create-disconnect", "start-disconnect", "stop-disconnect", "remove-disconnect"} {
		t.Run(fault, func(t *testing.T) {
			c, task, state, posts, files := databaseTaskFixture(t, fault)
			ctx := context.Background()
			created, e := c.CreateDatabaseTask(ctx, task)
			if fault == "create-disconnect" {
				if e == nil || posts.Load() != 1 {
					t.Fatal(e)
				}
				if _, e = c.CreateDatabaseTask(ctx, task); e == nil || posts.Load() != 1 {
					t.Fatal("uncertain create repeated")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			_, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit)
			if fault == "start-disconnect" {
				if e == nil || posts.Load() != 2 {
					t.Fatal(e)
				}
				if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || posts.Load() != 2 {
					t.Fatal("uncertain start repeated")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if fault == "stop-disconnect" {
				if e = c.StopDatabaseTask(ctx, task, created.ID, 3); e == nil || posts.Load() != 3 {
					t.Fatal(e)
				}
				if e = c.StopDatabaseTask(ctx, task, created.ID, 3); e == nil || posts.Load() != 3 {
					t.Fatal("uncertain stop repeated")
				}
				return
			}
			state.Store(3)
			if e = c.RemoveDatabaseTask(ctx, task, created.ID); e == nil || posts.Load() != 3 {
				t.Fatal(e)
			}
			if e = c.RemoveDatabaseTask(ctx, task, created.ID); e == nil || posts.Load() != 3 {
				t.Fatal("uncertain removal repeated")
			}
		})
	}
}

func TestDatabaseTaskCancellationAndUnknownIDPreserveEffects(t *testing.T) {
	c, task, _, posts, files := databaseTaskFixture(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := c.CreateDatabaseTask(ctx, task); e == nil || posts.Load() != 0 {
		t.Fatal("cancelled create mutated")
	}
	created, e := c.CreateDatabaseTask(context.Background(), task)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.startDatabaseTask(ctx, task, created.ID, files, 30, databaseAdmit); e == nil || posts.Load() != 1 {
		t.Fatal("cancelled start mutated")
	}
	if _, e = c.startDatabaseTask(context.Background(), task, strings.Repeat("f", 64), files, 30, databaseAdmit); e == nil || posts.Load() != 1 {
		t.Fatal("unknown task started")
	}
	if e = c.RemoveDatabaseTask(context.Background(), task, strings.Repeat("f", 64)); e == nil || posts.Load() != 1 {
		t.Fatal("unknown task removed")
	}
}

func TestRuntimeAccessTaskRequiresItsPrivateRuntimeConfiguration(t *testing.T) {
	for _, kind := range []string{DatabaseRuntimePrepare, DatabaseRuntimeCheck} {
		t.Run(kind, func(t *testing.T) {
			c, task, _, posts, files := databaseTaskFixture(t, "", kind)
			created, e := c.CreateDatabaseTask(context.Background(), task)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.startDatabaseTask(context.Background(), task, created.ID, files[:2], 30, databaseAdmit); e == nil || posts.Load() != 1 {
				t.Fatal("runtime input missing but task started", e)
			}
		})
	}
}
