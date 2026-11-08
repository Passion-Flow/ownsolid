package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// keyFiles encrypt or sign what the database holds. Restoring a database next to other
// keys would leave its secrets unreadable, so a mismatch is refused.
var keyFiles = []string{"config/auth-hmac", "config/mail-keyring.json"}

// swapAttempts bounds renaming the database while the health check connects to it.
const (
	swapAttempts   = 10
	swapRetryDelay = time.Second
)

// Restore replaces the installation's database with a backup. It loads and checks the
// backup in a separate database first, so a bad backup never touches the current one,
// and a restored database that fails its checks is swapped back.
func Restore(ctx context.Context, c Config, runner StreamRunner, from string, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("INSTALL_ROOT_REQUIRED: restore writes files owned by the service users; run it as root")
	}
	manifest, e := readBackup(from)
	if e != nil {
		return e
	}
	fmt.Fprintf(out, "step verify-backup: ok created_at=%s schema=%d\n", manifest.CreatedAt.Format(time.RFC3339), manifest.SchemaVersion)
	unlock, e := lockOperation(c)
	if e != nil {
		return e
	}
	defer unlock()
	if e := checkBoundOperation(c); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.BackupSeconds)*time.Second)
	defer cancel()
	o := newOperation(c, runner, out)
	if e = o.restore(ctx, from, manifest); e != nil {
		e = retryWith(e, "restore")
		o.record("restore", "failed", from+": "+e.Error())
		return e
	}
	o.record("restore", "complete", from)
	return nil
}

// readBackup checks that a backup is whole and unchanged before anything is touched.
func readBackup(from string) (backupManifest, error) {
	var manifest backupManifest
	if !filepath.IsAbs(from) {
		return manifest, fmt.Errorf("INSTALL_BACKUP_INVALID: %s must be an absolute path", from)
	}
	raw, e := os.ReadFile(filepath.Join(from, backupManifestName))
	if e != nil {
		return manifest, fmt.Errorf("INSTALL_BACKUP_INVALID: %s has no readable manifest, so it is not a complete backup", from)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&manifest); e != nil {
		return manifest, fmt.Errorf("INSTALL_BACKUP_INVALID: %s: %v", backupManifestName, e)
	}
	if manifest.FormatVersion != backupFormatVersion {
		return manifest, fmt.Errorf("INSTALL_BACKUP_INVALID: format version %d is not %d", manifest.FormatVersion, backupFormatVersion)
	}
	if manifest.SchemaVersion < 1 || len(manifest.Tables) == 0 {
		return manifest, errors.New("INSTALL_BACKUP_INVALID: the manifest describes no database")
	}
	if e = verifyBackupFile(from, manifest.Database, backupDatabaseName); e != nil {
		return manifest, e
	}
	for _, file := range manifest.Files {
		if e = verifyBackupFile(filepath.Join(from, backupFilesName), file, file.Path); e != nil {
			return manifest, e
		}
	}
	return manifest, nil
}

// verifyBackupFile refuses a path that leaves the backup, which a changed manifest
// could otherwise use to read or later overwrite any file on the host.
func verifyBackupFile(root string, file backupFile, relative string) error {
	if !filepath.IsLocal(relative) {
		return fmt.Errorf("INSTALL_BACKUP_INVALID: %q leaves the backup", relative)
	}
	f, e := os.Open(filepath.Join(root, relative))
	if e != nil {
		return fmt.Errorf("INSTALL_BACKUP_CORRUPT: %s is missing", relative)
	}
	defer f.Close()
	hash := sha256.New()
	written, e := io.Copy(hash, f)
	if e != nil {
		return fmt.Errorf("INSTALL_BACKUP_CORRUPT: %s: %v", relative, e)
	}
	if written != file.Bytes || hex.EncodeToString(hash.Sum(nil)) != file.SHA256 {
		return fmt.Errorf("INSTALL_BACKUP_CORRUPT: %s does not match the manifest", relative)
	}
	return nil
}

