package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	backupFormatVersion = 1
	backupPrefix        = "ownsolid-backup-"
	backupPartialSuffix = ".partial"
	backupManifestName  = "manifest.json"
	backupDatabaseName  = "database.dump"
	backupFilesName     = "files"
	backupTimeLayout    = "20060102T150405Z"
	operationLockName   = "operation.lock"
	operationLogName    = "operations.log"
	maintenanceDatabase = "postgres"
	// cleanupTimeout bounds removing a scratch database after the operation's own
	// deadline has passed, so a timeout does not leave it behind.
	cleanupTimeout = 2 * time.Minute
)

// tableName limits what is interpolated into the fingerprint query.
var tableName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// StreamRunner also runs commands whose input or output is too large to hold in memory.
type StreamRunner interface {
	Runner
	// Stream connects stdin and stdout, and returns what the command wrote to stderr.
	Stream(ctx context.Context, stdin io.Reader, stdout io.Writer, name string, args ...string) (string, error)
}

// Stream implements StreamRunner with the same closed environment as Run.
func (ExecRunner) Stream(ctx context.Context, stdin io.Reader, stdout io.Writer, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = closedEnvironment()
	command.Stdin, command.Stdout = stdin, stdout
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	e := command.Run()
	return diagnostics.String(), e
}

// backupFile describes one file of a backup. Owner and mode let a restore put a file
// back exactly, because serving reads some files as the runtime user.
type backupFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Mode   uint32 `json:"mode"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
}

// tableFingerprint is a table's row count and an order-independent sum of its row
// hashes, as read from a database restored from the dump.
type tableFingerprint struct {
	Name string `json:"name"`
	Rows string `json:"rows"`
	Sum  string `json:"sum"`
}

// backupManifest records what a backup holds and what its database must contain.
type backupManifest struct {
	FormatVersion int                `json:"format_version"`
	CreatedAt     time.Time          `json:"created_at"`
	ControlImage  string             `json:"control_image"`
	PostgresImage string             `json:"postgres_image"`
	SchemaVersion int                `json:"schema_version"`
	Database      backupFile         `json:"database"`
	Tables        []tableFingerprint `json:"tables"`
	Files         []backupFile       `json:"files"`
}

// operation is a backup or a restore: an install step sequence that also streams.
type operation struct {
	*installer
	stream StreamRunner
	now    func() time.Time
}

func newOperation(c Config, runner StreamRunner, out io.Writer) *operation {
	return &operation{installer: &installer{c, runner, ProbeOrigin, out}, stream: runner, now: time.Now}
}

// lockOperation excludes a second apply, backup or restore on this installation. The
// kernel releases the lock when the process ends, so a crash cannot leave it held.
func lockOperation(c Config) (func(), error) {
	if e := os.MkdirAll(c.InstallRoot, 0o700); e != nil {
		return nil, fmt.Errorf("INSTALL_DIRECTORY_FAILED: %s", c.InstallRoot)
	}
	f, e := os.OpenFile(filepath.Join(c.InstallRoot, operationLockName), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if e != nil {
		return nil, fmt.Errorf("INSTALL_LOCK_FAILED: %s", c.InstallRoot)
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("INSTALL_BUSY: another apply, backup or restore is running on this installation")
	}
	return func() { f.Close() }, nil
}

// record appends a privileged action to the operator log. Serving never reads it. A
// write failure is reported, because the action itself has already happened.
func (o *operation) record(action, outcome, detail string) {
	line, e := json.Marshal(map[string]string{"time": o.now().UTC().Format(time.RFC3339), "action": action, "outcome": outcome, "detail": detail})
	if e == nil {
		var f *os.File
		if f, e = os.OpenFile(filepath.Join(o.config.Layout().Operator, operationLogName), os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0o600); e == nil {
			_, e = f.Write(append(line, '\n'))
			e = errors.Join(e, f.Close())
		}
	}
	if e != nil {
		fmt.Fprintf(o.out, "warning: the operator log could not be written: %v\n", e)
	}
}

func (o *operation) composeArguments(args ...string) []string {
	l := o.config.Layout()
	return append([]string{"compose", "--env-file", l.ComposeEnv, "-f", l.ComposeFile}, args...)
}

// query runs SQL and returns only what the server answered, so a client warning on
// stderr cannot be parsed as data.
func (o *operation) query(ctx context.Context, database, sql string) (string, error) {
	var answer bytes.Buffer
	diagnostics, e := o.stream.Stream(ctx, nil, &answer, "docker", o.composeArguments("exec", "--no-TTY", "postgres", "psql", "-X", "-At", "-v", "ON_ERROR_STOP=1", "-U", migratorRole, "-d", database, "-c", sql)...)
	if e != nil {
		return "", fmt.Errorf("%s", lastLines(diagnostics+answer.String()))
	}
	return strings.TrimSpace(answer.String()), nil
}

func (o *operation) createDatabase(ctx context.Context, name string) error {
	_, e := o.query(ctx, maintenanceDatabase, `CREATE DATABASE "`+name+`" TEMPLATE template0`)
	return e
}

// dropDatabase removes a database this operation created. It outlives a cancelled ctx.
func (o *operation) dropDatabase(ctx context.Context, name string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if _, e := o.query(cleanup, maintenanceDatabase, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`); e != nil {
		fmt.Fprintf(o.out, "warning: database %s was left behind and can be dropped by hand: %v\n", name, e)
	}
}

