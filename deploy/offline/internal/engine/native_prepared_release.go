package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const RegistryPreparedReleaseRole = "registry-release"
const preparedReleaseMarker = ".projectone-unpacked-release.json"

type PreparedReleaseRequest struct {
	Root           string              `json:"root"`
	Verification   bundle.Verification `json:"verification"`
	ArchiveSHA256  string              `json:"archive_sha256"`
	ManifestSHA256 string              `json:"manifest_sha256"`
}
type PreparedReleaseEntry struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256,omitempty"`
}
type PreparedReleaseProfile struct {
	RequestSHA256  string                 `json:"request_sha256"`
	ArchiveSHA256  string                 `json:"archive_sha256"`
	ManifestSHA256 string                 `json:"manifest_sha256"`
	EntriesSHA256  string                 `json:"entries_sha256"`
	Files          int                    `json:"files"`
	Bytes          int64                  `json:"bytes"`
	Entries        []PreparedReleaseEntry `json:"entries"`
}
type preparedReleaseSnapshot struct {
	paths map[string]os.FileInfo
}

func preparedReleaseRequestSHA(r PreparedReleaseRequest, trustSHA string) string {
	raw, _ := json.Marshal(struct {
		Request     PreparedReleaseRequest `json:"request"`
		TrustSHA256 string                 `json:"trust_sha256"`
	}{r, trustSHA})
	return wire.Digest(append(raw, '\n'))
}
func preparedReleaseSame(a, b os.FileInfo) bool {
	if a == nil || b == nil || !os.SameFile(a, b) || a.Mode() != b.Mode() || a.Size() != b.Size() || !a.ModTime().Equal(b.ModTime()) {
		return false
	}
	x, ok := a.Sys().(*syscall.Stat_t)
	y, yok := b.Sys().(*syscall.Stat_t)
	if !ok || !yok || x.Uid != y.Uid || x.Gid != y.Gid || x.Nlink != y.Nlink {
		return false
	}
	for _, name := range []string{"Ctim", "Ctimespec"} {
		av := reflect.ValueOf(x).Elem().FieldByName(name)
		bv := reflect.ValueOf(y).Elem().FieldByName(name)
		if av.IsValid() && bv.IsValid() && av.CanInterface() && bv.CanInterface() {
			return reflect.DeepEqual(av.Interface(), bv.Interface())
		}
	}
	return false
}
func (s preparedReleaseSnapshot) unchanged() bool {
	for name, before := range s.paths {
		after, e := os.Lstat(name)
		resolved, re := filepath.EvalSymlinks(name)
		if e != nil || re != nil || resolved != name || !preparedReleaseSame(before, after) {
			return false
		}
	}
	return true
}
func preparedReleaseSourceInfo(name string, directory bool) (os.FileInfo, error) {
	bad := failure("OFFLINE_ENGINE_PREPARED_SOURCE_IDENTITY_INVALID")
	resolved, e := filepath.EvalSymlinks(name)
	info, se := os.Lstat(name)
	if e != nil || se != nil || resolved != name {
		return nil, bad
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() {
		return nil, bad
	}
	if directory {
		if info.Mode() != os.ModeDir|0700 {
			return nil, bad
		}
	} else if info.Mode() != 0600 || !info.Mode().IsRegular() || st.Nlink != 1 || info.Size() < 1 {
		return nil, bad
	}
	return info, nil
}
func preparedReleaseRead(ctx context.Context, name string, maximum int64, snapshot *preparedReleaseSnapshot) (string, int64, error) {
	info, e := preparedReleaseSourceInfo(name, false)
	if e != nil || info.Size() > maximum {
		return "", 0, failure("OFFLINE_ENGINE_PREPARED_SOURCE_FILE_INVALID")
	}
	f, e := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return "", 0, e
	}
	opened, se := f.Stat()
	if se != nil || !preparedReleaseSame(info, opened) {
		f.Close()
		return "", 0, failure("OFFLINE_ENGINE_PREPARED_SOURCE_CHANGED")
	}
	h := sha256.New()
	n, re := io.CopyBuffer(h, io.LimitReader(restoreContextReader{ctx, f}, info.Size()+1), make([]byte, 64<<10))
	after, ae := f.Stat()
	ce := f.Close()
	named, ne := os.Lstat(name)
	if re != nil || ae != nil || ce != nil || ne != nil || n != info.Size() || !preparedReleaseSame(info, after) || !preparedReleaseSame(info, named) {
		return "", 0, failure("OFFLINE_ENGINE_PREPARED_SOURCE_CHANGED")
	}
	snapshot.paths[name] = info
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// Describe verifies the ORIGINAL signed prepared inventory and original sibling
// journal, then binds every byte of the normalized runtime-owned projection.
// No tar/archive scratch, filesystem mutation or application proof is created.
func DescribePreparedRelease(ctx context.Context, r PreparedReleaseRequest) (PreparedReleaseProfile, error) {
	p, _, e := describePreparedRelease(ctx, r)
	return p, e
}
func describePreparedRelease(ctx context.Context, r PreparedReleaseRequest) (PreparedReleaseProfile, preparedReleaseSnapshot, error) {
	var out PreparedReleaseProfile
	snapshot := preparedReleaseSnapshot{paths: map[string]os.FileInfo{}}
	bad := failure("OFFLINE_ENGINE_PREPARED_SOURCE_INVALID")
	if ctx == nil || ctx.Err() != nil || !filepath.IsAbs(r.Root) || filepath.Clean(r.Root) != r.Root || !wire.ValidSHA256(r.ArchiveSHA256) || !wire.ValidSHA256(r.ManifestSHA256) || r.Verification.Validate() != nil {
		return out, snapshot, bad
	}
	verified, e := bundle.VerifyPrepared(ctx, r.Verification, r.Root, r.ArchiveSHA256, r.ManifestSHA256)
	if e != nil {
		return out, snapshot, e
	}
	root, e := preparedReleaseSourceInfo(r.Root, true)
	if e != nil {
		return out, snapshot, e
	}
	snapshot.paths[r.Root] = root
	trustHash, _, e := preparedReleaseRead(ctx, r.Verification.TrustFile, r.Verification.MaximumMetadataBytes, &snapshot)
	if e != nil {
		return out, snapshot, e
	}
	out = PreparedReleaseProfile{RequestSHA256: preparedReleaseRequestSHA(r, trustHash), ArchiveSHA256: r.ArchiveSHA256, ManifestSHA256: r.ManifestSHA256, Entries: []PreparedReleaseEntry{{Name: "private", Type: "directory"}, {Name: "store", Type: "directory"}}}
	directories := map[string]bool{"private": true, "store": true}
	files := map[string]wire.Asset{}
	for _, a := range verified.Manifest.Assets {
		files[a.Path] = a
	}
	for _, name := range []string{"manifest.json", "signature.json", preparedReleaseMarker} {
		if _, exists := files[name]; exists {
			return PreparedReleaseProfile{}, snapshot, bad
		}
		files[name] = wire.Asset{}
	}
	for relative, a := range files {
		// SafePath is the signed ASSET namespace and deliberately excludes the
		// factory's three fixed authority files (including the hidden marker).
		if a.ID != "" && !wire.SafePath(relative) {
			return PreparedReleaseProfile{}, snapshot, bad
		}
		maximum := r.Verification.MaximumMetadataBytes
		if relative == "manifest.json" {
			maximum = r.Verification.MaximumManifestBytes
		} else if a.ID != "" {
			maximum = a.Size
		}
		hash, size, e := preparedReleaseRead(ctx, filepath.Join(r.Root, filepath.FromSlash(relative)), maximum, &snapshot)
		if e != nil {
			return PreparedReleaseProfile{}, snapshot, e
		}
		if a.ID != "" && (hash != a.SHA256 || size != a.Size) || relative == "manifest.json" && hash != r.ManifestSHA256 {
			return PreparedReleaseProfile{}, snapshot, bad
		}
		out.Entries = append(out.Entries, PreparedReleaseEntry{Name: "private/" + relative, Type: "file", Bytes: size, SHA256: hash})
		out.Files++
		out.Bytes += size
		for parent := path.Dir(relative); parent != "."; parent = path.Dir(parent) {
			projected := "private/" + parent
			if !directories[projected] {
				info, e := preparedReleaseSourceInfo(filepath.Join(r.Root, filepath.FromSlash(parent)), true)
				if e != nil {
					return PreparedReleaseProfile{}, snapshot, e
				}
				snapshot.paths[filepath.Join(r.Root, filepath.FromSlash(parent))] = info
				directories[projected] = true
				out.Entries = append(out.Entries, PreparedReleaseEntry{Name: projected, Type: "directory"})
			}
		}
	}
	journalHash, journalBytes, e := preparedReleaseRead(ctx, r.Root+".journal.jsonl", 1<<20, &snapshot)
	if e != nil {
		return PreparedReleaseProfile{}, snapshot, e
	}
	out.Entries = append(out.Entries, PreparedReleaseEntry{Name: "private.journal.jsonl", Type: "file", Bytes: journalBytes, SHA256: journalHash})
	out.Files++
	out.Bytes += journalBytes
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Name < out.Entries[j].Name })
	raw, _ := json.Marshal(out.Entries)
	out.EntriesSHA256 = wire.Digest(raw)
	if len(out.Entries) > r.Verification.MaximumArchiveEntries || int64(len(raw)) > r.Verification.MaximumManifestBytes || out.Bytes > r.Verification.MaximumTotalBytes+r.Verification.MaximumManifestBytes+3*r.Verification.MaximumMetadataBytes+(1<<20) {
		return PreparedReleaseProfile{}, snapshot, failure("OFFLINE_ENGINE_PREPARED_INVENTORY_LIMIT")
	}
	if _, e = bundle.VerifyPrepared(ctx, r.Verification, r.Root, r.ArchiveSHA256, r.ManifestSHA256); e != nil || !snapshot.unchanged() {
		return PreparedReleaseProfile{}, snapshot, failure("OFFLINE_ENGINE_PREPARED_SOURCE_CHANGED")
	}
	return out, snapshot, nil
}
func preparedReleaseProfileValid(p PreparedReleaseProfile) bool {
	if !wire.ValidSHA256(p.RequestSHA256) || !wire.ValidSHA256(p.ArchiveSHA256) || !wire.ValidSHA256(p.ManifestSHA256) || !wire.ValidSHA256(p.EntriesSHA256) || len(p.Entries) < 6 || len(p.Entries) > maximumNativePostgreSQLArchiveEntries || p.Files < 4 || p.Bytes < 1 || p.Bytes > maximumNativePostgreSQLArchiveBytes {
		return false
	}
	seen := map[string]string{}
	files := 0
	var size int64
	previous := ""
	manifestBound := false
	metadata := int64(2)
	for _, e := range p.Entries {
		encoded, error := json.Marshal(e)
		if error != nil || int64(len(encoded)+1) > (16<<20)-metadata {
			return false
		}
		metadata += int64(len(encoded) + 1)
		if !nativeArchivePath(e.Name) || e.Name <= previous || e.Name != "private" && e.Name != "store" && e.Name != "private.journal.jsonl" && !strings.HasPrefix(e.Name, "private/") {
			return false
		}
		previous = e.Name
		if e.Type == "directory" {
			if e.Bytes != 0 || e.SHA256 != "" {
				return false
			}
		} else if e.Type == "file" {
			if e.Bytes < 1 || !wire.ValidSHA256(e.SHA256) || size > maximumNativePostgreSQLArchiveBytes-e.Bytes {
				return false
			}
			files++
			size += e.Bytes
		} else {
			return false
		}
		seen[e.Name] = e.Type
		if e.Name == "private/manifest.json" && e.Type == "file" && e.SHA256 == p.ManifestSHA256 {
			manifestBound = true
		}
	}
	if !manifestBound || seen["private/signature.json"] != "file" || seen["private/"+preparedReleaseMarker] != "file" || seen["private"] != "directory" || seen["store"] != "directory" || seen["private.journal.jsonl"] != "file" {
		return false
	}
	for name, kind := range seen {
		if strings.HasPrefix(name, "private/") && seen[path.Dir(name)] != "directory" || name == "store" && kind != "directory" {
			return false
		}
	}
	raw, _ := json.Marshal(p.Entries)
	return files == p.Files && size == p.Bytes && wire.Digest(raw) == p.EntriesSHA256
}
func preparedReleaseMaximumTar(p PreparedReleaseProfile) int64 {
	// One normal header plus conservative PAX path/size blocks per entry; data
	// rounding and bounded daemon trailer are included without a scratch copy.
	if !preparedReleaseProfileValid(p) || p.Bytes > math.MaxInt64-int64(len(p.Entries))*4096-archiveReadbackReserveBytes {
		return 0
	}
	return p.Bytes + int64(len(p.Entries))*4096 + archiveReadbackReserveBytes
}
func preparedReleaseAllocation(p PreparedReleaseProfile, fs NativePostgreSQLStorageFilesystem, minimum int64) error {
	bad := failure("OFFLINE_ENGINE_PREPARED_TARGET_CAPACITY_REQUIRED")
	if !preparedReleaseProfileValid(p) || fs.BackingRole != NativePostgreSQLDurableBacking || fs.TotalBytes < 1 || fs.AllocationUnitBytes < 1 || fs.AvailableBytes < 0 || minimum < 0 {
		return bad
	}
	var needed int64
	for _, e := range p.Entries {
		bytes := e.Bytes
		if e.Type == "directory" {
			bytes = fs.AllocationUnitBytes
		}
		if bytes > math.MaxInt64-fs.AllocationUnitBytes {
			return bad
		}
		blocks := (bytes + fs.AllocationUnitBytes - 1) / fs.AllocationUnitBytes
		if blocks > math.MaxInt64/fs.AllocationUnitBytes || needed > math.MaxInt64-blocks*fs.AllocationUnitBytes {
			return bad
		}
		needed += blocks * fs.AllocationUnitBytes
	}
	// Additional allocation units cover directory/inode/journal metadata. The
	// payload itself is streamed once; there is no archive or readback scratch.
	metadata := int64(len(p.Entries) + 8)
	if metadata > math.MaxInt64/4 || metadata*4 > math.MaxInt64/fs.AllocationUnitBytes || needed > math.MaxInt64-metadata*4*fs.AllocationUnitBytes {
		return bad
	}
	needed += metadata * 4 * fs.AllocationUnitBytes
	floor := fs.TotalBytes/10*3 + (fs.TotalBytes%10*3+9)/10
	reserve := max(floor, minimum)
	if reserve > math.MaxInt64-needed || fs.AvailableBytes < reserve+needed {
		return bad
	}
	return nil
}
