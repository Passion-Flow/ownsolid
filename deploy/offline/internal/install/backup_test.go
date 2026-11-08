package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeDatabase answers the commands a backup or restore streams, like a database whose
// tables are fixed. Each answer can be overridden to inject a failure.
type fakeDatabase struct {
	fakeRunner
	streamed []string
	loaded   string
	override func(line string) (string, error, bool)
}

func (d *fakeDatabase) Stream(_ context.Context, stdin io.Reader, stdout io.Writer, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	d.streamed = append(d.streamed, line)
	d.commands = append(d.commands, line)
	if d.override != nil {
		if answer, e, handled := d.override(line); handled {
			io.WriteString(stdout, answer)
			if e != nil {
				return "database said no", e
			}
			return "", nil
		}
	}
	switch {
	case strings.Contains(line, "pg_dump"):
		io.WriteString(stdout, "DUMP BYTES")
	case strings.Contains(line, "pg_restore"):
		raw, e := io.ReadAll(stdin)
		if e != nil {
			return "", e
		}
		d.loaded = string(raw)
	case strings.Contains(line, "SELECT c.relname"):
		io.WriteString(stdout, "tenants\nusers\n")
	case strings.Contains(line, "UNION ALL"):
		io.WriteString(stdout, "tenants|1|5\nusers|2|9\n")
	case strings.Contains(line, "max(version)"):
		io.WriteString(stdout, "32\n")
	case strings.Contains(line, "pg_roles"):
		io.WriteString(stdout, "1\n")
	}
	return "", nil
}

func (d *fakeDatabase) ran(part string) bool {
	return slices.ContainsFunc(d.commands, func(line string) bool { return strings.Contains(line, part) })
}

// position returns where a command ran, so a test can state an order.
func (d *fakeDatabase) position(t *testing.T, part string) int {
	t.Helper()
	i := slices.IndexFunc(d.commands, func(line string) bool { return strings.Contains(line, part) })
	if i < 0 {
		t.Fatalf("%q never ran:\n%s", part, strings.Join(d.commands, "\n"))
	}
	return i
}

func testOperation(t *testing.T, c Config, d *fakeDatabase, out *bytes.Buffer) *operation {
	t.Helper()
	o := newOperation(c, d, out)
	o.probe = func(context.Context, Config, string) error { return nil }
	o.now = func() time.Time { return time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC) }
	return o
}

// installedConfig is a configuration whose install_root holds what apply writes.
func installedConfig(t *testing.T) Config {
	t.Helper()
	c := validConfig(t)
	if e := Materialize(c); e != nil {
		t.Fatal(e)
	}
	return c
}

func makeBackup(t *testing.T, c Config) string {
	t.Helper()
	path, e := testOperation(t, c, &fakeDatabase{}, &bytes.Buffer{}).backup(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return path
}

func TestBackupWritesAVerifiedBackupAndDropsItsScratchDatabase(t *testing.T) {
	c := installedConfig(t)
	d := &fakeDatabase{}
	output := t.TempDir()
	path, e := testOperation(t, c, d, &bytes.Buffer{}).backup(context.Background(), output)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Base(path) != "ownsolid-backup-20261104T080000Z" {
		t.Fatal(path)
	}
	manifest, e := readBackup(path)
	if e != nil {
		t.Fatal(e)
	}
	if manifest.SchemaVersion != 32 || len(manifest.Tables) != 2 || manifest.Tables[1] != (tableFingerprint{"users", "2", "9"}) || manifest.ControlImage != c.Images.Control {
		t.Fatalf("%+v", manifest)
	}
	if d.loaded != "DUMP BYTES" {
		t.Fatalf("the verification restored %q, not the dump", d.loaded)
	}
	if d.position(t, "pg_dump") > d.position(t, "pg_restore") || d.position(t, "pg_restore") > d.position(t, "DROP DATABASE") {
		t.Fatalf("order:\n%s", strings.Join(d.commands, "\n"))
	}
	if !slices.ContainsFunc(manifest.Files, func(f backupFile) bool { return f.Path == "config/mail-keyring.json" && f.Bytes > 0 }) {
		t.Fatalf("the keyring is not in the backup: %+v", manifest.Files)
	}
	st, e := os.Stat(path)
	if e != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("%v %v", st, e)
	}
	if exists(path + backupPartialSuffix) {
		t.Fatal("the partial directory remains")
	}
}

