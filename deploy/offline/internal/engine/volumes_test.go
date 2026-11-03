package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type volumeFixture struct {
	volume  atomic.Bool
	utility atomic.Bool
	posts   atomic.Int32
	puts    atomic.Int32
	stored  atomic.Value
}

// Every HTTP response in this fixture is an explicitly synthetic Engine double.
func volumeClient(t *testing.T, fault string) (*Client, Ownership, string, *volumeFixture) {
	t.Helper()
	o := Ownership{strings.Repeat("a", 32), strings.Repeat("b", 32), "configuration"}
	image := "sha256:" + strings.Repeat("c", 64)
	state := &volumeFixture{}
	state.stored.Store([]byte(nil))
	if fault == "existing" {
		state.volume.Store(true)
	}
	resources := wire.UtilityResources{MemoryBytes: 24 << 20, MilliCPU: 50, PIDs: 12}
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		write := func(value any) { w.Header().Set("Content-Type", "application/json"); json.NewEncoder(w).Encode(value) }
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1.49/volumes/"+o.VolumeName():
			if !state.volume.Load() {
				w.WriteHeader(404)
				return
			}
			labels := o.labels()
			if fault == "label" {
				labels["io.projectone.operation"] = "foreign"
			}
			driver := "local"
			options := map[string]string{}
			if fault == "driver" {
				driver = "foreign"
			}
			if fault == "options" {
				options["device"] = "/foreign"
			}
			write(Volume{Name: o.VolumeName(), Driver: driver, Scope: "local", Labels: labels, Options: options})
		case r.Method == "POST" && r.URL.Path == "/v1.49/volumes/create":
			state.posts.Add(1)
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("wrong JSON content type")
			}
			state.volume.Store(true)
			if fault == "disconnect" {
				conn, _, e := w.(http.Hijacker).Hijack()
				if e == nil {
					conn.Close()
				}
				return
			}
			w.WriteHeader(201)
			write(Volume{Name: o.VolumeName(), Driver: "local", Scope: "local", Labels: o.labels()})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1.49/images/"):
			volumes := map[string]any{}
			if fault == "implicit-volume" {
				volumes["/unknown"] = map[string]any{}
			}
			write(map[string]any{"Id": image, "Os": "linux", "Architecture": "amd64", "RootFS": map[string]any{"Type": "layers", "Layers": []string{}}, "Config": map[string]any{"Volumes": volumes}})
		case r.Method == "GET" && r.URL.Path == "/v1.49/containers/json":
			if r.URL.Query().Get("all") != "1" {
				t.Error("consumer inspection omitted stopped objects")
			}
			if fault == "consumer" {
				write([]any{map[string]string{"Id": strings.Repeat("f", 64)}})
			} else if state.utility.Load() {
				write([]any{map[string]string{"Id": strings.Repeat("d", 64)}})
			} else {
				write([]any{})
			}
		case r.Method == "POST" && r.URL.Path == "/v1.49/containers/create":
			state.posts.Add(1)
			if r.Header.Get("Content-Type") != "application/json" || r.URL.Query().Get("platform") != "linux/amd64" {
				t.Error("wrong container protocol/profile")
			}
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["Image"] != image || body["User"] != "10033:10034" {
				t.Error("unbound image/user")
			}
			state.utility.Store(true)
			w.WriteHeader(201)
			write(map[string]any{"Id": strings.Repeat("d", 64), "Warnings": []string{}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/json") && strings.Contains(r.URL.Path, "/containers/"):
			if !state.utility.Load() {
				w.WriteHeader(404)
				return
			}
			labels := o.labels()
			labels["image-provenance"] = "inherited-immutable-fixture"
			status := "created"
			started := neverStarted
			if fault == "started" {
				status = "exited"
				started = "2026-10-17T00:00:00Z"
			}
			mount := map[string]any{"Type": "volume", "Name": o.VolumeName(), "Destination": UtilityDestination, "Driver": "local", "RW": true}
			if fault == "mount" {
				mount["Name"] = "foreign"
			}
			host := map[string]any{"NetworkMode": "none", "ReadonlyRootfs": true, "Memory": resources.MemoryBytes, "NanoCpus": resources.MilliCPU * nanosecondsPerMillicore, "PidsLimit": resources.PIDs, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "RestartPolicy": map[string]string{"Name": "no"}, "Mounts": []any{map[string]any{"Type": "volume", "Source": o.VolumeName(), "Target": UtilityDestination, "ReadOnly": false, "VolumeOptions": map[string]any{"NoCopy": true}}}}
			if fault == "privilege" {
				host["Privileged"] = true
			}
			write(map[string]any{"Id": strings.Repeat("d", 64), "Name": "/" + o.utilityName(), "Image": image, "Config": map[string]any{"Image": image, "User": "10033:10034", "Entrypoint": []string{utilityEntrypoint}, "Labels": labels}, "State": map[string]any{"Status": status, "Pid": 0, "StartedAt": started}, "Mounts": []any{mount}, "HostConfig": host})
		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/containers/"):
			if r.URL.Query().Get("force") != "0" || r.URL.Query().Get("v") != "0" {
				t.Error("destructive cleanup flags")
			}
			state.utility.Store(false)
			w.WriteHeader(204)
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/archive"):
			state.puts.Add(1)
			if r.URL.Query().Get("copyUIDGID") != "true" || r.URL.Query().Get("noOverwriteDirNonDir") != "true" || r.Header.Get("Content-Type") != "application/x-tar" {
				t.Error("unsafe archive protocol")
			}
			raw, e := io.ReadAll(r.Body)
			if e != nil {
				t.Error(e)
			}
			state.stored.Store(raw)
			w.WriteHeader(200)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/archive"):
			var output bytes.Buffer
			writer := tar.NewWriter(&output)
			writer.WriteHeader(&tar.Header{Name: "volume-init", Typeflag: tar.TypeDir, Mode: 0755})
			archive := tar.NewReader(bytes.NewReader(state.stored.Load().([]byte)))
			for {
				h, e := archive.Next()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Error(e)
					break
				}
				copy := *h
				copy.Name = "volume-init/" + h.Name
				content, _ := io.ReadAll(archive)
				if fault == "readback" && h.Typeflag == tar.TypeReg {
					content[0] ^= 1
				}
				writer.WriteHeader(&copy)
				writer.Write(content)
			}
			writer.Close()
			w.Write(output.Bytes())
		default:
			t.Error("unexpected or forbidden API route", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	c.config.Utility = &resources
	c.config.ResponseBytes = 65536
	return c, o, image, state
}
func TestTypedVolumeProjectionIdentityPrivateBytesAndNoReplay(t *testing.T) {
	c, o, image, state := volumeClient(t, "")
	ctx := context.Background()
	if _, e := c.CreateVolume(ctx, o); e != nil {
		t.Fatal(e)
	}
	if _, e := c.CreateUtility(ctx, o, image, 10033, 10034); e != nil {
		t.Fatal(e)
	}
	files := []ConfigurationFile{{Name: "control.yaml", Contents: []byte("Explicit synthetic configuration only")}}
	if e := c.PutConfiguration(ctx, o, image, 10033, 10034, files); e != nil {
		t.Fatal(e)
	}
	result, e := c.InspectConfiguration(ctx, o, image, 10033, 10034, files)
	if e != nil || result.Status != "verified_private_volume_payload" || result.Files != 1 || result.Bytes != int64(len(files[0].Contents)) {
		t.Fatal(result, e)
	}
	if e = c.PutConfiguration(ctx, o, image, 10033, 10034, files); e == nil || state.puts.Load() != 1 {
		t.Fatal("nonempty write replayed", e)
	}
	raw, _ := json.Marshal(files)
	if bytes.Contains(raw, files[0].Contents) {
		t.Fatal("configuration bytes leaked into JSON")
	}
	if e = c.RemoveUtility(ctx, o, image, 10033, 10034); e != nil || state.utility.Load() || !state.volume.Load() {
		t.Fatal("cleanup removed retained data or left utility", e)
	}
}
func TestTypedVolumeAdmissionFailuresAndUncertainCreate(t *testing.T) {
	for _, fault := range []string{"existing", "label", "driver", "options", "disconnect", "implicit-volume", "consumer", "started", "mount", "privilege", "readback"} {
		t.Run(fault, func(t *testing.T) {
			c, o, image, state := volumeClient(t, fault)
			ctx := context.Background()
			_, e := c.CreateVolume(ctx, o)
			if fault == "existing" || fault == "label" || fault == "driver" || fault == "options" || fault == "disconnect" {
				if e == nil {
					t.Fatal("unsafe volume admitted")
				}
				before := state.posts.Load()
				if _, e = c.CreateVolume(ctx, o); e == nil || state.posts.Load() != before {
					t.Fatal("uncertain/existing create resent", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			_, e = c.CreateUtility(ctx, o, image, 10033, 10034)
			if fault != "readback" {
				if e == nil {
					t.Fatal("unsafe utility admitted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			files := []ConfigurationFile{{Name: "secret", Contents: []byte("explicit synthetic secret")}}
			if e = c.PutConfiguration(ctx, o, image, 10033, 10034, files); e == nil || e.Error() != "OFFLINE_ENGINE_CONFIGURATION_WRITE_UNCERTAIN" {
				t.Fatal("bad readback accepted", e)
			}
			if e = c.PutConfiguration(ctx, o, image, 10033, 10034, files); e == nil || state.puts.Load() != 1 {
				t.Fatal("uncertain write replayed", e)
			}
		})
	}
}
func TestConfigurationInputGuardsBeforeEffects(t *testing.T) {
	c, _, _, state := volumeClient(t, "")
	for _, name := range []string{"../escape", "nested/file", "/root", ".hidden", "manifest.json", ""} {
		if e := c.ValidateConfigurationFiles([]ConfigurationFile{{Name: name, Contents: []byte("test")}}, 10033, 10034); e == nil {
			t.Fatal("unsafe name admitted", name)
		}
	}
	for _, files := range [][]ConfigurationFile{{{Name: "same", Contents: []byte("one")}, {Name: "same", Contents: []byte("two")}}, {{Name: "empty"}}, {{Name: "large", Contents: bytes.Repeat([]byte("x"), 65536)}}} {
		if e := c.ValidateConfigurationFiles(files, 10033, 10034); e == nil {
			t.Fatal("unsafe input admitted")
		}
	}
	if state.posts.Load() != 0 || state.puts.Load() != 0 {
		t.Fatal("input validation mutated engine")
	}
}