// loadDump restores a dump into database. Owners and privileges are kept: the roles
// belong to the PostgreSQL cluster, and the runtime grants are part of what is restored.
func (o *operation) loadDump(ctx context.Context, database, path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	diagnostics, e := o.stream.Stream(ctx, f, io.Discard, "docker", o.composeArguments("exec", "--no-TTY", "postgres", "pg_restore", "-U", migratorRole, "-d", database, "--exit-on-error")...)
	if e != nil {
		return fmt.Errorf("%s", lastLines(diagnostics))
	}
	return nil
}

// fingerprint reads every table of database, each partitioned table through its parent, because
// partitions come and go while control runs. hashtextextended and a numeric sum need no
// sort, so this stays cheap on a large table and does not depend on row order.
func (o *operation) fingerprint(ctx context.Context, database string) ([]tableFingerprint, error) {
	raw, e := o.query(ctx, database, "SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p') AND NOT c.relispartition ORDER BY 1")
	if e != nil {
		return nil, e
	}
	if raw == "" {
		return nil, errors.New("the database has no tables")
	}
	selects := []string{}
	for _, name := range strings.Split(raw, "\n") {
		if !tableName.MatchString(name) {
			return nil, fmt.Errorf("unexpected table name %q", name)
		}
		selects = append(selects, fmt.Sprintf(`SELECT '%[1]s',count(*),coalesce(sum(hashtextextended(t::text,0)::numeric),0) FROM public."%[1]s" t`, name))
	}
	raw, e = o.query(ctx, database, strings.Join(selects, " UNION ALL ")+" ORDER BY 1")
	if e != nil {
		return nil, e
	}
	tables := []tableFingerprint{}
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Split(line, "|")
		if len(fields) != 3 {
			return nil, fmt.Errorf("unexpected fingerprint line %q", line)
		}
		tables = append(tables, tableFingerprint{fields[0], fields[1], fields[2]})
	}
	return tables, nil
}

func (o *operation) schemaVersion(ctx context.Context, database string) (int, error) {
	raw, e := o.query(ctx, database, "SELECT max(version) FROM schema_migrations")
	if e != nil {
		return 0, e
	}
	version, e := strconv.Atoi(raw)
	if e != nil || version < 1 {
		return 0, fmt.Errorf("unexpected schema version %q", raw)
	}
	return version, nil
}

