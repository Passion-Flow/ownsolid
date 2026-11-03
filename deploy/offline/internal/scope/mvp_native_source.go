package scope

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

const mvpNativeDirectory = "native-postgresql"
const mvpNativeObservationFile = "native-observation.json"
const mvpNativeReleaseFile = "source-release.json"
const mvpNativeSignatureFile = "source-signature.json"
const mvpNativeSourceDescriptorFile = "source-descriptor.json"

// This is an immutable wrapper around the original native directory, never a
// replacement native manifest or an assertion of target/import authority.
type mvpNativeBackupPlan struct {
	Forward               *mvpNativeForwardBackupBinding  `json:"forward,omitempty"`
	Restored              *mvpNativeRestoredBackupBinding `json:"restored,omitempty"`
	ReleaseViewBinding    *view.Configuration             `json:"release_view_binding"`
	Storage               mvpNativeBackupStoragePlan      `json:"storage"`
	ExecutionSHA256       string                          `json:"execution_sha256"`
	NativeUID             int                             `json:"native_uid"`
	NativeGID             int                             `json:"native_gid"`
	FormatVersion         int                             `json:"format_version"`
	ScopeID               string                          `json:"scope_id"`
	AttemptID             string                          `json:"attempt_id"`
	ReleaseID             string                          `json:"release_id"`
	Profile               string                          `json:"profile"`
	ConfigurationSHA256   string                          `json:"configuration_sha256"`
	ReleaseManifestSHA256 string                          `json:"release_manifest_sha256"`
	ReleaseArchiveSHA256  string                          `json:"release_archive_sha256"`
	DescriptorSHA256      string                          `json:"descriptor_sha256"`
	RuntimeSHA256         string                          `json:"runtime_sha256"`
	Request               configuration.BackupRequest     `json:"request"`
	NativeRequest         wire.PostgreSQLBackupRequest    `json:"native_request"`
	ConfigurationFiles    []BackupConfigurationFile       `json:"configuration_files"`
	Steps                 []BackupStep                    `json:"steps"`
}

type mvpNativeInventoryEntry struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Mode   uint32 `json:"mode"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256,omitempty"`
}

type mvpNativeBackupManifest struct {
	FormatVersion         int                                  `json:"format_version"`
	Status                string                               `json:"status"`
	ScopeID               string                               `json:"scope_id"`
	AttemptID             string                               `json:"attempt_id"`
	PlanSHA256            string                               `json:"plan_sha256"`
	ReleaseID             string                               `json:"release_id"`
	Profile               string                               `json:"profile"`
	ReleaseManifestSHA256 string                               `json:"release_manifest_sha256"`
	NativePostgreSQL      wire.NativePostgreSQLBackupReference `json:"native_postgresql"`
	ObservationSHA256     string                               `json:"observation_sha256"`
	Entries               []mvpNativeInventoryEntry            `json:"entries"`
}

type mvpNativeRestoreSource struct {
	NativeArchiveFile    string                                `json:"-"`
	NativeArchiveProfile engine.NativePostgreSQLArchiveProfile `json:"-"`
	Result               RestoreSourceResult                   `json:"-"`
	Request              wire.PostgreSQLBackupRequest          `json:"-"`
	ArchiveSHA256        string                                `json:"-"`
	NativeDirectory      string                                `json:"-"`
	Plan                 mvpNativeBackupPlan                   `json:"-"`
}

// false is reserved for genuinely legacy inputs. A native directory, native
// field, MVP profile or schema29 can never fall through to the old split reader.
func inspectMVPNativeRestoreSource(ctx context.Context, c configuration.Config, r configuration.RestoreSourceRequest) (RestoreSourceResult, bool, error) {
	handled, e := hasMVPNativeSource(c, r)
	if e != nil || !handled {
		return RestoreSourceResult{}, handled, e
	}
	s, e := readMVPNativeRestoreSource(ctx, c, r)
	return s.Result, true, e
}

