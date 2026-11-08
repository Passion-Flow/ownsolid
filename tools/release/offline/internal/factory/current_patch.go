package factory

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const patchBaselinePrefix = "baseline-manifest-sha256="

func patchBaseline(m wire.Manifest) (string, error) {
	baseline := ""
	for _, prerequisite := range m.Prerequisites {
		if digest, found := strings.CutPrefix(prerequisite, patchBaselinePrefix); found {
			if baseline != "" || !wire.ValidSHA256(digest) {
				return "", fail("RELEASE_PATCH_BASELINE_INVALID")
			}
			baseline = digest
		}
	}
	if baseline == "" {
		return "", fail("RELEASE_PATCH_BASELINE_REQUIRED")
	}
	return baseline, nil
}

type patchInput struct {
	result Result
	path   string
}

func preparePatchInput(ctx context.Context, c Config, archive, directory string) (patchInput, error) {
	result, err := Unpack(ctx, c, archive, directory, "", "")
	return patchInput{result: result, path: directory}, err
}

func (p patchInput) assetsByPath() map[string]wire.Asset {
	files := make(map[string]wire.Asset, len(p.result.Manifest.Assets))
	for _, asset := range p.result.Manifest.Assets {
		files[asset.Path] = asset
	}
	return files
}

func samePayload(a, b wire.Asset) bool {
	return a.Size == b.Size && a.SHA256 == b.SHA256
}

// BuildCurrentPatch writes an incremental transfer between two complete signed releases.
func BuildCurrentPatch(ctx context.Context, c Config, baseline, target string) (Result, error) {
	c = WithCurrentProfile(c)
	if mounted(c.MountRoot) != nil {
		return Result{}, fail("RELEASE_REAL_MOUNT_REQUIRED")
	}
	work, err := os.MkdirTemp(c.MountRoot, ".ownsolid-patch-build-")
	if err != nil {
		return Result{}, fail("RELEASE_PATCH_STAGING_UNAVAILABLE")
	}
	defer os.RemoveAll(work)
	base, err := preparePatchInput(ctx, c, baseline, filepath.Join(work, "baseline"))
	if err != nil {
		return Result{}, err
	}
	next, err := preparePatchInput(ctx, c, target, filepath.Join(work, "target"))
	if err != nil {
		return Result{}, err
	}
	expected, err := patchBaseline(next.result.Manifest)
	if err != nil {
		return Result{}, err
	}
	if base.result.ManifestSHA256 != expected || next.result.ManifestSHA256 == expected {
		return Result{}, fail("RELEASE_PATCH_BASELINE_MISMATCH")
	}
	raw, err := readRegular(filepath.Join(next.path, "manifest.json"), c.MaximumManifestBytes, true)
	if err != nil {
		return Result{}, err
	}
	signature, err := readRegular(filepath.Join(next.path, "signature.json"), c.MaximumMetadataBytes, true)
	if err != nil {
		return Result{}, err
	}
	paths := base.assetsByPath()
	var payloadBytes int64
	changed := 0
	for _, asset := range next.result.Manifest.Assets {
		if !samePayload(paths[asset.Path], asset) {
			payloadBytes += asset.Size
			changed++
		}
	}
	result := next.result
	if err := writePatchArchive(ctx, c, raw, signature, payloadBytes, changed, func(archive *tar.Writer) error {
		for _, asset := range next.result.Manifest.Assets {
			if !samePayload(paths[asset.Path], asset) {
				if err := addPreparedAsset(ctx, archive, next.path, asset); err != nil {
					return err
				}
			}
		}
		return nil
	}, func(path string) error {
		file, err := openRegular(path, archiveLimit(c), true)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		result.ArchiveSHA256, err = streamHash(ctx, file, nil, info.Size())
		return err
	}); err != nil {
		return Result{}, err
	}
	result.Status = "incremental_patch_written"
	result.Qualification = "target_signature_and_baseline_required_for_reconstruction"
	result.Assets = changed
	return result, nil
}

func addPreparedAsset(ctx context.Context, archive *tar.Writer, directory string, asset wire.Asset) error {
	file, err := openRegular(filepath.Join(directory, filepath.FromSlash(asset.Path)), asset.Size, true)
	if err != nil {
		return err
	}
	defer file.Close()
	return addPatchAsset(ctx, archive, file, asset)
}

func addPatchAsset(ctx context.Context, archive *tar.Writer, source io.Reader, asset wire.Asset) error {
	if err := archive.WriteHeader(releaseArchiveHeader(asset.Path, asset.Size)); err != nil {
		return fail("RELEASE_PATCH_ARCHIVE_WRITE_FAILED")
	}
	digest, err := streamHash(ctx, source, archive, asset.Size)
	if err != nil {
		return err
	}
	if digest != asset.SHA256 {
		return fail("RELEASE_PATCH_ASSET_HASH_INVALID")
	}
	return nil
}