func (o *operation) restore(ctx context.Context, from string, manifest backupManifest) error {
	fresh, e := o.admitKeys(from, manifest)
	if e != nil {
		return e
	}
	if fresh {
		if e = o.restoreFiles(from, manifest); e != nil {
			return &StepError{Step: "restore", Code: "INSTALL_RESTORE_FILES_FAILED", Detail: e.Error()}
		}
		fmt.Fprintln(o.out, "step restore-files: ok")
	}
	if e = o.prepareTarget(ctx); e != nil {
		if fresh {
			return e
		}
		return o.abandon(ctx, e)
	}
	// Before the database changes: serving refuses a database naming a model it cannot find.
	restored, e := o.restoreModels(from, manifest)
	if e != nil {
		e = &StepError{Step: "restore", Code: "INSTALL_RESTORE_MODELS_FAILED", Detail: e.Error()}
		if fresh {
			return e
		}
		return o.abandon(ctx, e)
	}
	fmt.Fprintf(o.out, "step restore-models: ok restored=%d\n", restored)
	stamp := strings.ToLower(o.now().UTC().Format(backupTimeLayout))
	incoming, previous := "ownsolid_restore_"+stamp, "ownsolid_before_restore_"+stamp
	if e = o.createDatabase(ctx, incoming); e != nil {
		return o.abandon(ctx, &StepError{Step: "restore", Code: "INSTALL_RESTORE_LOAD_FAILED", Detail: e.Error()})
	}
	swapped := false
	defer func() {
		if !swapped {
			o.dropDatabase(ctx, incoming)
		}
	}()
	if e = o.loadDump(ctx, incoming, filepath.Join(from, backupDatabaseName)); e != nil {
		return o.abandon(ctx, &StepError{Step: "restore", Code: "INSTALL_RESTORE_LOAD_FAILED", Detail: e.Error()})
	}
	tables, e := o.fingerprint(ctx, incoming)
	if e != nil {
		return o.abandon(ctx, &StepError{Step: "restore", Code: "INSTALL_RESTORE_LOAD_FAILED", Detail: e.Error()})
	}
	if !slices.Equal(tables, manifest.Tables) {
		return o.abandon(ctx, &StepError{Step: "restore", Code: "INSTALL_RESTORE_CONTENT_MISMATCH", Detail: "the restored tables differ from what the backup recorded"})
	}
	fmt.Fprintf(o.out, "step load: ok tables=%d\n", len(tables))
	if e = o.swap(ctx, databaseName, previous, incoming); e != nil {
		return o.abandon(ctx, &StepError{Step: "restore", Code: "INSTALL_RESTORE_SWAP_FAILED", Detail: e.Error()})
	}
	swapped = true
	if e = o.adoptRestored(ctx); e != nil {
		return o.rollBack(ctx, previous, incoming, e)
	}
	fmt.Fprintf(o.out, "RESTORE_COMPLETE previous_database=%s\n", previous)
	fmt.Fprintln(o.out, "Everyone signs in again and node agents join again: restoring revokes sessions and node credentials.")
	return nil
}

// admitKeys reports whether this is an empty installation that takes its files from the
// backup. A running one keeps its files, and they must match the backup's keys.
func (o *operation) admitKeys(from string, manifest backupManifest) (bool, error) {
	l := o.config.Layout()
	if !exists(filepath.Join(l.Secrets, "postgres-password")) {
		return true, nil
	}
	for _, name := range keyFiles {
		i := slices.IndexFunc(manifest.Files, func(f backupFile) bool { return f.Path == name })
		if i < 0 {
			return false, fmt.Errorf("INSTALL_BACKUP_INVALID: the backup has no %s", name)
		}
		e := verifyBackupFile(l.Root, manifest.Files[i], name)
		if e != nil {
			return false, fmt.Errorf("INSTALL_RESTORE_KEYS_DIFFER: %s differs from the backup, so the restored data could not be read. Restore onto an empty install_root, or put back the file from the backup", name)
		}
	}
	return false, nil
}

// restoreFiles puts the key files of an empty installation back with their owners.
func (o *operation) restoreFiles(from string, manifest backupManifest) error {
	for _, file := range manifest.Files {
		if o.isModelFile(file) {
			continue
		}
		if e := o.restoreFile(from, file); e != nil {
			return e
		}
	}
	return nil
}

// restoreFile copies one file of a backup to where it was, with its owner and mode.
func (o *operation) restoreFile(from string, file backupFile) error {
	target := filepath.Join(o.config.Layout().Root, file.Path)
	copied, e := copyPrivateFile(filepath.Join(from, backupFilesName, file.Path), target)
	if e != nil {
		return e
	}
	if copied.SHA256 != file.SHA256 {
		return fmt.Errorf("%s changed while it was restored", file.Path)
	}
	if e = os.Chown(target, file.UID, file.GID); e != nil {
		return e
	}
	return os.Chmod(target, os.FileMode(file.Mode))
}

func (o *operation) isModelFile(file backupFile) bool {
	relative, e := filepath.Rel(o.config.Layout().Root, o.modelObjects())
	return e == nil && strings.HasPrefix(file.Path, relative+string(filepath.Separator))
}

// restoreModels puts back every published model that is missing or damaged, and leaves one
// that matches, so restoring on the same host does not rewrite the whole store. Files the
// backup does not name stay: an unreferenced model takes space but harms nothing.
func (o *operation) restoreModels(from string, manifest backupManifest) (int, error) {
	restored := 0
	root := o.config.Layout().Root
	for _, file := range manifest.Files {
		if !o.isModelFile(file) {
			continue
		}
		if verifyBackupFile(root, file, file.Path) == nil {
			continue
		}
		if e := os.Remove(filepath.Join(root, file.Path)); e != nil && !errors.Is(e, os.ErrNotExist) {
			return restored, e
		}
		if e := o.restoreFile(from, file); e != nil {
			return restored, e
		}
		restored++
	}
	return restored, o.ownModelDirectories()
}

