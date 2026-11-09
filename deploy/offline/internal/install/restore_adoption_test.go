package install

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoreRollsBackBeforeServingWhenAdoptionFails(t *testing.T) {
	c := installedConfig(t)
	path := makeBackup(t, c)
	d := &fakeDatabase{override: func(line string) (string, error, bool) {
		return "database setting write failed", errors.New("exit status 1"), strings.Contains(line, "ALTER DATABASE %I SET "+restoreAdoptionSetting)
	}}
	var out bytes.Buffer
	err := restoreFrom(t, c, d, path, &out)
	if err == nil || !strings.Contains(err.Error(), "INSTALL_RESTORE_ADOPTION_FAILED") {
		t.Fatalf("restore returned %v", err)
	}
	if d.position(t, renameBack) > d.position(t, "up --detach --wait --no-build control") {
		t.Fatal("control started before the previous database was restored")
	}
	if strings.Contains(out.String(), "RESTORE_COMPLETE") {
		t.Fatal("failed adoption reported a complete restore")
	}
}

func TestApplyStopsBeforeBootstrapWhenRestoreStateIsUnavailable(t *testing.T) {
	c := validConfig(t)
	runner := &fakeRunner{answer: func(line string) (string, error) {
		if strings.Contains(line, "pg_catalog.pg_db_role_setting") {
			return "", errors.New("exit status 1")
		}
		return successOutput(line), nil
	}}
	err := testInstaller(c, runner, &bytes.Buffer{}).administrator(context.Background())
	if err == nil || !strings.Contains(err.Error(), "INSTALL_RESTORE_ADOPTION_CHECK_FAILED") {
		t.Fatalf("administrator returned %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatal("ran a bootstrap command without verifying the restore state")
	}
}

func assertApplyAfterRestore(t *testing.T, c Config, installer, config string, run func(string, ...string) (string, error)) {
	t.Helper()
	op := newOperation(c, ExecRunner{}, io.Discard)
	query := func(sql string) string {
		t.Helper()
		output, err := op.query(t.Context(), databaseName, sql)
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		return output
	}
	adopted := func(want bool) {
		t.Helper()
		got, err := op.hasRestoreAdoption(t.Context())
		if err != nil || got != want {
			t.Fatalf("database adoption = %t, want %t: %v", got, want, err)
		}
	}
	const physicalDatabase = "SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()"
	before := query(physicalDatabase)
	administrator := installedAdministrator(t, op)
	credential, err := os.ReadFile(c.CredentialPath())
	if err != nil {
		t.Fatal(err)
	}
	binding := query("SELECT " + restoreAdoptionBinding)
	query("ALTER ROLE " + migratorRole + " IN DATABASE " + databaseName +
		" SET " + restoreAdoptionSetting + " TO '" + binding + "'")
	adopted(false)
	query("ALTER ROLE " + migratorRole + " IN DATABASE " + databaseName + " RESET " + restoreAdoptionSetting)
	t.Log("a matching role default could not impersonate a completed restore")

	backups := t.TempDir()
	out, err := run(installer, "backup", "--config", config, "--output", backups)
	t.Logf("backup:\n%s", out)
	if err != nil || !strings.Contains(out, "step verify: ok") {
		t.Fatalf("backup was not verified: %v", err)
	}
	entries, err := os.ReadDir(backups)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one verified backup: %v", err)
	}
	out, err = run(installer, "restore", "--config", config, "--from", filepath.Join(backups, entries[0].Name()))
	t.Logf("restore:\n%s", out)
	if err != nil || !strings.Contains(out, "RESTORE_COMPLETE") {
		t.Fatalf("restore failed: %v", err)
	}
	if after := query(physicalDatabase); before == "" || after == "" || after == before {
		t.Fatal("restore did not switch to a new database OID")
	}
	adopted(true)
	query("ALTER DATABASE " + databaseName + " SET " + restoreAdoptionSetting + " TO '" + binding + "'")
	adopted(false)
	if err := op.recordRestoreAdoption(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	adopted(true)
	t.Log("the previous physical identity could not impersonate the adopted database")

	for range 2 {
		out, err := run(installer, "apply", "--config", config)
		t.Logf("apply after restore:\n%s", out)
		if err != nil || !strings.Contains(out, "BOOTSTRAP_RESTORED_VERIFIED") || !strings.Contains(out, "INSTALL_COMPLETE") {
			t.Fatalf("apply after restore failed: %v", err)
		}
	}
	out, err = run(installer, "status", "--config", config)
	t.Logf("status after restore:\n%s", out)
	if err != nil || !strings.Contains(out, c.Origins.Admin+": ready") || !strings.Contains(out, c.Origins.Portal+": ready") {
		t.Fatalf("both entrances must be ready after repeated apply: %v", err)
	}
	if after := installedAdministrator(t, op); after != administrator {
		t.Fatal("restore or apply changed the administrator, tenant or workspace")
	}
	if after, err := os.ReadFile(c.CredentialPath()); err != nil || !bytes.Equal(after, credential) {
		t.Fatal("restore or apply changed the retained administrator credential")
	}
	t.Log("the PostgreSQL database OID changed and the administrator, tenant, workspace and credential bytes matched")
}