// admitBackupOutput returns the directory to create, and refuses a location inside the
// installation: losing the installation must not lose its backups.
func admitBackupOutput(c Config, output string) error {
	if !filepath.IsAbs(output) || filepath.Clean(output) != output {
		return fmt.Errorf("INSTALL_BACKUP_OUTPUT_INVALID: %s must be a clean absolute path", output)
	}
	st, e := os.Lstat(output)
	if e != nil || !st.IsDir() {
		return fmt.Errorf("INSTALL_BACKUP_OUTPUT_INVALID: %s must be an existing directory, not a symlink", output)
	}
	if output == c.InstallRoot || strings.HasPrefix(output, c.InstallRoot+string(filepath.Separator)) {
		return fmt.Errorf("INSTALL_BACKUP_OUTPUT_INVALID: %s is inside install_root", output)
	}
	return nil
}

// Backup writes a verified backup of the database and the key files under output.
// Serving keeps running: pg_dump reads one consistent snapshot.
func Backup(ctx context.Context, c Config, runner StreamRunner, output string, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("INSTALL_ROOT_REQUIRED: backup reads files owned by the service users; run it as root")
	}
	if e := admitBackupOutput(c, output); e != nil {
		return e
	}
	unlock, e := lockOperation(c)
	if e != nil {
		return e
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.BackupSeconds)*time.Second)
	defer cancel()
	o := newOperation(c, runner, out)
	path, e := o.backup(ctx, output)
	if e != nil {
		e = retryWith(e, "backup")
		o.record("backup", "failed", e.Error())
		return e
	}
	o.record("backup", "complete", path)
	fmt.Fprintf(out, "BACKUP_COMPLETE path=%s\n", path)
	return nil
}

func (o *operation) backup(ctx context.Context, output string) (string, error) {
	stamp := o.now().UTC().Format(backupTimeLayout)
	final := filepath.Join(output, backupPrefix+stamp)
	if exists(final) {
		return "", fmt.Errorf("INSTALL_BACKUP_EXISTS: %s", final)
	}
	if _, e := o.query(ctx, databaseName, "SELECT 1"); e != nil {
		return "", &StepError{Step: "backup", Code: "INSTALL_DATABASE_UNAVAILABLE", Detail: e.Error()}
	}
	partial := final + backupPartialSuffix
	if e := os.Mkdir(partial, 0o700); e != nil {
		return "", fmt.Errorf("INSTALL_BACKUP_OUTPUT_FAILED: %v", e)
	}
	complete := false
	defer func() {
		if !complete {
			os.RemoveAll(partial)
		}
	}()
	manifest := backupManifest{FormatVersion: backupFormatVersion, CreatedAt: o.now().UTC(), ControlImage: o.config.Images.Control, PostgresImage: o.config.Images.Postgres}
	var e error
	if manifest.Database, e = o.dump(ctx, filepath.Join(partial, backupDatabaseName)); e != nil {
		return "", &StepError{Step: "backup", Code: "INSTALL_BACKUP_DUMP_FAILED", Detail: e.Error()}
	}
	fmt.Fprintln(o.out, "step dump: ok")
	// A backup counts only if it restores, so the fingerprint comes from a restored copy.
	scratch := "ownsolid_verify_" + strings.ToLower(stamp)
	if e = o.createDatabase(ctx, scratch); e != nil {
		return "", &StepError{Step: "backup", Code: "INSTALL_BACKUP_VERIFY_FAILED", Detail: e.Error()}
	}
	defer o.dropDatabase(ctx, scratch)
	if e = o.loadDump(ctx, scratch, filepath.Join(partial, backupDatabaseName)); e != nil {
		return "", &StepError{Step: "backup", Code: "INSTALL_BACKUP_VERIFY_FAILED", Detail: e.Error()}
	}
	if manifest.SchemaVersion, e = o.schemaVersion(ctx, scratch); e != nil {
		return "", &StepError{Step: "backup", Code: "INSTALL_BACKUP_VERIFY_FAILED", Detail: e.Error()}
	}
	if manifest.Tables, e = o.fingerprint(ctx, scratch); e != nil {
		return "", &StepError{Step: "backup", Code: "INSTALL_BACKUP_VERIFY_FAILED", Detail: e.Error()}
	}
	fmt.Fprintf(o.out, "step verify: ok tables=%d schema=%d\n", len(manifest.Tables), manifest.SchemaVersion)
	if manifest.Files, e = o.copyKeyFiles(filepath.Join(partial, backupFilesName)); e != nil {
		return "", &StepError{Step: "backup", Code: "INSTALL_BACKUP_FILES_FAILED", Detail: e.Error()}
	}
	fmt.Fprintf(o.out, "step files: ok files=%d\n", len(manifest.Files))
	if e = writeManifest(filepath.Join(partial, backupManifestName), manifest); e != nil {
		return "", &StepError{Step: "backup", Code: "INSTALL_BACKUP_OUTPUT_FAILED", Detail: e.Error()}
	}
	if e = os.Rename(partial, final); e != nil {
		return "", &StepError{Step: "backup", Code: "INSTALL_BACKUP_OUTPUT_FAILED", Detail: e.Error()}
	}
	complete = true
	return final, syncDirectory(output)
}

