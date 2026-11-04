package factory

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Passion-Flow/ownsolid/api/release/releasetest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Every key/model/image in these fixtures is synthetic and has no production provenance.
func setup(t *testing.T) (Config, wire.Manifest, ed25519.PrivateKey) {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	mount := root
	for mounted(mount) != nil {
		parent := filepath.Dir(mount)
		if parent == mount {
			t.Skip("tests require an actual nonroot external mounted TMPDIR; run make test-offline on Linux")
		}
		mount = parent
	}
	assets := filepath.Join(root, "assets")
	if e := os.Mkdir(assets, 0700); e != nil {
		t.Fatal(e)
	}
	c, e := LoadConfig([]byte(`{}`), nil)
	if e != nil {
		t.Fatal(e)
	}
	c.MountRoot = mount
	c.ArtifactRoot = assets
	c.ManifestFile = filepath.Join(root, "manifest.json")
	c.TrustFile = filepath.Join(root, "trust.json")
	c.PrivateKeyFile = filepath.Join(root, "synthetic-private-key")
	c.OutputFile = filepath.Join(root, "synthetic.tar.gz")
	c.AllowTestFixture = true
	c.MaximumAssetBytes = 1 << 20
	c.MaximumTotalBytes = 1 << 22
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{19}, ed25519.SeedSize))
	public := key.Public().(ed25519.PublicKey)
	put(t, c.PrivateKeyFile, []byte(base64.StdEncoding.EncodeToString(key)))
	put(t, c.TrustFile, Encode(Trust{Keys: map[string]string{wire.Digest(public): base64.StdEncoding.EncodeToString(public)}}))
	m := wire.Manifest{FormatVersion: 1, ReleaseID: "synthetic-fixture-1", Profile: wire.FixtureProfile, SourceSHA256: strings.Repeat("a", 64), SchemaVersion: 16, MinimumPredecessor: 15, MaximumPredecessor: 16, Capabilities: []string{"M01"}, Prerequisites: []string{}, Assets: []wire.Asset{}}
	for _, spec := range []struct{ id, kind, content string }{{"license", "license", "Synthetic test license\n"}, {"fixture", "operator", "Synthetic operator input, never a platform executable\n"}} {
		raw := []byte(spec.content)
		a := wire.Asset{ID: spec.id, Path: spec.id, Kind: spec.kind, Size: int64(len(raw)), SHA256: wire.Digest(raw), Licenses: []string{}}
		if spec.kind != "license" {
			a.Licenses = []string{"license"}
		}
		m.Assets = append(m.Assets, a)
		put(t, filepath.Join(assets, a.Path), raw)
	}
	put(t, c.ManifestFile, Encode(m))
	return c, m, key
}
func put(t *testing.T, name string, raw []byte) {
	t.Helper()
	if e := os.WriteFile(name, raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestEnvironmentConfiguration(t *testing.T) {
	c, e := LoadConfig([]byte(`{"allow_test_fixture":true,"timeout_seconds":200}`), []byte("PROJECT_ONE_OFFLINE_FACTORY_ALLOW_TEST_FIXTURE=false\nPROJECT_ONE_OFFLINE_FACTORY_TIMEOUT_SECONDS=7\n"))
	if e != nil || c.AllowTestFixture || c.TimeoutSeconds != 7 {
		t.Fatal("env precedence/false", e)
	}
	for name, env := range map[string]string{"unknown": "UNUSED=true", "duplicate": "PROJECT_ONE_OFFLINE_FACTORY_TIMEOUT_SECONDS=7\nPROJECT_ONE_OFFLINE_FACTORY_TIMEOUT_SECONDS=8", "truthy": "PROJECT_ONE_OFFLINE_FACTORY_ALLOW_TEST_FIXTURE=False", "empty": "PROJECT_ONE_OFFLINE_FACTORY_TIMEOUT_SECONDS=", "injection": "PROJECT_ONE_OFFLINE_FACTORY_OUTPUT_FILE=$(pwd)", "range": "PROJECT_ONE_OFFLINE_FACTORY_TIMEOUT_SECONDS=0"} {
		t.Run(name, func(t *testing.T) {
			if _, e := LoadConfig([]byte(`{}`), []byte(env)); e == nil {
				t.Fatal("invalid environment accepted")
			}
		})
	}
	for _, raw := range []string{`{"timeout_seconds":null}`, `{"timeout_seconds":1,"timeout_seconds":2}`, `{"unknown":true}`, `{"compression_level":0}`, `{"maximum_metadata_bytes":1}`, `{"maximum_total_bytes":1}`} {
		if _, e := LoadConfig([]byte(raw), nil); e == nil {
			t.Fatal("invalid config", raw)
		}
	}
}
func TestActualFixtureArchiveRoundtripAndExclusiveOutput(t *testing.T) {
	c, _, _ := setup(t)
	result, e := Build(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	if result.Qualification != "synthetic_test_fixture_only_never_complete_platform_bundle" || result.Assets != 2 {
		t.Fatal(result)
	}
	before, _ := os.ReadFile(c.OutputFile)
	r, e := Inspect(context.Background(), c, c.OutputFile)
	if e != nil || r.ArchiveSHA256 != wire.Digest(before) {
		t.Fatal(r, e)
	}
	if _, e = Build(context.Background(), c); e == nil {
		t.Fatal("occupied output overwritten")
	}
	after, _ := os.ReadFile(c.OutputFile)
	if !bytes.Equal(before, after) {
		t.Fatal("occupied output changed")
	}
	if _, e = os.Stat(c.OutputFile + ".incomplete"); !os.IsNotExist(e) {
		t.Fatal("successful scratch not removed")
	}
	events, e := os.ReadFile(c.OutputFile + ".journal.jsonl")
	if e != nil || !bytes.Contains(events, []byte(`"status":"published"`)) || bytes.Contains(events, []byte(c.PrivateKeyFile)) {
		t.Fatal("journal invalid")
	}
}
func TestInputFailuresAreCollectedAndNoOutputIsCreated(t *testing.T) {
	c, m, _ := setup(t)
	if e := os.Remove(filepath.Join(c.ArtifactRoot, m.Assets[0].Path)); e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(c.ArtifactRoot, m.Assets[1].Path), bytes.Repeat([]byte("x"), int(m.Assets[1].Size)))
	v, e := Validate(context.Background(), c)
	if e == nil || len(v.Issues) != 2 {
		t.Fatal(v, e)
	}
	if _, e = Build(context.Background(), c); e == nil {
		t.Fatal("invalid source built")
	}
	for _, s := range []string{"", ".incomplete", ".journal.jsonl"} {
		if _, e = os.Stat(c.OutputFile + s); !os.IsNotExist(e) {
			t.Fatal("output on validation failure")
		}
	}
}
func TestSymlinkHardlinkAndSecretInputs(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "secret-mode", "trust-mismatch", "key-in-assets", "fake-mount"} {
		t.Run(kind, func(t *testing.T) {
			c, m, _ := setup(t)
			file := filepath.Join(c.ArtifactRoot, m.Assets[1].Path)
			switch kind {
			case "symlink":
				os.Rename(file, file+".original")
				os.Symlink(file+".original", file)
			case "hardlink":
				os.Link(file, file+".link")
			case "secret-mode":
				os.Chmod(c.PrivateKeyFile, 0644)
			case "trust-mismatch":
				put(t, c.TrustFile, Encode(Trust{Keys: map[string]string{strings.Repeat("b", 64): "invalid"}}))
			case "key-in-assets":
				c.PrivateKeyFile = file
			case "fake-mount":
				c.MountRoot = filepath.Dir(c.ArtifactRoot)
			}
			if _, e := Build(context.Background(), c); e == nil {
				t.Fatal("unsafe input accepted")
			}
			if _, e := os.Stat(c.OutputFile); !os.IsNotExist(e) {
				t.Fatal("final output created")
			}
		})
	}
}
func TestDeadlineRefusesAndPreservesInputs(t *testing.T) {
	c, _, _ := setup(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, e := Build(ctx, c); e == nil {
		t.Fatal("deadline ignored")
	}
	if _, e := os.Stat(c.OutputFile); !os.IsNotExist(e) {
		t.Fatal("final exists")
	}
	if _, e := os.Stat(c.ManifestFile); e != nil {
		t.Fatal("input removed")
	}
}

func fixtureArchive(t *testing.T, c Config, m wire.Manifest, key ed25519.PrivateKey, change func(*[]byte, *wire.Signature, []*tar.Header, [][]byte)) []byte {
	t.Helper()
	raw := Encode(m)
	signature := wire.Signature{Algorithm: wire.SignatureAlgorithm, KeyID: wire.Digest(key.Public().(ed25519.PublicKey)), ManifestSHA256: wire.Digest(raw), Value: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte(signatureDomain), raw...)))}
	headers := []*tar.Header{}
	contents := [][]byte{}
	for _, a := range m.Assets {
		b, e := os.ReadFile(filepath.Join(c.ArtifactRoot, a.Path))
		if e != nil {
			t.Fatal(e)
		}
		contents = append(contents, b)
		headers = append(headers, &tar.Header{Name: a.Path, Mode: 0644, Size: int64(len(b)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR})
	}
	if change != nil {
		change(&raw, &signature, headers, contents)
	}
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	tw := tar.NewWriter(gz)
	for _, p := range []struct {
		name string
		b    []byte
	}{{"manifest.json", raw}, {"signature.json", Encode(signature)}} {
		if e := tw.WriteHeader(&tar.Header{Name: p.name, Mode: 0644, Size: int64(len(p.b)), Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		tw.Write(p.b)
	}
	for i, h := range headers {
		if e := tw.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write(contents[i])
		}
	}
	tw.Close()
	gz.Close()
	return output.Bytes()
}
func TestArchiveTrustAndEntryAttacks(t *testing.T) {
	for _, attack := range []string{"payload", "signature", "manifest", "unknown-key", "traversal", "symlink", "hardlink", "duplicate", "mode", "gzip-trailer", "second-gzip", "truncated"} {
		t.Run(attack, func(t *testing.T) {
			c, m, key := setup(t)
			raw := fixtureArchive(t, c, m, key, func(raw *[]byte, s *wire.Signature, headers []*tar.Header, contents [][]byte) {
				switch attack {
				case "payload":
					contents[1][0] ^= 1
				case "signature":
					s.Value = base64.StdEncoding.EncodeToString(make([]byte, 64))
				case "manifest":
					*raw = append(*raw, ' ')
				case "unknown-key":
					s.KeyID = strings.Repeat("b", 64)
				case "traversal":
					headers[1].Name = "../escape"
				case "symlink":
					headers[1].Typeflag = tar.TypeSymlink
					headers[1].Size = 0
					headers[1].Linkname = "/escape"
				case "hardlink":
					headers[1].Typeflag = tar.TypeLink
					headers[1].Size = 0
					headers[1].Linkname = "license"
				case "duplicate":
					headers[1].Name = headers[0].Name
				case "mode":
					headers[1].Mode = 0777
				}
			})
			switch attack {
			case "gzip-trailer":
				raw = append(raw, []byte("extra")...)
			case "second-gzip":
				raw = append(raw, raw...)
			case "truncated":
				raw = raw[:len(raw)-7]
			}
			put(t, c.OutputFile, raw)
			if _, e := Inspect(context.Background(), c, c.OutputFile); e == nil {
				t.Fatal("archive attack accepted")
			}
		})
	}
}

func TestOCIClosure(t *testing.T) {
	for _, attack := range []string{"valid", "wrong-arch", "wrong-id", "wrong-config", "blob-corrupt", "missing-blob", "remote-descriptor", "duplicate", "trailing"} {
		t.Run(attack, func(t *testing.T) {
			c, _, _ := setup(t)
			identity := map[string]any{"os": "linux", "architecture": "amd64", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{}}, "config": map[string]any{"Cmd": nil}}
			if attack == "wrong-arch" {
				identity["architecture"] = "arm64"
			}
			config := Encode(identity)
			id := "sha256:" + wire.Digest(config)
			configDescriptor := descriptor{MediaType: "application/vnd.oci.image.config.v1+json", Digest: id, Size: int64(len(config))}
			if attack == "remote-descriptor" {
				configDescriptor.URLs = []string{"https://invalid.example/never-fetch"}
			}
			manifest := Encode(imageDocument{SchemaVersion: 2, Config: configDescriptor, Layers: []descriptor{}})
			manifestID := "sha256:" + wire.Digest(manifest)
			index := Encode(imageDocument{SchemaVersion: 2, Manifests: []descriptor{{MediaType: "application/vnd.oci.image.manifest.v1+json", Digest: manifestID, Size: int64(len(manifest))}}})
			var image bytes.Buffer
			tw := tar.NewWriter(&image)
			entries := []struct {
				name string
				raw  []byte
			}{{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)}, {"index.json", index}, {"blobs/sha256/" + wire.Digest(manifest), manifest}, {"blobs/sha256/" + wire.Digest(config), config}}
			if attack == "missing-blob" {
				entries = entries[:3]
			}
			if attack == "blob-corrupt" {
				entries[3].raw = append([]byte(nil), config...)
				entries[3].raw[0] = '['
			}
			if attack == "duplicate" {
				entries = append(entries, entries[0])
			}
			for _, entry := range entries {
				tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0644, Size: int64(len(entry.raw)), Typeflag: tar.TypeReg})
				tw.Write(entry.raw)
			}
			tw.Close()
			if attack == "trailing" {
				image.WriteString("extra")
			}
			if attack == "wrong-id" {
				manifestID = "sha256:" + strings.Repeat("b", 64)
			}
			if attack == "wrong-config" {
				id = "sha256:" + strings.Repeat("d", 64)
			}
			a := wire.Asset{Kind: "image_archive", ImageID: manifestID, ImageConfigDigest: id, Architecture: "amd64", Size: int64(image.Len())}
			e := validateImage(context.Background(), bytes.NewReader(image.Bytes()), a, c)
			if attack == "valid" && e != nil {
				t.Fatal(e)
			}
			if attack != "valid" && e == nil {
				t.Fatal("unsafe OCI accepted")
			}
		})
	}
}

