package scope

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const mvpNativeArchiveFile = "native-postgresql.tar"
const mvpNativeArchiveDescriptorFile = "native-archive.json"

func mvpNativeArchiveRelative(name, root string) (string, bool) {
	name = strings.TrimSuffix(name, "/")
	if name == root {
		return ".", true
	}
	if !strings.HasPrefix(name, root+"/") {
		return "", false
	}
	relative := strings.TrimPrefix(name, root+"/")
	if relative == "" || path.Clean(relative) != relative || path.IsAbs(relative) || !utf8.ValidString(relative) || strings.ContainsFunc(relative, unicode.IsControl) {
		return "", false
	}
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	return relative, true
}

// The Engine's original descriptor validates all raw tar bytes first. Extraction
// preserves native names/modes and original file contents; host UID custody is
// the current private Scope owner, while original numeric IDs remain in the
// retained unchanged tar. A failure retains its exact partial child and tar.
func extractMVPNativeArchive(ctx context.Context, root *os.Root, input *os.File, a engine.NativePostgreSQLArchiveArtifact, limits configuration.BackupRequest, maximumMetadata int64) (engine.NativePostgreSQLArchiveProfile, error) {
	if ctx == nil || root == nil || input == nil || limits.Validate() != nil || filepath.Clean(root.Name()) != limits.DestinationRoot {
		return engine.NativePostgreSQLArchiveProfile{}, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_CONTEXT_INVALID")
	}
	profile, e := engine.DescribeNativePostgreSQLArchive(ctx, input, a)
	if e != nil {
		return profile, e
	}
	before, e := input.Stat()
	if e != nil || !privateRegular(before, a.Bytes) {
		return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_PRIVATE_INPUT_REQUIRED")
	}
	namedBefore, ne := root.Lstat(mvpNativeArchiveFile)
	if ne != nil || !samePrivateSnapshot(before, namedBefore, a.Bytes) {
		return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_PRIVATE_INPUT_REQUIRED")
	}
	if _, e = input.Seek(0, io.SeekStart); e != nil {
		return profile, e
	}
	if e = root.Mkdir(mvpNativeDirectory, 0700); e != nil {
		return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_CHILD_OCCUPIED")
	}
	if e = syncBackupDirectory(root, "."); e != nil {
		return profile, e
	}
	native, e := root.OpenRoot(mvpNativeDirectory + "/.")
	if e != nil {
		return profile, e
	}
	defer native.Close()
	hash := sha256.New()
	source := io.TeeReader(mvpNativeContextReader{ctx, io.LimitReader(input, a.Bytes+1)}, hash)
	reader := tar.NewReader(source)
	directories := []struct {
		name string
		mode os.FileMode
	}{}
	seen := map[string]bool{}
	var copied int64
	metadata := maximumMetadata
	for {
		if ctx.Err() != nil {
			return profile, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_EXTRACTION_FAILED")
		}
		relative, ok := mvpNativeArchiveRelative(header.Name, a.RootName)
		if !ok || seen[relative] || header.Uid != a.UID || header.Gid != a.GID || header.Mode < 0 || header.Mode > 0777 || header.Mode&0022 != 0 || len(seen) >= a.MaximumEntries {
			return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_CHANGED")
		}
		seen[relative] = true
		metadata -= int64(len(relative) + 256)
		if metadata < 0 {
			return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_ENTRY_LIMIT")
		}
		if relative != "." && !seen[path.Dir(relative)] {
			return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_PARENT_REQUIRED")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 || header.Mode != 0700 && header.Mode != 0555 || relative == "." && header.Mode != 0700 {
				return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_CHANGED")
			}
			if relative != "." {
				if e = native.Mkdir(relative, 0700); e != nil {
					return profile, e
				}
			}
			directories = append(directories, struct {
				name string
				mode os.FileMode
			}{relative, os.FileMode(header.Mode)})
		case tar.TypeReg:
			if relative == "." || header.Size < 0 || header.Size > a.Bytes-copied {
				return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_CHANGED")
			}
			f, e := native.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
			if e != nil {
				return profile, e
			}
			n, copyError := io.CopyBuffer(f, reader, make([]byte, 64<<10))
			if copyError == nil && n == header.Size {
				copyError = f.Chmod(os.FileMode(header.Mode))
			}
			if copyError == nil {
				copyError = f.Sync()
			}
			closeError := f.Close()
			if copyError != nil || closeError != nil || n != header.Size {
				return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_PARTIAL_RETAINED")
			}
			copied += n
		default:
			return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_CHANGED")
		}
	}
	// Consume and hash all original padding too; the descriptor is the exact
	// original byte stream, independently of a daemon's later tar framing.
	if _, e = io.Copy(io.Discard, source); e != nil {
		return profile, e
	}
	after, e := input.Stat()
	namedAfter, ne := root.Lstat(mvpNativeArchiveFile)
	if e != nil || ne != nil || !samePrivateSnapshot(before, after, a.Bytes) || !samePrivateSnapshot(before, namedAfter, a.Bytes) || hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return profile, failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_CHANGED")
	}
	for i := len(directories) - 1; i >= 0; i-- {
		d := directories[i]
		if e = native.Chmod(d.name, d.mode); e != nil {
			return profile, e
		}
		if e = syncBackupDirectory(native, d.name); e != nil {
			return profile, e
		}
	}
	if e = syncBackupDirectory(root, "."); e != nil {
		return profile, e
	}
	entries, _, e := inventoryMVPNativeSource(ctx, root, limits.MaximumBytes, limits.MaximumEntries, maximumMetadata)
	if e != nil {
		return profile, e
	}
	if e = verifyMVPNativeCopiedArchive(entries, profile); e != nil {
		return profile, e
	}
	return profile, nil
}

