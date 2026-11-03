package mysqlbackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

const BackupCommand = "backup-mysql"
const ReconcileBackupCommand = "reconcile-mysql-backup"

type fileOutput struct {
	ctx                     context.Context
	file                    *os.File
	maximum, minimum, count int64
	hash                    hash.Hash
}

func (w *fileOutput) Write(value []byte) (int, error) {
	if e := w.ctx.Err(); e != nil {
		return 0, e
	}
	if int64(len(value)) > w.maximum-w.count {
		return 0, errors.New("MYSQL_BACKUP_STREAM_LIMIT_REACHED")
	}
	if e := requireSpaceFile(w.file, int64(len(value)), w.minimum); e != nil {
		return 0, e
	}
	n, e := w.file.Write(value)
	if n > 0 {
		w.count += int64(n)
		_, _ = w.hash.Write(value[:n])
	}
	if e != nil || n != len(value) {
		return n, errors.New("MYSQL_BACKUP_STREAM_WRITE_FAILED")
	}
	return n, nil
}
func newDirectory(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("MYSQL_BACKUP_PRIVATE_PATH_INVALID")
	}
	parent, e := openPrivateRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer parent.Close()
	if parent.Mkdir(filepath.Base(path), 0700) != nil {
		return nil, errors.New("MYSQL_BACKUP_DESTINATION_EXISTS")
	}
	if e = syncRoot(parent); e != nil {
		return nil, e
	}
	return openPrivateRoot(path)
}
func sourceTrees(ctx context.Context, c config.Config) ([]TreeReadback, int64, int, error) {
	if c.Database.MySQLBackup == nil {
		return nil, 0, 0, errors.New("MYSQL_BACKUP_CONFIGURATION_REQUIRED")
	}
	p := *c.Database.MySQLBackup
	proofs := []TreeReadback{}
	var bytes int64
	files := 0
	for _, path := range storeRoots(c) {
		proof, e := TreeDigest(ctx, path, p.MaximumStoreBytes-bytes, p.MaximumStoreFiles-files, p.MaximumManifestBytes)
		if e != nil {
			return nil, 0, 0, e
		}
		proofs = append(proofs, proof)
		bytes += proof.Bytes
		files += proof.Files
	}
	return proofs, bytes, files, nil
}
func snapshotTrees(ctx context.Context, root string, p config.MySQLBackupConfiguration) ([]StoreReadback, error) {
	proofs := []StoreReadback{}
	var bytes int64
	files := 0
	for _, kind := range []string{"models", "datasets", "training", "preparations"} {
		directory := snapshotDirectory + "/" + kind
		proof, e := TreeDigest(ctx, filepath.Join(root, filepath.FromSlash(directory)), p.MaximumStoreBytes-bytes, p.MaximumStoreFiles-files, p.MaximumManifestBytes)
		if e != nil {
			return nil, e
		}
		proofs = append(proofs, StoreReadback{kind, directory, proof})
		bytes += proof.Bytes
		files += proof.Files
	}
	return proofs, nil
}
func nativeSettings(c config.Config) (mysqlstore.Configuration, error) {
	raw, e := mysqloperator.ReadPrivateBytes(c.Database.MySQLConfigurationFile, mysqlstore.MaximumConfigurationBytes)
	if e != nil {
		return mysqlstore.Configuration{}, e
	}
	return mysqlstore.Decode(raw, nil)
}
func dumpArchive(ctx context.Context, native *Native, o *mysqloperator.Context, c config.Config, root *os.Root) (NativeExecution, string, int64, error) {
	archive, e := root.OpenFile(archiveName, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return NativeExecution{}, "", 0, errors.New("MYSQL_BACKUP_EXCLUSIVE_CREATE_FAILED")
	}
	diagnostic, e := root.OpenFile("native-dump.stderr", os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		archive.Close()
		return NativeExecution{}, "", 0, errors.New("MYSQL_BACKUP_EXCLUSIVE_CREATE_FAILED")
	}
	p := native.policy
	output := &fileOutput{ctx: ctx, file: archive, maximum: p.MaximumSQLBytes, minimum: p.MinimumFreeBytes, hash: sha256.New()}
	errorsOutput := &fileOutput{ctx: ctx, file: diagnostic, maximum: p.MaximumManifestBytes, minimum: p.MinimumFreeBytes, hash: sha256.New()}
	execution, runError := native.DumpMeasured(ctx, o, c, output, errorsOutput)
	// Always retain/sync the exact partial output after the real process joins.
	for _, file := range []*os.File{archive, diagnostic} {
		if file.Sync() != nil {
			runError = errors.Join(runError, errors.New("MYSQL_BACKUP_WRITE_UNCERTAIN"))
		}
		if file.Close() != nil {
			runError = errors.Join(runError, errors.New("MYSQL_BACKUP_WRITE_UNCERTAIN"))
		}
	}
	if e = syncRoot(root); e != nil {
		runError = errors.Join(runError, e)
	}
	if runError != nil {
		return NativeExecution{}, "", 0, runError
	}
	if output.count < 1 {
		return NativeExecution{}, "", 0, errors.New("MYSQL_BACKUP_EMPTY_ARCHIVE_REFUSED")
	}
	return execution, hex.EncodeToString(output.hash.Sum(nil)), output.count, nil
}
func publishCompletionWithPolicy(ctx context.Context, o *mysqloperator.Context, rootPath string, m BackupManifest, sha string, policy config.MySQLBackupConfiguration) error {
	matched, e := backupAudit(ctx, o, m, sha, false)
	if e != nil {
		return e
	}
	if !matched {
		return errors.New("MYSQL_BACKUP_COMPLETION_AUDIT_MISSING")
	}
	completion := BackupCompletion{"projectone.mysql.backup-completion.v1", m.BackupID, sha, m.BackupID}
	root, e := openPrivateRoot(rootPath)
	if e != nil {
		return e
	}
	defer root.Close()
	if _, e = root.Lstat(completeName); os.IsNotExist(e) {
		if e = writeDocument(root, completeName, completion, policy.MaximumManifestBytes); e != nil {
			return e
		}
	} else if e != nil {
		return errors.New("MYSQL_BACKUP_COMPLETION_REQUIRED")
	}
	return nil
}

