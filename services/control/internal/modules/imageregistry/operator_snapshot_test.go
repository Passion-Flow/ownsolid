package imageregistry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func operatorTestConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	c := Defaults()
	c.Endpoint = "https://127.0.0.1:44001"
	c.AllowedCIDRs = []string{"127.0.0.0/8"}
	c.CAFile = filepath.Join(root, "ca.pem")
	c.CredentialFile = filepath.Join(root, "credentials.json")
	c.StoreRoot = filepath.Join(root, "store")
	c.PreparedReleaseRoot = filepath.Join(root, "release")
	c.Verification.MountRoot = root
	c.Verification.TrustFile = filepath.Join(root, "trust.json")
	c.ReleaseManifestSHA256 = strings.Repeat("a", 64)
	c.ReleaseArchiveSHA256 = strings.Repeat("b", 64)
	if e := os.Mkdir(c.StoreRoot, 0700); e != nil {
		t.Fatal(e)
	}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	return c
}

// Explicit tiny synthetic OCI archive and locally generated test signing key.
// Other signed declarations are metadata doubles, never an available release,
// approved M06 image, manufacturer provenance or executable qualification.
// The actual CAS extractor, signature verifier, descriptors and bytes are real.
func operatorSyntheticCAS(t *testing.T, c *Config, ids []string) []cachedImage {
	t.Helper()
	encode := func(v any) []byte {
		raw, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	payload := []byte("Synthetic registry supplementary snapshot fixture.\n")
	if e := tw.WriteHeader(&tar.Header{Name: "fixture.txt", Mode: 0644, Size: int64(len(payload))}); e != nil {
		t.Fatal(e)
	}
	if _, e := tw.Write(payload); e != nil {
		t.Fatal(e)
	}
	if e := tw.Close(); e != nil {
		t.Fatal(e)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, e := gz.Write(layer.Bytes()); e != nil {
		t.Fatal(e)
	}
	if e := gz.Close(); e != nil {
		t.Fatal(e)
	}
	diff := "sha256:" + release.Digest(layer.Bytes())
	config := encode(map[string]any{"architecture": "amd64", "os": "linux", "rootfs": map[string]any{"type": "layers", "diff_ids": []string{diff}}})
	configDigest, layerDigest := "sha256:"+release.Digest(config), "sha256:"+release.Digest(compressed.Bytes())
	layerType := "application/vnd.oci.image.layer.v1.tar+gzip"
	manifest := encode(map[string]any{"schemaVersion": 2, "mediaType": ociManifest, "config": map[string]any{"digest": configDigest, "size": len(config), "mediaType": "application/vnd.oci.image.config.v1+json"}, "layers": []any{map[string]any{"digest": layerDigest, "size": compressed.Len(), "mediaType": layerType}}})
	manifestDigest := "sha256:" + release.Digest(manifest)
	index := encode(map[string]any{"schemaVersion": 2, "manifests": []any{map[string]any{"mediaType": ociManifest, "digest": manifestDigest, "size": len(manifest), "platform": map[string]string{"os": "linux", "architecture": "amd64"}}}})
	files := map[string][]byte{"oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`), "index.json": index, "blobs/sha256/" + manifestDigest[7:]: manifest, "blobs/sha256/" + configDigest[7:]: config, "blobs/sha256/" + layerDigest[7:]: compressed.Bytes()}
	var archive bytes.Buffer
	at := tar.NewWriter(&archive)
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		raw := files[name]
		if e := at.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(raw)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil {
			t.Fatal(e)
		}
		if _, e := at.Write(raw); e != nil {
			t.Fatal(e)
		}
	}
	if e := at.Close(); e != nil {
		t.Fatal(e)
	}
	asset := release.Asset{ID: "synthetic-snapshot-image", Path: "images/synthetic.tar", Kind: "image_archive", Size: int64(archive.Len()), SHA256: release.Digest(archive.Bytes()), Licenses: []string{"platform-license"}, ImageID: manifestDigest, ImageConfigDigest: configDigest, Architecture: "amd64"}
	m := release.Manifest{FormatVersion: 1, ReleaseID: "explicit-synthetic-operator-snapshot", Profile: release.PlatformProfile, SourceSHA256: strings.Repeat("a", 64), SchemaVersion: 16, MinimumPredecessor: 16, MaximumPredecessor: 16, Prerequisites: []string{"Synthetic declaration-only release; no runtime qualification"}, Assets: []release.Asset{asset}}
	for i := 1; i <= 16; i++ {
		m.Capabilities = append(m.Capabilities, fmt.Sprintf("M%02d", i))
	}
	required := release.RequiredAssets(m.Profile)
	requiredIDs := []string{}
	for id := range required {
		requiredIDs = append(requiredIDs, id)
	}
	sort.Strings(requiredIDs)
	for _, id := range requiredIDs {
		a := release.Asset{ID: id, Path: id, Kind: required[id], Size: 1, SHA256: strings.Repeat("b", 64), Licenses: []string{"platform-license"}}
		if a.Kind == "license" {
			a.Licenses = []string{}
		}
		if a.Kind == "image_archive" {
			a.ImageID = manifestDigest
			a.ImageConfigDigest = configDigest
			a.Architecture = "amd64"
		}
		m.Assets = append(m.Assets, a)
	}
	if e := m.Validate(c.Verification.MaximumAssets, c.Verification.MaximumAssetBytes, c.Verification.MaximumTotalBytes, false); e != nil {
		t.Fatal(e)
	}
	public, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(c.Verification.TrustFile, encode(map[string]any{"keys": map[string]string{release.Digest(public): base64.StdEncoding.EncodeToString(public)}}), 0600); e != nil {
		t.Fatal(e)
	}
	signed := encode(m)
	signature := release.Signature{Algorithm: release.SignatureAlgorithm, KeyID: release.Digest(public), ManifestSHA256: release.Digest(signed), Value: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("projectone-offline-release-v1\n"), signed...)))}
	c.ReleaseManifestSHA256 = release.Digest(signed)
	c.ReleaseArchiveSHA256 = release.Digest(archive.Bytes())
	proof := bundle.ImageProof{FormatVersion: 1, Manifest: signed, Signature: encode(signature), AssetID: asset.ID}
	if _, e = bundle.VerifyImageProof(t.Context(), c.Verification, proof); e != nil {
		t.Fatal("actual synthetic signature", e)
	}
	var inventory bundle.ImageInventory
	if e = json.Unmarshal(encode(map[string]any{"manifest_digest": manifestDigest, "config_digest": configDigest, "os": "linux", "architecture": "amd64", "diff_ids": []string{diff}, "layers": []any{map[string]any{"digest": layerDigest, "media_type": layerType, "size_bytes": compressed.Len(), "diff_id": diff}}, "expanded_bytes": layer.Len()}), &inventory); e != nil {
		t.Fatal(e)
	}
	root, e := privateRoot(c.StoreRoot)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	out := []cachedImage{}
	for _, id := range ids {
		if e = root.Mkdir(id, 0700); e != nil {
			t.Fatal(e)
		}
		if e = writePrivate(root, id+"/archive.tar", archive.Bytes()); e != nil {
			t.Fatal(e)
		}
		image := cachedImage{Asset: asset, ReleaseID: m.ReleaseID, ReleaseManifestSHA256: c.ReleaseManifestSHA256, ReleaseArchiveSHA256: c.ReleaseArchiveSHA256, Inventory: inventory, Archive: bundle.ImportReceipt{SourceSHA256: asset.SHA256, TransportSHA256: asset.SHA256, TransportBytes: asset.Size, ExpandedBytes: int64(layer.Len())}, Proof: proof}
		if e = extractImage(t.Context(), *c, root, id, &image); e != nil {
			t.Fatal("original CAS extractor", e)
		}
		if e = writePrivate(root, id+"/inventory.json", encode(image)); e != nil {
			t.Fatal(e)
		}
		if _, e = readCached(t.Context(), *c, id); e != nil {
			t.Fatal("original CAS readback", e)
		}
		out = append(out, image)
	}
	return out
}