func verifyMVPNativeCopiedArchive(entries []mvpNativeInventoryEntry, p engine.NativePostgreSQLArchiveProfile) error {
	native := []engine.NativePostgreSQLArchiveEntry{}
	for _, e := range entries {
		if e.Name != mvpNativeDirectory && !strings.HasPrefix(e.Name, mvpNativeDirectory+"/") {
			continue
		}
		relative := strings.TrimPrefix(e.Name, mvpNativeDirectory+"/")
		if e.Name == mvpNativeDirectory {
			relative = "."
		}
		native = append(native, engine.NativePostgreSQLArchiveEntry{Name: relative, Type: e.Kind, Mode: int64(e.Mode), Bytes: e.Bytes, SHA256: e.SHA256})
	}
	sort.Slice(native, func(i, j int) bool { return native[i].Name < native[j].Name })
	raw, e := json.Marshal(native)
	if e != nil || len(native) != p.Entries || wire.Digest(raw) != p.EntriesSHA256 {
		return failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_COPY_MISMATCH")
	}
	return nil
}

type mvpNativeContextReader struct {
	ctx   context.Context
	input io.Reader
}

func (r mvpNativeContextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.input.Read(p)
}

func readMVPNativeArchive(ctx context.Context, root *os.Root, c configuration.Config, p mvpNativeBackupPlan, entries []mvpNativeInventoryEntry) (engine.NativePostgreSQLArchiveProfile, error) {
	bad := failure("OFFLINE_SCOPE_NATIVE_ARCHIVE_SOURCE_INVALID")
	if e := verifyMVPNativeSourceDescriptor(root, c, p); e != nil {
		return engine.NativePostgreSQLArchiveProfile{}, e
	}
	raw, e := regular(root, mvpNativeArchiveDescriptorFile, c.Verification.MaximumMetadataBytes)
	var profile engine.NativePostgreSQLArchiveProfile
	if e != nil || wire.StrictJSON(raw, &profile) != nil || profile.Artifact.RootName != path.Base(p.NativeRequest.BackupRoot) || profile.Artifact.UID != p.NativeUID || profile.Artifact.GID != p.NativeGID || profile.Artifact.MaximumManifestBytes != p.NativeRequest.Policy.MaximumManifestBytes || profile.Artifact.MaximumEntries > p.Request.MaximumEntries || profile.Artifact.Bytes > p.Request.MaximumBytes {
		return profile, bad
	}
	before, e := root.Lstat(mvpNativeArchiveFile)
	if e != nil || !privateRegular(before, profile.Artifact.Bytes) {
		return profile, bad
	}
	input, e := root.OpenFile(mvpNativeArchiveFile, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return profile, bad
	}
	defer input.Close()
	opened, e := input.Stat()
	if e != nil || !samePrivateSnapshot(before, opened, profile.Artifact.Bytes) {
		return profile, bad
	}
	actual, e := engine.DescribeNativePostgreSQLArchive(ctx, input, profile.Artifact)
	if e != nil || actual != profile {
		return profile, bad
	}
	after, e := input.Stat()
	named, ne := root.Lstat(mvpNativeArchiveFile)
	if e != nil || ne != nil || !samePrivateSnapshot(before, after, profile.Artifact.Bytes) || !samePrivateSnapshot(before, named, profile.Artifact.Bytes) {
		return profile, bad
	}
	if e = verifyMVPNativeCopiedArchive(entries, profile); e != nil {
		return profile, e
	}
	return profile, nil
}
