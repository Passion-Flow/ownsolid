package mysqlbackup

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqloperator"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func restorePaths(c config.Config, backupRoot, contextPath string) error {
	if !canonicalPath(contextPath) || !canonicalPath(backupRoot) {
		return errors.New("MYSQL_RESTORE_PRIVATE_CONTEXT_REQUIRED")
	}
	if e := validateStoreRoots(c, backupRoot); e != nil {
		return e
	}
	for _, path := range append(storeRoots(c), backupRoot) {
		for _, record := range []string{contextPath, contextPath + ".intent", contextPath + ".files", contextPath + ".native.stderr"} {
			if relatedPaths(path, record) {
				return errors.New("MYSQL_BACKUP_RELATED_PATHS_REFUSED")
			}
		}
	}
	return nil
}
func restoreRecordsAbsent(path string) error {
	for _, record := range []string{path, path + ".intent", path + ".files", path + ".native.stderr"} {
		if _, e := os.Lstat(record); !os.IsNotExist(e) {
			return errors.New("MYSQL_RESTORE_CONTEXT_ALREADY_EXISTS")
		}
	}
	root, e := openPrivateRoot(filepath.Dir(path))
	if e != nil {
		return e
	}
	return root.Close()
}
func verifyFreshIntent(ctx context.Context, fresh *mysqloperator.Fresh, c config.Config, i RestoreIntent) error {
	if e := fresh.VerifyConfiguration(ctx, c); e != nil {
		return e
	}
	binding, e := fresh.Binding(ctx)
	if e != nil || binding != i.TargetBindingSHA256 {
		return errors.New("MYSQL_RESTORE_TARGET_INPUT_CHANGED")
	}
	lease, e := fresh.Lease(ctx)
	if e != nil {
		return e
	}
	target, _, e := connectionTarget(ctx, lease)
	if e != nil || target != i.Target {
		return errors.New("MYSQL_RESTORE_TARGET_CHANGED")
	}
	return nil
}
func importArchive(ctx context.Context, native *Native, fresh *mysqloperator.Fresh, c config.Config, i RestoreIntent, contextPath string) (NativeExecution, error) {
	root, e := openPrivateRoot(filepath.Dir(contextPath))
	if e != nil {
		return NativeExecution{}, e
	}
	diagnostic, e := root.OpenFile(filepath.Base(contextPath)+".native.stderr", os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		root.Close()
		return NativeExecution{}, errors.New("MYSQL_RESTORE_DIAGNOSTIC_EXISTS")
	}
	output := &fileOutput{ctx: ctx, file: diagnostic, maximum: native.policy.MaximumManifestBytes, minimum: native.policy.MinimumFreeBytes, hash: sha256.New()}
	execution, runError := native.ImportMeasured(ctx, fresh, c, filepath.Join(i.BackupRoot, archiveName), i.SQLSHA256, output)
	if diagnostic.Sync() != nil {
		runError = errors.Join(runError, errors.New("MYSQL_BACKUP_WRITE_UNCERTAIN"))
	}
	if diagnostic.Close() != nil {
		runError = errors.Join(runError, errors.New("MYSQL_BACKUP_WRITE_UNCERTAIN"))
	}
	if syncRoot(root) != nil {
		runError = errors.Join(runError, errors.New("MYSQL_BACKUP_WRITE_UNCERTAIN"))
	}
	if root.Close() != nil {
		runError = errors.Join(runError, errors.New("MYSQL_BACKUP_ROOT_CLOSE_FAILED"))
	}
	if runError != nil {
		return NativeExecution{}, runError
	}
	return execution, nil
}
func actualRootIdentities(c config.Config) ([]RootIdentity, error) {
	result := []RootIdentity{}
	for _, path := range storeRoots(c) {
		root, e := openPrivateRoot(path)
		if e != nil {
			return nil, e
		}
		info, e := root.Stat(".")
		closeError := root.Close()
		if e != nil || closeError != nil {
			return nil, errors.New("MYSQL_RESTORE_ROOT_CHANGED")
		}
		id, e := rootIdentity(path, info)
		if e != nil {
			return nil, e
		}
		result = append(result, id)
	}
	return result, nil
}
func publishRestoredContext(ctx context.Context, fresh *mysqloperator.Fresh, c config.Config, accounts *identity.Service, path string) (RestoredContext, error) {
	verified, e := verifyRestore(ctx, fresh, c, accounts, path, true, false)
	if e != nil {
		return RestoredContext{}, e
	}
	if e = writeRestoreDocument(path, verified, c.Database.MySQLBackup.MaximumManifestBytes); e != nil {
		return RestoredContext{}, e
	}
	return verifyRestore(ctx, fresh, c, accounts, path, true, true)
}

