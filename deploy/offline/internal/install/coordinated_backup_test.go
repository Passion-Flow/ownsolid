package install

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	license "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	"go.yaml.in/yaml/v3"
)

func TestCoordinatedBackupTracksShippedCompose(t *testing.T) {
	compose, err := os.ReadFile("../../../compose/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(compose)) != coordinatedComposeSHA256 {
		t.Fatal("review the coordinated backup writer inventory before admitting the updated Compose profile")
	}
}

func assertCoordinatedBackupIsolatedDocker(t *testing.T, installer, config string, licenseKey license.PrivateKey, run func(string, ...string) (string, error)) {
	t.Helper()
	c, err := LoadOperational(config)
	if err != nil {
		t.Fatal(err)
	}
	o := newOperation(c, ExecRunner{}, io.Discard)
	query := func(t *testing.T, database, statement string) string {
		t.Helper()
		out, err := o.query(t.Context(), database, statement)
		if err != nil {
			t.Fatalf("PostgreSQL: %v", err)
		}
		return out
	}
	read := func(t *testing.T, path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	write := func(t *testing.T, path string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	siteFile := func(t *testing.T, c Config) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "site.yaml")
		raw, err := yaml.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		write(t, path, raw)
		return path
	}
	privateDir := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	check := func(t *testing.T, path, want string, failed bool, args ...string) {
		t.Helper()
		out, err := run(installer, append(args, "--config", path)...)
		t.Logf("%s:\n%s", args[0], out)
		if (err != nil) != failed || !strings.Contains(out, want) {
			t.Fatalf("want %s (failure=%t), got %v", want, failed, err)
		}
	}
	credential := read(t, c.CredentialPath())
	admin := installedAdministrator(t, o)

	const marker = "Coordinated backup point"
	query(t, databaseName, "UPDATE tenants SET name='"+marker+"' WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)")
	query(t, maintenanceDatabase, "ALTER DATABASE ownsolid CONNECTION LIMIT 23")
	original, running, err := o.coordinatedControl(t.Context())
	if err != nil || !running {
		t.Fatalf("source control was not running: %v", err)
	}
	db, err := o.coordinatedDB(t.Context())
	if err != nil || db.Limit != 23 {
		t.Fatalf("source connection limit was not set: %+v, %v", db, err)
	}
	assertOriginal := func(t *testing.T) {
		t.Helper()
		actual, err := o.coordinatedDB(t.Context())
		id, up, containerErr := o.coordinatedControl(t.Context())
		if err != nil || actual != db || containerErr != nil || id != original || !up || coordinatedBackupPending(c) != nil {
			t.Fatalf("the exact prior database and control state was not restored: %v %v", err, containerErr)
		}
		if err := o.health(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	assertNoBackup := func(t *testing.T, dir string) {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("failure published or left a completed backup: %v: %v", entries, err)
		}
	}

	t.Run("refuses uncovered object state before quiescing", func(t *testing.T) {
		output := privateDir(t)
		remote := c
		remote.ControlSettings = map[string]any{"object_storage": map[string]any{"enabled": true}}
		check(t, siteFile(t, remote), "INSTALL_BACKUP_PROFILE_UNSUPPORTED", true, "backup-coordinated", "--output", output)
		assertOriginal(t)

		id := coldRestoreUUID(t)
		query(t, databaseName, fmt.Sprintf(`INSERT INTO artifact_objects
			(id,tenant_id,workspace_id,created_by,requested_audience,requested_epoch,execution_epoch,creation_key,fingerprint,
			store_id,name,purpose,expected_bytes,sha256,part_bytes,state,remote_etag,upload_expires_at,expires_at)
			SELECT '%s',tenant_id,default_workspace_id,primary_admin_user_id,'admin',1,1,gen_random_uuid(),decode(repeat('00',32),'hex'),
			gen_random_uuid(),'Backup object fixture','model_bundle',1,repeat('a',64),5242880,'ready','fixture',now()+interval '1 hour',now()+interval '2 hours'
			FROM installation_state WHERE id=1`, id))
		check(t, config, "INSTALL_BACKUP_PROFILE_UNSUPPORTED", true, "backup-coordinated", "--output", output)
		if got := query(t, databaseName, "SELECT remote_etag FROM artifact_objects WHERE id='"+id+"'"); got != "fixture" {
			t.Fatal("refusal changed the original object record")
		}
		query(t, databaseName, "DELETE FROM artifact_objects WHERE id='"+id+"'")
		assertOriginal(t)
		assertNoBackup(t, output)
	})
	if t.Failed() {
		return
	}
	t.Run("detects a protected file rewrite and restores service", func(t *testing.T) {
		output := privateDir(t)
		path := filepath.Join(c.Layout().Config, "backup-race-fixture")
		write(t, path, []byte("fixture"))
		t.Run("rewrite with original bytes and mtime", func(t *testing.T) {
			interruptInstallerDocker(t, "before-dump-file-change")
			wrapper := filepath.SplitList(os.Getenv("PATH"))[0]
			write(t, filepath.Join(wrapper, "change-file"), []byte(path))
			check(t, config, "INSTALL_BACKUP_FILES_CHANGED", true, "backup-coordinated", "--output", output)
		})
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		assertOriginal(t)
		assertNoBackup(t, output)
	})
	if t.Failed() {
		return
	}
	t.Run("blocks database writes after a killed installer and recovers", func(t *testing.T) {
		output := privateDir(t)
		t.Run("kill after the fence and before the dump", func(t *testing.T) {
			interruptInstallerDocker(t, "before-dump")
			check(t, config, "step backup-fence: ok", true, "backup-coordinated", "--output", output)
		})
		closed, err := o.coordinatedDB(t.Context())
		id, running, containerErr := o.coordinatedControl(t.Context())
		if err != nil || closed.Limit != 0 || containerErr != nil || running || id != original || coordinatedBackupPending(c) == nil {
			t.Fatalf("the durable fence was lost: %v %v", err, containerErr)
		}
		// Use PostgreSQL's own local connection path. The installer does not
		// get a chance to reject this request on behalf of the runtime role.
		result, err := run("docker", composeArguments(c, "exec", "--no-TTY", "postgres", "psql", "-X", "-U", runtimeRole, "-d", databaseName,
			"-v", "ON_ERROR_STOP=1", "-c", "UPDATE tenants SET name='write during backup'")...)
		if err == nil || !strings.Contains(result, "too many connections for database") {
			t.Fatalf("PostgreSQL admitted the injected writer: %v: %s", err, result)
		}
		t.Log("PostgreSQL itself refused the injected runtime connection")
		check(t, config, "INSTALL_BACKUP_RECOVERY_REQUIRED", true, "backup", "--output", output)
		check(t, config, "BACKUP_RECOVERED", false, "recover-backup")
		check(t, config, "BACKUP_NO_RECOVERY_PENDING", false, "recover-backup")
		assertOriginal(t)
	})
	if t.Failed() {
		return
	}
	workspace := query(t, databaseName, "SELECT default_workspace_id FROM installation_state WHERE id=1")
	modelFile := filepath.Join(os.Getenv("OWNSOLID_E2E_MODEL_ARCHIVE"), "model.tar")
	if _, err := os.Stat(modelFile); err != nil {
		t.Fatal("the coordinated drill needs the pinned model.tar")
	}
	model := publishColdRestoreModel(t, coldRestoreLogin(t, c, c.Origins.Admin, credential), licenseKey, workspace, modelFile)
	t.Run("retries and reads the verified database, keys and model back on an empty site", func(t *testing.T) {
		output := privateDir(t)
		check(t, config, "BACKUP_COMPLETE", false, "backup-coordinated", "--output", output)
		assertOriginal(t)
		backups, err := filepath.Glob(filepath.Join(output, backupPrefix+"*"))
		if err != nil || len(backups) != 1 {
			t.Fatalf("expected one verified backup, got %v: %v", backups, err)
		}
		backup := backups[0]
		manifest, err := readBackup(backup)
		if err != nil || len(manifest.Tables) == 0 {
			t.Fatalf("coordinated backup readback: %v", err)
		}
		if out, err := run("docker", composeArguments(c, "down")...); err != nil {
			t.Fatalf("stop source: %v: %s", err, out)
		}
		target := c
		target.InstallRoot = filepath.Join(filepath.Dir(c.InstallRoot), "recovered")
		target.Project = c.Project + "-rel103-recovered"
		target.Images.ArchiveFile = ""
		path := siteFile(t, target)
		check(t, path, "RESTORE_COMPLETE", false, "restore", "--from", backup)
		restored := newOperation(target, ExecRunner{}, io.Discard)
		if got, err := restored.query(t.Context(), databaseName, "SELECT name FROM tenants WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)"); err != nil || got != marker {
			t.Fatalf("the restored database did not preserve the backup point: %v", err)
		}
		if installedAdministrator(t, restored) != admin || string(read(t, target.CredentialPath())) != string(credential) {
			t.Fatal("administrator identity or key material changed")
		}
		for _, key := range keyFiles {
			for _, file := range manifest.Files {
				if file.Path == key {
					if err := verifyBackupFile(target.InstallRoot, file, key); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		authenticated := coldRestoreLogin(t, target, target.Origins.Admin, credential)
		coldRestoreLogin(t, target, target.Origins.Portal, credential)
		assertColdRestoreModel(t, authenticated, target, model)
		if out, err := run("docker", composeArguments(target, "down")...); err != nil {
			t.Fatalf("stop restored site: %v: %s", err, out)
		}
	})
}

func TestPendingCoordinatedBackupBlocksInstallerOperations(t *testing.T) {
	c := validConfig(t)
	if err := os.MkdirAll(c.InstallRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.InstallRoot, coordinatedBackupIntentName), []byte("record"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockOperation(c)
	if unlock != nil {
		unlock()
	}
	if !errors.Is(err, errBackupPending) {
		t.Fatalf("expected backup recovery gate: %v", err)
	}
	unlock, err = lockRestoreOperation(c)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