// Backup is an explicit stopped-runtime local filesystem variant. It exports
// real files and native SQL before writing a success audit/completion. No source
// database is created, migrated, restarted, copied as a live directory or deleted.
func Backup(ctx context.Context, o *mysqloperator.Context, c config.Config, accounts *identity.Service, rootPath, reason string, schemaSteps int) (manifest BackupManifest, result error) {
	if ctx == nil || c.Database.MySQLBackup == nil || c.Database.MySQLBackup.Validate() != nil || !modelpreparations.ValidOperatorReason(reason) || schemaSteps < 1 {
		return BackupManifest{}, errors.New("MYSQL_BACKUP_CONFIGURATION_OR_REASON_REQUIRED")
	}
	p := *c.Database.MySQLBackup
	bounded, cancel := context.WithTimeout(ctx, time.Duration(p.OperationSeconds)*time.Second)
	defer cancel()
	if e := validateStoreRoots(c, rootPath); e != nil {
		return BackupManifest{}, e
	}
	if _, e := os.Lstat(rootPath); !os.IsNotExist(e) {
		return BackupManifest{}, errors.New("MYSQL_BACKUP_DESTINATION_EXISTS")
	}
	if e := o.VerifyConfiguration(bounded, c); e != nil {
		return BackupManifest{}, e
	}
	if e := o.VerifyInstallation(bounded); e != nil {
		return BackupManifest{}, e
	}
	settings, e := nativeSettings(c)
	if e != nil {
		return BackupManifest{}, e
	}
	native, e := NewNative(bounded, p, settings)
	if e != nil {
		return BackupManifest{}, e
	}
	owners, e := openOwners(bounded, o, c, accounts)
	if e != nil {
		return BackupManifest{}, e
	}
	defer func() {
		if e := owners.Close(); e != nil {
			manifest = BackupManifest{}
			result = errors.Join(result, e)
		}
	}()
	beforeFiles, bytes, files, e := sourceTrees(bounded, c)
	if e != nil {
		return BackupManifest{}, e
	}
	metadataReserve := int64(models.MaximumSnapshotMetadataBytes) + c.Policy().Datasets.SnapshotManifestBytes + c.Policy().Training.SnapshotManifestBytes + c.Policy().ModelLifecycle.SnapshotManifestBytes
	if bytes > p.MaximumStoreBytes-metadataReserve || files > p.MaximumStoreFiles-4 {
		return BackupManifest{}, errors.New("MYSQL_BACKUP_STORE_LIMIT_REACHED")
	}
	additional := bytes + metadataReserve + p.MaximumSQLBytes + p.MaximumManifestBytes*3
	if e = requireSpace(filepath.Dir(rootPath), additional, p.MinimumFreeBytes); e != nil {
		return BackupManifest{}, e
	}
	before, e := ReadDatabase(bounded, o, p, false)
	if e != nil {
		return BackupManifest{}, e
	}
	root, e := newDirectory(rootPath)
	if e != nil {
		return BackupManifest{}, e
	}
	defer func() {
		if root.Close() != nil {
			manifest = BackupManifest{}
			result = errors.Join(result, errors.New("MYSQL_BACKUP_ROOT_CLOSE_FAILED"))
		}
	}()
	if root.Mkdir(snapshotDirectory, 0700) != nil {
		return BackupManifest{}, errors.New("MYSQL_BACKUP_EXCLUSIVE_CREATE_FAILED")
	}
	if e = syncRoot(root); e != nil {
		return BackupManifest{}, e
	}
	if e = owners.Export(bounded, filepath.Join(rootPath, snapshotDirectory)); e != nil {
		return BackupManifest{}, e
	}
	execution, sqlSHA, sqlBytes, e := dumpArchive(bounded, native, o, c, root)
	if e != nil {
		return BackupManifest{}, e
	}
	after, e := ReadDatabase(bounded, o, p, false)
	if e != nil {
		return BackupManifest{}, e
	}
	if !reflect.DeepEqual(before, after) {
		return BackupManifest{}, errors.New("MYSQL_BACKUP_DATABASE_CHANGED")
	}
	afterFiles, _, _, e := sourceTrees(bounded, c)
	if e != nil {
		return BackupManifest{}, e
	}
	if !reflect.DeepEqual(beforeFiles, afterFiles) {
		return BackupManifest{}, errors.New("MYSQL_BACKUP_SOURCE_FILES_CHANGED")
	}
	stores, e := snapshotTrees(bounded, rootPath, p)
	if e != nil {
		return BackupManifest{}, e
	}
	target, e := o.Target(bounded)
	if e != nil {
		return BackupManifest{}, e
	}
	binding, e := o.Binding(bounded)
	if e != nil {
		return BackupManifest{}, e
	}
	manifest = BackupManifest{Format: "projectone.mysql.logical-backup.v1", BackupID: uuid.NewString(), CreatedAt: time.Now().UTC(), Reason: reason, Source: target, SourceBindingSHA256: binding, TenantID: owners.tenant, SchemaSteps: schemaSteps, NativeArtifactManifestSHA256: native.manifestSHA256, DumpSHA256: p.DumpSHA256, DumpExecution: execution, SQLFile: archiveName, SQLSHA256: sqlSHA, SQLBytes: sqlBytes, Data: before, Stores: stores}
	if e = manifest.validate(p); e != nil {
		return BackupManifest{}, e
	}
	if e = writeDocument(root, manifestName, manifest, p.MaximumManifestBytes); e != nil {
		return BackupManifest{}, e
	}
	actual, sha, e := loadManifest(bounded, rootPath, p, false)
	if e != nil {
		return BackupManifest{}, e
	}
	if !reflect.DeepEqual(actual, manifest) {
		return BackupManifest{}, errors.New("MYSQL_BACKUP_MANIFEST_CHANGED")
	}
	matched, e := backupAudit(bounded, o, manifest, sha, true)
	if e != nil {
		return BackupManifest{}, e
	}
	if !matched {
		return BackupManifest{}, errors.New("MYSQL_BACKUP_COMPLETION_AUDIT_MISSING")
	}
	if e = publishCompletionWithPolicy(bounded, o, rootPath, manifest, sha, p); e != nil {
		return BackupManifest{}, e
	}
	verified, _, e := loadManifest(bounded, rootPath, p, true)
	if e != nil {
		return BackupManifest{}, e
	}
	return verified, nil
}