// Restore imports only an explicitly selected new isolated target. Every partial
// output is retained; no error retries DDL/import/invalidation or deletes a scope.
func Restore(ctx context.Context, fresh *mysqloperator.Fresh, c config.Config, accounts *identity.Service, backupRoot, contextPath, reason string, isolated bool) (restored RestoredContext, result error) {
	if !isolated {
		return RestoredContext{}, errors.New("MYSQL_RESTORE_EXPLICIT_ISOLATED_TARGET_REQUIRED")
	}
	if ctx == nil || fresh == nil || accounts == nil || c.Database.MySQLBackup == nil || c.Database.MySQLBackup.Validate() != nil || !modelpreparations.ValidOperatorReason(reason) {
		return RestoredContext{}, errors.New("MYSQL_RESTORE_CONFIGURATION_OR_REASON_REQUIRED")
	}
	p := *c.Database.MySQLBackup
	bounded, cancel := context.WithTimeout(ctx, time.Duration(p.OperationSeconds)*time.Second)
	defer cancel()
	if e := restorePaths(c, backupRoot, contextPath); e != nil {
		return RestoredContext{}, e
	}
	if e := restoreRecordsAbsent(contextPath); e != nil {
		return RestoredContext{}, e
	}
	m, manifestSHA, e := loadManifest(bounded, backupRoot, p, true)
	if e != nil {
		return RestoredContext{}, e
	}
	if e = fresh.VerifyConfiguration(bounded, c); e != nil {
		return RestoredContext{}, e
	}
	if e = fresh.RequireEmpty(bounded); e != nil {
		return RestoredContext{}, e
	}
	lease, e := fresh.Lease(bounded)
	if e != nil {
		return RestoredContext{}, e
	}
	target, _, e := connectionTarget(bounded, lease)
	if e != nil {
		return RestoredContext{}, e
	}
	binding, e := fresh.Binding(bounded)
	if e != nil {
		return RestoredContext{}, e
	}
	roots, e := acquireFreshRoots(storeRoots(c))
	if e != nil {
		return RestoredContext{}, e
	}
	defer func() {
		if e := roots.Close(); e != nil {
			restored = RestoredContext{}
			result = errors.Join(result, e)
		}
	}()
	i := RestoreIntent{Format: "projectone.mysql.restore-intent.v1", RestoreID: uuid.NewString(), BackupID: m.BackupID, CreatedAt: time.Now().UTC(), Reason: reason, BackupRoot: backupRoot, ManifestSHA256: manifestSHA, SQLSHA256: m.SQLSHA256, SourceDataSHA256: m.Data.SHA256, TenantID: m.TenantID, Target: target, TargetBindingSHA256: binding, SchemaSteps: m.SchemaSteps, Roots: roots.Identities()}
	if e = i.validate(c, m, manifestSHA); e != nil {
		return RestoredContext{}, e
	}
	// Conservatively reserve the whole four-store limit on each actual target
	// filesystem; remote database capacity is an explicit operator admission duty.
	for _, path := range append(storeRoots(c), filepath.Dir(contextPath), p.ScratchRoot) {
		if e = requireSpace(path, p.MaximumStoreBytes+p.MaximumManifestBytes*4, p.MinimumFreeBytes); e != nil {
			return RestoredContext{}, e
		}
	}
	settings, e := fresh.Settings(bounded)
	if e != nil {
		return RestoredContext{}, e
	}
	native, e := NewNative(bounded, p, settings)
	if e != nil {
		return RestoredContext{}, e
	}
	if e = roots.Check(true); e != nil {
		return RestoredContext{}, e
	}
	if e = verifyFreshIntent(bounded, fresh, c, i); e != nil {
		return RestoredContext{}, e
	}
	if e = writeRestoreDocument(contextPath+".intent", i, p.MaximumManifestBytes); e != nil {
		return RestoredContext{}, e
	}
	intentSHA, e := documentSHA(i)
	if e != nil {
		return RestoredContext{}, e
	}
	execution, e := importArchive(bounded, native, fresh, c, i, contextPath)
	if e != nil {
		return RestoredContext{}, e
	}
	o, e := fresh.Promote(bounded)
	if e != nil {
		return RestoredContext{}, e
	}
	if e = o.VerifyIdentity(bounded, accounts); e != nil {
		return RestoredContext{}, e
	}
	targetData, e := ReadDatabase(bounded, o, p, false)
	if e != nil {
		return RestoredContext{}, e
	}
	if !reflect.DeepEqual(targetData, m.Data) {
		return RestoredContext{}, errors.New("MYSQL_RESTORE_SOURCE_ROW_IMAGE_MISMATCH")
	}
	if e = roots.Check(true); e != nil {
		return RestoredContext{}, e
	}
	if e = importOwners(bounded, o, c, accounts, filepath.Join(backupRoot, snapshotDirectory)); e != nil {
		return RestoredContext{}, e
	}
	if e = roots.Check(false); e != nil {
		return RestoredContext{}, e
	}
	// Owner imports do no SQL mutations: repeat the full source image before the
	// single authority transaction, rather than accepting filesystem-only success.
	targetData, e = ReadDatabase(bounded, o, p, false)
	if e != nil {
		return RestoredContext{}, e
	}
	if !reflect.DeepEqual(targetData, m.Data) {
		return RestoredContext{}, errors.New("MYSQL_RESTORE_SOURCE_ROW_IMAGE_MISMATCH")
	}
	trees, _, _, e := sourceTrees(bounded, c)
	if e != nil {
		return RestoredContext{}, e
	}
	files := RestoredFiles{Format: "projectone.mysql.restored-files.v1", RestoreID: i.RestoreID, IntentSHA256: intentSHA, NativeArtifactManifestSHA256: native.manifestSHA256, ClientSHA256: p.ClientSHA256, ImportExecution: execution, Trees: trees}
	if e = files.validate(i, intentSHA, p); e != nil {
		return RestoredContext{}, e
	}
	if e = writeRestoreDocument(contextPath+".files", files, p.MaximumManifestBytes); e != nil {
		return RestoredContext{}, e
	}
	// Reopen immutable original records before writing SQL; replacement/tampering
	// cannot turn in-memory success into a foreign unknown-commit recovery intent.
	actualI, actualFiles, actualM, actualIntentSHA, filesSHA, e := readRestoreRecords(bounded, c, contextPath)
	if e != nil {
		return RestoredContext{}, e
	}
	if !reflect.DeepEqual(i, actualI) || !reflect.DeepEqual(files, actualFiles) || !reflect.DeepEqual(m, actualM) || actualIntentSHA != intentSHA {
		return RestoredContext{}, errors.New("MYSQL_RESTORE_ORIGINAL_RECORD_CHANGED")
	}
	if e = roots.Check(false); e != nil {
		return RestoredContext{}, e
	}
	if e = verifyFreshIntent(bounded, fresh, c, i); e != nil {
		return RestoredContext{}, e
	}
	if _, e = invalidateRestore(bounded, o, accounts, i, intentSHA, filesSHA, m, p); e != nil {
		return RestoredContext{}, e
	}
	if e = roots.Check(false); e != nil {
		return RestoredContext{}, e
	}
	return publishRestoredContext(bounded, fresh, c, accounts, contextPath)
}

