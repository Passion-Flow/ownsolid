package factory

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const PreparedMarker = ".projectone-unpacked-release.json"

func InspectExpected(ctx context.Context, c Config, source, archiveSHA, manifestSHA string) (Result, error) {
	if !wire.ValidSHA256(archiveSHA) || !wire.ValidSHA256(manifestSHA) {
		return Result{}, fail("RELEASE_INDEPENDENT_EXPECTED_DIGESTS_REQUIRED")
	}
	r, e := Inspect(ctx, c, source)
	if e != nil {
		return r, e
	}
	if r.ArchiveSHA256 != archiveSHA || r.ManifestSHA256 != manifestSHA {
		return Result{}, fail("RELEASE_EXPECTED_DIGEST_MISMATCH")
	}
	if r.Status != "passed_signed_archive_full_readback" {
		return Result{}, fail("RELEASE_UNCERTAIN_PUBLICATION_NOT_PREPARABLE")
	}
	return r, nil
}
func Unpack(ctx context.Context, c Config, source, destination, archiveSHA, manifestSHA string) (Result, error) {
	return unpack(ctx, c, source, destination, archiveSHA, manifestSHA, unpackIO{syncDirectory: func(f *os.File) error { return f.Sync() }, afterInspection: func() {}})
}

func inspectForPreparation(ctx context.Context, c Config, source, archiveSHA, manifestSHA string) (Result, error) {
	if !c.current || archiveSHA != "" || manifestSHA != "" {
		return InspectExpected(ctx, c, source, archiveSHA, manifestSHA)
	}
	result, err := Inspect(ctx, c, source)
	if err == nil && result.Status != "passed_signed_archive_full_readback" {
		return Result{}, fail("RELEASE_UNCERTAIN_PUBLICATION_NOT_PREPARABLE")
	}
	return result, err
}

type unpackIO struct {
	syncDirectory   func(*os.File) error
	afterInspection func()
}