// dump streams pg_dump to path, hashing as it writes.
func (o *operation) dump(ctx context.Context, path string) (backupFile, error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		return backupFile{}, e
	}
	defer f.Close()
	hash := sha256.New()
	counter := &countingWriter{}
	diagnostics, e := o.stream.Stream(ctx, nil, io.MultiWriter(f, hash, counter), "docker", o.composeArguments("exec", "--no-TTY", "postgres", "pg_dump", "-U", migratorRole, "-d", databaseName, "-Fc")...)
	if e != nil {
		return backupFile{}, fmt.Errorf("%s", lastLines(diagnostics))
	}
	if counter.bytes == 0 {
		return backupFile{}, errors.New("pg_dump wrote nothing")
	}
	if e = f.Sync(); e != nil {
		return backupFile{}, e
	}
	return backupFile{Path: backupDatabaseName, SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: counter.bytes, Mode: 0o600}, nil
}

type countingWriter struct{ bytes int64 }

func (w *countingWriter) Write(p []byte) (int, error) {
	w.bytes += int64(len(p))
	return len(p), nil
}

// copyKeyFiles copies what the database cannot be used without: the session and mail
// keys that encrypt its contents, the database identities and the TLS material.
func (o *operation) copyKeyFiles(target string) ([]backupFile, error) {
	l := o.config.Layout()
	files := []backupFile{}
	for _, directory := range []string{l.Config, l.Operator, l.Secrets} {
		e := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("%s is not a regular file", path)
			}
			if entry.Name() == operationLogName {
				return nil
			}
			relative, e := filepath.Rel(l.Root, path)
			if e != nil {
				return e
			}
			file, e := copyPrivateFile(path, filepath.Join(target, relative))
			if e != nil {
				return e
			}
			file.Path = relative
			files = append(files, file)
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	return files, nil
}

// copyPrivateFile copies source to a new private file and reports what it copied.
func copyPrivateFile(source, target string) (backupFile, error) {
	in, e := os.Open(source)
	if e != nil {
		return backupFile{}, e
	}
	defer in.Close()
	st, e := in.Stat()
	if e != nil {
		return backupFile{}, e
	}
	if e = os.MkdirAll(filepath.Dir(target), 0o700); e != nil {
		return backupFile{}, e
	}
	out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		return backupFile{}, e
	}
	defer out.Close()
	hash := sha256.New()
	written, e := io.Copy(io.MultiWriter(out, hash), in)
	if e != nil {
		return backupFile{}, e
	}
	if e = out.Sync(); e != nil {
		return backupFile{}, e
	}
	file := backupFile{SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: written, Mode: uint32(st.Mode().Perm())}
	if owner, ok := st.Sys().(*syscall.Stat_t); ok {
		file.UID, file.GID = int(owner.Uid), int(owner.Gid)
	}
	return file, nil
}

// writeManifest is the last file a backup gets: its presence marks a whole backup.
func writeManifest(path string, manifest backupManifest) error {
	raw, e := json.MarshalIndent(manifest, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		return e
	}
	defer f.Close()
	if _, e = f.Write(append(raw, '\n')); e != nil {
		return e
	}
	return f.Sync()
}

func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