func verifyRestore(ctx context.Context, fresh *mysqloperator.Fresh, c config.Config, accounts *identity.Service, path string, requireFresh, readPublished bool) (RestoredContext, error) {
	i, files, m, intentSHA, filesSHA, e := readRestoreRecords(ctx, c, path)
	if e != nil {
		return RestoredContext{}, e
	}
	if e = restorePaths(c, i.BackupRoot, path); e != nil {
		return RestoredContext{}, e
	}
	if e = verifyFreshIntent(ctx, fresh, c, i); e != nil {
		return RestoredContext{}, e
	}
	o, e := fresh.Promote(ctx)
	if e != nil {
		return RestoredContext{}, e
	}
	if e = o.VerifyIdentity(ctx, accounts); e != nil {
		return RestoredContext{}, e
	}
	ids, e := actualRootIdentities(c)
	if e != nil {
		return RestoredContext{}, e
	}
	if !reflect.DeepEqual(ids, i.Roots) {
		return RestoredContext{}, errors.New("MYSQL_RESTORE_ROOT_CHANGED")
	}
	if requireFresh {
		owners, e := openOwners(ctx, o, c, accounts)
		if e != nil {
			return RestoredContext{}, e
		}
		if e = owners.Close(); e != nil {
			return RestoredContext{}, e
		}
		trees, _, _, e := sourceTrees(ctx, c)
		if e != nil {
			return RestoredContext{}, e
		}
		if !reflect.DeepEqual(trees, files.Trees) {
			return RestoredContext{}, errors.New("MYSQL_RESTORE_TARGET_FILES_CHANGED")
		}
	}
	post, e := readCommittedRestore(ctx, o, i, intentSHA, filesSHA, m, *c.Database.MySQLBackup, requireFresh)
	if e != nil {
		return RestoredContext{}, e
	}
	result := contextFrom(i, intentSHA, filesSHA, post)
	if !result.valid() {
		return RestoredContext{}, errors.New("MYSQL_RESTORE_CONTEXT_INVALID")
	}
	if readPublished {
		var actual RestoredContext
		if _, e = readPrivateDocument(path, c.Database.MySQLBackup.MaximumManifestBytes, &actual); e != nil {
			return RestoredContext{}, e
		}
		if !reflect.DeepEqual(actual, result) {
			return RestoredContext{}, errors.New("MYSQL_RESTORE_CONTEXT_CHANGED")
		}
	}
	return result, nil
}

