package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Native archive bounds cover the original aggregate SQL+store+configuration
// trees. Actual selected limits still come from the frozen request/descriptor.
const maximumNativePostgreSQLArchiveBytes int64 = 3 << 40
const maximumNativePostgreSQLArchiveEntries = 2*maximumStoreArchiveEntries + 1024

type NativePostgreSQLArchiveArtifact struct {
	RootName             string `json:"root_name"`
	UID                  int    `json:"uid"`
	GID                  int    `json:"gid"`
	Bytes                int64  `json:"bytes"`
	SHA256               string `json:"sha256"`
	MaximumEntries       int    `json:"maximum_entries"`
	MaximumManifestBytes int64  `json:"maximum_manifest_bytes"`
}

type NativePostgreSQLArchiveProfile struct {
	Artifact      NativePostgreSQLArchiveArtifact `json:"artifact"`
	EntriesSHA256 string                          `json:"entries_sha256"`
	Entries       int                             `json:"entries"`
}

type NativePostgreSQLArchiveEntry struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Mode   int64  `json:"mode"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256,omitempty"`
}

type NativePostgreSQLArchive struct {
	Profile       NativePostgreSQLArchiveProfile `json:"profile"`
	Entries       []NativePostgreSQLArchiveEntry `json:"entries"`
	Qualification string                         `json:"qualification"`
}

func nativeArchivePath(s string) bool {
	if s == "" || !utf8.ValidString(s) || path.IsAbs(s) || path.Clean(s) != s || strings.ContainsFunc(s, unicode.IsControl) {
		return false
	}
	for _, p := range strings.Split(s, "/") {
		if p == "" || p == "." || p == ".." {
			return false
		}
	}
	return true
}
func (a NativePostgreSQLArchiveArtifact) valid() bool {
	return nativeArchivePath(a.RootName) && !strings.Contains(a.RootName, "/") && a.UID > 0 && a.GID > 0 && a.UID <= math.MaxInt32 && a.GID <= math.MaxInt32 && a.Bytes >= tarEndBytes && a.Bytes <= maximumNativePostgreSQLArchiveBytes && wire.ValidSHA256(a.SHA256) && a.MaximumEntries >= 6 && a.MaximumEntries <= maximumNativePostgreSQLArchiveEntries && a.MaximumManifestBytes >= 4096 && a.MaximumManifestBytes <= 16<<20
}
func (p NativePostgreSQLArchiveProfile) valid() bool {
	return p.Artifact.valid() && wire.ValidSHA256(p.EntriesSHA256) && p.Entries >= 6 && p.Entries <= p.Artifact.MaximumEntries
}

