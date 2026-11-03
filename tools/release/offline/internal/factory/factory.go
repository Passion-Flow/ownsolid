package factory

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const signatureDomain = "projectone-offline-release-v1\n"

type Issue struct {
	AssetID string `json:"asset_id,omitempty"`
	Code    string `json:"code"`
}
type Validation struct {
	Status     string        `json:"status"`
	Profile    string        `json:"profile"`
	ReleaseID  string        `json:"release_id"`
	Issues     []Issue       `json:"issues"`
	TotalBytes int64         `json:"total_bytes"`
	Raw        []byte        `json:"-"`
	Manifest   wire.Manifest `json:"-"`
}
type Result struct {
	Status         string        `json:"status"`
	Profile        string        `json:"profile"`
	ReleaseID      string        `json:"release_id"`
	ManifestSHA256 string        `json:"manifest_sha256"`
	ArchiveSHA256  string        `json:"archive_sha256"`
	Assets         int           `json:"assets"`
	Qualification  string        `json:"qualification"`
	Manifest       wire.Manifest `json:"-"`
}
type Trust struct {
	Keys map[string]string `json:"keys"`
}

func loadTrust(c Config) (map[string]ed25519.PublicKey, error) {
	raw, e := readRegular(c.TrustFile, c.MaximumMetadataBytes, true)
	if e != nil {
		return nil, fail("RELEASE_PROTECTED_TRUST_REQUIRED")
	}
	var trust Trust
	if wire.StrictJSON(raw, &trust) != nil || len(trust.Keys) < 1 || len(trust.Keys) > 16 {
		return nil, fail("RELEASE_TRUST_INVALID")
	}
	out := map[string]ed25519.PublicKey{}
	for id, value := range trust.Keys {
		key, e := base64.StdEncoding.Strict().DecodeString(value)
		if e != nil || len(key) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(key) != value || wire.Digest(key) != id {
			return nil, fail("RELEASE_TRUST_INVALID")
		}
		out[id] = key
	}
	return out, nil
}
func verifySignature(raw, signature []byte, trust map[string]ed25519.PublicKey) error {
	var s wire.Signature
	if wire.StrictJSON(signature, &s) != nil || s.Algorithm != wire.SignatureAlgorithm || s.ManifestSHA256 != wire.Digest(raw) {
		return fail("RELEASE_SIGNATURE_INVALID")
	}
	key, ok := trust[s.KeyID]
	value, e := base64.StdEncoding.Strict().DecodeString(s.Value)
	if !ok || e != nil || base64.StdEncoding.EncodeToString(value) != s.Value || !ed25519.Verify(key, append([]byte(signatureDomain), raw...), value) {
		return fail("RELEASE_SIGNATURE_UNTRUSTED_OR_INVALID")
	}
	return nil
}
func Validate(ctx context.Context, c Config) (Validation, error) {
	v := Validation{Status: "failed_input_validation", Issues: []Issue{}}
	if mounted(c.MountRoot) != nil || !within(c.MountRoot, c.ArtifactRoot) || privateDirectory(c.ArtifactRoot) != nil || !within(c.MountRoot, c.ManifestFile) {
		return v, fail("RELEASE_MOUNTED_PRIVATE_INPUT_REQUIRED")
	}
	raw, e := readRegular(c.ManifestFile, c.MaximumManifestBytes, true)
	if e != nil {
		return v, e
	}
	var m wire.Manifest
	if e = wire.StrictJSON(raw, &m); e != nil {
		return v, e
	}
	v.Raw = raw
	v.Manifest = m
	v.Profile = m.Profile
	v.ReleaseID = m.ReleaseID
	if e = m.Validate(c.MaximumAssets, c.MaximumAssetBytes, c.MaximumTotalBytes, c.AllowTestFixture); e != nil {
		present := map[string]string{}
		for _, a := range m.Assets {
			present[a.ID] = a.Kind
		}
		required := m.RequiredAssets()
		ids := []string{}
		for id := range required {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if present[id] != required[id] {
				v.Issues = append(v.Issues, Issue{id, "RELEASE_REQUIRED_ASSET_MISSING_OR_WRONG_KIND"})
			}
		}
		return v, e
	}
	if len(m.Assets)+2 > c.MaximumArchiveEntries {
		return v, fail("RELEASE_ARCHIVE_ENTRY_LIMIT")
	}
	for _, a := range m.Assets {
		v.TotalBytes += a.Size
		name := filepath.Join(c.ArtifactRoot, filepath.FromSlash(a.Path))
		f, e := openRegular(name, a.Size, false)
		if e != nil {
			v.Issues = append(v.Issues, Issue{a.ID, e.Error()})
			continue
		}
		before, _ := f.Stat()
		digest, e := checkAsset(ctx, f, nil, a, c)
		after, se := f.Stat()
		closed := f.Close()
		pathAfter, pe := os.Lstat(name)
		if e == nil && (se != nil || pe != nil || closed != nil || !sameVersion(before, after) || !sameVersion(before, pathAfter)) {
			e = fail("RELEASE_INPUT_CHANGED")
		}
		if e == nil && digest != a.SHA256 {
			e = fail("RELEASE_ASSET_HASH_INVALID")
		}
		if e != nil {
			v.Issues = append(v.Issues, Issue{a.ID, e.Error()})
		}
	}
	if len(v.Issues) > 0 {
		return v, fail("RELEASE_INPUT_ASSETS_INVALID")
	}
	v.Status = "passed_declared_inventory_not_installation"
	return v, nil
}
func checkAsset(ctx context.Context, r io.Reader, w io.Writer, a wire.Asset, c Config) (string, error) {
	if file, ok := r.(*os.File); ok {
		var e error
		r, e = newStreamReader(ctx, file)
		if e != nil {
			return "", e
		}
	}
	if a.Kind == wire.LicenseTrustAssetKind {
		return checkLicenseTrust(ctx, r, w, a)
	}
	if a.Kind != "image_archive" && a.Kind != "deb" {
		return streamHash(ctx, r, w, a.Size)
	}
	h := sha256.New()
	target := io.Writer(h)
	if w != nil {
		target = io.MultiWriter(w, h)
	}
	counter := &countReader{r: io.TeeReader(checkedReader{ctx, r}, target)}
	if a.Kind == "image_archive" {
		if e := validateImage(ctx, counter, a, c); e != nil {
			return "", e
		}
	} else {
		if e := validateDeb(counter, a.Size); e != nil {
			return "", e
		}
	}
	if counter.n != a.Size {
		return "", fail("RELEASE_ASSET_SIZE_INVALID")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type countReader struct {
	r io.Reader
	n int64
}

func (r *countReader) Read(p []byte) (int, error) { n, e := r.r.Read(p); r.n += int64(n); return n, e }
func archiveLimit(c Config) int64 {
	return c.MaximumTotalBytes + 2*c.MaximumManifestBytes + int64(c.MaximumAssets)*4096 + 1048576
}

func Inspect(ctx context.Context, c Config, name string) (Result, error) {
	result := Result{}
	if mounted(c.MountRoot) != nil || !within(c.MountRoot, name) {
		return result, fail("RELEASE_MOUNTED_BUNDLE_REQUIRED")
	}
	trust, e := loadTrust(c)
	if e != nil {
		return result, e
	}
	links := uint64(1)
	uncertain := false
	if info, se := os.Lstat(name); se == nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Nlink == 2 {
			base := strings.TrimSuffix(name, ".incomplete")
			other := base + ".incomplete"
			if other == name {
				other = base
			}
			peer, pe := os.Lstat(other)
			journal, je := readRegular(base+".journal.jsonl", 1048576, true)
			if pe != nil || je != nil || !os.SameFile(info, peer) || privateDirectory(filepath.Dir(name)) != nil || !bytes.Contains(journal, []byte(`"status":"full_readback_passed"`)) {
				return result, fail("RELEASE_UNKNOWN_PUBLICATION_LINKS_REFUSED")
			}
			links = 2
			uncertain = true
		}
	}
	f, e := openRegularLinks(name, archiveLimit(c), true, links)
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
		return result, fail("RELEASE_GZIP_INVALID")
	}
	defer compressed.Close()
	compressed.Multistream(false)
	t := tar.NewReader(compressed)
	readMetadata := func(name string, maximum int64) ([]byte, error) {
		h, e := t.Next()
		if e != nil || h.Name != name || !safeHeader(h) || h.Size < 1 || h.Size > maximum {
			return nil, fail("RELEASE_ARCHIVE_METADATA_INVALID")
		}
		return io.ReadAll(t)
	}
	raw, e := readMetadata("manifest.json", c.MaximumManifestBytes)
	if e != nil {
		return result, e
	}
	signature, e := readMetadata("signature.json", c.MaximumMetadataBytes)
	if e != nil {
		return result, e
	}
	if e = verifySignature(raw, signature, trust); e != nil {
		return result, e
	}
	var m wire.Manifest
	if wire.StrictJSON(raw, &m) != nil || m.Validate(c.MaximumAssets, c.MaximumAssetBytes, c.MaximumTotalBytes, c.AllowTestFixture) != nil || len(m.Assets)+2 > c.MaximumArchiveEntries {
		return result, fail("RELEASE_ARCHIVE_MANIFEST_INVALID")
	}
	for _, a := range m.Assets {
		h, e := t.Next()
		if e != nil || !safeAssetHeader(h, a) {
			return result, fail("RELEASE_ARCHIVE_ASSET_INVALID")
		}
		digest, e := checkAsset(ctx, t, nil, a, c)
		if e != nil {
			return result, e
		}
		if digest != a.SHA256 {
			return result, fail("RELEASE_ARCHIVE_ASSET_HASH_INVALID")
		}
	}
	if _, e = t.Next(); e != io.EOF {
		return result, fail("RELEASE_ARCHIVE_EXTRA_OR_TRUNCATED_ENTRY")
	}
	tail, e := io.ReadAll(io.LimitReader(compressed, 10241))
	if e != nil || len(tail) > 10240 || len(bytes.Trim(tail, "\x00")) != 0 {
		return result, fail("RELEASE_ARCHIVE_TRAILING_DATA")
	}
	if _, e = buffer.Peek(1); e != io.EOF {
		return result, fail("RELEASE_ARCHIVE_MULTIPLE_GZIP_OR_TRAILING_DATA")
	}
	after, se := f.Stat()
	pathAfter, pe := os.Lstat(name)
	if se != nil || pe != nil || !sameVersion(before, after) || !sameVersion(before, pathAfter) {
		return result, fail("RELEASE_INPUT_CHANGED")
	}
	qualification := "signed_declared_inventory_bytes_only_not_installation_or_target"
	if m.Profile == wire.FixtureProfile {
		qualification = "synthetic_test_fixture_only_never_complete_platform_bundle"
	}
	status := "passed_signed_archive_full_readback"
	if uncertain {
		status = "passed_signed_archive_readback_publication_links_uncertain"
		qualification += "_publication_not_confirmed"
	}
	return Result{Status: status, Profile: m.Profile, ReleaseID: m.ReleaseID, ManifestSHA256: wire.Digest(raw), ArchiveSHA256: hex.EncodeToString(sum.Sum(nil)), Assets: len(m.Assets), Qualification: qualification, Manifest: m}, nil
}
func safeHeader(h *tar.Header) bool {
	return h != nil && h.Typeflag == tar.TypeReg && h.Mode == 0644 && h.Uid == 0 && h.Gid == 0 && h.Linkname == "" && len(h.PAXRecords) == 0 && len(h.Xattrs) == 0
}

