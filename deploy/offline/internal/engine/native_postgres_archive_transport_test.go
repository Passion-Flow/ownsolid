package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func nativeArchiveInputFixture(t *testing.T, fault string) (*Client, DatabaseTask, string, *atomic.Int32, []byte, NativePostgreSQLArchiveProfile) {
	t.Helper()
	task := nativeBackupVerifierTaskFixture()
	task.NativePostgreSQLBackupVerifier.BackupParent = true
	owner := task.NativePostgreSQLBackupVerifier.BackupOwner
	image := "sha256:" + strings.Repeat("c", 64)
	raw, a := nativeArchiveFixture(t, "")
	out, e := readNativePostgreSQLArchive(bytes.NewReader(raw), io.Discard, a, a.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	resources := wire.UtilityResources{MemoryBytes: 24 << 20, MilliCPU: 50, PIDs: 12}
	var copied atomic.Bool
	var puts atomic.Int32
	id := strings.Repeat("d", 64)
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(v) }
		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/images/"):
			write(map[string]any{"Id": image, "Os": "linux", "Architecture": "amd64", "RootFS": map[string]any{"Type": "layers", "Layers": []string{}}, "Config": map[string]any{"Volumes": map[string]any{}}})
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/volumes/"):
			write(Volume{Name: owner.VolumeName(), Driver: "local", Scope: "local", Labels: owner.labels()})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/containers/json"):
			if fault == "foreign-consumer" {
				write([]RuntimeObject{{ID: strings.Repeat("f", 64)}})
			} else {
				write([]RuntimeObject{{ID: id}})
			}
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/json"):
			status, started := "created", neverStarted
			if fault == "started" {
				status, started = "exited", "2026-11-03T00:00:00Z"
			}
			host := map[string]any{"NetworkMode": "none", "ReadonlyRootfs": true, "Memory": resources.MemoryBytes, "NanoCpus": resources.MilliCPU * nanosecondsPerMillicore, "PidsLimit": resources.PIDs, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "RestartPolicy": map[string]string{"Name": "no"}, "Mounts": []any{map[string]any{"Type": "volume", "Source": owner.VolumeName(), "Target": UtilityDestination, "ReadOnly": false, "VolumeOptions": map[string]any{"NoCopy": true}}}}
			mount := map[string]any{"Type": "volume", "Name": owner.VolumeName(), "Destination": UtilityDestination, "Driver": "local", "RW": true}
			write(map[string]any{"Id": id, "Name": "/" + owner.utilityName(), "Image": image, "Config": map[string]any{"Image": image, "User": strconv.Itoa(task.UID) + ":" + strconv.Itoa(task.GID), "Entrypoint": []string{utilityEntrypoint}, "Labels": owner.labels()}, "State": map[string]any{"Status": status, "Pid": 0, "StartedAt": started}, "Mounts": []any{mount}, "HostConfig": host})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/archive"):
			destination := r.URL.Query().Get("path")
			if destination == UtilityDestination {
				var b bytes.Buffer
				tw := tar.NewWriter(&b)
				tw.WriteHeader(&tar.Header{Name: "volume-init", Typeflag: tar.TypeDir, Mode: 0755})
				tw.WriteHeader(&tar.Header{Name: "volume-init/private", Typeflag: tar.TypeDir, Mode: 0700, Uid: task.UID, Gid: task.GID})
				if copied.Load() || fault == "nonempty" {
					tw.WriteHeader(&tar.Header{Name: "volume-init/private/source", Typeflag: tar.TypeDir, Mode: 0700, Uid: task.UID, Gid: task.GID})
				}
				tw.Close()
				w.Write(b.Bytes())
				return
			}
			if destination != UtilityDestination+"/private/source" {
				t.Error("unfixed source readback", destination)
			}
			if !copied.Load() {
				w.WriteHeader(404)
				return
			}
			changed := "changed-header-time"
			if fault == "readback-mode" {
				changed = "changed-mode"
			}
			observed, _ := nativeArchiveFixture(t, changed)
			w.Write(observed)
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/archive"):
			puts.Add(1)
			if r.URL.Query().Get("path") != UtilityDestination+"/private" || r.URL.Query().Get("copyUIDGID") != "true" || r.URL.Query().Get("noOverwriteDirNonDir") != "true" {
				t.Error("unsafe native copy query")
			}
			body, e := io.ReadAll(r.Body)
			if e != nil || !bytes.Equal(body, raw) {
				t.Error("original tar changed")
			}
			copied.Store(true)
			if fault == "copy-lost" {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			w.WriteHeader(200)
		default:
			t.Error("unexpected native input route", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	c.config.Utility = &resources
	c.config.ResponseBytes = 65536
	return c, task, image, &puts, raw, out.Profile
}

func nativeArchiveHostFile(t *testing.T, raw []byte) *os.File {
	t.Helper()
	name := filepath.Join(t.TempDir(), "native.tar")
	if e := os.WriteFile(name, raw, 0600); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(name)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestNativePostgreSQLArchiveUnixImportOnceAndIndependentInventoryReadback(t *testing.T) {
	for _, fault := range []string{"", "copy-lost"} {
		t.Run(fault, func(t *testing.T) {
			c, task, image, puts, raw, p := nativeArchiveInputFixture(t, fault)
			f := nativeArchiveHostFile(t, raw)
			got, e := c.PutNativePostgreSQLBackupArchive(context.Background(), task, image, f, p)
			if fault == "" && (e != nil || got != p) {
				t.Fatal("original native copy/readback", e)
			}
			if fault != "" && e == nil {
				t.Fatal("unknown copy accepted")
			}
			got, e = c.InspectNativePostgreSQLBackupArchiveInput(context.Background(), task, image, p)
			if e != nil || got != p || puts.Load() != 1 {
				t.Fatal("readonly exact copied inventory failed", e)
			}
			if _, e = c.PutNativePostgreSQLBackupArchive(context.Background(), task, image, f, p); e == nil || puts.Load() != 1 {
				t.Fatal("existing native input overwritten/replayed")
			}
		})
	}
}

func TestNativePostgreSQLArchiveUnixRejectsForeignStartedNonemptyAndModeChanges(t *testing.T) {
	for _, fault := range []string{"foreign-consumer", "started", "nonempty", "readback-mode"} {
		t.Run(fault, func(t *testing.T) {
			c, task, image, puts, raw, p := nativeArchiveInputFixture(t, fault)
			f := nativeArchiveHostFile(t, raw)
			if _, e := c.PutNativePostgreSQLBackupArchive(context.Background(), task, image, f, p); e == nil {
				t.Fatal("unsafe copied input accepted")
			}
			expected := int32(0)
			if fault == "readback-mode" {
				expected = 1
			}
			if puts.Load() != expected {
				t.Fatal("unintended copy mutation", puts.Load())
			}
		})
	}
}

func TestNativePostgreSQLArchiveUnixExportExactTerminalTreeAndRejectTaskChanges(t *testing.T) {
	for _, fault := range []string{"native-archive", "native-archive-change"} {
		t.Run(fault, func(t *testing.T) {
			c, task, state, effects, _ := nativeRecoveryLifecycleFixture(t, NativePostgreSQLBackup, fault)
			ctx := context.Background()
			created, e := c.CreateDatabaseTask(ctx, task)
			if e != nil {
				t.Fatal(e)
			}
			var target bytes.Buffer
			if _, e = c.StreamNativePostgreSQLBackupArchive(ctx, task, created.ID, 1<<20, 64, 3, &target); e == nil || target.Len() != 0 {
				t.Fatal("export before native completion")
			}
			state.Store(3)
			got, e := c.StreamNativePostgreSQLBackupArchive(ctx, task, created.ID, 1<<20, 64, 3, &target)
			if fault == "native-archive-change" {
				if e == nil {
					t.Fatal("changed original task accepted")
				}
				return
			}
			raw, _ := nativeArchiveFixture(t, "")
			if e != nil || !got.Profile.valid() || !bytes.Equal(target.Bytes(), raw) || effects.Load() != 1 {
				t.Fatal("original complete tree export changed/effected", e)
			}
		})
	}
}
