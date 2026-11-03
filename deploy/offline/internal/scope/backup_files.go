package scope

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const backupPlanFile = "plan.json"
const backupManifestFile = "backup.json"

type BackupManifest struct {
	FormatVersion         int                         `json:"format_version"`
	Status                string                      `json:"status"`
	ScopeID               string                      `json:"scope_id"`
	AttemptID             string                      `json:"attempt_id"`
	PlanSHA256            string                      `json:"plan_sha256"`
	ReleaseID             string                      `json:"release_id"`
	Profile               string                      `json:"profile"`
	ConfigurationSHA256   string                      `json:"configuration_sha256"`
	ReleaseManifestSHA256 string                      `json:"release_manifest_sha256"`
	Source                engine.PostgresBackupSource `json:"source"`
	Stores                []string                    `json:"stores"`
	Files                 []engine.BackupArchiveFile  `json:"files"`
	Qualification         string                      `json:"qualification"`
}

func syncBackupDirectory(root *os.Root, name string) error {
	f, e := root.Open(name)
	if e != nil {
		return failure("OFFLINE_SCOPE_BACKUP_DIRECTORY_SYNC_FAILED")
	}
	defer f.Close()
	if f.Sync() != nil {
		return failure("OFFLINE_SCOPE_BACKUP_DIRECTORY_SYNC_FAILED")
	}
	return nil
}
func writeBackupFile(root *os.Root, name string, raw []byte) error {
	if e := writeNew(root, name, raw); e != nil {
		return e
	}
	return syncBackupDirectory(root, path.Dir(name))
}

func backupHashFile(ctx context.Context, root *os.Root, name string, maximum int64) (engine.BackupArchiveFile, error) {
	return backupHashFileLinks(ctx, root, name, maximum, 1)
}

// The two-link case is reserved for an independently observed exact pending
// publication pair. Ordinary backup/inspection paths always require one link.
func backupHashFileLinks(ctx context.Context, root *os.Root, name string, maximum int64, links uint64) (engine.BackupArchiveFile, error) {
	r := engine.BackupArchiveFile{Name: name}
	if !wire.SafePath(name) || maximum < 1 {
		return r, failure("OFFLINE_SCOPE_BACKUP_FILE_CONTEXT_INVALID")
	}
	before, e := root.Lstat(name)
	if e != nil || !owned(before) || before.Mode() != 0600 || before.Size() < 1 || before.Size() > maximum {
		return r, failure("OFFLINE_SCOPE_BACKUP_PRIVATE_FILE_REQUIRED")
	}
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok || uint64(st.Nlink) != links {
		return r, failure("OFFLINE_SCOPE_BACKUP_FILE_LINK_REFUSED")
	}
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return r, failure("OFFLINE_SCOPE_BACKUP_FILE_UNAVAILABLE")
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return r, failure("OFFLINE_SCOPE_BACKUP_FILE_CHANGED")
	}
	hash := sha256.New()
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		if ctx.Err() != nil {
			return r, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		n, e := f.Read(buffer)
		if n > 0 {
			total += int64(n)
			if total > maximum {
				return r, failure("OFFLINE_SCOPE_BACKUP_BYTE_LIMIT")
			}
			hash.Write(buffer[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return r, failure("OFFLINE_SCOPE_BACKUP_FILE_READ_FAILED")
		}
	}
	after, e := f.Stat()
	named, ne := root.Lstat(name)
	if e != nil || ne != nil || !os.SameFile(before, after) || !os.SameFile(before, named) || before.Mode() != after.Mode() || before.Size() != total || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return r, failure("OFFLINE_SCOPE_BACKUP_FILE_CHANGED")
	}
	if a, ok := after.Sys().(*syscall.Stat_t); !ok || uint64(a.Nlink) != links {
		return r, failure("OFFLINE_SCOPE_BACKUP_FILE_LINK_REFUSED")
	}
	if a, ok := named.Sys().(*syscall.Stat_t); !ok || uint64(a.Nlink) != links {
		return r, failure("OFFLINE_SCOPE_BACKUP_FILE_LINK_REFUSED")
	}
	r.Bytes = total
	r.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return r, nil
}

func reserveBackupFiles(p BackupPlan, files map[string][]engine.ConfigurationFile) error {
	if !privateDirectory(filepath.Dir(p.Request.DestinationRoot)) {
		return failure("OFFLINE_SCOPE_BACKUP_PRIVATE_PARENT_REQUIRED")
	}
	if os.Mkdir(p.Request.DestinationRoot, 0700) != nil {
		return failure("OFFLINE_SCOPE_BACKUP_DESTINATION_OCCUPIED")
	}
	parent, e := os.Open(filepath.Dir(p.Request.DestinationRoot))
	if e != nil {
		return e
	}
	e = parent.Sync()
	parent.Close()
	if e != nil {
		return failure("OFFLINE_SCOPE_BACKUP_DIRECTORY_SYNC_FAILED")
	}
	root, e := openPrivateDirectory(p.Request.DestinationRoot)
	if e != nil {
		return e
	}
	defer root.Close()
	if e = writeBackupFile(root, backupPlanFile, encode(p)); e != nil {
		return e
	}
	if e = root.Mkdir("configuration", 0700); e != nil {
		return e
	}
	if e = syncBackupDirectory(root, "."); e != nil {
		return e
	}
	roles := []string{}
	for role := range files {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		if len(files[role]) == 0 {
			continue
		}
		directory := "configuration/" + role
		if e = root.Mkdir(directory, 0700); e != nil {
			return e
		}
		if e = syncBackupDirectory(root, "configuration"); e != nil {
			return e
		}
		for _, file := range files[role] {
			if e = writeBackupFile(root, directory+"/"+file.Name, file.Contents); e != nil {
				return e
			}
		}
	}
	return nil
}