// ownModelDirectories gives the runtime user the directories restoring created, because the
// store refuses to open any other owner's.
func (o *operation) ownModelDirectories() error {
	store := o.config.Layout().Models
	return filepath.WalkDir(store, func(path string, entry fs.DirEntry, e error) error {
		if errors.Is(e, fs.ErrNotExist) && path == store {
			return nil
		}
		if e != nil || !entry.IsDir() {
			return e
		}
		return own(path, o.config, true)
	})
}

// prepareTarget brings the installation to where a database can be loaded. A cluster
// that already has its roles skips the schema steps: restore must work on a database
// too damaged to migrate. A new cluster needs them to create the roles the dump grants to.
func (o *operation) prepareTarget(ctx context.Context) error {
	steps := []struct {
		name string
		run  func(context.Context) error
	}{
		{"preflight", o.preflight},
		{"configuration", func(context.Context) error { return Materialize(o.config) }},
		{"images", o.images},
		{"stop-control", o.stopControl},
		{"database", o.database},
		{"telemetry", o.telemetry},
	}
	if e := o.runSteps(ctx, steps); e != nil {
		return e
	}
	role, e := o.query(ctx, maintenanceDatabase, "SELECT count(*) FROM pg_roles WHERE rolname='"+runtimeRole+"'")
	if e != nil {
		return wrapStep("database", &StepError{Code: "INSTALL_DATABASE_UNAVAILABLE", Detail: e.Error()})
	}
	if role == "1" {
		return nil
	}
	return o.runSteps(ctx, []struct {
		name string
		run  func(context.Context) error
	}{{"vector-extension", o.vectorExtension}, {"schema", o.schema}, {"runtime-access", o.runtimeAccess}})
}

func (o *operation) runSteps(ctx context.Context, steps []struct {
	name string
	run  func(context.Context) error
}) error {
	for _, step := range steps {
		stepContext, cancel := context.WithTimeout(ctx, time.Duration(o.config.StepSeconds)*time.Second)
		e := step.run(stepContext)
		cancel()
		if e != nil {
			return wrapStep(step.name, e)
		}
		fmt.Fprintf(o.out, "step %s: ok\n", step.name)
	}
	return nil
}

// swap renames both databases in one transaction, so the name always resolves. It ends
// other sessions first; control is stopped, and what remains is the health check.
func (o *operation) swap(ctx context.Context, current, keepAs, next string) error {
	statement := fmt.Sprintf(`BEGIN; ALTER DATABASE "%s" RENAME TO "%s"; ALTER DATABASE "%s" RENAME TO "%s"; COMMIT;`, current, keepAs, next, current)
	var e error
	for attempt := 0; attempt < swapAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(swapRetryDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if _, e = o.query(ctx, maintenanceDatabase, fmt.Sprintf(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname IN ('%s','%s') AND pid<>pg_backend_pid()`, current, next)); e != nil {
			continue
		}
		if _, e = o.query(ctx, maintenanceDatabase, statement); e == nil {
			return nil
		}
	}
	return e
}

// adoptRestored upgrades the restored database to this release, checks it, revokes what
// the backup's point in time may no longer justify, and starts serving on it.
func (o *operation) adoptRestored(ctx context.Context) error {
	return o.runSteps(ctx, []struct {
		name string
		run  func(context.Context) error
	}{
		{"schema", o.schema},
		{"runtime-access", o.runtimeAccess},
		{"check-schema", o.checkSchema},
		{"invalidate-restored-authority", o.invalidateRestored},
		{"record-restore-adoption", o.recordRestoreAdoption},
		{"control", o.control},
		{"health", o.health},
	})
}

func (o *operation) invalidateRestored(ctx context.Context) error {
	_, e := o.operator(ctx, "RESTORED_AUTH_INVALIDATED", "invalidate-restored-auth", "--config", o.operatorConfig(), "--reason", "Restored from a backup")
	return e
}

// abandon returns the installation to serving on its current database after a failure
// that left that database untouched. A failure to start is added to the cause.
func (o *operation) abandon(ctx context.Context, cause error) error {
	restart, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(o.config.StepSeconds)*time.Second)
	defer cancel()
	if e := o.control(restart); e != nil {
		return errors.Join(cause, fmt.Errorf("control did not start again: %w", e))
	}
	return cause
}

// rollBack puts the previous database back after the restored one failed its checks.
func (o *operation) rollBack(ctx context.Context, previous, incoming string, cause error) error {
	restart, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(o.config.StepSeconds)*time.Second)
	defer cancel()
	if e := o.stopControl(restart); e != nil {
		return errors.Join(cause, fmt.Errorf("control did not stop for the rollback: %w", e))
	}
	if e := o.swap(restart, databaseName, incoming, previous); e != nil {
		return errors.Join(cause, fmt.Errorf("the previous database is still named %s and was not put back: %w", previous, e))
	}
	o.dropDatabase(restart, incoming)
	fmt.Fprintf(o.out, "step roll-back: the database from before the restore is back\n")
	return o.abandon(restart, cause)
}