// VerifyRestoredContext verifies actual immutable intent/files, strict schema,
// target/input binding and committed audit. Fresh verification additionally gates
// further operator mutations on the original post-image and four actual stores.
func VerifyRestoredContext(ctx context.Context, fresh *mysqloperator.Fresh, c config.Config, accounts *identity.Service, path string, requireFresh bool) (RestoredContext, error) {
	if ctx == nil || fresh == nil || accounts == nil || c.Database.MySQLBackup == nil || c.Database.MySQLBackup.Validate() != nil {
		return RestoredContext{}, errors.New("MYSQL_RESTORE_CONFIGURATION_REQUIRED")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.Database.MySQLBackup.OperationSeconds)*time.Second)
	defer cancel()
	return verifyRestore(bounded, fresh, c, accounts, path, requireFresh, true)
}

func ReconcileRestore(ctx context.Context, fresh *mysqloperator.Fresh, c config.Config, accounts *identity.Service, path string, isolated bool) (RestoredContext, error) {
	if !isolated {
		return RestoredContext{}, errors.New("MYSQL_RESTORE_EXPLICIT_ISOLATED_TARGET_REQUIRED")
	}
	if ctx == nil || fresh == nil || accounts == nil || c.Database.MySQLBackup == nil || c.Database.MySQLBackup.Validate() != nil {
		return RestoredContext{}, errors.New("MYSQL_RESTORE_CONFIGURATION_REQUIRED")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.Database.MySQLBackup.OperationSeconds)*time.Second)
	defer cancel()
	if _, e := os.Lstat(path); e == nil {
		return verifyRestore(bounded, fresh, c, accounts, path, true, true)
	} else if !os.IsNotExist(e) {
		return RestoredContext{}, errors.New("MYSQL_RESTORE_CONTEXT_INVALID")
	}
	return publishRestoredContext(bounded, fresh, c, accounts, path)
}
