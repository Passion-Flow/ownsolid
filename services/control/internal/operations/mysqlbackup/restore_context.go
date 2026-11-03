package mysqlbackup

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const RestoreCommand = "restore-mysql"
const ReconcileRestoreCommand = "reconcile-mysql-restore"
const restoreAuditAction = "operator.mysql_restore_verified"

type RootIdentity struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	UID    uint32 `json:"uid"`
	GID    uint32 `json:"gid"`
}
type RestoreIntent struct {
	Format              string           `json:"format"`
	RestoreID           string           `json:"restore_id"`
	BackupID            string           `json:"backup_id"`
	CreatedAt           time.Time        `json:"created_at"`
	Reason              string           `json:"reason"`
	BackupRoot          string           `json:"backup_root"`
	ManifestSHA256      string           `json:"manifest_sha256"`
	SQLSHA256           string           `json:"sql_sha256"`
	SourceDataSHA256    string           `json:"source_data_sha256"`
	TenantID            string           `json:"tenant_id"`
	Target              ConnectionTarget `json:"target"`
	TargetBindingSHA256 string           `json:"target_binding_sha256"`
	SchemaSteps         int              `json:"schema_steps"`
	Roots               []RootIdentity   `json:"roots"`
}
type RestoredFiles struct {
	Format                       string          `json:"format"`
	RestoreID                    string          `json:"restore_id"`
	IntentSHA256                 string          `json:"intent_sha256"`
	NativeArtifactManifestSHA256 string          `json:"native_artifact_manifest_sha256"`
	ClientSHA256                 string          `json:"client_sha256"`
	ImportExecution              NativeExecution `json:"import_execution"`
	Trees                        []TreeReadback  `json:"trees"`
}

// RestoredContext is a MySQL-only proof. It is not a PostgreSQL packet or release
// attestation, nor evidence that physical restored workloads have been stopped.
type RestoredContext struct {
	Format              string           `json:"format"`
	RestoreID           string           `json:"restore_id"`
	BackupID            string           `json:"backup_id"`
	IntentSHA256        string           `json:"intent_sha256"`
	FilesSHA256         string           `json:"files_sha256"`
	PostDataSHA256      string           `json:"post_data_sha256"`
	AuditEventID        string           `json:"audit_event_id"`
	Target              ConnectionTarget `json:"target"`
	TargetBindingSHA256 string           `json:"target_binding_sha256"`
	TenantID            string           `json:"tenant_id"`
	SchemaSteps         int              `json:"schema_steps"`
}

func canonicalPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != string(filepath.Separator)
}
func (i RestoreIntent) validate(c config.Config, m BackupManifest, manifestSHA string) error {
	bad := errors.New("MYSQL_RESTORE_INTENT_INVALID")
	if i.Format != "projectone.mysql.restore-intent.v1" || !canonicalUUID(i.RestoreID, 4) || i.BackupID != m.BackupID || i.CreatedAt.IsZero() || !modelpreparations.ValidOperatorReason(i.Reason) || !canonicalPath(i.BackupRoot) || i.ManifestSHA256 != manifestSHA || i.SQLSHA256 != m.SQLSHA256 || i.SourceDataSHA256 != m.Data.SHA256 || i.TenantID != m.TenantID || i.SchemaSteps != m.SchemaSteps || !i.Target.valid() || !digestPattern.MatchString(i.TargetBindingSHA256) || len(i.Roots) != 4 {
		return bad
	}
	if i.Target.ServerUUID == m.Source.ServerUUID && i.Target.Database == m.Source.Database || i.Target.Principal != m.Source.Principal || i.Target.Version != m.Source.Version {
		return errors.New("MYSQL_RESTORE_ISOLATED_TARGET_REQUIRED")
	}
	for n, path := range storeRoots(c) {
		r := i.Roots[n]
		if r.Path != path || !canonicalPath(r.Path) || r.Device == 0 || r.Inode == 0 {
			return bad
		}
	}
	return nil
}
func (f RestoredFiles) validate(i RestoreIntent, intentSHA string, p config.MySQLBackupConfiguration) error {
	bad := errors.New("MYSQL_RESTORE_FILES_PROOF_INVALID")
	if !digestPattern.MatchString(f.NativeArtifactManifestSHA256) || f.ClientSHA256 != p.ClientSHA256 || f.Format != "projectone.mysql.restored-files.v1" || f.RestoreID != i.RestoreID || f.IntentSHA256 != intentSHA || len(f.Trees) != 4 {
		return bad
	}
	x := f.ImportExecution
	if !x.Joined || x.ExitCode != 0 || x.PID < 1 || x.StartedAt.IsZero() || x.CompletedAt.Before(x.StartedAt) || x.File != p.ClientFile || !canonicalPath(x.File) || x.GOOS != "linux" && x.GOOS != "darwin" || x.GOARCH != "arm64" && x.GOARCH != "amd64" {
		return bad
	}
	if !importArgumentsValid(x.Argv, i.Target.Database) {
		return bad
	}
	var bytes int64
	files := 0
	for _, t := range f.Trees {
		if !digestPattern.MatchString(t.SHA256) || t.Bytes < 0 || t.Bytes > p.MaximumStoreBytes-bytes || t.Files < 0 || t.Files > p.MaximumStoreFiles-files {
			return bad
		}
		bytes += t.Bytes
		files += t.Files
	}
	return nil
}
func contextFrom(i RestoreIntent, intentSHA, filesSHA, postSHA string) RestoredContext {
	return RestoredContext{"projectone.mysql.restored-context.v1", i.RestoreID, i.BackupID, intentSHA, filesSHA, postSHA, i.RestoreID, i.Target, i.TargetBindingSHA256, i.TenantID, i.SchemaSteps}
}
func (c RestoredContext) valid() bool {
	return c.Format == "projectone.mysql.restored-context.v1" && canonicalUUID(c.RestoreID, 4) && canonicalUUID(c.BackupID, 4) && c.AuditEventID == c.RestoreID && c.Target.valid() && canonicalUUID(c.TenantID, 0) && c.SchemaSteps > 0 && digestPattern.MatchString(c.IntentSHA256) && digestPattern.MatchString(c.FilesSHA256) && digestPattern.MatchString(c.PostDataSHA256) && digestPattern.MatchString(c.TargetBindingSHA256)
}
func readRestoreRecords(ctx context.Context, c config.Config, path string) (RestoreIntent, RestoredFiles, BackupManifest, string, string, error) {
	var i RestoreIntent
	var f RestoredFiles
	bad := func(e error) (RestoreIntent, RestoredFiles, BackupManifest, string, string, error) {
		return RestoreIntent{}, RestoredFiles{}, BackupManifest{}, "", "", e
	}
	if c.Database.MySQLBackup == nil || c.Database.MySQLBackup.Validate() != nil || !canonicalPath(path) {
		return bad(errors.New("MYSQL_BACKUP_CONFIGURATION_REQUIRED"))
	}
	p := *c.Database.MySQLBackup
	raw, e := readPrivateDocument(path+".intent", p.MaximumManifestBytes, &i)
	if e != nil {
		return bad(e)
	}
	m, manifestSHA, e := loadManifest(ctx, i.BackupRoot, p, true)
	if e != nil {
		return bad(e)
	}
	if e = i.validate(c, m, manifestSHA); e != nil {
		return bad(e)
	}
	intentSHA := mysqloperator.SHA256(raw)
	raw, e = readPrivateDocument(path+".files", p.MaximumManifestBytes, &f)
	if e != nil {
		return bad(e)
	}
	if e = f.validate(i, intentSHA, p); e != nil {
		return bad(e)
	}
	return i, f, m, intentSHA, mysqloperator.SHA256(raw), nil
}
func writeRestoreDocument(path string, value any, maximum int64) error {
	root, e := openPrivateRoot(filepath.Dir(path))
	if e != nil {
		return e
	}
	e = writeDocument(root, filepath.Base(path), value, maximum)
	return errors.Join(e, root.Close())
}
func documentSHA(value any) (string, error) {
	raw, e := json.Marshal(value)
	if e != nil {
		return "", errors.New("MYSQL_RESTORE_PROOF_INVALID")
	}
	return mysqloperator.SHA256(append(raw, '\n')), nil
}