func TestBackupKeepsTelemetryAndDataOut(t *testing.T) {
	c := installedConfig(t)
	marker := filepath.Join(c.Layout().Logs, "marker")
	if e := os.WriteFile(marker, []byte("telemetry"), 0o600); e != nil {
		t.Fatal(e)
	}
	manifest, e := readBackup(makeBackup(t, c))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range manifest.Files {
		if strings.HasPrefix(f.Path, "data") {
			t.Fatalf("%s is in the backup", f.Path)
		}
	}
}

const publishedModel = "data/models/objects/11111111-1111-4111-8111-111111111111/model.safetensors"

// withPublishedModel leaves one published model and one unfinished upload in the store.
func withPublishedModel(t *testing.T, c Config) {
	t.Helper()
	path := filepath.Join(c.InstallRoot, publishedModel)
	if e := os.MkdirAll(filepath.Dir(path), 0o700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte("weights"), 0o444); e != nil {
		t.Fatal(e)
	}
	upload := filepath.Join(c.Layout().Models, "imports", "22222222-2222-4222-8222-222222222222", "chunk-000000000000")
	if e := os.MkdirAll(filepath.Dir(upload), 0o700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(upload, []byte("half"), 0o600); e != nil {
		t.Fatal(e)
	}
}

func TestBackupHoldsPublishedModelsAndNotUploads(t *testing.T) {
	c := installedConfig(t)
	withPublishedModel(t, c)
	manifest, e := readBackup(makeBackup(t, c))
	if e != nil {
		t.Fatal(e)
	}
	var models []string
	for _, f := range manifest.Files {
		if strings.HasPrefix(f.Path, "data") {
			models = append(models, f.Path)
		}
	}
	if !slices.Equal(models, []string{publishedModel}) {
		t.Fatal(models)
	}
}

func TestBackupHoldsNoModelsBeforeTheFirstImport(t *testing.T) {
	c := installedConfig(t)
	out := &bytes.Buffer{}
	if _, e := testOperation(t, c, &fakeDatabase{}, out).backup(context.Background(), t.TempDir()); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "step models: ok files=0") {
		t.Fatal(out.String())
	}
}

func TestRestorePutsBackAMissingModelBeforeTheDatabaseChanges(t *testing.T) {
	c := installedConfig(t)
	withPublishedModel(t, c)
	path := makeBackup(t, c)
	target := filepath.Join(c.InstallRoot, publishedModel)
	if e := os.RemoveAll(filepath.Dir(target)); e != nil {
		t.Fatal(e)
	}
	out := &bytes.Buffer{}
	if e := restoreFrom(t, c, &fakeDatabase{}, path, out); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(target)
	if e != nil || string(raw) != "weights" {
		t.Fatalf("%q %v", raw, e)
	}
	st, e := os.Stat(target)
	if e != nil || st.Mode().Perm() != 0o444 {
		t.Fatalf("%v %v", st, e)
	}
	if !strings.Contains(out.String(), "step restore-models: ok restored=1") {
		t.Fatal(out.String())
	}
	if strings.Index(out.String(), "step restore-models") > strings.Index(out.String(), "step load") {
		t.Fatal("the models must be back before the database changes")
	}
}

func TestRestoreLeavesAMatchingModelAndReplacesADamagedOne(t *testing.T) {
	c := installedConfig(t)
	withPublishedModel(t, c)
	path := makeBackup(t, c)
	out := &bytes.Buffer{}
	if e := restoreFrom(t, c, &fakeDatabase{}, path, out); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "step restore-models: ok restored=0") {
		t.Fatal(out.String())
	}
	target := filepath.Join(c.InstallRoot, publishedModel)
	if e := os.Chmod(target, 0o644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(target, []byte("damaged"), 0o644); e != nil {
		t.Fatal(e)
	}
	out.Reset()
	if e := restoreFrom(t, c, &fakeDatabase{}, path, out); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(target)
	if e != nil || string(raw) != "weights" {
		t.Fatalf("%q %v", raw, e)
	}
	if !strings.Contains(out.String(), "step restore-models: ok restored=1") {
		t.Fatal(out.String())
	}
}