func hasMVPNativeSource(c configuration.Config, r configuration.RestoreSourceRequest) (bool, error) {
	if r.Validate() != nil {
		return true, failure("OFFLINE_SCOPE_NATIVE_SOURCE_REQUEST_INVALID")
	}
	root, e := openBackupDirectory(r.BackupRoot)
	if e != nil {
		return true, e
	}
	defer root.Close()
	if _, e = root.Lstat(mvpNativeDirectory); e == nil {
		return true, nil
	} else if !os.IsNotExist(e) {
		return true, e
	}
	for _, name := range []string{backupPlanFile, backupManifestFile} {
		raw, e := regular(root, name, c.Verification.MaximumMetadataBytes)
		if e != nil {
			return true, e
		}
		// UniqueJSON checks the entire object before looking for the native marker.
		var obj map[string]json.RawMessage
		if wire.UniqueJSON(raw, &obj) != nil || obj == nil {
			return true, failure("OFFLINE_SCOPE_NATIVE_SOURCE_METADATA_INVALID")
		}
		for name, value := range obj {
			if strings.EqualFold(name, "native_postgresql") || strings.EqualFold(name, "native_request") {
				return true, nil
			}
			if strings.EqualFold(name, "profile") {
				var profile string
				if json.Unmarshal(value, &profile) == nil && profile == wire.MVPPostgreSQLCPUProfile {
					return true, nil
				}
			}
			if strings.EqualFold(name, "source") {
				var source struct {
					SchemaVersion int `json:"schema_version"`
				}
				if wire.UniqueJSON(value, &source) == nil && source.SchemaVersion == wire.MVPPostgreSQLCPUV1SchemaVersion {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func readMVPNativeRestoreSource(ctx context.Context, c configuration.Config, r configuration.RestoreSourceRequest) (mvpNativeRestoreSource, error) {
	var out mvpNativeRestoreSource
	bad := failure("OFFLINE_SCOPE_NATIVE_SOURCE_BINDING_INVALID")
	if ctx == nil || r.Validate() != nil {
		return out, bad
	}
	if ctx.Err() != nil {
		return out, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if !mounted(c.Verification.MountRoot) || !contained(c.Verification.MountRoot, r.BackupRoot) || !canonical(r.BackupRoot) {
		return out, failure("OFFLINE_SCOPE_RESTORE_MOUNTED_BACKUP_REQUIRED")
	}
	for _, target := range []string{c.ScopeRoot, c.DestinationRoot} {
		if target != "" && (target == r.BackupRoot || contained(target, r.BackupRoot) || contained(r.BackupRoot, target)) {
			return out, failure("OFFLINE_SCOPE_RESTORE_SOURCE_TARGET_OVERLAP")
		}
	}
	root, e := openBackupDirectory(r.BackupRoot)
	if e != nil {
		return out, e
	}
	defer root.Close()
	raw, e := regular(root, backupPlanFile, c.Verification.MaximumMetadataBytes)
	if e != nil || wire.StrictJSON(raw, &out.Plan) != nil || !validMVPNativeBackupPlan(out.Plan) {
		return out, bad
	}
	p := out.Plan
	manifest, e := regular(root, backupManifestFile, c.Verification.MaximumMetadataBytes)
	var m mvpNativeBackupManifest
	if e != nil || wire.Digest(manifest) != r.ManifestSHA256 || wire.StrictJSON(manifest, &m) != nil || m.FormatVersion != 2 || m.Status != "complete_selected_profile_backup_restore_pending" || m.ScopeID != p.ScopeID || m.AttemptID != p.AttemptID || m.PlanSHA256 != wire.Digest(raw) || m.ReleaseID != p.ReleaseID || m.Profile != p.Profile || m.ReleaseManifestSHA256 != p.ReleaseManifestSHA256 || !m.NativePostgreSQL.Valid() || !m.NativePostgreSQL.RegistryConfigured || m.NativePostgreSQL.Registry == nil || !wire.ValidSHA256(m.ObservationSHA256) {
		return out, bad
	}
	if p.Request.MaximumBytes > r.MaximumBytes || p.Request.MaximumEntries > r.MaximumEntries {
		return out, failure("OFFLINE_SCOPE_RESTORE_SOURCE_DECLARED_LIMIT_EXCEEDS_READER")
	}
	// This verifies the original signed source declaration with the configured
	// release trust, not the independent target's release or an embedded key.
	if e = verifyMVPNativeSourceRelease(root, c, p); e != nil {
		return out, e
	}
	if e = verifyMVPNativeOriginalDescriptor(root, c, p); e != nil {
		return out, e
	}
	if e = readMVPNativeReleasePolicy(root, c, p); e != nil {
		return out, e
	}
	entries, total, e := inventoryMVPNativeSource(ctx, root, p.Request.MaximumBytes, p.Request.MaximumEntries, c.Verification.MaximumMetadataBytes)
	if e != nil {
		return out, e
	}
	if !reflect.DeepEqual(entries, m.Entries) || total > r.MaximumBytes-int64(len(manifest)) || len(entries)+2 > r.MaximumEntries {
		return out, failure("OFFLINE_SCOPE_NATIVE_SOURCE_INVENTORY_CHANGED")
	}
	original, e := regular(root, mvpNativeObservationFile, c.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(original) != m.ObservationSHA256 {
		return out, bad
	}
	obs, e := engine.DecodeNativePostgreSQLBackupObservation(original, c.Verification.MaximumMetadataBytes, p.NativeRequest.BackupID, wire.Digest(encode(p.NativeRequest)), m.NativePostgreSQL.CandidateSHA256, p.ReleaseManifestSHA256)
	if e != nil || obs.NativePostgreSQL == nil || !reflect.DeepEqual(*obs.NativePostgreSQL, m.NativePostgreSQL) {
		return out, bad
	}
	if e = verifyMVPNativePayloadInventory(root, c, entries); e != nil {
		return out, e
	}
	source, archive, e := readMVPNativeCandidate(root, c, p, m.NativePostgreSQL)
	if e != nil {
		return out, e
	}
	if _, e = readMVPNativeExecution(root, c, p); e != nil {
		return out, e
	}
	archiveProfile, e := readMVPNativeArchive(ctx, root, c, p, entries)
	if e != nil {
		return out, e
	}
	out.NativeArchiveFile = filepath.Join(r.BackupRoot, mvpNativeArchiveFile)
	out.NativeArchiveProfile = archiveProfile
	for _, f := range p.ConfigurationFiles {
		found := false
		for _, entry := range entries {
			if entry.Name == "configuration/"+f.Role+"/"+f.File.Name && entry.Kind == "file" && entry.Mode == 0600 && entry.Bytes == f.File.Bytes && entry.SHA256 == f.File.SHA256 {
				found = true
				break
			}
		}
		if !found {
			return out, failure("OFFLINE_SCOPE_BACKUP_CONFIGURATION_CHANGED")
		}
	}
	files := 1
	for _, entry := range entries {
		if entry.Kind == "file" {
			files++
		}
	}
	out.Request = p.NativeRequest
	out.ArchiveSHA256 = archive
	out.NativeDirectory = filepath.Join(r.BackupRoot, mvpNativeDirectory)
	ref := m.NativePostgreSQL
	out.Result = RestoreSourceResult{Status: "verified_independent_backup_source_not_restored", RequestSHA256: wire.Digest(encode(r)), ManifestSHA256: r.ManifestSHA256, PlanSHA256: m.PlanSHA256, SourceScopeID: p.ScopeID, SourceAttemptID: p.AttemptID, ReleaseID: p.ReleaseID, Profile: p.Profile, Source: source, Stores: []string{"datasets", "models", "preparations", "training"}, Files: files, Bytes: total + int64(len(manifest)), ReleaseManifestSHA256: p.ReleaseManifestSHA256, NativePostgreSQL: &ref, Qualification: "readonly_original_verified_native_backup_inventory_only_not_target_restore_or_runtime_qualification"}
	return out, nil
}

func validMVPNativeBackupPlan(p mvpNativeBackupPlan) bool {
	r := p.NativeRequest
	if p.NativeUID < 1 || p.NativeGID < 1 || p.NativeUID > 1<<31-1 || p.NativeGID > 1<<31-1 || !wire.ValidSHA256(p.ExecutionSHA256) || p.FormatVersion != 2 || !identityPattern.MatchString(p.ScopeID) || !identityPattern.MatchString(p.AttemptID) || p.ScopeID == p.AttemptID || p.ReleaseID == "" || len(p.ReleaseID) > 128 || p.Profile != wire.MVPPostgreSQLCPUProfile || !wire.ValidSHA256(p.ConfigurationSHA256) || !wire.ValidSHA256(p.ReleaseManifestSHA256) || !wire.ValidSHA256(p.ReleaseArchiveSHA256) || !wire.ValidSHA256(p.DescriptorSHA256) || !wire.ValidSHA256(p.RuntimeSHA256) || p.Request.Validate() != nil || !r.Valid() || r.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || r.SourceScopeID != p.ScopeID || r.BackupID != p.AttemptID || r.SourceManifestSHA256 != p.ReleaseManifestSHA256 || r.Reason != p.Request.Reason || len(p.ConfigurationFiles) < 1 || len(p.ConfigurationFiles) > 1024 || len(p.Steps) < 2 || len(p.Steps) > 128 {
		return false
	}
	if !validMVPNativeForwardBinding(p) || !validMVPNativeRestoredBinding(p) || !validMVPNativeReleasePolicyBinding(p) || !validMVPNativeBackupStorage(p) || p.Steps[0] != (BackupStep{Phase: "reserve"}) || p.Steps[len(p.Steps)-1] != (BackupStep{Phase: "complete"}) {
		return false
	}
	seen := map[string]bool{}
	for _, f := range p.ConfigurationFiles {
		key := f.Role + "/" + f.File.Name
		if !strings.HasSuffix(f.Role, "-config") || roleServices[f.Role] == "" || !wire.SafePath(f.File.Name) || strings.Contains(f.File.Name, "/") || f.File.Bytes < 1 || !wire.ValidSHA256(f.File.SHA256) || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

// Native files retain their original owner modes, including sealed model/OCI
// payloads and empty files. Wrapper/configuration files keep the old0600 guard.
func mvpNativeInfo(info os.FileInfo, native bool) bool {
	if info == nil || !owned(info) {
		return false
	}
	if info.IsDir() {
		return info.Mode() == os.ModeDir|0700 || native && info.Mode() == os.ModeDir|0555
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1 && info.Mode().IsRegular() && info.Size() >= 0 && (native && info.Mode() <= 0777 && info.Mode().Perm()&0022 == 0 || !native && info.Mode() == 0600 && info.Size() > 0)
}
func sameMVPNativeInfo(a, b os.FileInfo, native bool) bool {
	if !mvpNativeInfo(a, native) || !mvpNativeInfo(b, native) || !os.SameFile(a, b) || a.Mode() != b.Mode() || a.Size() != b.Size() || a.ModTime() != b.ModTime() || !samePrivateChangeTime(a, b) {
		return false
	}
	x, y := a.Sys().(*syscall.Stat_t), b.Sys().(*syscall.Stat_t)
	return x.Uid == y.Uid && x.Gid == y.Gid
}

// Pins every directory and file descriptor across the whole traversal. The
// portable identity contains paths/modes/bytes/hashes, never inode or timestamps.
func inventoryMVPNativeSource(ctx context.Context, root *os.Root, maximum int64, limit int, metadata int64) ([]mvpNativeInventoryEntry, int64, error) {
	entries := []mvpNativeInventoryEntry{}
	var total int64
	var usedMetadata int64
	if ctx == nil || maximum < 1 || limit < 1 || metadata < 1 {
		return nil, 0, failure("OFFLINE_SCOPE_NATIVE_SOURCE_LIMIT_INVALID")
	}
	var walk func(*os.Root, string, string) error
	walk = func(parent *os.Root, leaf, name string) error {
		if ctx.Err() != nil {
			return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		native := name == mvpNativeDirectory || strings.HasPrefix(name, mvpNativeDirectory+"/")
		before, e := parent.Lstat(leaf)
		if e != nil || !mvpNativeInfo(before, native) {
			return failure("OFFLINE_SCOPE_NATIVE_SOURCE_PRIVATE_ENTRY_REQUIRED")
		}
		usedMetadata += int64(len(name) + 256)
		if usedMetadata > metadata || len(entries) >= limit-1 {
			return failure("OFFLINE_SCOPE_NATIVE_SOURCE_ENTRY_LIMIT")
		}
		entry := mvpNativeInventoryEntry{Name: name, Kind: "directory", Mode: uint32(before.Mode().Perm())}
		if before.IsDir() {
			child, e := parent.OpenRoot(leaf + "/.")
			if e != nil {
				return failure("OFFLINE_SCOPE_NATIVE_SOURCE_DIRECTORY_CHANGED")
			}
			defer child.Close()
			opened, e := child.Stat(".")
			if e != nil || !sameMVPNativeInfo(before, opened, native) {
				return failure("OFFLINE_SCOPE_NATIVE_SOURCE_DIRECTORY_CHANGED")
			}
			entries = append(entries, entry)
			dir, e := child.Open(".")
			if e != nil {
				return e
			}
			defer dir.Close()
			names := []string{}
			for {
				items, re := dir.ReadDir(128)
				for _, item := range items {
					n := item.Name()
					if (native && (n == "" || n == "." || n == ".." || strings.Contains(n, "/") || !utf8.ValidString(n) || strings.ContainsFunc(n, unicode.IsControl))) || (!native && (!wire.SafePath(n) || strings.Contains(n, "/"))) {
						return failure("OFFLINE_SCOPE_NATIVE_SOURCE_ENTRY_INVALID")
					}
					usedMetadata += int64(len(n) + 32)
					if usedMetadata > metadata || len(names) > limit-len(entries) {
						return failure("OFFLINE_SCOPE_NATIVE_SOURCE_ENTRY_LIMIT")
					}
					names = append(names, n)
				}
				if re == io.EOF {
					break
				}
				if re != nil {
					return re
				}
			}
			sort.Strings(names)
			for _, n := range names {
				if name == "." && n == backupManifestFile {
					continue
				}
				next := path.Join(name, n)
				if e = walk(child, n, next); e != nil {
					return e
				}
			}
			after, e := child.Stat(".")
			named, ne := parent.Lstat(leaf)
			if e != nil || ne != nil || !sameMVPNativeInfo(before, after, native) || !sameMVPNativeInfo(before, named, native) {
				return failure("OFFLINE_SCOPE_NATIVE_SOURCE_DIRECTORY_CHANGED")
			}
			return nil
		}
		if before.Size() > maximum-total {
			return failure("OFFLINE_SCOPE_NATIVE_SOURCE_BYTE_LIMIT")
		}
		f, e := parent.OpenFile(leaf, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return failure("OFFLINE_SCOPE_NATIVE_SOURCE_FILE_UNAVAILABLE")
		}
		defer f.Close()
		opened, e := f.Stat()
		if e != nil || !sameMVPNativeInfo(before, opened, native) {
			return failure("OFFLINE_SCOPE_NATIVE_SOURCE_FILE_CHANGED")
		}
		h := sha256.New()
		buffer := make([]byte, 64<<10)
		var count int64
		for {
			if ctx.Err() != nil {
				return failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
			}
			n, re := f.Read(buffer)
			if n > 0 {
				count += int64(n)
				if count > maximum-total {
					return failure("OFFLINE_SCOPE_NATIVE_SOURCE_BYTE_LIMIT")
				}
				h.Write(buffer[:n])
			}
			if re == io.EOF {
				break
			}
			if re != nil {
				return failure("OFFLINE_SCOPE_NATIVE_SOURCE_FILE_READ_FAILED")
			}
		}
		after, e := f.Stat()
		named, ne := parent.Lstat(leaf)
		if e != nil || ne != nil || count != before.Size() || !sameMVPNativeInfo(before, after, native) || !sameMVPNativeInfo(before, named, native) {
			return failure("OFFLINE_SCOPE_NATIVE_SOURCE_FILE_CHANGED")
		}
		entry.Kind = "file"
		entry.Bytes = count
		entry.SHA256 = hex.EncodeToString(h.Sum(nil))
		entries = append(entries, entry)
		total += count
		return nil
	}
	// Root's named identity must remain pinned too, not merely its descriptor.
	before, e := os.Lstat(filepath.Clean(root.Name()))
	if e != nil || !canonical(filepath.Clean(root.Name())) {
		return nil, 0, failure("OFFLINE_SCOPE_NATIVE_SOURCE_DIRECTORY_CHANGED")
	}
	if e = walk(root, ".", "."); e != nil {
		return nil, 0, e
	}
	after, e := os.Lstat(filepath.Clean(root.Name()))
	if e != nil || !sameMVPNativeInfo(before, after, false) {
		return nil, 0, failure("OFFLINE_SCOPE_NATIVE_SOURCE_DIRECTORY_CHANGED")
	}
	return entries, total, nil
}

func verifyMVPNativeSourceRelease(root *os.Root, c configuration.Config, p mvpNativeBackupPlan) error {
	bad := failure("OFFLINE_SCOPE_NATIVE_SOURCE_RELEASE_INVALID")
	raw, e := regular(root, mvpNativeReleaseFile, c.Verification.MaximumManifestBytes)
	if e != nil || wire.Digest(raw) != p.ReleaseManifestSHA256 {
		return bad
	}
	var m wire.Manifest
	if wire.StrictJSON(raw, &m) != nil || m.Validate(c.Verification.MaximumAssets, c.Verification.MaximumAssetBytes, c.Verification.MaximumTotalBytes, c.Verification.AllowTestFixture) != nil || m.Profile != wire.MVPPostgreSQLCPUProfile || m.ReleaseID != p.ReleaseID {
		return bad
	}
	images := map[string]string{}
	for _, asset := range m.Assets {
		if asset.ID == "control-image" || asset.ID == "postgres-image" {
			images[asset.ID] = asset.ImageID
		}
	}
	if images["control-image"] != "sha256:"+p.NativeRequest.ControlImageSHA256 || images["postgres-image"] != "sha256:"+p.NativeRequest.PostgresImageSHA256 {
		return bad
	}
	sigraw, e := regular(root, mvpNativeSignatureFile, c.Verification.MaximumMetadataBytes)
	if e != nil {
		return bad
	}
	var sig wire.Signature
	if wire.StrictJSON(sigraw, &sig) != nil || sig.Algorithm != wire.SignatureAlgorithm || sig.ManifestSHA256 != p.ReleaseManifestSHA256 {
		return bad
	}
	trustRaw, e := bundle.ReadProtectedConfiguration(c.Verification.TrustFile)
	if e != nil || int64(len(trustRaw)) > c.Verification.MaximumMetadataBytes {
		return bad
	}
	var trust struct {
		Keys map[string]string `json:"keys"`
	}
	if wire.StrictJSON(trustRaw, &trust) != nil || len(trust.Keys) < 1 || len(trust.Keys) > 16 {
		return bad
	}
	var key ed25519.PublicKey
	for id, value := range trust.Keys {
		decoded, e := base64.StdEncoding.Strict().DecodeString(value)
		if e != nil || len(decoded) != ed25519.PublicKeySize || base64.StdEncoding.EncodeToString(decoded) != value || wire.Digest(decoded) != id {
			return bad
		}
		if id == sig.KeyID {
			key = ed25519.PublicKey(decoded)
		}
	}
	signed, e := base64.StdEncoding.Strict().DecodeString(sig.Value)
	if e != nil || base64.StdEncoding.EncodeToString(signed) != sig.Value || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, append([]byte("projectone-offline-release-v1\n"), raw...), signed) {
		return bad
	}
	return nil
}

// The original complete verifier must have run before wrapper publication. This
// reader then binds its exact retained output to every unchanged byte, including
// the original completion and registry receipt. It never publishes native state.
func readMVPNativeCandidate(root *os.Root, c configuration.Config, p mvpNativeBackupPlan, ref wire.NativePostgreSQLBackupReference) (engine.PostgresBackupSource, string, error) {
	var source engine.PostgresBackupSource
	bad := failure("OFFLINE_SCOPE_NATIVE_CANDIDATE_BINDING_INVALID")
	raw, e := regular(root, mvpNativeDirectory+"/candidate.json", c.Verification.MaximumMetadataBytes)
	var m struct {
		Format           string                       `json:"format"`
		Request          wire.PostgreSQLBackupRequest `json:"request"`
		RequestSHA256    string                       `json:"request_sha256"`
		CreatedAt        string                       `json:"created_at"`
		Database         json.RawMessage              `json:"database"`
		DatabaseSnapshot string                       `json:"database_snapshot"`
		Sources          []json.RawMessage            `json:"sources"`
		Stores           []struct {
			Kind string          `json:"kind"`
			Tree json.RawMessage `json:"tree"`
		} `json:"stores"`
		Archive struct {
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"archive"`
		Configuration          json.RawMessage `json:"configuration"`
		EffectiveConfiguration json.RawMessage `json:"effective_configuration"`
		MVPSource              json.RawMessage `json:"mvp_source,omitempty"`
	}
	if e != nil || wire.Digest(raw) != ref.CandidateSHA256 || wire.StrictJSON(raw, &m) != nil || m.Format != "projectone.postgres.matched-backup.v1" || !reflect.DeepEqual(m.Request, p.NativeRequest) || m.RequestSHA256 != wire.Digest(encode(p.NativeRequest)) || ref.RequestSHA256 != m.RequestSHA256 || ref.SourceManifestSHA256 != p.ReleaseManifestSHA256 || !wire.ValidSHA256(m.Archive.SHA256) || m.Archive.Bytes < 1 || len(m.Sources) != 4 || len(m.Stores) != 4 {
		return source, "", bad
	}
	for i, kind := range []string{"models", "datasets", "training", "preparations"} {
		if m.Stores[i].Kind != kind {
			return source, "", bad
		}
	}
	var db struct {
		TenantID      string `json:"tenant_id"`
		Database      string `json:"database"`
		Operator      string `json:"operator"`
		SchemaVersion int    `json:"schema_version"`
		Tables        int    `json:"tables"`
	}
	if wire.UniqueJSON(m.Database, &db) != nil || db.SchemaVersion != ref.SchemaVersion || db.Tables != ref.TableCount || db.TenantID != p.NativeRequest.TenantID || db.Database != p.NativeRequest.Database || db.Operator != p.NativeRequest.Operator {
		return source, "", bad
	}
	if nativeValueDigest(m.MVPSource) != ref.MVPSourceSHA256 {
		return source, "", bad
	}
	var binding struct {
		RegistryConfigured bool `json:"registry_configured"`
		Registry           *struct {
			Receipt json.RawMessage `json:"receipt"`
			Tree    struct {
				SHA256 string `json:"sha256"`
			} `json:"tree"`
		} `json:"registry"`
	}
	if wire.UniqueJSON(m.MVPSource, &binding) != nil || !binding.RegistryConfigured || binding.Registry == nil || ref.Registry == nil || nativeValueDigest(binding.Registry.Receipt) != ref.Registry.ReceiptSHA256 || binding.Registry.Tree.SHA256 != ref.Registry.PayloadTreeSHA256 {
		return source, "", bad
	}
	var facts struct {
		SourceMetadata *struct {
			ServerVersionNumber int    `json:"server_version_number"`
			VectorVersion       string `json:"vector_version"`
		} `json:"source_metadata"`
	}
	if wire.UniqueJSON(m.MVPSource, &facts) != nil || facts.SourceMetadata == nil {
		return source, "", failure("OFFLINE_SCOPE_NATIVE_SOURCE_ORIGINAL_DATABASE_FACTS_REQUIRED")
	}
	source = engine.PostgresBackupSource{Database: db.Database, Operator: db.Operator, TenantID: db.TenantID, SchemaVersion: db.SchemaVersion, ServerVersionNumber: facts.SourceMetadata.ServerVersionNumber, VectorVersion: facts.SourceMetadata.VectorVersion}
	if !source.Valid() {
		return source, "", failure("OFFLINE_SCOPE_NATIVE_SOURCE_ORIGINAL_DATABASE_FACTS_REQUIRED")
	}
	var receipt struct {
		ManifestSHA256 string `json:"manifest_sha256"`
		Pins           int    `json:"pins"`
		Files          int    `json:"files"`
		Bytes          int64  `json:"bytes"`
	}
	if wire.UniqueJSON(binding.Registry.Receipt, &receipt) != nil || receipt.ManifestSHA256 != ref.Registry.ManifestSHA256 || receipt.Pins != ref.Registry.Pins || receipt.Files != ref.Registry.Files || receipt.Bytes != ref.Registry.Bytes {
		return source, "", bad
	}
	prior, e := regular(root, mvpNativeDirectory+"/independent-readback.json", c.Verification.MaximumMetadataBytes)
	var readback struct {
		Format                 string `json:"format"`
		BackupID               string `json:"backup_id"`
		RequestSHA256          string `json:"request_sha256"`
		ManifestSHA256         string `json:"manifest_sha256"`
		ArchiveSHA256          string `json:"archive_sha256"`
		ArchiveDirectorySHA256 string `json:"archive_directory_sha256"`
		ObservedAt             string `json:"observed_at"`
		CurrentLockHeld        bool   `json:"current_lock_held"`
	}
	if e != nil || wire.Digest(prior) != ref.ReadbackSHA256 || wire.StrictJSON(prior, &readback) != nil || readback.Format != "projectone.postgres.independent-readback.v1" || readback.BackupID != p.AttemptID || readback.RequestSHA256 != ref.RequestSHA256 || readback.ManifestSHA256 != ref.CandidateSHA256 || readback.ArchiveSHA256 != m.Archive.SHA256 || readback.CurrentLockHeld {
		return source, "", bad
	}
	complete, e := regular(root, mvpNativeDirectory+"/complete.json", c.Verification.MaximumMetadataBytes)
	var completion struct {
		Format         string `json:"format"`
		BackupID       string `json:"backup_id"`
		ManifestSHA256 string `json:"manifest_sha256"`
		ReadbackSHA256 string `json:"readback_sha256"`
		CompletedAt    string `json:"completed_at"`
		Qualification  string `json:"qualification"`
	}
	if e != nil || wire.Digest(complete) != ref.CompletionSHA256 || wire.StrictJSON(complete, &completion) != nil || completion.Format != "projectone.postgres.backup-complete.v1" || completion.BackupID != p.AttemptID || completion.ManifestSHA256 != ref.CandidateSHA256 || completion.ReadbackSHA256 != ref.ReadbackSHA256 {
		return source, "", bad
	}
	if _, e = time.Parse(time.RFC3339Nano, m.CreatedAt); e != nil {
		return source, "", bad
	}
	if _, e = time.Parse(time.RFC3339Nano, readback.ObservedAt); e != nil {
		return source, "", bad
	}
	if _, e = time.Parse(time.RFC3339Nano, completion.CompletedAt); e != nil {
		return source, "", bad
	}
	if !wire.ValidSHA256(readback.ArchiveDirectorySHA256) || completion.Qualification == "" {
		return source, "", bad
	}
	return source, m.Archive.SHA256, nil
}
func nativeValueDigest(raw json.RawMessage) string {
	var compact bytes.Buffer
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Compact(&compact, raw) != nil {
		return ""
	}
	return wire.Digest(append(compact.Bytes(), '\n'))
}

// Reconstruct only the original portable tree digest from the already reread
// actual entries. This binds every store/configuration/registry descriptor to
// bytes; it is not another SQL/archive/signature verifier.
type mvpNativeTreeProof struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Files  int    `json:"files"`
}

func mvpNativeTreeFromInventory(entries []mvpNativeInventoryEntry, prefix string) (mvpNativeTreeProof, error) {
	var result mvpNativeTreeProof
	h := sha256.New()
	encoder := json.NewEncoder(h)
	found := false
	for _, e := range entries {
		if e.Name != prefix && !strings.HasPrefix(e.Name, prefix+"/") {
			continue
		}
		relative := strings.TrimPrefix(e.Name, prefix+"/")
		if e.Name == prefix {
			relative = "."
			found = e.Kind == "directory"
		}
		if e.Kind == "directory" {
			if encoder.Encode(struct {
				Path, Kind string
				Mode       uint32
			}{relative, "directory", e.Mode}) != nil {
				return result, failure("OFFLINE_SCOPE_NATIVE_SOURCE_TREE_INVALID")
			}
		} else if e.Kind == "file" {
			if encoder.Encode(struct {
				Path, Kind, SHA256 string
				Mode               uint32
				Bytes              int64
			}{relative, "file", e.SHA256, e.Mode, e.Bytes}) != nil {
				return result, failure("OFFLINE_SCOPE_NATIVE_SOURCE_TREE_INVALID")
			}
			result.Bytes += e.Bytes
			result.Files++
		} else {
			return result, failure("OFFLINE_SCOPE_NATIVE_SOURCE_TREE_INVALID")
		}
	}
	if !found {
		return result, failure("OFFLINE_SCOPE_NATIVE_SOURCE_TREE_MISSING")
	}
	result.SHA256 = hex.EncodeToString(h.Sum(nil))
	return result, nil
}
func verifyMVPNativePayloadInventory(root *os.Root, c configuration.Config, entries []mvpNativeInventoryEntry) error {
	bad := failure("OFFLINE_SCOPE_NATIVE_SOURCE_PAYLOAD_BINDING_INVALID")
	raw, e := regular(root, mvpNativeDirectory+"/candidate.json", c.Verification.MaximumMetadataBytes)
	if e != nil {
		return e
	}
	var m struct {
		Stores []struct {
			Kind string             `json:"kind"`
			Tree mvpNativeTreeProof `json:"tree"`
		} `json:"stores"`
		Configuration mvpNativeTreeProof `json:"configuration"`
		Archive       struct {
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"archive"`
		EffectiveConfiguration struct {
			SHA256 string `json:"sha256"`
			Bytes  int64  `json:"bytes"`
		} `json:"effective_configuration"`
		MVPSource struct {
			Registry *struct {
				Tree mvpNativeTreeProof `json:"tree"`
			} `json:"registry"`
		} `json:"mvp_source"`
	}
	if wire.UniqueJSON(raw, &m) != nil || len(m.Stores) != 4 || m.MVPSource.Registry == nil {
		return bad
	}
	for i, role := range []string{"models", "datasets", "training", "preparations"} {
		if m.Stores[i].Kind != role {
			return bad
		}
		proof, e := mvpNativeTreeFromInventory(entries, mvpNativeDirectory+"/stores/"+role)
		if e != nil || proof != m.Stores[i].Tree {
			return bad
		}
	}
	for prefix, want := range map[string]mvpNativeTreeProof{mvpNativeDirectory + "/configuration": m.Configuration, mvpNativeDirectory + "/artifacts/imageregistry": m.MVPSource.Registry.Tree} {
		proof, e := mvpNativeTreeFromInventory(entries, prefix)
		if e != nil || proof != want {
			return bad
		}
	}
	for name, want := range map[string]struct {
		SHA256 string `json:"sha256"`
		Bytes  int64  `json:"bytes"`
	}{mvpNativeDirectory + "/postgres.dump": m.Archive, mvpNativeDirectory + "/effective-control.yaml": m.EffectiveConfiguration} {
		found := false
		for _, e := range entries {
			if e.Name == name && e.Kind == "file" && e.SHA256 == want.SHA256 && e.Bytes == want.Bytes {
				found = true
				break
			}
		}
		if !found {
			return bad
		}
	}
	return nil
}
