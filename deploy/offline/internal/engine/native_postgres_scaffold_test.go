package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func scaffoldTaskFixture(kind string) DatabaseTask {
	if kind == NativePostgreSQLBackupVerify {
		return nativeBackupVerifierTaskFixture()
	}
	t := nativeRecoveryFixture(kind)
	owner := Ownership{t.Owner.ScopeID, t.Owner.OperationID, NativePostgreSQLScaffoldRole + "-" + t.Owner.OperationID}
	t.NativePostgreSQLRecovery.Scaffold = &NativePostgreSQLVerifierPlaceholder{Owner: owner, Target: "/registry"}
	if kind == NativePostgreSQLBackup {
		t.NativePostgreSQLRecovery.Registry = &NativePostgreSQLRegistryMount{Owner: Ownership{t.Owner.ScopeID, strings.Repeat("0", 32), NativePostgreSQLRegistryRole}, Target: "/registry/cas"}
	} else {
		// Exercise all original store/record ancestors without inventing a CAS
		// claim for a backup reference which declares no registry payload.
		t.NativePostgreSQLRecovery.Scaffold.Target = "/fixture"
		t.NativePostgreSQLRecovery.RestoreRequest.Policy.MaximumManifestBytes = 16384
	}
	return t
}

func scaffoldArchiveFixture(t *testing.T, task DatabaseTask, plan nativePostgreSQLScaffoldPlan, fault string, empty bool) []byte {
	t.Helper()
	root := path.Base(UtilityDestination)
	headers := []*tar.Header{{Name: root, Typeflag: tar.TypeDir, Mode: 0755}}
	if !empty {
		for _, name := range plan.directories {
			headers = append(headers, &tar.Header{Name: root + "/" + name, Typeflag: tar.TypeDir, Mode: 0700, Uid: task.UID, Gid: task.GID})
		}
		switch fault {
		case "file":
			headers[1].Typeflag = tar.TypeReg
			headers[1].Size = 1
		case "symlink":
			headers[1].Typeflag = tar.TypeSymlink
			headers[1].Linkname = "/foreign"
		case "hardlink":
			headers[1].Typeflag = tar.TypeLink
			headers[1].Linkname = "/foreign"
		case "extra":
			headers = append(headers, &tar.Header{Name: root + "/private/foreign", Typeflag: tar.TypeDir, Mode: 0700, Uid: task.UID, Gid: task.GID})
		case "duplicate":
			headers = append(headers, headers[1])
		case "partial":
			headers = headers[:len(headers)-1]
		case "mode":
			headers[1].Mode = 0755
		case "uid":
			headers[1].Uid++
		case "gid":
			headers[1].Gid++
		case "wrapper":
			headers[0].Uid = task.UID
		case "escape":
			headers[1].Name = root + "/private/../private"
		case "absolute":
			headers[1].Name = "/" + headers[1].Name
		case "double_slash":
			headers[1].Name += "//"
		case "xattr":
			headers[1].PAXRecords = map[string]string{"SCHILY.xattr.user.foreign": "untrusted"}
		}
	}
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	for _, header := range headers {
		if e := writer.WriteHeader(header); e != nil {
			t.Fatal(e)
		}
		if header.Size != 0 {
			if _, e := writer.Write([]byte("x")); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	result := raw.Bytes()
	if fault == "truncated" {
		result = result[:len(result)-512]
	}
	if fault == "trailing" {
		result = append(result, append(bytes.Repeat([]byte{1}, 512), make([]byte, 1024)...)...)
	}
	if fault == "oversized" {
		result = append(result, make([]byte, int(plan.maximumMetadata))...)
	}
	return result
}

func TestNativePostgreSQLScaffoldDerivesOnlyFrozenActualMountChildren(t *testing.T) {
	for _, kind := range []string{NativePostgreSQLBackup, NativePostgreSQLRestore, NativePostgreSQLRestoreVerify, NativePostgreSQLBackupVerify} {
		t.Run(kind, func(t *testing.T) {
			task := scaffoldTaskFixture(kind)
			_, plan, e := nativePostgreSQLScaffoldContext(task)
			if e != nil {
				t.Fatal(e)
			}
			if kind == NativePostgreSQLBackup && !reflect.DeepEqual(plan.directories, []string{"private", "private/cas"}) {
				t.Fatal("unused prepared root or arbitrary directory invented", plan.directories)
			}
			if kind == NativePostgreSQLBackupVerify && !reflect.DeepEqual(plan.directories, []string{"private", "private/backups", "private/backups/source", "private/scratch"}) {
				t.Fatal("original verifier mount ancestry omitted", plan.directories)
			}
			if kind == NativePostgreSQLRestore || kind == NativePostgreSQLRestoreVerify {
				for _, expected := range []string{"private/configuration", "private/stores/models", "private/stores/datasets", "private/stores/training", "private/stores/preparations", "private/restores"} {
					found := false
					for _, name := range plan.directories {
						found = found || name == expected
					}
					if !found {
						t.Fatal("actual mount ancestor omitted", expected)
					}
				}
			}
		})
	}
	task := scaffoldTaskFixture(NativePostgreSQLBackup)
	_, before, e := nativePostgreSQLScaffoldContext(task)
	if e != nil {
		t.Fatal(e)
	}
	task.NativePostgreSQLRecovery.PreparedRelease = &NativePostgreSQLPreparedMount{Owner: Ownership{task.Owner.ScopeID, task.Owner.OperationID, NativePostgreSQLPreparedRole + "-" + task.Owner.OperationID}, Target: "/registry/prepared"}
	_, after, e := nativePostgreSQLScaffoldContext(task)
	if e != nil {
		t.Fatal(e)
	}
	if len(after.directories) != len(before.directories)+1 || after.directories[len(after.directories)-1] != "private/prepared" {
		t.Fatal("actual optional mount was not distinguished from its absent configuration", after.directories)
	}
}

func TestNativePostgreSQLScaffoldStrictWholeArchiveReadback(t *testing.T) {
	task := scaffoldTaskFixture(NativePostgreSQLBackup)
	_, plan, e := nativePostgreSQLScaffoldContext(task)
	if e != nil {
		t.Fatal(e)
	}
	for _, fault := range []string{"", "file", "symlink", "hardlink", "extra", "duplicate", "partial", "mode", "uid", "gid", "wrapper", "escape", "absolute", "double_slash", "xattr", "truncated", "trailing", "oversized"} {
		t.Run(fault, func(t *testing.T) {
			result, e := inspectNativePostgreSQLScaffoldArchive(scaffoldArchiveFixture(t, task, plan, fault, false), task, plan)
			if fault == "" {
				if e != nil || result.Status != ConfigurationPayloadVerified || result.Files != 0 || result.Bytes != 0 || !strings.Contains(result.Qualification, "not_durability_prepared_release_CAS") {
					t.Fatal("empty directories promoted to content/device proof", result, e)
				}
			} else if e == nil {
				t.Fatal("unsafe whole-volume archive admitted", fault)
			}
		})
	}
	result, e := inspectNativePostgreSQLScaffoldArchive(scaffoldArchiveFixture(t, task, plan, "", true), task, plan)
	if e != nil || result.Status != "observed_empty_owned_volume" {
		t.Fatal("genuine empty original volume refused", result, e)
	}
}

type scaffoldHTTPState struct {
	puts        atomic.Int32
	archives    atomic.Int32
	utilityGets atomic.Int32
	consumers   atomic.Int32
	stored      atomic.Value
	mutate      func()
}

// All replies come from a real local Unix-HTTP Engine double, never Docker/SQL.
func scaffoldHTTPClient(t *testing.T, task DatabaseTask, fault string) (*Client, string, *scaffoldHTTPState) {
	t.Helper()
	_, plan, e := nativePostgreSQLScaffoldContext(task)
	if e != nil {
		t.Fatal(e)
	}
	owner := plan.owner
	initializer := "sha256:" + strings.Repeat("5", 64)
	state := &scaffoldHTTPState{}
	state.stored.Store([]byte(nil))
	resources := wire.UtilityResources{MemoryBytes: 24 << 20, MilliCPU: 50, PIDs: 12}
	client, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			if e := json.NewEncoder(w).Encode(v); e != nil {
				t.Error(e)
			}
		}
		id := strings.Repeat("d", 64)
		if fault == "utility_replaced" && state.archives.Load() > 0 {
			id = strings.Repeat("e", 64)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1.49/volumes/"+owner.VolumeName():
			labels := owner.labels()
			if fault == "volume_label" {
				labels["io.projectone.operation"] = "foreign"
			}
			write(Volume{Name: owner.VolumeName(), Driver: "local", Scope: "local", Labels: labels})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.49/containers/"+owner.utilityName()+"/json":
			state.utilityGets.Add(1)
			status, started := "created", neverStarted
			if fault == "started" {
				status = "exited"
				started = "2026-11-03T00:00:00Z"
			}
			image := initializer
			if fault == "image" {
				image = "sha256:" + strings.Repeat("6", 64)
			}
			mount := map[string]any{"Type": "volume", "Name": owner.VolumeName(), "Destination": UtilityDestination, "Driver": "local", "RW": true}
			host := map[string]any{"NetworkMode": "none", "ReadonlyRootfs": true, "Memory": resources.MemoryBytes, "NanoCpus": resources.MilliCPU * nanosecondsPerMillicore, "PidsLimit": resources.PIDs, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "RestartPolicy": map[string]string{"Name": "no"}, "Mounts": []any{map[string]any{"Type": "volume", "Source": owner.VolumeName(), "Target": UtilityDestination, "ReadOnly": false, "VolumeOptions": map[string]any{"NoCopy": true}}}}
			write(map[string]any{"Id": id, "Name": "/" + owner.utilityName(), "Image": image, "Config": map[string]any{"Image": image, "User": strconv.Itoa(task.UID) + ":" + strconv.Itoa(task.GID), "Entrypoint": []string{utilityEntrypoint}, "Labels": owner.labels()}, "State": map[string]any{"Status": status, "Pid": 0, "StartedAt": started}, "Mounts": []any{mount}, "HostConfig": host})
		case r.Method == http.MethodGet && r.URL.Path == "/v1.49/containers/json":
			state.consumers.Add(1)
			if r.URL.Query().Get("all") != "1" {
				t.Error("stopped consumers omitted")
			}
			if fault == "consumer" || fault == "consumer_after_get" && state.archives.Load() > 0 {
				id = strings.Repeat("f", 64)
			}
			write([]any{map[string]string{"Id": id}})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/archive"):
			state.archives.Add(1)
			if r.URL.Query().Get("path") != UtilityDestination {
				t.Error("readback was not the whole original volume")
			}
			if state.mutate != nil {
				state.mutate()
				state.mutate = nil
			}
			stored := state.stored.Load().([]byte)
			if len(stored) == 0 {
				w.Write(scaffoldArchiveFixture(t, task, plan, "", true))
				return
			}
			if fault == "readback_extra" {
				w.Write(scaffoldArchiveFixture(t, task, plan, "extra", false))
				return
			}
			var raw bytes.Buffer
			writer := tar.NewWriter(&raw)
			writer.WriteHeader(&tar.Header{Name: path.Base(UtilityDestination), Typeflag: tar.TypeDir, Mode: 0755})
			reader := tar.NewReader(bytes.NewReader(stored))
			for {
				h, e := reader.Next()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Error(e)
					break
				}
				copy := *h
				copy.Name = path.Base(UtilityDestination) + "/" + h.Name
				if e = writer.WriteHeader(&copy); e != nil {
					t.Error(e)
				}
			}
			writer.Close()
			w.Write(raw.Bytes())
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/archive"):
			state.puts.Add(1)
			if r.Header.Get("Content-Type") != "application/x-tar" || r.URL.Query().Get("path") != UtilityDestination || r.URL.Query().Get("copyUIDGID") != "true" || r.URL.Query().Get("noOverwriteDirNonDir") != "true" {
				t.Error("original no-overwrite private archive protocol changed")
			}
			body, e := io.ReadAll(r.Body)
			if e != nil {
				t.Error(e)
			}
			if fault != "lost_before_write" {
				state.stored.Store(body)
			}
			if fault == "lost_after_write" || fault == "lost_before_write" {
				connection, _, e := w.(http.Hijacker).Hijack()
				if e != nil {
					t.Error(e)
				} else {
					connection.Close()
				}
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Error("forbidden Engine effect/route", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	client.config.Utility = &resources
	client.config.ResponseBytes = 65536
	return client, initializer, state
}

func TestNativePostgreSQLScaffoldUnixHTTPOnceOnlyAndInspectReconciliation(t *testing.T) {
	for _, kind := range []string{NativePostgreSQLBackup, NativePostgreSQLRestore, NativePostgreSQLRestoreVerify, NativePostgreSQLBackupVerify} {
		t.Run(kind, func(t *testing.T) {
			task := scaffoldTaskFixture(kind)
			client, initializer, state := scaffoldHTTPClient(t, task, "")
			result, e := client.PutNativePostgreSQLScaffold(t.Context(), task, initializer)
			if e != nil || result.Status != ConfigurationPayloadVerified || result.Files != 0 || result.Bytes != 0 || state.puts.Load() != 1 {
				t.Fatal(result, e, state.puts.Load())
			}
			if _, e = client.PutNativePostgreSQLScaffold(t.Context(), task, initializer); e == nil || state.puts.Load() != 1 {
				t.Fatal("existing namespace overwritten/adopted", e, state.puts.Load())
			}
			if result, e = client.InspectNativePostgreSQLScaffold(t.Context(), task, initializer); e != nil || result.Status != ConfigurationPayloadVerified || state.puts.Load() != 1 {
				t.Fatal("readonly reconciliation changed volume", result, e)
			}
		})
	}
	for _, fault := range []string{"lost_after_write", "lost_before_write"} {
		t.Run(fault, func(t *testing.T) {
			task := scaffoldTaskFixture(NativePostgreSQLBackup)
			client, initializer, state := scaffoldHTTPClient(t, task, fault)
			if _, e := client.PutNativePostgreSQLScaffold(t.Context(), task, initializer); e == nil || e.Error() != "OFFLINE_ENGINE_NATIVE_SCAFFOLD_WRITE_UNCERTAIN" {
				t.Fatal("lost original effect promoted or retried", e)
			}
			result, e := client.InspectNativePostgreSQLScaffold(t.Context(), task, initializer)
			if e != nil || state.puts.Load() != 1 {
				t.Fatal("read-only unknown observation mutated", result, e)
			}
			want := ConfigurationPayloadVerified
			if fault == "lost_before_write" {
				want = "observed_empty_owned_volume"
			}
			if result.Status != want {
				t.Fatal("unknown outcome fabricated completion", result.Status)
			}
		})
	}
}

func TestNativePostgreSQLScaffoldUnixHTTPOriginalOwnerAndConsumerGuards(t *testing.T) {
	for _, fault := range []string{"volume_label", "started", "image", "consumer", "consumer_after_get", "utility_replaced", "readback_extra"} {
		t.Run(fault, func(t *testing.T) {
			task := scaffoldTaskFixture(NativePostgreSQLBackup)
			client, initializer, state := scaffoldHTTPClient(t, task, fault)
			if _, e := client.PutNativePostgreSQLScaffold(t.Context(), task, initializer); e == nil {
				t.Fatal("foreign/changed original context admitted", fault)
			}
			if fault != "readback_extra" && state.puts.Load() != 0 {
				t.Fatal("guard failed after mutation", fault, state.puts.Load())
			}
			if state.puts.Load() > 1 {
				t.Fatal("failed write replayed")
			}
		})
	}
}

func TestNativePostgreSQLScaffoldFreezesCallerAndBoundsBeforeRequests(t *testing.T) {
	task := scaffoldTaskFixture(NativePostgreSQLBackup)
	client, initializer, state := scaffoldHTTPClient(t, task, "")
	state.mutate = func() {
		task.NativePostgreSQLRecovery.Scaffold.Target = "/caller-change"
		task.NativePostgreSQLRecovery.Registry.Target = "/caller-change/foreign"
		task.NativePostgreSQLRecovery.BackupRequest.Reason = "Caller changed after admission"
	}
	if _, e := client.PutNativePostgreSQLScaffold(t.Context(), task, initializer); e != nil {
		t.Fatal("frozen original task changed during request", e)
	}
	var names []string
	reader := tar.NewReader(bytes.NewReader(state.stored.Load().([]byte)))
	for {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		names = append(names, h.Name)
	}
	if !reflect.DeepEqual(names, []string{"private", "private/cas"}) {
		t.Fatal("caller mutation reached emitted directory names", names)
	}
	for _, fault := range []string{"no_profile", "foreign_owner", "invalid_target", "metadata_limit", "response_limit", "initializer", "cancelled", "nil_context"} {
		t.Run(fault, func(t *testing.T) {
			task := scaffoldTaskFixture(NativePostgreSQLBackup)
			client, initializer, state := scaffoldHTTPClient(t, task, "")
			ctx := t.Context()
			switch fault {
			case "no_profile":
				task.NativePostgreSQLRecovery = nil
			case "foreign_owner":
				task.NativePostgreSQLRecovery.Scaffold.Owner.ScopeID = strings.Repeat("f", 32)
			case "invalid_target":
				task.NativePostgreSQLRecovery.Registry.Target = "/registry/cas/../escape"
			case "metadata_limit":
				task.NativePostgreSQLRecovery.Registry.Target = "/registry/a/b/c/d/e/f/g/h/i/j"
			case "response_limit":
				client.config.ResponseBytes = archiveReadbackReserveBytes + tarEndBytes
			case "initializer":
				initializer = "container-id-not-an-image-digest"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil_context":
				ctx = nil
			}
			if _, e := client.PutNativePostgreSQLScaffold(ctx, task, initializer); e == nil || state.puts.Load() != 0 || state.utilityGets.Load() != 0 || state.archives.Load() != 0 {
				t.Fatal("invalid context/limit reached Engine", fault, e)
			}
		})
	}
}