func TestRestoreLeavesTheDatabaseAloneWhenAModelCannotBePutBack(t *testing.T) {
	c := installedConfig(t)
	withPublishedModel(t, c)
	path := makeBackup(t, c)
	objects := filepath.Join(c.Layout().Models, "objects")
	if e := os.RemoveAll(objects); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(objects, []byte("not a directory"), 0o600); e != nil {
		t.Fatal(e)
	}
	d := &fakeDatabase{}
	e := restoreFrom(t, c, d, path, &bytes.Buffer{})
	if e == nil || !strings.Contains(e.Error(), "INSTALL_RESTORE_MODELS_FAILED") {
		t.Fatal(e)
	}
	if d.ran("pg_restore") {
		t.Fatal("the database must not change when the models cannot be put back")
	}
}

func TestBackupFailureLeavesNothingBehind(t *testing.T) {
	for name, failing := range map[string]string{"dump": "pg_dump", "verification": "pg_restore", "fingerprint": "UNION ALL", "schema version": "max(version)"} {
		t.Run(name, func(t *testing.T) {
			c := installedConfig(t)
			d := &fakeDatabase{override: func(line string) (string, error, bool) {
				if strings.Contains(line, failing) {
					return "", errors.New("exit status 1"), true
				}
				return "", nil, false
			}}
			output := t.TempDir()
			_, e := testOperation(t, c, d, &bytes.Buffer{}).backup(context.Background(), output)
			if e == nil || !strings.Contains(e.Error(), "INSTALL_BACKUP_") || !strings.Contains(e.Error(), "database said no") {
				t.Fatalf("%v", e)
			}
			entries, _ := os.ReadDir(output)
			if len(entries) != 0 {
				t.Fatalf("left behind: %v", entries)
			}
			if failing != "pg_dump" && !d.ran("DROP DATABASE") {
				t.Fatal("the scratch database was left behind")
			}
		})
	}
}

func TestBackupRefusesAnEmptyDump(t *testing.T) {
	d := &fakeDatabase{override: func(line string) (string, error, bool) { return "", nil, strings.Contains(line, "pg_dump") }}
	_, e := testOperation(t, installedConfig(t), d, &bytes.Buffer{}).backup(context.Background(), t.TempDir())
	if e == nil || !strings.Contains(e.Error(), "INSTALL_BACKUP_DUMP_FAILED") {
		t.Fatalf("%v", e)
	}
}

func TestBackupRefusesToOverwrite(t *testing.T) {
	c := installedConfig(t)
	path := makeBackup(t, c)
	_, e := testOperation(t, c, &fakeDatabase{}, &bytes.Buffer{}).backup(context.Background(), filepath.Dir(path))
	if e == nil || !strings.Contains(e.Error(), "INSTALL_BACKUP_EXISTS") {
		t.Fatalf("%v", e)
	}
}

func TestBackupOutputMustBeOutsideTheInstallation(t *testing.T) {
	c := validConfig(t)
	outside := t.TempDir()
	if e := admitBackupOutput(c, outside); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(t.TempDir(), "link")
	if e := os.Symlink(outside, link); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"relative", outside + "/../" + filepath.Base(outside), filepath.Join(outside, "missing"), link, c.InstallRoot, filepath.Join(c.InstallRoot, "backups")} {
		if e := os.MkdirAll(c.InstallRoot, 0o700); e != nil {
			t.Fatal(e)
		}
		if e := admitBackupOutput(c, bad); e == nil || !strings.Contains(e.Error(), "INSTALL_BACKUP_OUTPUT_INVALID") {
			t.Errorf("%s: %v", bad, e)
		}
	}
}

func TestOnlyOneOperationAtATime(t *testing.T) {
	c := validConfig(t)
	unlock, e := lockOperation(c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = lockOperation(c); e == nil || !strings.Contains(e.Error(), "INSTALL_BUSY") {
		t.Fatalf("%v", e)
	}
	unlock()
	again, e := lockOperation(c)
	if e != nil {
		t.Fatal(e)
	}
	again()
}

func editManifest(t *testing.T, path string, edit func(map[string]any)) {
	t.Helper()
	file := filepath.Join(path, backupManifestName)
	raw, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	document := map[string]any{}
	if e = json.Unmarshal(raw, &document); e != nil {
		t.Fatal(e)
	}
	edit(document)
	if raw, e = json.Marshal(document); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, raw, 0o600); e != nil {
		t.Fatal(e)
	}
}

