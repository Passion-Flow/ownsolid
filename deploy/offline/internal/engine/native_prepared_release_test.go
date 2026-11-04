package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Every key and payload is a synthetic fixture, never a platform release.
func preparedReleaseFixture(t *testing.T) PreparedReleaseRequest {
	t.Helper()
	base := t.TempDir()
	if e := os.Chmod(base, 0700); e != nil {
		t.Fatal(e)
	}
	mount := base
	for {
		var here, parent syscall.Stat_t
		if syscall.Stat(mount, &here) != nil || syscall.Stat(filepath.Dir(mount), &parent) != nil {
			t.Fatal("storage unavailable")
		}
		if here.Dev != parent.Dev {
			break
		}
		if mount == "/" {
			t.Skip("actual nonroot mounted TMPDIR required; run make test-offline on Linux")
		}
		mount = filepath.Dir(mount)
	}
	root := filepath.Join(base, "release")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	put := func(name string, raw []byte) {
		if e := os.MkdirAll(filepath.Dir(name), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(name, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{39}, ed25519.SeedSize))
	public := key.Public().(ed25519.PublicKey)
	trust := filepath.Join(base, "trust.json")
	raw, _ := json.Marshal(map[string]any{"keys": map[string]string{wire.Digest(public): base64.StdEncoding.EncodeToString(public)}})
	put(trust, raw)
	manifest := wire.Manifest{FormatVersion: 1, ReleaseID: "prepared-sdk-synthetic", Profile: wire.FixtureProfile, SourceSHA256: strings.Repeat("a", 64), SchemaVersion: 16, MinimumPredecessor: 15, MaximumPredecessor: 16, Capabilities: []string{"M01"}, Prerequisites: []string{}, Assets: []wire.Asset{}}
	for _, spec := range []struct {
		id, path, kind string
		data           []byte
	}{{"license", "licenses/test.txt", "license", []byte("Synthetic license\n")}, {"fixture", "assets/" + strings.Repeat("long", 20) + "/" + strings.Repeat("sub", 15) + "/operator.bin", "operator", bytes.Repeat([]byte("z"), 256<<10)}} {
		licenses := []string{}
		if spec.kind != "license" {
			licenses = []string{"license"}
		}
		manifest.Assets = append(manifest.Assets, wire.Asset{ID: spec.id, Path: spec.path, Kind: spec.kind, Size: int64(len(spec.data)), SHA256: wire.Digest(spec.data), Licenses: licenses})
		put(filepath.Join(root, filepath.FromSlash(spec.path)), spec.data)
	}
	raw, _ = json.Marshal(manifest)
	raw = append(raw, '\n')
	put(filepath.Join(root, "manifest.json"), raw)
	signature := wire.Signature{Algorithm: wire.SignatureAlgorithm, KeyID: wire.Digest(public), ManifestSHA256: wire.Digest(raw), Value: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("projectone-offline-release-v1\n"), raw...)))}
	signed, _ := json.Marshal(signature)
	put(filepath.Join(root, "signature.json"), signed)
	archive := strings.Repeat("c", 64)
	metadata, _ := json.Marshal(map[string]any{"status": "prepared_verified_inventory_only", "archive_sha256": archive, "manifest_sha256": wire.Digest(raw), "profile": manifest.Profile, "release_id": manifest.ReleaseID})
	put(filepath.Join(root, preparedReleaseMarker), metadata)
	var journal []byte
	for _, status := range []string{"reserved", "prepared_verified_inventory_only"} {
		event, _ := json.Marshal(map[string]any{"status": status, "code": "", "archive_sha256": archive, "manifest_sha256": wire.Digest(raw), "time": "2026-11-03T00:00:00Z"})
		journal = append(journal, append(event, '\n')...)
	}
	put(root+".journal.jsonl", journal)
	v := bundle.Defaults()
	v.MountRoot = mount
	v.TrustFile = trust
	v.AllowTestFixture = true
	v.MaximumAssetBytes = 1 << 20
	v.MaximumTotalBytes = 4 << 20
	v.MinimumFreeBytes = 1 << 20
	return PreparedReleaseRequest{Root: root, Verification: v, ArchiveSHA256: archive, ManifestSHA256: wire.Digest(raw)}
}
func TestPreparedReleaseDescribeBindsTreeJournalAndTrust(t *testing.T) {
	r := preparedReleaseFixture(t)
	p, e := DescribePreparedRelease(context.Background(), r)
	if e != nil || !preparedReleaseProfileValid(p) || p.Bytes <= 4096 || p.Files != 6 {
		t.Fatal("signed complete inventory refused", p, e)
	}
	journal, _ := os.ReadFile(r.Root + ".journal.jsonl")
	journal = bytes.ReplaceAll(journal, []byte("00:00:00Z"), []byte("00:00:01Z"))
	os.WriteFile(r.Root+".journal.jsonl", journal, 0600)
	changed, e := DescribePreparedRelease(context.Background(), r)
	if e != nil || changed.EntriesSHA256 == p.EntriesSHA256 {
		t.Fatal("equivalent new journal was not bound", e)
	}
	var trust map[string]map[string]string
	raw, _ := os.ReadFile(r.Verification.TrustFile)
	json.Unmarshal(raw, &trust)
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{40}, 32)).Public().(ed25519.PublicKey)
	trust["keys"][wire.Digest(key)] = base64.StdEncoding.EncodeToString(key)
	raw, _ = json.Marshal(trust)
	os.WriteFile(r.Verification.TrustFile, raw, 0600)
	updated, e := DescribePreparedRelease(context.Background(), r)
	if e != nil || updated.RequestSHA256 == changed.RequestSHA256 || updated.EntriesSHA256 != changed.EntriesSHA256 {
		t.Fatal("protected trust bytes not independently frozen", e)
	}
}
func TestPreparedReleaseRejectsUnsafeSource(t *testing.T) {
	for _, fault := range []string{"payload", "permission", "symlink", "hardlink", "extra"} {
		t.Run(fault, func(t *testing.T) {
			r := preparedReleaseFixture(t)
			file := filepath.Join(r.Root, "licenses/test.txt")
			switch fault {
			case "payload":
				os.WriteFile(file, []byte("altered"), 0600)
			case "permission":
				os.Chmod(file, 0644)
			case "symlink":
				os.Rename(file, file+".old")
				os.Symlink(file+".old", file)
			case "hardlink":
				os.Link(file, file+".link")
			case "extra":
				os.Mkdir(filepath.Join(r.Root, "untrusted"), 0700)
			}
			if _, e := DescribePreparedRelease(context.Background(), r); e == nil {
				t.Fatal("unsafe prepared source admitted")
			}
		})
	}
}
func preparedReleaseWrapped(t *testing.T, payload []byte, change func(*tar.Header, []byte) []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := tar.NewWriter(&out)
	if e := writer.WriteHeader(&tar.Header{Name: "volume-init", Typeflag: tar.TypeDir, Mode: 0755}); e != nil {
		t.Fatal(e)
	}
	if payload != nil {
		reader := tar.NewReader(bytes.NewReader(payload))
		for {
			h, e := reader.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			data, e := io.ReadAll(reader)
			if e != nil {
				t.Fatal(e)
			}
			h.Name = "volume-init/" + h.Name
			if change != nil {
				data = change(h, data)
			}
			if e = writer.WriteHeader(h); e != nil {
				t.Fatal(e)
			}
			if _, e = writer.Write(data); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := writer.Close(); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}
func TestPreparedReleaseStreamAndReadbackRefuseMetadataCorruption(t *testing.T) {
	r := preparedReleaseFixture(t)
	p, s, e := describePreparedRelease(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = writePreparedReleaseArchive(context.Background(), &out, r, p, s, 10033, 10034, nil); e != nil {
		t.Fatal(e)
	}
	good := preparedReleaseWrapped(t, out.Bytes(), nil)
	observed, e := readPreparedReleaseArchive(bytes.NewReader(good), 10033, 10034, p, preparedReleaseMaximumTar(p))
	if e != nil || observed.Status != ConfigurationPayloadVerified || observed.Bytes != p.Bytes {
		t.Fatal("complete runtime-owned tree refused", e)
	}
	for name, modify := range map[string]func(*tar.Header, []byte) []byte{
		"owner": func(h *tar.Header, b []byte) []byte {
			if h.Typeflag == tar.TypeReg {
				h.Uid = 0
			}
			return b
		},
		"mode": func(h *tar.Header, b []byte) []byte {
			if h.Typeflag == tar.TypeReg {
				h.Mode = 0644
			}
			return b
		},
		"bytes": func(h *tar.Header, b []byte) []byte {
			if len(b) > 0 {
				b = append([]byte(nil), b...)
				b[0] ^= 1
			}
			return b
		},
		"escape": func(h *tar.Header, b []byte) []byte {
			if h.Typeflag == tar.TypeReg {
				h.Name = "volume-init/private/../escape"
			}
			return b
		},
		"pax": func(h *tar.Header, b []byte) []byte {
			if h.Typeflag == tar.TypeReg {
				h.PAXRecords = map[string]string{"PROJECTONE.unknown": "invalid"}
				h.Format = tar.FormatPAX
			}
			return b
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := preparedReleaseWrapped(t, out.Bytes(), modify)
			if _, e := readPreparedReleaseArchive(bytes.NewReader(bad), 10033, 10034, p, preparedReleaseMaximumTar(p)); e == nil {
				t.Fatal("corrupt archive admitted")
			}
		})
	}
	for _, bad := range [][]byte{good[:len(good)-1], append(append([]byte(nil), good...), good...), append(append([]byte(nil), good...), bytes.Repeat([]byte{1}, 512)...)} {
		if _, e := readPreparedReleaseArchive(bytes.NewReader(bad), 10033, 10034, p, preparedReleaseMaximumTar(p)); e == nil {
			t.Fatal("truncated or appended archive admitted")
		}
	}
	empty, e := readPreparedReleaseArchive(bytes.NewReader(preparedReleaseWrapped(t, nil, nil)), 10033, 10034, p, preparedReleaseMaximumTar(p))
	if e != nil || empty.Status != "observed_empty_owned_volume" {
		t.Fatal("entire empty volume not distinguished", e)
	}
}

type preparedReleaseProtocolState struct {
	puts   atomic.Int32
	stored atomic.Value
}

func preparedReleaseProtocolFixture(t *testing.T, fault string) (*Client, Ownership, string, *preparedReleaseProtocolState) {
	t.Helper()
	o := Ownership{strings.Repeat("a", 32), strings.Repeat("b", 32), RegistryPreparedReleaseRole}
	image := "sha256:" + strings.Repeat("c", 64)
	id := strings.Repeat("d", 64)
	state := &preparedReleaseProtocolState{}
	state.stored.Store([]byte(nil))
	resources := wire.UtilityResources{MemoryBytes: 24 << 20, MilliCPU: 50, PIDs: 12}
	c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		write := func(v any) { json.NewEncoder(w).Encode(v) }
		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/volumes/"):
			write(Volume{Name: o.VolumeName(), Driver: "local", Scope: "local", Labels: o.labels()})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/containers/json"):
			if fault == "foreign" {
				write([]RuntimeObject{{ID: strings.Repeat("f", 64)}})
			} else {
				write([]RuntimeObject{{ID: id}})
			}
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/json"):
			write(map[string]any{"Id": id, "Name": "/" + o.utilityName(), "Image": image, "Config": map[string]any{"Image": image, "User": "10033:10034", "Entrypoint": []string{utilityEntrypoint}, "Labels": o.labels()}, "State": map[string]any{"Status": "created", "Pid": 0, "StartedAt": neverStarted}, "Mounts": []any{map[string]any{"Type": "volume", "Name": o.VolumeName(), "Destination": UtilityDestination, "Driver": "local", "RW": true}}, "HostConfig": map[string]any{"NetworkMode": "none", "ReadonlyRootfs": true, "Memory": resources.MemoryBytes, "NanoCpus": resources.MilliCPU * nanosecondsPerMillicore, "PidsLimit": resources.PIDs, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "RestartPolicy": map[string]string{"Name": "no"}, "Mounts": []any{map[string]any{"Type": "volume", "Source": o.VolumeName(), "Target": UtilityDestination, "ReadOnly": false, "VolumeOptions": map[string]bool{"NoCopy": true}}}}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/archive"):
			if r.URL.Query().Get("path") != UtilityDestination {
				t.Error("unexpected archive path")
			}
			payload := state.stored.Load().([]byte)
			var modify func(*tar.Header, []byte) []byte
			if fault == "corrupt" && len(payload) > 0 {
				modify = func(h *tar.Header, b []byte) []byte {
					if len(b) > 0 {
						b = append([]byte(nil), b...)
						b[0] ^= 1
					}
					return b
				}
			}
			w.Write(preparedReleaseWrapped(t, payload, modify))
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/archive"):
			state.puts.Add(1)
			if r.URL.Query().Get("path") != UtilityDestination || r.URL.Query().Get("copyUIDGID") != "true" || r.URL.Query().Get("noOverwriteDirNonDir") != "true" || r.Header.Get("Content-Type") != "application/x-tar" {
				t.Error("unsafe stream PUT")
			}
			raw, e := io.ReadAll(io.LimitReader(r.Body, 4<<20))
			if e != nil {
				t.Error(e)
			}
			state.stored.Store(raw)
			if fault == "lost-put" {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Error("unexpected mutation/route", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	c.config.Utility = &resources
	return c, o, image, state
}
func TestPreparedReleaseProtocolSinglePutAndUnknownGetOnly(t *testing.T) {
	for _, fault := range []string{"", "lost-put", "corrupt", "foreign", "capacity", "target-change"} {
		t.Run(fault, func(t *testing.T) {
			r := preparedReleaseFixture(t)
			p, s, e := describePreparedRelease(context.Background(), r)
			if e != nil {
				t.Fatal(e)
			}
			c, o, image, state := preparedReleaseProtocolFixture(t, fault)
			channel := preparedReleaseConnection{client: c, close: func() {}, proof: func() string { return "explicit_synthetic_protocol_observer" }}
			observe := func(context.Context, preparedReleaseConnection, Ownership) (NativePostgreSQLStorageObservation, error) {
				fs := NativePostgreSQLStorageFilesystem{BackingRole: NativePostgreSQLDurableBacking, FilesystemType: 0xef53, Device: 64512, Inode: 100, TotalBytes: 1 << 40, AvailableBytes: 1 << 39, AllocationUnitBytes: 4096}
				if fault == "capacity" {
					fs.AvailableBytes = 1
				}
				target := fs
				target.Inode = 101
				if fault == "target-change" && state.puts.Load() > 0 {
					target.Inode++
				}
				return NativePostgreSQLStorageObservation{EngineSHA256: strings.Repeat("1", 64), EndpointSHA256: strings.Repeat("2", 64), PeerProof: "explicit_synthetic_protocol_observer", Filesystem: fs, SourceOwners: []NativePostgreSQLStorageOwner{{Owner: o, Filesystem: target, Mountpoint: "/synthetic-volume-only"}}}, nil
			}
			e = putPreparedReleaseObserved(context.Background(), channel, o, image, 10033, 10034, r, p, s, observe, func(NativePostgreSQLStorageOwner, int64, int64) error { return nil })
			if (e == nil) != (fault == "") {
				t.Fatal("copy outcome incorrect", e)
			}
			expected := int32(1)
			if fault == "foreign" || fault == "capacity" {
				expected = 0
			}
			if state.puts.Load() != expected {
				t.Fatal("unexpected PUT count", state.puts.Load())
			}
			if fault == "" || fault == "lost-put" {
				verified, e := inspectPreparedReleaseObserved(context.Background(), channel, o, image, 10033, 10034, p, observe)
				if e != nil || verified.Status != ConfigurationPayloadVerified || p.Bytes <= c.config.ResponseBytes {
					t.Fatal("full GET-only inventory not verified beyond ResponseBytes", e)
				}
				if fault == "" {
					if e = putPreparedReleaseObserved(context.Background(), channel, o, image, 10033, 10034, r, p, s, observe, func(NativePostgreSQLStorageOwner, int64, int64) error { return nil }); e == nil {
						t.Fatal("existing projection overwritten")
					}
				}
				if state.puts.Load() != 1 {
					t.Fatal("unknown copy replayed")
				}
			}
		})
	}
}
func TestPreparedReleasePhysicalCapacityAndPublicPeerRefusal(t *testing.T) {
	r := preparedReleaseFixture(t)
	p, e := DescribePreparedRelease(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	fs := NativePostgreSQLStorageFilesystem{BackingRole: NativePostgreSQLDurableBacking, TotalBytes: 1 << 30, AvailableBytes: 1 << 30, AllocationUnitBytes: 4096}
	if e = preparedReleaseAllocation(p, fs, 1<<20); e != nil {
		t.Fatal("admitted bounded synthetic allocation refused", e)
	}
	for _, change := range []func(*NativePostgreSQLStorageFilesystem){func(f *NativePostgreSQLStorageFilesystem) { f.AvailableBytes = 1 << 28 }, func(f *NativePostgreSQLStorageFilesystem) { f.BackingRole = "unproven_loop" }, func(f *NativePostgreSQLStorageFilesystem) { f.AllocationUnitBytes = 0 }} {
		bad := fs
		change(&bad)
		if preparedReleaseAllocation(p, bad, 1<<20) == nil {
			t.Fatal("unsafe target capacity admitted")
		}
	}
	if preparedReleaseAllocation(p, fs, 1<<30) == nil {
		t.Fatal("minimum-free requirement ignored")
	}
	c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("unproven peer reached HTTP")
		w.WriteHeader(500)
	})
	c.config.Profile = c.policy.NativeProfile
	owner := Ownership{strings.Repeat("a", 32), strings.Repeat("b", 32), RegistryPreparedReleaseRole}
	if _, e = c.ObservePreparedReleaseStorage(context.Background(), owner); e == nil || calls.Load() != 0 {
		t.Fatal("proxy peer accepted before original kernel proof", e)
	}
}