// RebuildCurrentPatch reconstitutes and verifies the complete signed target.
// expectedBaseline is the digest of the installation's accepted release.
func RebuildCurrentPatch(ctx context.Context, c Config, baseline, patch, expectedBaseline string) (Result, error) {
	c = WithCurrentProfile(c)
	if mounted(c.MountRoot) != nil || !within(c.MountRoot, patch) || !wire.ValidSHA256(expectedBaseline) {
		return Result{}, fail("RELEASE_PATCH_INPUT_INVALID")
	}
	trust, err := loadTrust(c)
	if err != nil {
		return Result{}, err
	}
	file, err := openRegular(patch, archiveLimit(c), true)
	if err != nil {
		return Result{}, err
	}
	defer file.Close()
	input, err := newStreamReader(ctx, file)
	if err != nil {
		return Result{}, err
	}
	buffer := bufio.NewReader(input)
	compressed, err := gzip.NewReader(buffer)
	if err != nil {
		return Result{}, fail("RELEASE_PATCH_ARCHIVE_INVALID")
	}
	defer compressed.Close()
	compressed.Multistream(false)
	archive := tar.NewReader(checkedReader{ctx, compressed})
	readMetadata := func(name string, maximum int64) ([]byte, error) {
		header, err := archive.Next()
		if err != nil || header.Name != name || !safeHeader(header) || header.Size < 1 || header.Size > maximum {
			return nil, fail("RELEASE_PATCH_METADATA_INVALID")
		}
		return io.ReadAll(archive)
	}
	raw, err := readMetadata("manifest.json", c.MaximumManifestBytes)
	if err != nil {
		return Result{}, err
	}
	signature, err := readMetadata("signature.json", c.MaximumMetadataBytes)
	if err != nil {
		return Result{}, err
	}
	if err := verifySignature(raw, signature, trust); err != nil {
		return Result{}, err
	}
	var target wire.Manifest
	if wire.StrictJSON(raw, &target) != nil || c.validateManifest(target) != nil || len(target.Assets)+2 > c.MaximumArchiveEntries {
		return Result{}, fail("RELEASE_PATCH_TARGET_MANIFEST_INVALID")
	}
	expected, err := patchBaseline(target)
	if err != nil {
		return Result{}, err
	}
	if expected != expectedBaseline || wire.Digest(raw) == expected {
		return Result{}, fail("RELEASE_PATCH_BASELINE_MISMATCH")
	}
	work, err := os.MkdirTemp(c.MountRoot, ".ownsolid-patch-apply-")
	if err != nil {
		return Result{}, fail("RELEASE_PATCH_STAGING_UNAVAILABLE")
	}
	defer os.RemoveAll(work)
	base, err := preparePatchInput(ctx, c, baseline, filepath.Join(work, "baseline"))
	if err != nil {
		return Result{}, err
	}
	if base.result.ManifestSHA256 != expected {
		return Result{}, fail("RELEASE_PATCH_BASELINE_MISMATCH")
	}
	paths := base.assetsByPath()
	var payloadBytes int64
	for _, asset := range target.Assets {
		payloadBytes += asset.Size
	}
	var result Result
	err = writePatchArchive(ctx, c, raw, signature, payloadBytes, len(target.Assets), func(full *tar.Writer) error {
		for _, asset := range target.Assets {
			if samePayload(paths[asset.Path], asset) {
				if err := addPreparedAsset(ctx, full, base.path, asset); err != nil {
					return err
				}
				continue
			}
			header, err := archive.Next()
			if err != nil || !safeAssetHeader(header, asset) {
				return fail("RELEASE_PATCH_ASSET_INVALID")
			}
			if err := addPatchAsset(ctx, full, archive, asset); err != nil {
				return err
			}
		}
		if _, err := archive.Next(); err != io.EOF {
			return fail("RELEASE_PATCH_EXTRA_OR_TRUNCATED_ENTRY")
		}
		tail, err := io.ReadAll(io.LimitReader(compressed, 10241))
		if err != nil || len(tail) > 10240 || len(bytes.Trim(tail, "\x00")) != 0 {
			return fail("RELEASE_PATCH_TRAILING_DATA")
		}
		if _, err := buffer.Peek(1); err != io.EOF {
			return fail("RELEASE_PATCH_MULTIPLE_GZIP_OR_TRAILING_DATA")
		}
		return nil
	}, func(path string) error {
		var err error
		result, err = Inspect(ctx, c, path)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func writePatchArchive(ctx context.Context, c Config, raw, signature []byte, payloadBytes int64, assetCount int, payload func(*tar.Writer) error, verify func(string) error) error {
	parent, err := outputParent(c)
	if err != nil {
		return err
	}
	defer parent.Close()
	if err := diskAdmission(c, payloadBytes+int64(len(raw)+len(signature))+int64(assetCount)*2048+1048576); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(c.OutputFile), ".ownsolid-patch-")
	if err != nil {
		return fail("RELEASE_PATCH_ARCHIVE_WRITE_FAILED")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	output, err := newStreamWriter(ctx, file)
	if err != nil {
		return err
	}
	compressed, err := gzip.NewWriterLevel(output, c.CompressionLevel)
	if err != nil {
		return err
	}
	archive := tar.NewWriter(compressed)
	for _, item := range []struct {
		name string
		data []byte
	}{{"manifest.json", raw}, {"signature.json", signature}} {
		if err := archive.WriteHeader(releaseArchiveHeader(item.name, int64(len(item.data)))); err != nil {
			return fail("RELEASE_PATCH_ARCHIVE_WRITE_FAILED")
		}
		if _, err := archive.Write(item.data); err != nil {
			return fail("RELEASE_PATCH_ARCHIVE_WRITE_FAILED")
		}
	}
	if err := payload(archive); err != nil {
		return err
	}
	if err := errors.Join(archive.Close(), compressed.Close(), output.finish(), file.Sync(), file.Close()); err != nil {
		return fail("RELEASE_PATCH_ARCHIVE_WRITE_FAILED")
	}
	if verify != nil {
		if err := verify(file.Name()); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return fail("RELEASE_DEADLINE_OR_INTERRUPTION")
	}
	if err := os.Link(file.Name(), c.OutputFile); err != nil {
		return fail("RELEASE_EXCLUSIVE_PUBLICATION_FAILED")
	}
	if err := os.Remove(file.Name()); err != nil || parent.Sync() != nil {
		return fail("RELEASE_PUBLICATION_DIRECTORY_SYNC_UNCERTAIN")
	}
	return nil
}
