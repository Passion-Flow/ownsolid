package main

import (
	"flag"
	"io"
	"os"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/mysqlbackup"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func nativeFlagFixture(t *testing.T, command string, args ...string) error {
	t.Helper()
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	c := f.String("config", "", "")
	runtime := f.String("runtime-config", "", "")
	backup := f.String("backup-root", "", "")
	restore := f.String("restore-context", "", "")
	reason := f.String("reason", "", "")
	isolated := f.Bool("confirm-isolated-mysql-restore", false, "")
	f.Bool("foreign", false, "")
	if e := f.Parse(args); e != nil {
		return e
	}
	return validateMySQLNativeFlags(f, command, *c, *runtime, *backup, *restore, *reason, *isolated)
}
func TestNativeMySQLCLIUsesExplicitIsolatedTrueAndExactOwnedFlags(t *testing.T) {
	for _, c := range []struct {
		command string
		args    []string
	}{
		{mysqlbackup.BackupCommand, []string{"--config=/synthetic/operator", "--backup-root=/synthetic/new-backup", "--reason=Synthetic explicit backup"}},
		{mysqlbackup.ReconcileBackupCommand, []string{"--config=/synthetic/operator", "--backup-root=/synthetic/backup"}},
		{mysqlbackup.RestoreCommand, []string{"--config=/synthetic/operator", "--backup-root=/synthetic/backup", "--restore-context=/synthetic/restore", "--reason=Synthetic isolated restore", "--confirm-isolated-mysql-restore=true"}},
		{mysqlbackup.ReconcileRestoreCommand, []string{"--config=/synthetic/operator", "--restore-context=/synthetic/restore", "--confirm-isolated-mysql-restore=true"}},
		{databaseaccess.CheckRestoredMySQLCommand, []string{"--config=/synthetic/operator", "--runtime-config=/synthetic/runtime", "--restore-context=/synthetic/restore"}},
	} {
		if e := nativeFlagFixture(t, c.command, c.args...); e != nil {
			t.Fatal("valid explicit arguments", c.command, e)
		}
		if e := nativeFlagFixture(t, c.command, append(append([]string{}, c.args...), "--foreign=false")...); e == nil {
			t.Fatal("foreign false argument accepted", c.command)
		}
	}
	for _, args := range [][]string{
		{"--config=/synthetic/operator", "--backup-root=/synthetic/backup", "--restore-context=/synthetic/restore", "--reason=Synthetic isolated restore"},
		{"--config=/synthetic/operator", "--backup-root=/synthetic/backup", "--restore-context=/synthetic/restore", "--reason=Synthetic isolated restore", "--confirm-isolated-mysql-restore=false"},
		{"--config=/synthetic/operator", "--backup-root=/synthetic/backup", "--restore-context=/synthetic/restore", "--reason=", "--confirm-isolated-mysql-restore=true"},
	} {
		if nativeFlagFixture(t, mysqlbackup.RestoreCommand, args...) == nil {
			t.Fatal("implicit/false/incomplete restore accepted")
		}
	}
	if nativeFlagFixture(t, "serve", "--confirm-isolated-mysql-restore=false") == nil {
		t.Fatal("foreign explicit false restore flag ignored")
	}
}
func TestNativeMySQLRunRefusesBeforeAnyConfigurationOrDatabaseAccess(t *testing.T) {
	original := os.Args
	defer func() { os.Args = original }()
	os.Args = []string{"control", mysqlbackup.RestoreCommand, "--config=/nonexistent/synthetic-config", "--backup-root=/nonexistent/backup", "--restore-context=/nonexistent/restore", "--reason=Synthetic isolated restore", "--confirm-isolated-mysql-restore=false"}
	if e := run(); e == nil || e.Error() != "MYSQL_NATIVE_OPERATOR_ARGUMENT_INVALID" {
		t.Fatal("invalid target reached configuration/database", e)
	}
	var absent *selectedDatabase
	if status, e := absent.nativeMySQLCommand(t.Context(), config.Config{}, mysqlbackup.RestoreCommand, "", "", "", true); e == nil || status != "" {
		t.Fatal("missing database projected actual restore", e)
	}
	// Parser checks above are synthetic only; no successful native/SQL CLI run is
	// inferred from flags or a source-selected complete catalog count.
}
