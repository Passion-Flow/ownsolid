package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"path"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Every server here is the existing explicit Unix Engine protocol test double.
// No Docker daemon, volume, process action or version2 TLS plan is executed.
type configurationPayloadFixture struct {
	owner                                                  Ownership
	object                                                 RuntimeContainer
	destination                                            string
	files                                                  []ConfigurationFile
	manifest                                               []ConfigurationFileDigest
	archive                                                []byte
	fault                                                  string
	onFirst                                                func()
	archiveEntered                                         chan struct{}
	runtimeReads, volumeReads, consumerReads, archiveReads atomic.Int32
}

func configurationPayloadData(t *testing.T) *configurationPayloadFixture {
	t.Helper()
	scope, object := stoppedControlFixture()
	op := strings.Repeat("d", 32)
	f := &configurationPayloadFixture{owner: Ownership{scope, op, TLSConfigurationRole + "-" + op}, object: object, destination: "/run/projectone/control"}
	f.files = []ConfigurationFile{
		{Name: "runtime.yaml", Contents: []byte("synthetic protected runtime, not an installation")},
		{Name: "machine-key.pem", Contents: []byte("synthetic-secret-machine-key-do-not-serialize-6b7e")},
		{Name: "browser-key.pem", Contents: []byte("synthetic-secret-browser-key-do-not-serialize-9c2f")},
		{Name: "license-trust.json", Contents: []byte("synthetic public trust plus binary \x00\xff")},
	}
	for _, file := range f.files {
		f.manifest = append(f.manifest, ConfigurationFileDigest{Name: file.Name, Bytes: int64(len(file.Contents)), SHA256: wire.Digest(file.Contents)})
	}
	raw, e := json.Marshal(f.object)
	if e != nil {
		t.Fatal(e)
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		t.Fatal("synthetic runtime object")
	}
	values["HostConfig"].(map[string]any)["Mounts"] = []any{map[string]any{"Type": "volume", "Source": f.owner.VolumeName(), "Target": f.destination, "ReadOnly": true, "VolumeOptions": map[string]any{"NoCopy": true}}}
	values["Mounts"] = []any{map[string]any{"Type": "volume", "Name": f.owner.VolumeName(), "Destination": f.destination, "Driver": "local", "RW": false}}
	raw, e = json.Marshal(values)
	if e != nil || json.Unmarshal(raw, &f.object) != nil {
		t.Fatal("synthetic private mounts")
	}
	f.archive = configurationReadbackTar(t, f.files, f.destination, false)
	return f
}