func unpack(ctx context.Context, c Config, source, destination, archiveSHA, manifestSHA string, operations unpackIO) (result Result, err error) {
	verified, e := inspectForPreparation(ctx, c, source, archiveSHA, manifestSHA)
	archiveSHA, manifestSHA = verified.ArchiveSHA256, verified.ManifestSHA256
	if e != nil {
		return result, e
	}
	operations.afterInspection()
	parentName := filepath.Dir(destination)
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || !within(c.MountRoot, destination) || privateDirectory(parentName) != nil {
		return result, fail("RELEASE_NEW_PRIVATE_PREPARATION_REQUIRED")
	}
	for _, name := range []string{destination, destination + ".journal.jsonl"} {
		if _, e := os.Lstat(name); !os.IsNotExist(e) {
			return result, fail("RELEASE_PREPARATION_OCCUPIED_OR_UNCERTAIN")
		}
	}
	capacity := c
	capacity.OutputFile = destination
	total := int64(0)
	for _, a := range verified.Manifest.Assets {
		total += a.Size
	}
	if e = diskAdmission(capacity, total+c.MaximumManifestBytes+c.MaximumMetadataBytes+1048576); e != nil {
		return result, e
	}
	parent, e := os.Open(parentName)
	if e != nil {
		return result, fail("RELEASE_PREPARATION_PARENT_UNAVAILABLE")
	}
	defer parent.Close()
	journal, e := os.OpenFile(destination+".journal.jsonl", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return result, fail("RELEASE_PREPARATION_JOURNAL_OCCUPIED")
	}
	defer journal.Close()
	event := func(status, code string) error {
		if _, e := journal.Write(Encode(map[string]any{"status": status, "code": code, "archive_sha256": archiveSHA, "manifest_sha256": manifestSHA, "time": time.Now().UTC().Format(time.RFC3339Nano)})); e != nil || journal.Sync() != nil {
			return fail("RELEASE_PREPARATION_JOURNAL_FAILED")
		}
		return nil
	}
	defer func() {
		if err != nil {
			_ = event("failed_preserve_partial_or_uncertain_preparation", ErrorCode(err))
		}
	}()
	if e = event("reserved", ""); e != nil {
		return result, e
	}
	if operations.syncDirectory(parent) != nil {
		return result, fail("RELEASE_PREPARATION_DIRECTORY_SYNC_FAILED")
	}
	if e = os.Mkdir(destination, 0700); e != nil {
		return result, fail("RELEASE_PREPARATION_DESTINATION_OCCUPIED")
	}
	root, e := os.OpenRoot(destination)
	if e != nil {
		return result, fail("RELEASE_PREPARATION_ROOT_UNAVAILABLE")
	}
	defer root.Close()
	f, e := openRegular(source, archiveLimit(c), true)
	if e != nil {
		return result, e
	}
	defer f.Close()
	before, _ := f.Stat()
	input, e := newStreamReader(ctx, f)
	if e != nil {
		return result, e
	}
	sum := sha256.New()
	buffer := bufio.NewReader(io.TeeReader(input, sum))
	compressed, e := gzip.NewReader(buffer)
	if e != nil {
		return result, fail("RELEASE_PREPARATION_GZIP_CHANGED")
	}
	defer compressed.Close()
	compressed.Multistream(false)
	archive := tar.NewReader(compressed)
	directories := map[string]bool{".": true}
	writeFile := func(name string, size int64, expected string) error {
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			directories[dir] = true
		}
		if dir := path.Dir(name); dir != "." {
			if e = root.MkdirAll(filepath.FromSlash(dir), 0700); e != nil {
				return fail("RELEASE_PREPARATION_DIRECTORY_FAILED")
			}
		}
		output, e := root.OpenFile(filepath.FromSlash(name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return fail("RELEASE_PREPARATION_FILE_OCCUPIED_OR_UNWRITABLE")
		}
		digest, readError := streamHash(ctx, archive, output, size)
		syncError := output.Sync()
		closeError := output.Close()
		if readError != nil {
			return readError
		}
		if syncError != nil || closeError != nil {
			return fail("RELEASE_PREPARATION_FILE_SYNC_FAILED")
		}
		if digest != expected {
			return fail("RELEASE_PREPARATION_PAYLOAD_CHANGED")
		}
		return nil
	}
	for _, name := range []string{"manifest.json", "signature.json"} {
		h, e := archive.Next()
		limit := c.MaximumManifestBytes
		if name == "signature.json" {
			limit = c.MaximumMetadataBytes
		}
		if e != nil || !safeHeader(h) || h.Name != name || h.Size < 1 || h.Size > limit {
			return result, fail("RELEASE_PREPARATION_METADATA_CHANGED")
		}
		raw, e := io.ReadAll(archive)
		if e != nil {
			return result, fail("RELEASE_PREPARATION_METADATA_READ_FAILED")
		}
		if name == "manifest.json" && wire.Digest(raw) != manifestSHA {
			return result, fail("RELEASE_PREPARATION_MANIFEST_CHANGED")
		}
		output, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return result, fail("RELEASE_PREPARATION_METADATA_WRITE_FAILED")
		}
		n, we := output.Write(raw)
		se := output.Sync()
		ce := output.Close()
		if we != nil || se != nil || ce != nil || n != len(raw) {
			return result, fail("RELEASE_PREPARATION_METADATA_WRITE_FAILED")
		}
	}
	for _, a := range verified.Manifest.Assets {
		h, e := archive.Next()
		if e != nil || !safeAssetHeader(h, a) {
			return result, fail("RELEASE_PREPARATION_ENTRY_CHANGED")
		}
		if e = writeFile(a.Path, a.Size, a.SHA256); e != nil {
			return result, e
		}
	}
	if _, e = archive.Next(); e != io.EOF {
		return result, fail("RELEASE_PREPARATION_EXTRA_OR_TRUNCATED_ENTRY")
	}
	tail, e := io.ReadAll(io.LimitReader(compressed, 10241))
	if e != nil || len(tail) > 10240 || len(bytes.Trim(tail, "\x00")) != 0 {
		return result, fail("RELEASE_PREPARATION_TRAILING_DATA")
	}
	if _, e = buffer.Peek(1); e != io.EOF {
		return result, fail("RELEASE_PREPARATION_TRAILING_GZIP_DATA")
	}
	after, se := f.Stat()
	pathAfter, pe := os.Lstat(source)
	if se != nil || pe != nil || !sameVersion(before, after) || !sameVersion(before, pathAfter) || hex.EncodeToString(sum.Sum(nil)) != archiveSHA {
		return result, fail("RELEASE_PREPARATION_SOURCE_CHANGED")
	}
	ordered := []string{}
	for dir := range directories {
		ordered = append(ordered, dir)
	}
	sort.Slice(ordered, func(i, j int) bool { return strings.Count(ordered[i], "/") > strings.Count(ordered[j], "/") })
	for _, dir := range ordered {
		folder, e := root.Open(filepath.FromSlash(dir))
		if e != nil {
			return result, fail("RELEASE_PREPARATION_DIRECTORY_FAILED")
		}
		syncError := operations.syncDirectory(folder)
		folder.Close()
		if syncError != nil {
			return result, fail("RELEASE_PREPARATION_DIRECTORY_SYNC_FAILED")
		}
	}
	if ctx.Err() != nil {
		return result, fail("RELEASE_PREPARATION_DEADLINE_OR_INTERRUPTION")
	}
	marker, e := root.OpenFile(PreparedMarker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return result, fail("RELEASE_PREPARATION_MARKER_OCCUPIED")
	}
	raw := Encode(map[string]any{"status": "prepared_verified_inventory_only", "archive_sha256": archiveSHA, "manifest_sha256": manifestSHA, "profile": verified.Profile, "release_id": verified.ReleaseID})
	n, writeError := marker.Write(raw)
	syncError := marker.Sync()
	closeError := marker.Close()
	if writeError != nil || syncError != nil || closeError != nil || n != len(raw) {
		return result, fail("RELEASE_PREPARATION_MARKER_WRITE_FAILED")
	}
	folder, e := root.Open(".")
	if e != nil {
		return result, fail("RELEASE_PREPARATION_DIRECTORY_FAILED")
	}
	syncError = operations.syncDirectory(folder)
	folder.Close()
	if syncError != nil || operations.syncDirectory(parent) != nil {
		return result, fail("RELEASE_PREPARATION_COMMIT_UNCERTAIN")
	}
	if ctx.Err() != nil {
		return result, fail("RELEASE_PREPARATION_COMMIT_UNCERTAIN")
	}
	if e = event("prepared_verified_inventory_only", ""); e != nil {
		return result, e
	}
	verified.Status = "prepared_verified_inventory_only_not_installed"
	return verified, nil
}