func TestReadBackupRefusesAnythingChanged(t *testing.T) {
	c := installedConfig(t)
	for name, test := range map[string]struct {
		change func(t *testing.T, path string)
		want   string
	}{
		"tampered dump": {func(t *testing.T, path string) {
			os.WriteFile(filepath.Join(path, backupDatabaseName), []byte("DUMP BYTEZ"), 0o600)
		}, "INSTALL_BACKUP_CORRUPT"},
		"truncated dump": {func(t *testing.T, path string) {
			os.WriteFile(filepath.Join(path, backupDatabaseName), []byte("DUMP"), 0o600)
		}, "INSTALL_BACKUP_CORRUPT"},
		"missing dump": {func(t *testing.T, path string) { os.Remove(filepath.Join(path, backupDatabaseName)) }, "INSTALL_BACKUP_CORRUPT"},
		"tampered key": {func(t *testing.T, path string) {
			os.WriteFile(filepath.Join(path, backupFilesName, "config", "auth-hmac"), []byte("x"), 0o600)
		}, "INSTALL_BACKUP_CORRUPT"},
		"no manifest": {func(t *testing.T, path string) { os.Remove(filepath.Join(path, backupManifestName)) }, "INSTALL_BACKUP_INVALID"},
		"later format": {func(t *testing.T, path string) {
			editManifest(t, path, func(m map[string]any) { m["format_version"] = 2 })
		}, "INSTALL_BACKUP_INVALID"},
		"unknown field": {func(t *testing.T, path string) { editManifest(t, path, func(m map[string]any) { m["extra"] = true }) }, "INSTALL_BACKUP_INVALID"},
		"no tables":     {func(t *testing.T, path string) { editManifest(t, path, func(m map[string]any) { m["tables"] = nil }) }, "INSTALL_BACKUP_INVALID"},
		"escaping path": {func(t *testing.T, path string) {
			editManifest(t, path, func(m map[string]any) { m["files"].([]any)[0].(map[string]any)["path"] = "../../etc/passwd" })
		}, "leaves the backup"},
	} {
		t.Run(name, func(t *testing.T) {
			path := makeBackup(t, c)
			test.change(t, path)
			if _, e := readBackup(path); e == nil || !strings.Contains(e.Error(), test.want) {
				t.Fatalf("%v", e)
			}
		})
	}
	if _, e := readBackup("relative"); e == nil || !strings.Contains(e.Error(), "INSTALL_BACKUP_INVALID") {
		t.Fatalf("%v", e)
	}
}

func restoreFrom(t *testing.T, c Config, d *fakeDatabase, path string, out *bytes.Buffer) error {
	t.Helper()
	manifest, e := readBackup(path)
	if e != nil {
		t.Fatal(e)
	}
	return testOperation(t, c, d, out).restore(context.Background(), path, manifest)
}

const (
	renameIn   = `RENAME TO "ownsolid_before_restore_20261104t080000z"; ALTER DATABASE "ownsolid_restore_20261104t080000z" RENAME TO "ownsolid"`
	renameBack = `"ownsolid" RENAME TO "ownsolid_restore_20261104t080000z"; ALTER DATABASE "ownsolid_before_restore_20261104t080000z" RENAME TO "ownsolid"`
)

func TestRestoreChecksSwapsThenRevokesAndServes(t *testing.T) {
	c := installedConfig(t)
	path := makeBackup(t, c)
	d := &fakeDatabase{}
	var out bytes.Buffer
	if e := restoreFrom(t, c, d, path, &out); e != nil {
		t.Fatal(e)
	}
	if d.loaded != "DUMP BYTES" {
		t.Fatalf("restored %q", d.loaded)
	}
	order := []string{"stop control", "pg_restore", "UNION ALL", renameIn, " migrate ", "prepare-runtime-access", "check-schema", "invalidate-restored-auth", "ownsolid.restore_adoption", "up --detach --wait --no-build control"}
	for i := 1; i < len(order); i++ {
		if d.position(t, order[i-1]) > d.position(t, order[i]) {
			t.Fatalf("%q ran after %q:\n%s", order[i-1], order[i], strings.Join(d.commands, "\n"))
		}
	}
	if d.ran("DROP DATABASE") {
		t.Fatal("the database from before the restore must be kept")
	}
	if !strings.Contains(out.String(), "RESTORE_COMPLETE previous_database=ownsolid_before_restore_20261104t080000z") {
		t.Fatal(out.String())
	}
}