func (f *configurationPayloadFixture) client(t *testing.T) (*Client, *atomic.Int32) {
	t.Helper()
	c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Body != nil && r.ContentLength != 0 {
			t.Error("configuration reader submitted a mutation/body")
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/v1.49/containers/" + RuntimeContainerName(f.owner.ScopeID, "control") + "/json":
			count := f.runtimeReads.Add(1)
			if count == 1 && f.onFirst != nil {
				f.onFirst()
			}
			if f.fault == "runtime_missing" || f.fault == "runtime_missing_after" && count > 1 {
				w.WriteHeader(404)
				return
			}
			raw, _ := json.Marshal(f.object)
			var reply RuntimeContainer
			if json.Unmarshal(raw, &reply) != nil {
				t.Error("synthetic runtime clone")
				return
			}
			switch f.fault {
			case "runtime_owner":
				reply.Config.Labels["io.projectone.isolated-scope"] = strings.Repeat("f", 32)
			case "wrong_id":
				reply.ID = strings.Repeat("f", 64)
			case "wrong_user":
				reply.Config.User = "10034:10033"
			case "host_writable":
				reply.HostConfig.Mounts[0].ReadOnly = false
			case "host_nocopy":
				reply.HostConfig.Mounts[0].VolumeOptions.NoCopy = false
			case "host_subpath":
				reply.HostConfig.Mounts[0].VolumeOptions.Subpath = "foreign"
			case "actual_writable":
				reply.Mounts[0].RW = true
			case "actual_driver":
				reply.Mounts[0].Driver = "foreign"
			case "host_duplicate":
				reply.HostConfig.Mounts = append(reply.HostConfig.Mounts, reply.HostConfig.Mounts[0])
			case "actual_alias":
				alias := reply.Mounts[0]
				alias.Destination = "/foreign"
				reply.Mounts = append(reply.Mounts, alias)
			case "nested_mount":
				alias := reply.HostConfig.Mounts[0]
				alias.Target = f.destination + "/private"
				reply.HostConfig.Mounts = append(reply.HostConfig.Mounts, alias)
			case "runtime_digest_after":
				if count > 1 {
					reply.Config.Env = append(reply.Config.Env, "SYNTHETIC_PRIVATE_CHANGED=unprinted")
				}
			}
			json.NewEncoder(w).Encode(reply)
		case "/v1.49/volumes/" + f.owner.VolumeName():
			count := f.volumeReads.Add(1)
			if f.fault == "volume_missing" {
				w.WriteHeader(404)
				return
			}
			volume := Volume{Name: f.owner.VolumeName(), Driver: "local", Scope: "local", Labels: f.owner.labels()}
			switch f.fault {
			case "volume_owner":
				volume.Labels["io.projectone.operation"] = strings.Repeat("f", 32)
			case "volume_driver":
				volume.Driver = "foreign"
			case "volume_options":
				volume.Options = map[string]string{"device": "foreign"}
			case "volume_changed_after":
				if count > 1 {
					volume.Labels["synthetic-observation"] = "changed"
				}
			case "volume_owner_after":
				if count > 1 {
					volume.Labels["io.projectone.operation"] = strings.Repeat("f", 32)
				}
			}
			json.NewEncoder(w).Encode(volume)
		case "/v1.49/containers/json":
			count := f.consumerReads.Add(1)
			var filters map[string][]string
			if len(r.URL.Query()) != 2 || r.URL.Query().Get("all") != "1" || json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters) != nil || len(filters) != 1 || len(filters["volume"]) != 1 || filters["volume"][0] != f.owner.VolumeName() {
				t.Error("unbound consumer inventory query")
				w.WriteHeader(400)
				return
			}
			consumers := []RuntimeObject{{ID: f.object.ID}}
			if f.fault == "consumer_empty" || f.fault == "consumer_empty_after" && count > 1 {
				consumers = []RuntimeObject{}
			}
			if f.fault == "consumer_foreign" || f.fault == "consumer_foreign_after" && count > 1 {
				consumers = append(consumers, RuntimeObject{ID: strings.Repeat("f", 64)})
			}
			if f.fault == "consumer_duplicate" || f.fault == "consumer_duplicate_after" && count > 1 {
				consumers = append(consumers, consumers[0])
			}
			json.NewEncoder(w).Encode(consumers)
		case "/v1.49/containers/" + f.object.ID + "/archive":
			f.archiveReads.Add(1)
			if len(r.URL.Query()) != 1 || len(r.URL.Query()["path"]) != 1 || r.URL.Query().Get("path") != f.destination {
				t.Error("unbound archive request")
				w.WriteHeader(400)
				return
			}
			if f.archiveEntered != nil {
				close(f.archiveEntered)
				<-r.Context().Done()
				return
			}
			if f.fault == "archive_status" {
				w.WriteHeader(500)
				return
			}
			if f.fault == "archive_response_limit" {
				w.Header().Set("Content-Length", "1048577")
				w.WriteHeader(200)
				return
			}
			w.Write(f.archive)
		default:
			t.Error("unexpected configuration readback route")
			w.WriteHeader(404)
		}
	})
	c.config.ResponseBytes = 1 << 20
	return c, calls
}

func configurationPayloadFailure(t *testing.T, files []ConfigurationFile, e error, code string) {
	t.Helper()
	if e == nil || files != nil || DiagnosticCode(e) == "OFFLINE_ENGINE_UNCLASSIFIED_FAILURE" || code != "" && DiagnosticCode(e) != code {
		t.Fatal("payload failure returned partial bytes or lost its fixed diagnostic")
	}
}