func verifyBackupReservation(ctx context.Context, root *os.Root, p BackupPlan) error {
	raw, e := regular(root, backupPlanFile, 1<<20)
	if e != nil || wire.Digest(raw) != wire.Digest(encode(p)) {
		return failure("OFFLINE_SCOPE_BACKUP_PLAN_CHANGED")
	}
	for _, entry := range p.ConfigurationFiles {
		f, e := backupHashFile(ctx, root, "configuration/"+entry.Role+"/"+entry.File.Name, entry.File.Bytes)
		if e != nil || f.Bytes != entry.File.Bytes || f.SHA256 != entry.File.SHA256 {
			return failure("OFFLINE_SCOPE_BACKUP_CONFIGURATION_CHANGED")
		}
	}
	return nil
}

func backupExpectedFiles(p BackupPlan) []string {
	names := []string{backupPlanFile}
	for _, f := range p.ConfigurationFiles {
		names = append(names, "configuration/"+f.Role+"/"+f.File.Name)
	}
	for role := range p.Jobs {
		names = append(names, role+".tar", role+".json")
	}
	sort.Strings(names)
	return names
}
func (p BackupPlan) buildManifest(ctx context.Context, root *os.Root) (BackupManifest, error) {
	m := BackupManifest{FormatVersion: 1, Status: "complete_selected_profile_backup_restore_pending", ScopeID: p.ScopeID, AttemptID: p.AttemptID, PlanSHA256: wire.Digest(encode(p)), ReleaseID: p.ReleaseID, Profile: p.Profile, ConfigurationSHA256: p.ConfigurationSHA256, ReleaseManifestSHA256: p.ManifestSHA256, Stores: []string{}, Files: []engine.BackupArchiveFile{}, Qualification: "selected_declared_profile_logical_database_files_and_configuration_only_fresh_restore_upgrade_full_package_and_native_target_pending"}
	if e := verifyBackupReservation(ctx, root, p); e != nil {
		return m, e
	}
	var total int64
	for _, name := range backupExpectedFiles(p) {
		f, e := backupHashFile(ctx, root, name, p.Request.MaximumBytes-total)
		if e != nil {
			return m, e
		}
		total += f.Bytes
		m.Files = append(m.Files, f)
	}
	var pg engine.PostgresBackupArchive
	raw, e := regular(root, "postgres.json", 1<<20)
	if e != nil || wire.StrictJSON(raw, &pg) != nil || pg.Status != "verified_exited_postgres_custom_archive_bytes" {
		return m, failure("OFFLINE_SCOPE_BACKUP_DATABASE_METADATA_REQUIRED")
	}
	m.Source = pg.Source
	for role, job := range p.Jobs {
		if job.Store == nil {
			continue
		}
		var a engine.StoreBackupArchive
		raw, e := regular(root, role+".json", 1<<20)
		if e != nil || wire.StrictJSON(raw, &a) != nil || a.Role != role || a.Status != "verified_exited_store_snapshot_archive_bytes" {
			return m, failure("OFFLINE_SCOPE_BACKUP_STORE_METADATA_REQUIRED")
		}
		m.Stores = append(m.Stores, role)
		bound := false
		for _, f := range m.Files {
			if f.Name == role+".tar" && f.Bytes == a.Bytes && f.SHA256 == a.SHA256 {
				bound = true
			}
		}
		if !bound {
			return m, failure("OFFLINE_SCOPE_BACKUP_STORE_ARCHIVE_CHANGED")
		}
	}
	bound := false
	for _, f := range m.Files {
		if f.Name == "postgres.tar" && f.Bytes == pg.Bytes && f.SHA256 == pg.SHA256 {
			bound = true
		}
	}
	if !bound {
		return m, failure("OFFLINE_SCOPE_BACKUP_DATABASE_ARCHIVE_CHANGED")
	}
	sort.Strings(m.Stores)
	if _, e := remainingBackupEntries(ctx, root, p); e != nil {
		return m, e
	}
	return m, nil
}

func inspectBackupFiles(ctx context.Context, root *os.Root, p BackupPlan) (BackupManifest, engine.BackupArchiveFile, error) {
	expected, e := p.buildManifest(ctx, root)
	if e != nil {
		return expected, engine.BackupArchiveFile{}, e
	}
	raw, e := regular(root, backupManifestFile, 1<<20)
	if e != nil || wire.Digest(raw) != wire.Digest(encode(expected)) {
		return expected, engine.BackupArchiveFile{}, failure("OFFLINE_SCOPE_BACKUP_MANIFEST_CHANGED")
	}
	allowed := map[string]bool{backupManifestFile: true}
	directories := map[string]bool{".": true}
	for _, n := range backupExpectedFiles(p) {
		allowed[n] = true
		for parent := path.Dir(n); parent != "."; parent = path.Dir(parent) {
			directories[parent] = true
		}
	}
	count := 0
	e = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		count++
		if count > p.Request.MaximumEntries {
			return failure("OFFLINE_SCOPE_BACKUP_ENTRY_LIMIT")
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if entry.IsDir() {
			if !directories[name] || info.Mode() != os.ModeDir|0700 || !owned(info) {
				return failure("OFFLINE_SCOPE_BACKUP_PRIVATE_DIRECTORY_REQUIRED")
			}
			return nil
		}
		if !allowed[name] || !info.Mode().IsRegular() {
			return failure("OFFLINE_SCOPE_BACKUP_UNDECLARED_ENTRY")
		}
		return nil
	})
	if e != nil {
		return expected, engine.BackupArchiveFile{}, e
	}
	f, e := backupHashFile(ctx, root, backupManifestFile, 1<<20)
	return expected, f, e
}