func ReconcileBackup(ctx context.Context, o *mysqloperator.Context, c config.Config, rootPath string) (BackupManifest, error) {
	if ctx == nil || c.Database.MySQLBackup == nil || c.Database.MySQLBackup.Validate() != nil {
		return BackupManifest{}, errors.New("MYSQL_BACKUP_CONFIGURATION_REQUIRED")
	}
	p := *c.Database.MySQLBackup
	bounded, cancel := context.WithTimeout(ctx, time.Duration(p.OperationSeconds)*time.Second)
	defer cancel()
	if e := o.VerifyConfiguration(bounded, c); e != nil {
		return BackupManifest{}, e
	}
	if e := o.VerifyInstallation(bounded); e != nil {
		return BackupManifest{}, e
	}
	m, sha, e := loadManifest(bounded, rootPath, p, false)
	if e != nil {
		return BackupManifest{}, e
	}
	target, e := o.Target(bounded)
	if e != nil || target != m.Source {
		return BackupManifest{}, errors.New("MYSQL_BACKUP_SOURCE_TARGET_MISMATCH")
	}
	binding, e := o.Binding(bounded)
	if e != nil || binding != m.SourceBindingSHA256 {
		return BackupManifest{}, errors.New("MYSQL_BACKUP_SOURCE_INPUT_MISMATCH")
	}
	if e = publishCompletionWithPolicy(bounded, o, rootPath, m, sha, p); e != nil {
		return BackupManifest{}, e
	}
	verified, _, e := loadManifest(bounded, rootPath, p, true)
	if e != nil {
		return BackupManifest{}, e
	}
	return verified, nil
}