func TestConfigJSONUsesNoAmbientOverrides(t *testing.T) {
	t.Setenv("PROJECT_ONE_OFFLINE_FACTORY_ALLOW_TEST_FIXTURE", "true")
	c, e := LoadConfig([]byte(`{}`), nil)
	if e != nil || c.AllowTestFixture {
		t.Fatal("ambient input affected configuration")
	}
	raw := Encode(c)
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		t.Fatal("config serialization")
	}
}

func TestPublicationFailureRecoveryWithExplicitDurabilityDoubles(t *testing.T) {
	for _, fault := range []string{"initial-directory-sync", "exclusive-link", "unlink", "final-directory-sync", "source-changed", "cancelled-after-reservation"} {
		t.Run(fault, func(t *testing.T) {
			c, m, _ := setup(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			syncs := 0
			ops := durableIO{syncDirectory: func(f *os.File) error {
				syncs++
				if fault == "initial-directory-sync" && syncs == 1 || fault == "final-directory-sync" && syncs == 2 {
					return errors.New("explicit synthetic directory fsync failure")
				}
				if fault == "source-changed" && syncs == 1 {
					put(t, filepath.Join(c.ArtifactRoot, m.Assets[1].Path), bytes.Repeat([]byte("x"), int(m.Assets[1].Size)))
				}
				if fault == "cancelled-after-reservation" && syncs == 1 {
					cancel()
				}
				return f.Sync()
			}, link: func(a, b string) error {
				if fault == "exclusive-link" {
					return errors.New("explicit synthetic link failure")
				}
				return os.Link(a, b)
			}, remove: func(p string) error {
				if fault == "unlink" {
					return errors.New("explicit synthetic unlink failure")
				}
				return os.Remove(p)
			}}
			if _, e := build(ctx, c, ops); e == nil {
				t.Fatal("fault marked passed")
			}
			events, e := os.ReadFile(c.OutputFile + ".journal.jsonl")
			if e != nil || bytes.Contains(events, []byte(`"status":"published"`)) || !bytes.Contains(events, []byte(`"status":"failed_preserve_incomplete_or_uncertain_publication"`)) {
				t.Fatal("failed journal lost", e)
			}
			if fault == "unlink" {
				r, e := Inspect(context.Background(), c, c.OutputFile)
				if e != nil || r.Status != "passed_signed_archive_readback_publication_links_uncertain" {
					t.Fatal("uncertain known links not readable", r, e)
				}
				if _, e = Build(context.Background(), c); e == nil {
					t.Fatal("uncertain publish retried")
				}
			} else if fault == "exclusive-link" {
				if _, e := Inspect(context.Background(), c, c.OutputFile+".incomplete"); e != nil {
					t.Fatal("verified incomplete lost", e)
				}
			}
			if fault != "unlink" && fault != "final-directory-sync" {
				if _, e = os.Stat(c.OutputFile); !os.IsNotExist(e) {
					t.Fatal("final-looking output before verified publication")
				}
			}
		})
	}
}
func TestCapacityBoundaryAndUnrecognizedHardlinks(t *testing.T) {
	for _, v := range []struct {
		total, available uint64
		additional       int64
		want             bool
	}{{100, 30, 0, true}, {100, 30, 1, false}, {101, 30, 0, false}, {101, 31, 0, true}, {100, 29, 0, false}, {100, 80, 51, false}, {100, 80, 50, true}, {100, 80, -1, false}, {0, 0, 0, false}} {
		if hasHeadroom(v.total, v.available, v.additional) != v.want {
			t.Fatal(v)
		}
	}
	c, _, _ := setup(t)
	if _, e := Build(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(c.OutputFile, c.OutputFile+".unexpected-link"); e != nil {
		t.Fatal(e)
	}
	if _, e := Inspect(context.Background(), c, c.OutputFile); e == nil {
		t.Fatal("unknown hardlink accepted")
	}
}

func TestConcurrentManufacturingHasOneDurableWinner(t *testing.T) {
	c, _, _ := setup(t)
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; _, e := Build(context.Background(), c); outcomes <- e }()
	}
	close(start)
	passed := 0
	for i := 0; i < 2; i++ {
		if <-outcomes == nil {
			passed++
		}
	}
	if passed != 1 {
		t.Fatal("expected one exclusive manufacturer", passed)
	}
	if _, e := Inspect(context.Background(), c, c.OutputFile); e != nil {
		t.Fatal("winner archive corrupted", e)
	}
	events, e := os.ReadFile(c.OutputFile + ".journal.jsonl")
	if e != nil || bytes.Count(events, []byte(`"status":"published"`)) != 1 {
		t.Fatal("winner journal corrupted", e)
	}
}
func TestAllRequiredAssetsAreReported(t *testing.T) {
	c, m, _ := setup(t)
	releasetest.Select(&m)
	put(t, c.ManifestFile, Encode(m))
	v, e := Validate(context.Background(), c)
	if e == nil || len(v.Issues) != len(wire.RequiredAssets(m.Profile)) {
		t.Fatal("missing production artifacts concealed", len(v.Issues), e)
	}
}
func TestDebEnvelopeRejectsTruncationExtraAndInvalidVersion(t *testing.T) {
	makeDeb := func() []byte {
		var b bytes.Buffer
		b.WriteString("!<arch>\n")
		for _, v := range []struct {
			name string
			raw  []byte
		}{{"debian-binary", []byte("2.0\n")}, {"control.tar.gz", []byte{0x1f, 0x8b, 1}}, {"data.tar.xz", []byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0, 1}}} {
			fmt.Fprintf(&b, "%-16s%-12d%-6d%-6d%-8s%-10d`\n", v.name+"/", 0, 0, 0, "100644", len(v.raw))
			b.Write(v.raw)
			if len(v.raw)%2 != 0 {
				b.WriteByte('\n')
			}
		}
		return b.Bytes()
	}
	raw := makeDeb()
	if e := validateDeb(bytes.NewReader(raw), int64(len(raw))); e != nil {
		t.Fatal(e)
	}
	for _, attack := range []string{"truncated", "extra", "version", "magic", "header-only"} {
		t.Run(attack, func(t *testing.T) {
			raw := makeDeb()
			switch attack {
			case "truncated":
				raw = raw[:len(raw)-3]
			case "extra":
				raw = append(raw, 'x')
			case "version":
				raw[68] = '9'
			case "magic":
				raw[0] = 'x'
			case "header-only":
				raw = raw[:8]
			}
			if validateDeb(bytes.NewReader(raw), int64(len(raw))) == nil {
				t.Fatal("invalid Debian package accepted")
			}
		})
	}
}