func TestOperatorSnapshotIndependentRealMultiWorkspaceCASAndTrustProjection(t *testing.T) {
	c := operatorTestConfig(t)
	ids := []string{"22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"}
	images := operatorSyntheticCAS(t, &c, ids)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	tree, e := scanOperatorTree(ctx, c.StoreRoot, c)
	if e != nil {
		t.Fatal(e)
	}
	defer tree.close()
	pins := []operatorPin{}
	for i, id := range ids {
		raw, _ := json.Marshal(images[i])
		pins = append(pins, operatorPin{id, fmt.Sprintf("44444444-4444-4444-8444-%012d", i+1), release.Digest(raw)})
	}
	trustSHA, e := operatorTrustSHA(ctx, c, c.Verification.TrustFile)
	if e != nil {
		t.Fatal(e)
	}
	metadata := []operatorMetadata{}
	for _, table := range operatorMetadataTables {
		metadata = append(metadata, operatorMetadata{table, 0, release.Digest(nil)})
	}
	m := operatorManifest{operatorManifestFormat, operatorTestBinding(), tree.identity, operatorConfigurationSHA(c), c.ReleaseManifestSHA256, c.ReleaseArchiveSHA256, trustSHA, pins, metadata, tree.files}
	raw, _ := json.Marshal(m)
	expected := m.receipt(raw)
	parent, _, e := operatorRoot(c.Verification.MountRoot)
	if e != nil {
		t.Fatal(e)
	}
	defer parent.Close()
	if e = parent.Mkdir("snapshot", 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(c.Verification.MountRoot, "snapshot")
	target, _, e := operatorRoot(path)
	if e != nil {
		t.Fatal(e)
	}
	defer target.Close()
	if e = copyOperatorPins(ctx, tree, target, pins, tree.files); e != nil {
		t.Fatal(e)
	}
	if e = writePrivate(target, operatorManifestName, raw); e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyOperatorSnapshot(ctx, c, path, expected); e != nil {
		t.Fatal("actual all-Workspace byte readback", e)
	}
	projection := filepath.Join(c.Verification.MountRoot, "projected-trust.json")
	trust, e := os.ReadFile(c.Verification.TrustFile)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(projection, trust, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(c.Verification.TrustFile, c.Verification.TrustFile+"-source"); e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyOperatorSnapshotWithTrustFile(ctx, c, path, expected, projection); e != nil {
		t.Fatal("explicit hash-matched trust projection", e)
	}
	changed := c
	changed.StoreRoot = filepath.Join(c.Verification.MountRoot, "foreign-root")
	if _, e = VerifyOperatorSnapshotWithTrustFile(ctx, changed, path, expected, projection); e == nil {
		t.Fatal("source configuration identity changed")
	}
	if e = os.WriteFile(projection, []byte("foreign trust"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyOperatorSnapshotWithTrustFile(ctx, c, path, expected, projection); e == nil {
		t.Fatal("foreign trust admitted")
	}
	if e = os.WriteFile(projection, trust, 0600); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(path, ids[0], "archive.tar")
	f, e := os.OpenFile(file, os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt([]byte("tamper"), 0); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if _, e = VerifyOperatorSnapshotWithTrustFile(ctx, c, path, expected, projection); e == nil {
		t.Fatal("tampered archive admitted")
	}
	t.Logf("real synthetic CAS: Workspaces=%d pins=%d files=%d bytes=%d manifest=%s", len(pins), expected.Pins, expected.Files, expected.Bytes, expected.ManifestSHA256)
}

func operatorTestBinding() OperatorSnapshotBinding {
	return OperatorSnapshotBinding{operatorApplicationProfile, "11111111-1111-4111-8111-111111111111", "synthetic_db_test", "synthetic_operator", strings.Repeat("a", 32), "synthetic-system", 42, 100}
}

func TestOperatorSnapshotRefusesBrowserPoolForeignAndUnboundedAuthority(t *testing.T) {
	c := operatorTestConfig(t)
	owner, e := NewOperatorSnapshot(c, operatorTestBinding())
	if e != nil {
		t.Fatal(e)
	}
	for _, q := range []transaction.Executor{nil, restoreNoQueryExecutor{dialect: transaction.MySQL}, restoreNoQueryStore{restoreNoQueryExecutor{dialect: transaction.PostgreSQL}}} {
		if e = owner.VerifyStoredWithin(t.Context(), q); e == nil {
			t.Fatal("independent pool/foreign executor admitted")
		}
	}
	if e = owner.VerifyStoredWithin(context.Background(), restoreNoQueryExecutor{dialect: transaction.PostgreSQL}); e == nil || e.Error() != "REGISTRY_OPERATOR_DEADLINE_REQUIRED" {
		t.Fatal(e)
	}
	for _, change := range []func(*OperatorSnapshotBinding){func(b *OperatorSnapshotBinding) { b.ApplicationProfile = "releases" }, func(b *OperatorSnapshotBinding) { b.ScopeID = "foreign" }, func(b *OperatorSnapshotBinding) { b.BackendPID = 0 }, func(b *OperatorSnapshotBinding) { b.TenantID = "" }} {
		b := operatorTestBinding()
		change(&b)
		if _, e = NewOperatorSnapshot(c, b); e == nil {
			t.Fatal("foreign binding admitted")
		}
	}
}

func TestOperatorSnapshotActualFilesystemCustodyAndBoundedCopy(t *testing.T) {
	c := operatorTestConfig(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	const pin = "22222222-2222-4222-8222-222222222222"
	for _, dir := range []string{pin, pin + "/blobs"} {
		if e := os.Mkdir(filepath.Join(c.StoreRoot, dir), 0700); e != nil {
			t.Fatal(e)
		}
	}
	name := pin + "/archive.tar"
	content := []byte("Explicit synthetic snapshot custody bytes.\n")
	if e := os.WriteFile(filepath.Join(c.StoreRoot, name), content, 0600); e != nil {
		t.Fatal(e)
	}
	tree, e := scanOperatorTree(ctx, c.StoreRoot, c)
	if e != nil {
		t.Fatal(e)
	}
	defer tree.close()
	if len(tree.files) != 1 || tree.bytes != int64(len(content)) {
		t.Fatal("actual content closure omitted")
	}
	target := filepath.Join(c.Verification.MountRoot, "copy")
	if e = os.Mkdir(target, 0700); e != nil {
		t.Fatal(e)
	}
	r, _, e := operatorRoot(target)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if e = copyOperatorPins(ctx, tree, r, []operatorPin{{ID: pin}}, tree.files); e != nil {
		t.Fatal(e)
	}
	copy, e := scanOperatorTree(ctx, target, c)
	if e != nil {
		t.Fatal(e)
	}
	defer copy.close()
	if operatorFilesSHA(copy.files) != operatorFilesSHA(tree.files) {
		t.Fatal("copy bytes changed")
	}
	if e = operatorCopy(ctx, tree, r, tree.files[0]); e == nil {
		t.Fatal("existing bytes overwritten")
	}
	before, e := os.Stat(filepath.Join(c.StoreRoot, name))
	if e != nil {
		t.Fatal(e)
	}
	mutated := append([]byte(nil), content...)
	mutated[0] ^= 1
	if e = os.WriteFile(filepath.Join(c.StoreRoot, name), mutated, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chtimes(filepath.Join(c.StoreRoot, name), before.ModTime(), before.ModTime()); e != nil {
		t.Fatal(e)
	}
	if e = tree.check(ctx); e == nil {
		t.Fatal("equal-length restored-mtime rewrite admitted")
	}
}

func TestOperatorSnapshotRejectsActualLinksFIFOUnknownAndCancellation(t *testing.T) {
	for _, fault := range []string{"symlink", "hardlink", "fifo", "mode", "foreign-directory", "missing", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			c := operatorTestConfig(t)
			name := filepath.Join(c.StoreRoot, "archive.tar")
			if e := os.WriteFile(name, []byte("synthetic"), 0600); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			switch fault {
			case "symlink":
				if e := os.Rename(name, name+"-original"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(name+"-original", name); e != nil {
					t.Fatal(e)
				}
			case "hardlink":
				if e := os.Link(name, name+"-second"); e != nil {
					t.Fatal(e)
				}
			case "fifo":
				if e := os.Remove(name); e != nil {
					t.Fatal(e)
				}
				if e := syscall.Mkfifo(name, 0600); e != nil {
					t.Fatal(e)
				}
			case "mode":
				if e := os.Chmod(name, 0644); e != nil {
					t.Fatal(e)
				}
			case "foreign-directory":
				if e := os.Mkdir(filepath.Join(c.StoreRoot, "unexpected"), 0700); e != nil {
					t.Fatal(e)
				}
			case "missing":
				if e := os.Remove(name); e != nil {
					t.Fatal(e)
				}
			case "cancelled":
				cancel()
			}
			tree, e := scanOperatorTree(ctx, c.StoreRoot, c)
			if tree != nil {
				defer tree.close()
				e = exactOperatorFiles(tree, map[string]bool{"archive.tar": true}, false)
			}
			if e == nil {
				t.Fatal("unsafe or incomplete closure admitted", fault)
			}
		})
	}
}

func TestOperatorSnapshotRootIdentityAndCompletionCannotBeAdopted(t *testing.T) {
	c := operatorTestConfig(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	tree, e := scanOperatorTree(ctx, c.StoreRoot, c)
	if e != nil {
		t.Fatal(e)
	}
	defer tree.close()
	if e = os.Rename(c.StoreRoot, c.StoreRoot+"-original"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(c.StoreRoot, 0700); e != nil {
		t.Fatal(e)
	}
	if e = tree.check(ctx); e == nil {
		t.Fatal("replacement root adopted")
	}
	if _, e = VerifyOperatorSnapshot(ctx, c, filepath.Join(c.Verification.MountRoot, "missing"), OperatorSnapshotReceipt{}); e == nil {
		t.Fatal("missing payload treated as empty")
	}
	if e = ReadbackOperatorImport(ctx, c, OperatorSnapshotImportReceipt{Complete: true}); e == nil {
		t.Fatal("manufactured completion admitted")
	}
}

func TestOperatorSnapshotInterruptedCopyRetainsUncertainBytesAndRefusesReplay(t *testing.T) {
	c := operatorTestConfig(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if e := os.WriteFile(filepath.Join(c.StoreRoot, "archive.tar"), []byte("Explicit interrupted-copy fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	source, e := scanOperatorTree(ctx, c.StoreRoot, c)
	if e != nil {
		t.Fatal(e)
	}
	defer source.close()
	path := filepath.Join(c.Verification.MountRoot, "uncertain")
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	target, _, e := operatorRoot(path)
	if e != nil {
		t.Fatal(e)
	}
	defer target.Close()
	interrupted, stop := context.WithCancel(ctx)
	stop()
	if e = operatorCopy(interrupted, source, target, source.files[0]); e == nil {
		t.Fatal("interrupted copy reported completion")
	}
	info, e := target.Lstat("archive.tar")
	if e != nil || info.Size() != 0 {
		t.Fatal("uncertain exact exclusive file was discarded", e)
	}
	if e = operatorCopy(ctx, source, target, source.files[0]); e == nil {
		t.Fatal("uncertain copy replay overwrote original file")
	}
}

func TestOperatorSnapshotEmptyManifestDiffersFromMissingPayload(t *testing.T) {
	c := operatorTestConfig(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	// No image is claimed by this empty snapshot. The trust file is still an
	// explicit retained protected input with an independently bound byte hash.
	if e := os.WriteFile(c.Verification.TrustFile, []byte(`{"keys":{}}`), 0600); e != nil {
		t.Fatal(e)
	}
	source, e := scanOperatorTree(ctx, c.StoreRoot, c)
	if e != nil {
		t.Fatal(e)
	}
	defer source.close()
	trustSHA, e := operatorTrustSHA(ctx, c, c.Verification.TrustFile)
	if e != nil {
		t.Fatal(e)
	}
	metadata := []operatorMetadata{}
	for _, table := range operatorMetadataTables {
		metadata = append(metadata, operatorMetadata{table, 0, release.Digest(nil)})
	}
	m := operatorManifest{operatorManifestFormat, operatorTestBinding(), source.identity, operatorConfigurationSHA(c), c.ReleaseManifestSHA256, c.ReleaseArchiveSHA256, trustSHA, []operatorPin{}, metadata, []operatorFile{}}
	raw, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	expected := m.receipt(raw)
	path := filepath.Join(c.Verification.MountRoot, "empty-snapshot")
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	target, _, e := operatorRoot(path)
	if e != nil {
		t.Fatal(e)
	}
	defer target.Close()
	if e = writePrivate(target, operatorManifestName, raw); e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyOperatorSnapshot(ctx, c, path, expected); e != nil {
		t.Fatal("explicit empty payload rejected", e)
	}
	if expected.Pins != 0 || expected.Files != 0 || expected.Bytes != 0 || !release.ValidSHA256(expected.ManifestSHA256) {
		t.Fatal("invalid empty receipt", expected)
	}
	if e = target.Remove(operatorManifestName); e != nil {
		t.Fatal(e)
	}
	if _, e = VerifyOperatorSnapshot(ctx, c, path, expected); e == nil {
		t.Fatal("missing manifest adopted as empty")
	}
}