func Build(ctx context.Context, c Config) (result Result, err error) {
	return build(ctx, c, durableIO{syncDirectory: func(f *os.File) error { return f.Sync() }, link: os.Link, remove: os.Remove})
}

type durableIO struct {
	syncDirectory func(*os.File) error
	link          func(string, string) error
	remove        func(string) error
}

func build(ctx context.Context, c Config, operations durableIO) (result Result, err error) {
	v, e := Validate(ctx, c)
	if e != nil {
		return result, e
	}
	trust, e := loadTrust(c)
	if e != nil {
		return result, e
	}
	if v.Manifest.Profile != wire.FixtureProfile && within(c.MountRoot, c.PrivateKeyFile) {
		return result, fail("RELEASE_SIGNING_KEY_MUST_REMAIN_INTERNAL")
	}
	if within(c.ArtifactRoot, c.PrivateKeyFile) {
		return result, fail("RELEASE_SIGNING_KEY_INSIDE_ASSETS_REFUSED")
	}
	keyRaw, e := readRegular(c.PrivateKeyFile, 4096, true)
	if e != nil {
		return result, fail("RELEASE_PRIVATE_SIGNING_KEY_REQUIRED")
	}
	encoded := strings.TrimSpace(string(keyRaw))
	key, e := base64.StdEncoding.Strict().DecodeString(encoded)
	if e != nil || len(key) != ed25519.PrivateKeySize || base64.StdEncoding.EncodeToString(key) != encoded {
		return result, fail("RELEASE_SIGNING_KEY_INVALID")
	}
	private := ed25519.PrivateKey(key)
	if !bytes.Equal(ed25519.NewKeyFromSeed(private.Seed()), private) {
		return result, fail("RELEASE_SIGNING_KEY_INVALID")
	}
	public := private.Public().(ed25519.PublicKey)
	id := wire.Digest(public)
	if !bytes.Equal(trust[id], public) {
		return result, fail("RELEASE_SIGNING_KEY_NOT_PINNED")
	}
	signature := Encode(wire.Signature{Algorithm: wire.SignatureAlgorithm, KeyID: id, ManifestSHA256: wire.Digest(v.Raw), Value: base64.StdEncoding.EncodeToString(ed25519.Sign(private, append([]byte(signatureDomain), v.Raw...)))})
	parent, e := outputParent(c)
	if e != nil {
		return result, e
	}
	defer parent.Close()
	if e = diskAdmission(c, v.TotalBytes+int64(len(v.Raw)+len(signature))+int64(len(v.Manifest.Assets))*2048+1048576); e != nil {
		return result, e
	}
	journal, e := os.OpenFile(c.OutputFile+".journal.jsonl", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return result, fail("RELEASE_JOURNAL_RESERVATION_FAILED")
	}
	defer journal.Close()
	event := func(status, code string) error {
		if _, e := journal.Write(Encode(map[string]any{"status": status, "code": code, "release_id": v.ReleaseID, "profile": v.Profile, "manifest_sha256": wire.Digest(v.Raw), "time": time.Now().UTC().Format(time.RFC3339Nano)})); e != nil {
			return fail("RELEASE_JOURNAL_WRITE_FAILED")
		}
		if journal.Sync() != nil {
			return fail("RELEASE_JOURNAL_SYNC_FAILED")
		}
		return nil
	}
	defer func() {
		if err != nil {
			_ = event("failed_preserve_incomplete_or_uncertain_publication", err.Error())
		}
	}()
	if e = event("reserved", ""); e != nil {
		return result, e
	}
	if operations.syncDirectory(parent) != nil {
		return result, fail("RELEASE_DIRECTORY_SYNC_FAILED")
	}
	f, e := os.OpenFile(c.OutputFile+".incomplete", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return result, fail("RELEASE_INCOMPLETE_RESERVATION_FAILED")
	}
	defer f.Close()
	output, e := newStreamWriter(ctx, f)
	if e != nil {
		return result, e
	}
	compressed, e := gzip.NewWriterLevel(output, c.CompressionLevel)
	if e != nil {
		return result, e
	}
	t := tar.NewWriter(compressed)
	write := func(name string, size int64, r io.Reader) error {
		if e := t.WriteHeader(releaseArchiveHeader(name, size)); e != nil {
			return fail("RELEASE_ARCHIVE_WRITE_FAILED")
		}
		_, e := streamHash(ctx, r, t, size)
		return e
	}
	if e = write("manifest.json", int64(len(v.Raw)), bytes.NewReader(v.Raw)); e != nil {
		return result, e
	}
	if e = write("signature.json", int64(len(signature)), bytes.NewReader(signature)); e != nil {
		return result, e
	}
	for _, a := range v.Manifest.Assets {
		file, e := openRegular(filepath.Join(c.ArtifactRoot, filepath.FromSlash(a.Path)), a.Size, false)
		if e != nil {
			return result, e
		}
		before, _ := file.Stat()
		if e = t.WriteHeader(releaseArchiveHeader(a.Path, a.Size)); e != nil {
			file.Close()
			return result, fail("RELEASE_ARCHIVE_WRITE_FAILED")
		}
		hash, e := checkAsset(ctx, file, t, a, c)
		after, se := file.Stat()
		file.Close()
		if e != nil {
			return result, e
		}
		if se != nil || !sameVersion(before, after) || hash != a.SHA256 {
			return result, fail("RELEASE_ASSET_CHANGED_DURING_BUILD")
		}
	}
	if t.Close() != nil || compressed.Close() != nil || output.finish() != nil || f.Sync() != nil || f.Close() != nil {
		return result, fail("RELEASE_ARCHIVE_FLUSH_FAILED")
	}
	if e = event("written_synced", ""); e != nil {
		return result, e
	}
	result, e = Inspect(ctx, c, c.OutputFile+".incomplete")
	if e != nil {
		return result, e
	}
	if result.ManifestSHA256 != wire.Digest(v.Raw) {
		return result, fail("RELEASE_READBACK_MANIFEST_CHANGED")
	}
	if e = event("full_readback_passed", ""); e != nil {
		return result, e
	}
	if ctx.Err() != nil {
		return result, fail("RELEASE_DEADLINE_OR_INTERRUPTION")
	}
	if e = operations.link(c.OutputFile+".incomplete", c.OutputFile); e != nil {
		return result, fail("RELEASE_EXCLUSIVE_PUBLICATION_FAILED")
	}
	if e = operations.remove(c.OutputFile + ".incomplete"); e != nil {
		return result, fail("RELEASE_PUBLICATION_UNCERTAIN_PRESERVE_LINKS")
	}
	if operations.syncDirectory(parent) != nil {
		return result, fail("RELEASE_PUBLICATION_DIRECTORY_SYNC_UNCERTAIN")
	}
	if ctx.Err() != nil {
		return result, fail("RELEASE_PUBLICATION_DEADLINE_UNCERTAIN")
	}
	if e = event("published", ""); e != nil {
		return result, e
	}
	return result, nil
}

func ReadConfigFile(name string) ([]byte, error) { return readRegular(name, 1048576, true) }
func ErrorCode(e error) string {
	if e == nil {
		return ""
	}
	if strings.HasPrefix(e.Error(), "RELEASE_") && !strings.ContainsAny(e.Error(), "\r\n") {
		return e.Error()
	}
	return "RELEASE_OPERATION_FAILED"
}
func PrintError(e error) string { return fmt.Sprintf("%s", ErrorCode(e)) }