func TestCurrentConfigurationPayloadInputBeforeIO(t *testing.T) {
	for _, fault := range []string{"nil_context", "nil_client", "scope", "operation", "role", "id", "destination", "traversal", "backslash", "control_path", "invalid_utf8_path", "root_path", "uid", "gid", "large_uid", "large_gid", "files", "duplicate", "unsafe_name", "nested_name", "backslash_name", "control_name", "zero_bytes", "negative_bytes", "overflow_bytes", "hash", "uppercase_hash", "budget", "total_budget", "negative_budget"} {
		t.Run(fault, func(t *testing.T) {
			f := configurationPayloadData(t)
			c, calls := f.client(t)
			ctx := context.Background()
			uid, gid := 10033, 10034
			id := f.object.ID
			switch fault {
			case "nil_context":
				ctx = nil
			case "nil_client":
				c = nil
			case "scope":
				f.owner.ScopeID = "foreign"
			case "operation":
				f.owner.OperationID = "foreign"
			case "role":
				f.owner.Role = TLSConfigurationRole
			case "id":
				id = "foreign"
			case "destination":
				f.destination = "/etc/private"
			case "traversal":
				f.destination = "/run/projectone/control/../foreign"
			case "backslash":
				f.destination = "/run/projectone/control\\foreign"
			case "control_path":
				f.destination = "/run/projectone/control\tforeign"
			case "invalid_utf8_path":
				f.destination = "/run/projectone/control\xffforeign"
			case "root_path":
				f.destination = "/run/projectone/"
			case "uid":
				uid = 0
			case "gid":
				gid = 0
			case "large_uid":
				uid = math.MaxInt32 + 1
			case "large_gid":
				gid = math.MaxInt32 + 1
			case "files":
				f.manifest = nil
			case "duplicate":
				f.manifest = append(f.manifest, f.manifest[0])
			case "unsafe_name":
				f.manifest[0].Name = "../key.pem"
			case "nested_name":
				f.manifest[0].Name = "child/key.pem"
			case "backslash_name":
				f.manifest[0].Name = "child\\key.pem"
			case "control_name":
				f.manifest[0].Name = "key\x00.pem"
			case "zero_bytes":
				f.manifest[0].Bytes = 0
			case "negative_bytes":
				f.manifest[0].Bytes = -1
			case "overflow_bytes":
				f.manifest[0].Bytes = math.MaxInt64
			case "hash":
				f.manifest[0].SHA256 = "invalid"
			case "uppercase_hash":
				f.manifest[0].SHA256 = strings.Repeat("A", 64)
			case "budget":
				c.config.ResponseBytes = 4096
			case "total_budget":
				f.manifest[0].Bytes = c.config.ResponseBytes - archiveReadbackReserveBytes
			case "negative_budget":
				c.config.ResponseBytes = math.MinInt64
			}
			files, e := c.ReadCurrentControlConfiguration(ctx, f.owner, id, f.destination, uid, gid, f.manifest)
			configurationPayloadFailure(t, files, e, currentConfigurationInputInvalid)
			if calls.Load() != 0 {
				t.Fatal("invalid payload input reached Engine I/O")
			}
		})
	}
}

func configurationPayloadBytes(t *testing.T, actual, expected []ConfigurationFile) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatal("incomplete verified inventory")
	}
	byName := make(map[string][]byte, len(expected))
	for _, file := range expected {
		byName[file.Name] = file.Contents
	}
	previous := ""
	for _, file := range actual {
		if file.Name <= previous || !bytes.Equal(file.Contents, byName[file.Name]) {
			t.Fatal("nondeterministic name order or mismatched private bytes")
		}
		previous = file.Name
	}
}

func TestCurrentConfigurationPayloadReadbackPreservesVerifiedBytes(t *testing.T) {
	for _, state := range []string{"created", "running", "exited", "failed_exited"} {
		t.Run(state, func(t *testing.T) {
			f := configurationPayloadData(t)
			if state != "created" {
				f.object.State.Status = "exited"
				f.object.State.StartedAt = "2026-10-18T00:00:00Z"
			}
			if state == "running" {
				f.object.State.Status = "running"
				f.object.State.Running = true
				f.object.State.PID = 42
			}
			if state == "failed_exited" {
				f.object.State.ExitCode = 1
			}
			c, calls := f.client(t)
			read := c.ReadCurrentControlConfiguration
			if state == "failed_exited" {
				read = c.ReadFailedControlConfiguration
			}
			actual, e := read(context.Background(), f.owner, f.object.ID, f.destination, 10033, 10034, f.manifest)
			if e != nil {
				t.Fatal("verified synthetic readback failed", DiagnosticCode(e))
			}
			configurationPayloadBytes(t, actual, f.files)
			if calls.Load() != 7 || f.archiveReads.Load() != 1 || f.runtimeReads.Load() != 2 || f.volumeReads.Load() != 2 || f.consumerReads.Load() != 2 {
				t.Fatal("missing before/after observations or a non-readback request")
			}
			actual[0].Contents[0] ^= 1
			actual[0].Name = "caller-owned-name"
			again, e := read(context.Background(), f.owner, f.object.ID, f.destination, 10033, 10034, f.manifest)
			if e != nil {
				t.Fatal("independent repeated readback failed", DiagnosticCode(e))
			}
			configurationPayloadBytes(t, again, f.files)
			if state == "failed_exited" {
				files, e := c.ReadCurrentControlConfiguration(context.Background(), f.owner, f.object.ID, f.destination, 10033, 10034, f.manifest)
				configurationPayloadFailure(t, files, e, currentConfigurationReadbackFailed)
			} else {
				files, e := c.ReadFailedControlConfiguration(context.Background(), f.owner, f.object.ID, f.destination, 10033, 10034, f.manifest)
				configurationPayloadFailure(t, files, e, currentConfigurationReadbackFailed)
			}
		})
	}
	t.Run("caller_manifest_is_frozen_before_network", func(t *testing.T) {
		f := configurationPayloadData(t)
		changed := make(chan struct{})
		f.onFirst = func() {
			f.manifest[0].Name = "caller-changed"
			f.manifest[1].Bytes = 1
			f.manifest[2].SHA256 = strings.Repeat("f", 64)
			close(changed)
		}
		c, _ := f.client(t)
		actual, e := c.ReadCurrentControlConfiguration(context.Background(), f.owner, f.object.ID, f.destination, 10033, 10034, f.manifest)
		select {
		case <-changed:
		case <-time.After(time.Second):
			t.Fatal("frozen-manifest control did not reach its owned first observation")
		}
		if e != nil {
			t.Fatal("caller-owned mutation changed frozen admission", DiagnosticCode(e))
		}
		configurationPayloadBytes(t, actual, f.files)
	})
}