func TestRestoreLeavesTheCurrentDatabaseAloneWhenTheBackupFailsToLoad(t *testing.T) {
	for name, override := range map[string]func(string) (string, error, bool){
		"load fails": func(line string) (string, error, bool) {
			return "", errors.New("exit status 1"), strings.Contains(line, "pg_restore")
		},
		"content differs": func(line string) (string, error, bool) {
			return "tenants|1|5\nusers|2|10\n", nil, strings.Contains(line, "UNION ALL")
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := installedConfig(t)
			path := makeBackup(t, c)
			d := &fakeDatabase{override: override}
			e := restoreFrom(t, c, d, path, &bytes.Buffer{})
			if e == nil || !strings.Contains(e.Error(), "INSTALL_RESTORE_") {
				t.Fatalf("%v", e)
			}
			if d.ran("RENAME TO") {
				t.Fatal("the current database was touched")
			}
			if !d.ran(`DROP DATABASE IF EXISTS "ownsolid_restore_20261104t080000z"`) {
				t.Fatal("the incoming database was left behind")
			}
			if d.position(t, "stop control") > d.position(t, "up --detach --wait --no-build control") {
				t.Fatal("control was not started again")
			}
		})
	}
}

func TestRestoreRollsBackWhenTheRestoredDatabaseFailsItsChecks(t *testing.T) {
	c := installedConfig(t)
	path := makeBackup(t, c)
	d := &fakeDatabase{}
	checks := 0
	d.answer = func(line string) (string, error) {
		if strings.Contains(line, "check-schema") {
			checks++
			return "SCHEMA_CATALOG_INCOMPATIBLE", errors.New("exit status 1")
		}
		return successOutput(line), nil
	}
	var out bytes.Buffer
	e := restoreFrom(t, c, d, path, &out)
	if e == nil || !strings.Contains(e.Error(), "check-schema") || !strings.Contains(e.Error(), "SCHEMA_CATALOG_INCOMPATIBLE") {
		t.Fatalf("%v", e)
	}
	if checks != 1 || d.ran("invalidate-restored-auth") {
		t.Fatal("the failed database was used after its check failed")
	}
	if d.position(t, renameIn) > d.position(t, renameBack) {
		t.Fatalf("order:\n%s", strings.Join(d.commands, "\n"))
	}
	if !d.ran(`DROP DATABASE IF EXISTS "ownsolid_restore_20261104t080000z"`) {
		t.Fatal("the failed database was left behind")
	}
	if strings.Contains(out.String(), "RESTORE_COMPLETE") || !strings.Contains(out.String(), "step roll-back") {
		t.Fatal(out.String())
	}
	if d.position(t, renameBack) > lastPosition(d, "up --detach --wait --no-build control") {
		t.Fatal("control was not started on the previous database")
	}
}

func lastPosition(d *fakeDatabase, part string) int {
	last := -1
	for i, line := range d.commands {
		if strings.Contains(line, part) {
			last = i
		}
	}
	return last
}

func TestRestoreReportsAFailedRollBackWithTheDatabaseName(t *testing.T) {
	c := installedConfig(t)
	path := makeBackup(t, c)
	d := &fakeDatabase{override: func(line string) (string, error, bool) {
		return "", errors.New("exit status 1"), strings.Contains(line, renameBack)
	}}
	d.answer = func(line string) (string, error) {
		if strings.Contains(line, "check-schema") {
			return "", errors.New("exit status 1")
		}
		return successOutput(line), nil
	}
	o := testOperation(t, c, d, &bytes.Buffer{})
	manifest, e := readBackup(path)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e = o.restore(ctx, path, manifest)
	if e == nil || !strings.Contains(e.Error(), "still named ownsolid_before_restore_20261104t080000z") {
		t.Fatalf("%v", e)
	}
}

