package imageregistry

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/google/uuid"
)

type cachedImage struct {
	Asset                 release.Asset         `json:"asset"`
	ReleaseID             string                `json:"release_id"`
	ReleaseManifestSHA256 string                `json:"release_manifest_sha256"`
	ReleaseArchiveSHA256  string                `json:"release_archive_sha256"`
	Inventory             bundle.ImageInventory `json:"inventory"`
	Archive               bundle.ImportReceipt  `json:"archive"`
	Descriptors           []Descriptor          `json:"descriptors"`
	Proof                 bundle.ImageProof     `json:"release_proof"`
}
type imageManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Config        struct {
		Digest    string   `json:"digest"`
		Size      int64    `json:"size"`
		MediaType string   `json:"mediaType"`
		URLs      []string `json:"urls"`
	} `json:"config"`
	Layers []struct {
		Digest    string   `json:"digest"`
		Size      int64    `json:"size"`
		MediaType string   `json:"mediaType"`
		URLs      []string `json:"urls"`
	} `json:"layers"`
}

func validID(v string) bool {
	u, e := uuid.Parse(v)
	return e == nil && u.String() == v && u != uuid.Nil
}
func privateRoot(path string) (*os.Root, error) {
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0700 || info.Mode()&os.ModeSymlink != 0 {
		return nil, failure("REGISTRY_STORE_INVALID")
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return nil, failure("REGISTRY_STORE_INVALID")
	}
	return root, nil
}
func syncDirectory(root *os.Root, name string) error {
	f, e := root.Open(name)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func writePrivate(root *os.Root, name string, raw []byte) error {
	f, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	return joinedClose(f, e)
}
func joinedClose(f *os.File, e error) error {
	closeErr := f.Close()
	if e != nil {
		return e
	}
	return closeErr
}
func hashFile(ctx context.Context, root *os.Root, name string, maximum int64) (string, int64, error) {
	f, e := root.Open(name)
	if e != nil {
		return "", 0, failure("REGISTRY_CONTENT_MISSING")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < 1 || info.Size() > maximum {
		return "", 0, failure("REGISTRY_CONTENT_INVALID")
	}
	h := sha256.New()
	n, e := io.Copy(h, &contextReader{ctx: ctx, r: io.LimitReader(f, maximum+1)})
	if e != nil || n != info.Size() {
		return "", 0, failure("REGISTRY_CONTENT_INVALID")
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func admitDisk(c Config) error {
	var fs syscall.Statfs_t
	if syscall.Statfs(c.StoreRoot, &fs) != nil || fs.Blocks == 0 {
		return failure("REGISTRY_STORE_INVALID")
	}
	// Two copies coexist during preparation: tagless archive and exact blob closure.
	available := uint64(fs.Bavail) * uint64(fs.Bsize)
	capacity := uint64(fs.Blocks) * uint64(fs.Bsize)
	needed := uint64(c.MaximumArchiveBytes)*2 + uint64(c.MaximumExpandedBytes)
	if available < needed || available-needed < capacity/100*uint64(c.MinimumHeadroomPercent) {
		return failure("REGISTRY_STORAGE_CAPACITY_REQUIRED")
	}
	return nil
}
func prepareImage(ctx context.Context, c Config, pin string, signed bundle.ImageProof, proof bundle.ImageProofResult) (cachedImage, error) {
	if !validID(pin) {
		return cachedImage{}, failure("VALIDATION_FAILED")
	}
	asset := proof.Asset
	if e := admitDisk(c); e != nil {
		return cachedImage{}, e
	}
	source, e := bundle.OpenImageSource(ctx, c.Verification, filepath.Join(c.PreparedReleaseRoot, asset.Path), asset, c.MaximumExpandedBytes)
	if e != nil {
		return cachedImage{}, failure("REGISTRY_IMAGE_INVALID")
	}
	defer source.Close()
	root, e := privateRoot(c.StoreRoot)
	if e != nil {
		return cachedImage{}, e
	}
	defer root.Close()
	stage := "staging-" + pin
	if e = root.Mkdir(stage, 0700); e != nil {
		return cachedImage{}, failure("REGISTRY_RECOVERY_REQUIRED")
	}
	// Failure preserves the exact owned stage; explicit reconciliation verifies it.
	f, e := root.OpenFile(stage+"/archive.tar", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return cachedImage{}, failure("REGISTRY_STORE_INVALID")
	}
	receipt, e := source.WriteTagless(ctx, f)
	if e == nil {
		e = f.Sync()
	}
	e = joinedClose(f, e)
	if e != nil {
		return cachedImage{}, failure("REGISTRY_IMAGE_INVALID")
	}
	out := cachedImage{Asset: asset, ReleaseID: proof.ReleaseID, ReleaseManifestSHA256: proof.ManifestSHA256, ReleaseArchiveSHA256: c.ReleaseArchiveSHA256, Inventory: source.Inventory(), Archive: receipt, Proof: signed}
	if receipt.TransportBytes > c.MaximumArchiveBytes {
		return cachedImage{}, failure("REGISTRY_CAPACITY_REQUIRED")
	}
	if e = extractImage(ctx, c, root, stage, &out); e != nil {
		return cachedImage{}, e
	}
	raw, e := json.Marshal(out)
	if int64(len(raw)) > c.Verification.MaximumMetadataBytes {
		return cachedImage{}, failure("REGISTRY_CAPACITY_REQUIRED")
	}
	if e != nil {
		return cachedImage{}, e
	}
	if e = writePrivate(root, stage+"/inventory.json", raw); e != nil {
		return cachedImage{}, failure("REGISTRY_STORE_INVALID")
	}
	if e = syncDirectory(root, stage+"/blobs"); e == nil {
		e = syncDirectory(root, stage)
	}
	if e == nil {
		e = root.Rename(stage, pin)
	}
	if e == nil {
		e = syncDirectory(root, ".")
	}
	if e != nil {
		return cachedImage{}, failure("REGISTRY_RECOVERY_REQUIRED")
	}
	return out, nil
}
func extractImage(ctx context.Context, c Config, root *os.Root, stage string, out *cachedImage) error {
	if e := root.Mkdir(stage+"/blobs", 0700); e != nil {
		return failure("REGISTRY_STORE_INVALID")
	}
	f, e := root.Open(stage + "/archive.tar")
	if e != nil {
		return failure("REGISTRY_CONTENT_MISSING")
	}
	defer f.Close()
	tr := tar.NewReader(&contextReader{ctx: ctx, r: f})
	seen := map[string]bool{}
	var manifestRaw []byte
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return failure("REGISTRY_IMAGE_INVALID")
		}
		if seen[h.Name] || h.Typeflag != tar.TypeReg || h.Size < 0 {
			return failure("REGISTRY_IMAGE_INVALID")
		}
		seen[h.Name] = true
		if h.Name == "index.json" || h.Name == "oci-layout" {
			if h.Size > c.Verification.MaximumMetadataBytes {
				return failure("REGISTRY_IMAGE_INVALID")
			}
			if _, e = io.Copy(io.Discard, tr); e != nil {
				return e
			}
			continue
		}
		if !strings.HasPrefix(h.Name, "blobs/sha256/") || !validDigest("sha256:"+strings.TrimPrefix(h.Name, "blobs/sha256/")) || h.Size < 1 || h.Size > c.MaximumBlobBytes {
			return failure("REGISTRY_IMAGE_INVALID")
		}
		digest := "sha256:" + strings.TrimPrefix(h.Name, "blobs/sha256/")
		name := stage + "/blobs/" + digest[7:]
		dest, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return failure("REGISTRY_STORE_INVALID")
		}
		hash := sha256.New()
		n, e := io.Copy(io.MultiWriter(dest, hash), tr)
		if e == nil {
			e = dest.Sync()
		}
		e = joinedClose(dest, e)
		if e != nil || n != h.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != digest {
			return failure("REGISTRY_DIGEST_MISMATCH")
		}
		if digest == out.Inventory.ManifestDigest {
			if n > c.Verification.MaximumMetadataBytes {
				return failure("REGISTRY_IMAGE_INVALID")
			}
			manifestRaw, e = root.ReadFile(name)
			if e != nil {
				return e
			}
		}
	}
	var m imageManifest
	if len(manifestRaw) == 0 || release.UniqueJSON(manifestRaw, &m) != nil || m.SchemaVersion != 2 || m.MediaType != ociManifest && m.MediaType != dockerManifest || m.Config.Digest != out.Inventory.ConfigDigest || len(m.Config.URLs) > 0 || len(m.Layers) != len(out.Inventory.Layers) {
		return failure("REGISTRY_IMAGE_INVALID")
	}
	out.Descriptors = []Descriptor{{out.Inventory.ManifestDigest, int64(len(manifestRaw)), m.MediaType, "manifest"}, {m.Config.Digest, m.Config.Size, m.Config.MediaType, "config"}}
	for i, l := range m.Layers {
		actual := out.Inventory.Layers[i]
		if len(l.URLs) > 0 || l.Digest != actual.Digest || l.Size != actual.Size || l.MediaType != actual.MediaType {
			return failure("REGISTRY_IMAGE_INVALID")
		}
		out.Descriptors = append(out.Descriptors, Descriptor{l.Digest, l.Size, l.MediaType, "layer"})
	}
	expected := map[string]bool{"index.json": true, "oci-layout": true}
	for _, d := range out.Descriptors {
		if !validDigest(d.Digest) || d.Size < 1 || d.Size > c.MaximumBlobBytes {
			return failure("REGISTRY_IMAGE_INVALID")
		}
		expected["blobs/sha256/"+d.Digest[7:]] = true
		sha, n, e := hashFile(ctx, root, stage+"/blobs/"+d.Digest[7:], c.MaximumBlobBytes)
		if e != nil || sha != d.Digest || n != d.Size {
			return failure("REGISTRY_DIGEST_MISMATCH")
		}
	}
	if len(seen) != len(expected) {
		return failure("REGISTRY_IMAGE_INVALID")
	}
	return writePrivate(root, stage+"/manifest.json", manifestRaw)
}
func readCacheNamed(ctx context.Context, c Config, pin string) (cachedImage, error) {
	if !validID(pin) && !(strings.HasPrefix(pin, "staging-") && validID(strings.TrimPrefix(pin, "staging-"))) {
		return cachedImage{}, failure("VALIDATION_FAILED")
	}
	root, e := privateRoot(c.StoreRoot)
	if e != nil {
		return cachedImage{}, e
	}
	defer root.Close()
	info, e := root.Stat(pin + "/inventory.json")
	if e != nil || info.Size() > c.Verification.MaximumMetadataBytes {
		return cachedImage{}, failure("REGISTRY_CONTENT_INVALID")
	}
	raw, e := root.ReadFile(pin + "/inventory.json")
	if e != nil {
		return cachedImage{}, failure("REGISTRY_CONTENT_MISSING")
	}
	var out cachedImage
	if release.StrictJSON(raw, &out) != nil || !release.ValidSHA256(out.ReleaseArchiveSHA256) || !release.ValidSHA256(out.ReleaseManifestSHA256) || out.Inventory.ManifestDigest != out.Asset.ImageID || out.Inventory.ConfigDigest != out.Asset.ImageConfigDigest || len(out.Descriptors) < 2 || out.Descriptors[0].Digest != out.Inventory.ManifestDigest {
		return cachedImage{}, failure("REGISTRY_CONTENT_INVALID")
	}
	sha, n, e := hashFile(ctx, root, pin+"/archive.tar", c.MaximumArchiveBytes)
	if e != nil || sha != "sha256:"+out.Archive.TransportSHA256 || n != out.Archive.TransportBytes {
		return cachedImage{}, failure("REGISTRY_DIGEST_MISMATCH")
	}
	for _, d := range out.Descriptors {
		if !validDigest(d.Digest) {
			return cachedImage{}, failure("REGISTRY_CONTENT_INVALID")
		}
		sha, n, e := hashFile(ctx, root, pin+"/blobs/"+d.Digest[7:], c.MaximumBlobBytes)
		if e != nil || sha != d.Digest || n != d.Size {
			return cachedImage{}, failure("REGISTRY_DIGEST_MISMATCH")
		}
	}
	verified, e := bundle.VerifyImageProof(ctx, c.Verification, out.Proof)
	if e != nil || verified.ManifestSHA256 != out.ReleaseManifestSHA256 || verified.ReleaseID != out.ReleaseID || !reflect.DeepEqual(verified.Asset, out.Asset) || verified.Asset.ImageID != out.Inventory.ManifestDigest || verified.Asset.ImageConfigDigest != out.Inventory.ConfigDigest {
		return cachedImage{}, failure("REGISTRY_RELEASE_INVALID")
	}
	return out, nil
}

func readCached(ctx context.Context, c Config, pin string) (cachedImage, error) {
	if !validID(pin) {
		return cachedImage{}, failure("VALIDATION_FAILED")
	}
	return readCacheNamed(ctx, c, pin)
}
func recoverPrepared(ctx context.Context, c Config, pin string) (cachedImage, error) {
	if !validID(pin) {
		return cachedImage{}, failure("VALIDATION_FAILED")
	}
	name := pin
	image, e := readCached(ctx, c, pin)
	if e != nil {
		name = "staging-" + pin
		image, e = readCacheNamed(ctx, c, name)
		if e != nil {
			return cachedImage{}, failure("REGISTRY_PREPARATION_INCOMPLETE")
		}
	}
	proof, e := bundle.VerifyPrepared(ctx, c.Verification, c.PreparedReleaseRoot, c.ReleaseArchiveSHA256, c.ReleaseManifestSHA256)
	if e != nil {
		return cachedImage{}, failure("REGISTRY_RELEASE_INVALID")
	}
	var actual release.Asset
	for _, asset := range proof.Manifest.Assets {
		if asset.ID == image.Asset.ID {
			actual = asset
		}
	}
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(image.Asset)
	if string(a) != string(b) || actual.Kind != "image_archive" || image.ReleaseID != proof.ReleaseID || image.ReleaseArchiveSHA256 != proof.ArchiveSHA256 || image.ReleaseManifestSHA256 != proof.ManifestSHA256 {
		return cachedImage{}, failure("REGISTRY_RELEASE_INVALID")
	}
	archive := actual
	archive.Size = image.Archive.TransportBytes
	archive.SHA256 = image.Archive.TransportSHA256
	source, e := bundle.OpenImageSource(ctx, c.Verification, filepath.Join(c.StoreRoot, name, "archive.tar"), archive, c.MaximumExpandedBytes)
	if e != nil {
		return cachedImage{}, failure("REGISTRY_IMAGE_INVALID")
	}
	defer source.Close()
	if source.Inventory().ManifestDigest != image.Inventory.ManifestDigest || source.Inventory().ConfigDigest != image.Inventory.ConfigDigest || source.Inventory().ExpandedBytes != image.Inventory.ExpandedBytes {
		return cachedImage{}, failure("REGISTRY_IMAGE_INVALID")
	}
	if name != pin {
		root, e := privateRoot(c.StoreRoot)
		if e != nil {
			return cachedImage{}, e
		}
		defer root.Close()
		if e = root.Rename(name, pin); e != nil {
			return cachedImage{}, failure("REGISTRY_RECOVERY_REQUIRED")
		}
		if e = syncDirectory(root, "."); e != nil {
			return cachedImage{}, failure("REGISTRY_RECOVERY_REQUIRED")
		}
	}
	return image, nil
}