func configurationPayloadAttack(t *testing.T, f *configurationPayloadFixture, fault string) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	reader := tar.NewReader(bytes.NewReader(f.archive))
	root := path.Base(f.destination)
	for {
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal("synthetic archive input")
		}
		data, e := io.ReadAll(reader)
		if e != nil {
			t.Fatal(e)
		}
		copy := *header
		if fault == "root_only" && copy.Name != root {
			continue
		}
		if fault == "missing_root" && copy.Name == root || fault == "missing_private" && copy.Name == root+"/private" || fault == "missing_file" && copy.Name == root+"/private/"+f.files[0].Name {
			continue
		}
		if copy.Name == root && fault == "root_owner" {
			copy.Uid = 10033
		}
		if copy.Name == root && fault == "root_group" {
			copy.Gid = 10034
		}
		if copy.Name == root && fault == "root_mode" {
			copy.Mode = 0700
		}
		if copy.Name == root+"/private" && fault == "private_mode" {
			copy.Mode = 0755
		}
		if copy.Name == root+"/private" && fault == "private_owner" {
			copy.Uid = 0
		}
		if copy.Name == root+"/private" && fault == "private_group" {
			copy.Gid = 0
		}
		if copy.Typeflag == tar.TypeReg {
			switch fault {
			case "file_owner":
				copy.Uid = 0
			case "file_group":
				copy.Gid = 0
			case "file_mode":
				copy.Mode = 0644
			case "hash":
				data[0] ^= 1
			case "size":
				data = append(data, 'x')
				copy.Size = int64(len(data))
			case "traversal":
				copy.Name = root + "/private/../" + path.Base(copy.Name)
			case "absolute":
				copy.Name = "/" + copy.Name
			case "symlink", "hardlink", "fifo":
				copy.Typeflag = tar.TypeSymlink
				copy.Linkname = "/synthetic-foreign"
				if fault == "hardlink" {
					copy.Typeflag = tar.TypeLink
				}
				if fault == "fifo" {
					copy.Typeflag = tar.TypeFifo
					copy.Linkname = ""
				}
				copy.Size = 0
				data = nil
			}
		}
		if e := writer.WriteHeader(&copy); e != nil {
			t.Fatal(e)
		}
		if len(data) > 0 {
			if _, e := writer.Write(data); e != nil {
				t.Fatal(e)
			}
		}
		if fault == "duplicate_file" && copy.Typeflag == tar.TypeReg {
			if writer.WriteHeader(&copy) != nil {
				t.Fatal("synthetic duplicate")
			}
			writer.Write(data)
		}
	}
	if fault == "unknown_file" {
		writer.WriteHeader(&tar.Header{Name: root + "/private/unknown", Typeflag: tar.TypeReg, Mode: 0600, Uid: 10033, Gid: 10034, Size: 1})
		writer.Write([]byte("x"))
	}
	if writer.Close() != nil {
		t.Fatal("synthetic archive close")
	}
	raw := output.Bytes()
	switch fault {
	case "truncated":
		raw = raw[:len(raw)-tarEndBytes-1]
	case "malformed":
		raw[0] ^= 1
	case "trailing":
		raw = append(raw, bytes.Repeat([]byte("x"), tarBlockBytes)...)
	case "second_archive":
		raw = append(raw, f.archive...)
	}
	return raw
}