func readNativePostgreSQLArchive(source io.Reader, sink io.Writer, a NativePostgreSQLArchiveArtifact, maximumBytes int64) (NativePostgreSQLArchive, error) {
	out := NativePostgreSQLArchive{Entries: []NativePostgreSQLArchiveEntry{}}
	bad := failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_INVALID")
	if source == nil || sink == nil || !nativeArchivePath(a.RootName) || strings.Contains(a.RootName, "/") || a.UID < 1 || a.GID < 1 || a.UID > math.MaxInt32 || a.GID > math.MaxInt32 || a.MaximumEntries < 6 || a.MaximumEntries > maximumNativePostgreSQLArchiveEntries || a.MaximumManifestBytes < 4096 || a.MaximumManifestBytes > 16<<20 || maximumBytes < tarEndBytes || maximumBytes > maximumNativePostgreSQLArchiveBytes {
		return out, bad
	}
	hash := sha256.New()
	counter := &backupArchiveCounter{reader: io.TeeReader(io.LimitReader(source, maximumBytes+1), io.MultiWriter(sink, hash))}
	reader := tar.NewReader(counter)
	seen := map[string]byte{}
	remaining := a.MaximumManifestBytes
	for {
		before := counter.bytes
		h, e := reader.Next()
		if e == io.EOF {
			if counter.bytes-before < tarEndBytes {
				return out, bad
			}
			break
		}
		if e != nil {
			return out, bad
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !nativeArchivePath(name) || name != a.RootName && !strings.HasPrefix(name, a.RootName+"/") || h.Linkname != "" || h.Uid != a.UID || h.Gid != a.GID || len(h.Xattrs) > 0 || len(seen) >= a.MaximumEntries {
			return out, bad
		}
		if _, ok := seen[name]; ok {
			return out, bad
		}
		for key, value := range h.PAXRecords {
			if key == "path" && value == h.Name {
				continue
			}
			if key == "size" && value == strconv.FormatInt(h.Size, 10) {
				continue
			}
			return out, bad
		}
		if name != a.RootName && seen[path.Dir(name)] != tar.TypeDir {
			return out, bad
		}
		seen[name] = h.Typeflag
		relative := strings.TrimPrefix(name, a.RootName+"/")
		if name == a.RootName {
			relative = "."
		}
		entry := NativePostgreSQLArchiveEntry{Name: relative, Mode: h.Mode, Bytes: h.Size}
		switch h.Typeflag {
		case tar.TypeDir:
			if h.Size != 0 || h.Mode != 0700 && h.Mode != 0555 || name == a.RootName && h.Mode != 0700 {
				return out, bad
			}
			entry.Type = "directory"
		case tar.TypeReg:
			if strings.HasSuffix(h.Name, "/") || name == a.RootName || h.Mode < 0 || h.Mode > 0777 || h.Mode&0022 != 0 || h.Size < 0 || h.Size > maximumBytes || counter.bytes > maximumBytes-h.Size {
				return out, bad
			}
			entry.Type = "file"
			sum := sha256.New()
			if n, e := io.CopyBuffer(sum, reader, make([]byte, 64<<10)); e != nil || n != h.Size {
				return out, bad
			}
			entry.SHA256 = hex.EncodeToString(sum.Sum(nil))
		default:
			return out, bad
		}
		encoded, e := json.Marshal(entry)
		if e != nil || int64(len(encoded)+1) > remaining {
			return out, bad
		}
		remaining -= int64(len(encoded) + 1)
		out.Entries = append(out.Entries, entry)
	}
	for _, name := range []string{"candidate.json", "complete.json", "independent-readback.json", "postgres.dump", "effective-control.yaml"} {
		if seen[a.RootName+"/"+name] != tar.TypeReg {
			return out, bad
		}
	}
	if seen[a.RootName] != tar.TypeDir {
		return out, bad
	}
	tail, e := io.ReadAll(io.LimitReader(counter, archiveReadbackReserveBytes+1))
	if e != nil || len(tail) > archiveReadbackReserveBytes || len(bytes.Trim(tail, "\x00")) != 0 || counter.bytes > maximumBytes {
		return out, bad
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Name < out.Entries[j].Name })
	inventory, e := json.Marshal(out.Entries)
	if e != nil || int64(len(inventory)) > a.MaximumManifestBytes {
		return out, bad
	}
	a.Bytes = counter.bytes
	a.SHA256 = hex.EncodeToString(hash.Sum(nil))
	out.Profile = NativePostgreSQLArchiveProfile{Artifact: a, EntriesSHA256: wire.Digest(inventory), Entries: len(out.Entries)}
	out.Qualification = "safe_original_native_tree_transport_only_not_signature_SQL_or_native_semantic_verification"
	return out, nil
}

// DescribeNativePostgreSQLArchive verifies a protected immutable original host
// tar and its complete mode/name/content inventory, without extracting it.
func DescribeNativePostgreSQLArchive(ctx context.Context, input *os.File, a NativePostgreSQLArchiveArtifact) (NativePostgreSQLArchiveProfile, error) {
	if ctx == nil || !a.valid() {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_DESCRIPTOR_INVALID")
	}
	before, e := restorePrivateFile(input, a.Bytes)
	if e != nil {
		return NativePostgreSQLArchiveProfile{}, e
	}
	if _, e = input.Seek(0, io.SeekStart); e != nil {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_SEEK_FAILED")
	}
	out, e := readNativePostgreSQLArchive(restoreContextReader{ctx, input}, io.Discard, a, a.Bytes)
	if e != nil || out.Profile.Artifact != a {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_DESCRIPTOR_CHANGED")
	}
	after, e := restorePrivateFile(input, a.Bytes)
	if e != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
		return NativePostgreSQLArchiveProfile{}, failure("OFFLINE_ENGINE_NATIVE_ARCHIVE_CHANGED")
	}
	return out.Profile, nil
}