func TestRestoreRefusesDifferentKeysBeforeTouchingAnything(t *testing.T) {
	c := installedConfig(t)
	path := makeBackup(t, c)
	if e := os.WriteFile(filepath.Join(c.Layout().Config, "mail-keyring.json"), []byte(`{"active":"k2"}`), 0o600); e != nil {
		t.Fatal(e)
	}
	d := &fakeDatabase{}
	e := restoreFrom(t, c, d, path, &bytes.Buffer{})
	if e == nil || !strings.Contains(e.Error(), "INSTALL_RESTORE_KEYS_DIFFER") {
		t.Fatalf("%v", e)
	}
	if len(d.commands) != 0 {
		t.Fatalf("ran: %v", d.commands)
	}
}

func TestRestoreOntoAnEmptyInstallationBringsBackItsFilesAndRoles(t *testing.T) {
	c := installedConfig(t)
	keyring, e := os.ReadFile(filepath.Join(c.Layout().Config, "mail-keyring.json"))
	if e != nil {
		t.Fatal(e)
	}
	password, e := os.ReadFile(filepath.Join(c.Layout().Secrets, "postgres-password"))
	if e != nil {
		t.Fatal(e)
	}
	path := makeBackup(t, c)
	if e = os.RemoveAll(c.InstallRoot); e != nil {
		t.Fatal(e)
	}
	d := &fakeDatabase{override: func(line string) (string, error, bool) { return "0\n", nil, strings.Contains(line, "pg_roles") }}
	if e = restoreFrom(t, c, d, path, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	restored, e := os.ReadFile(filepath.Join(c.Layout().Config, "mail-keyring.json"))
	if e != nil || !bytes.Equal(restored, keyring) {
		t.Fatalf("keyring not restored: %v", e)
	}
	if restored, e = os.ReadFile(filepath.Join(c.Layout().Secrets, "postgres-password")); e != nil || !bytes.Equal(restored, password) {
		t.Fatalf("password not restored: %v", e)
	}
	st, e := os.Stat(filepath.Join(c.Layout().Config, "mail-keyring.json"))
	if e != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", st, e)
	}
	if d.position(t, "prepare-runtime-access") > d.position(t, "pg_restore") {
		t.Fatal("the roles must exist before the dump grants to them")
	}
}

func TestRestoreWorksOnADatabaseTooDamagedToMigrate(t *testing.T) {
	c := installedConfig(t)
	path := makeBackup(t, c)
	d := &fakeDatabase{}
	if e := restoreFrom(t, c, d, path, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	if d.position(t, renameIn) > d.position(t, " migrate ") {
		t.Fatal("restore migrated the damaged database before replacing it")
	}
}

func TestSwapRetriesWhileASessionHoldsTheDatabase(t *testing.T) {
	attempts := 0
	d := &fakeDatabase{override: func(line string) (string, error, bool) {
		if !strings.Contains(line, "RENAME TO") {
			return "", nil, false
		}
		attempts++
		if attempts < 3 {
			return "", errors.New("exit status 1"), true
		}
		return "", nil, true
	}}
	o := testOperation(t, validConfig(t), d, &bytes.Buffer{})
	if e := o.swap(context.Background(), "a", "b", "c"); e != nil || attempts != 3 {
		t.Fatalf("%d %v", attempts, e)
	}
}

func TestFingerprintRefusesAnUnexpectedTableName(t *testing.T) {
	d := &fakeDatabase{override: func(line string) (string, error, bool) {
		return `users"; DROP TABLE x; --` + "\n", nil, strings.Contains(line, "SELECT c.relname")
	}}
	_, e := testOperation(t, validConfig(t), d, &bytes.Buffer{}).fingerprint(context.Background(), "ownsolid")
	if e == nil || d.ran("UNION ALL") {
		t.Fatalf("%v", e)
	}
}

func TestStepErrorsNameTheCommandToRunAgain(t *testing.T) {
	e := retryWith(errors.Join(&StepError{Step: "load", Code: "INSTALL_RESTORE_LOAD_FAILED"}, errors.New("control did not start again")), "restore")
	if !strings.Contains(e.Error(), "run restore again") {
		t.Fatal(e)
	}
	if e = (&StepError{Step: "schema", Code: "X"}); !strings.Contains(e.Error(), "run apply again") {
		t.Fatal(e)
	}
}