func TestCurrentConfigurationPayloadRejectsArchiveAndContext(t *testing.T) {
	for _, fault := range []string{"runtime_missing", "runtime_owner", "wrong_id", "wrong_user", "host_writable", "host_nocopy", "host_subpath", "actual_writable", "actual_driver", "host_duplicate", "actual_alias", "nested_mount", "volume_missing", "volume_owner", "volume_driver", "volume_options", "consumer_empty", "consumer_foreign", "consumer_duplicate", "runtime_missing_after", "runtime_digest_after", "volume_changed_after", "volume_owner_after", "consumer_empty_after", "consumer_foreign_after", "consumer_duplicate_after", "archive_status", "archive_response_limit"} {
		t.Run(fault, func(t *testing.T) {
			f := configurationPayloadData(t)
			f.fault = fault
			c, _ := f.client(t)
			files, e := c.ReadCurrentControlConfiguration(context.Background(), f.owner, f.object.ID, f.destination, 10033, 10034, f.manifest)
			code := ""
			if strings.HasSuffix(fault, "_after") {
				code = currentConfigurationChanged
			}
			if fault == "archive_response_limit" {
				code = "OFFLINE_ENGINE_RESPONSE_LIMIT"
			}
			configurationPayloadFailure(t, files, e, code)
		})
	}
	for _, fault := range []string{"root_owner", "root_group", "root_mode", "private_mode", "private_owner", "private_group", "file_owner", "file_group", "file_mode", "hash", "size", "symlink", "hardlink", "fifo", "traversal", "absolute", "root_only", "missing_root", "missing_private", "missing_file", "duplicate_file", "unknown_file", "truncated", "malformed", "trailing", "second_archive"} {
		t.Run("archive_"+fault, func(t *testing.T) {
			f := configurationPayloadData(t)
			f.archive = configurationPayloadAttack(t, f, fault)
			c, _ := f.client(t)
			files, e := c.ReadCurrentControlConfiguration(context.Background(), f.owner, f.object.ID, f.destination, 10033, 10034, f.manifest)
			configurationPayloadFailure(t, files, e, "OFFLINE_ENGINE_CONFIGURATION_INVENTORY_MISMATCH")
		})
	}
	t.Run("already_cancelled", func(t *testing.T) {
		f := configurationPayloadData(t)
		c, calls := f.client(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		files, e := c.ReadCurrentControlConfiguration(ctx, f.owner, f.object.ID, f.destination, 10033, 10034, f.manifest)
		configurationPayloadFailure(t, files, e, currentConfigurationReadbackFailed)
		if calls.Load() != 0 {
			t.Fatal("cancelled readback reached Engine")
		}
	})
	t.Run("cancel_during_archive", func(t *testing.T) {
		f := configurationPayloadData(t)
		f.archiveEntered = make(chan struct{})
		c, _ := f.client(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		var files []ConfigurationFile
		var e error
		go func() {
			defer close(done)
			files, e = c.ReadCurrentControlConfiguration(ctx, f.owner, f.object.ID, f.destination, 10033, 10034, f.manifest)
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("owned readback did not join")
			}
		})
		select {
		case <-f.archiveEntered:
			cancel()
		case <-time.After(time.Second):
			t.Fatal("synthetic archive was not entered")
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("cancelled readback did not return")
		}
		configurationPayloadFailure(t, files, e, "")
	})
}

func TestCurrentConfigurationPayloadSecretsNeverSerialize(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "current"
		if failed {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			f := configurationPayloadData(t)
			if failed {
				f.object.State.Status = "exited"
				f.object.State.StartedAt = "2026-10-18T00:00:00Z"
				f.object.State.ExitCode = 1
			}
			c, _ := f.client(t)
			read := c.ReadCurrentControlConfiguration
			if failed {
				read = c.ReadFailedControlConfiguration
			}
			files, e := read(context.Background(), f.owner, f.object.ID, f.destination, 10033, 10034, f.manifest)
			if e != nil {
				t.Fatal("verified private serialization fixture failed", DiagnosticCode(e))
			}
			raw, e := json.Marshal(files)
			if e != nil {
				t.Fatal(e)
			}
			var public []map[string]json.RawMessage
			if json.Unmarshal(raw, &public) != nil || len(public) != len(files) {
				t.Fatal("public inventory shape")
			}
			for _, item := range public {
				if len(item) != 1 || item["name"] == nil {
					t.Fatal("private payload entered public JSON fields")
				}
			}
			for _, file := range f.files {
				if bytes.Contains(raw, file.Contents) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(file.Contents))) {
					t.Fatal("private bytes entered JSON diagnostics")
				}
			}
		})
	}
}
