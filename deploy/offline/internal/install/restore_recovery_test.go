package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRestoreSwapReconcilesACommittedRenameWithNoReply(t *testing.T) {
	i := restoreIntent{Cluster: 80, Original: 10, Incoming: 20, Suffix: "0123456789abcdef01234567"}
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%t", reverse), func(t *testing.T) {
			d := &fakeDatabase{}
			attempts := 0
			d.override = func(line string) (string, error, bool) {
				if !strings.Contains(line, "pg_advisory_xact_lock") {
					return "", nil, false
				}
				attempts++
				d.catalog = i.swapped().key()
				if reverse {
					d.catalog = i.rolledBack().key()
				}
				return "", errors.New("lost reply"), true
			}
			o := testOperation(t, validConfig(t), d, nil)
			if err := o.renameForRestore(context.Background(), i, reverse); err != nil || attempts != 1 {
				t.Fatalf("rename attempts=%d, err=%v", attempts, err)
			}
		})
	}
}

func TestPendingRestoreBlocksOtherInstallOperations(t *testing.T) {
	c := validConfig(t)
	if err := os.MkdirAll(c.InstallRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.InstallRoot, restoreIntentName)
	if err := os.Symlink("missing-record", path); err != nil {
		t.Fatal(err)
	}
	if _, err := lockOperation(c); err == nil || !strings.Contains(err.Error(), "INSTALL_RESTORE_PENDING") {
		t.Fatalf("mutating lock accepted an unsafe restore record: %v", err)
	}
	if unlock, err := lockRestoreOperation(c); err != nil {
		t.Fatal(err)
	} else {
		defer unlock()
	}
	if _, err := readRestoreIntent(c); err == nil || !strings.Contains(err.Error(), "INSTALL_RESTORE_INTENT_INVALID") {
		t.Fatalf("recovery accepted an unsafe restore record: %v", err)
	}
}

func assertInterruptedRestores(t *testing.T, installer, config string, run func(string, ...string) (string, error)) {
	t.Helper()
	c, err := LoadOperational(config)
	if err != nil {
		t.Fatal(err)
	}
	o := newOperation(c, ExecRunner{}, io.Discard)
	query := func(t *testing.T, database, sql string) string {
		t.Helper()
		out, err := o.query(t.Context(), database, sql)
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		return out
	}
	checkedCommand := func(t *testing.T, action, want string, fail bool, extra ...string) string {
		t.Helper()
		out, err := run(installer, append([]string{action, "--config", config}, extra...)...)
		t.Logf("%s:\n%s", action, out)
		if (err != nil) != fail || !strings.Contains(out, want) {
			t.Fatalf("wanted %s (failure=%t), got %v", want, fail, err)
		}
		return out
	}
	const name = "SELECT name FROM tenants WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)"
	writeName := func(t *testing.T, value string) {
		t.Helper()
		query(t, databaseName, "UPDATE tenants SET name='"+value+"' WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)")
	}
	writeName(t, "Backed up before interruption")
	backups := t.TempDir()
	checkedCommand(t, "backup", "step verify: ok", false, "--output", backups)
	dirs, err := os.ReadDir(backups)
	if err != nil || len(dirs) != 1 {
		t.Fatalf("verified backup directory: %v (%d entries)", err, len(dirs))
	}
	backup := filepath.Join(backups, dirs[0].Name())
	for _, test := range []struct {
		name, checkpoint, phase, placement string
		rollback                           bool
	}{
		{"before exchange", "before-swap", restorePending, "before", false},
		{"after exchange committed", "after-swap", restorePending, "swapped", false},
		{"after rollback committed", "after-rollback", restoreRollback, "rolled-back", false},
		{"after writer started", "after-control", restoreServing, "swapped", false},
		{"explicit rollback after writes", "after-control", restoreServing, "swapped", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			writeName(t, "Original contains newer data")
			const oid = "SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()"
			original := query(t, databaseName, oid)
			interruptInstallerDocker(t, test.checkpoint)
			out, err := run(installer, "restore", "--config", config, "--from", backup)
			t.Logf("interrupted restore:\n%s", out)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Fatalf("wanted SIGKILL at %s, got %v", test.checkpoint, err)
			}
			i, err := readRestoreIntent(c)
			if err != nil || i == nil || i.Phase != test.phase || fmt.Sprint(i.Original) != original {
				t.Fatalf("unexpected durable restore record: %+v (%v)", i, err)
			}
			catalog, err := o.restoreCatalog(t.Context(), *i)
			placements := map[string]restoreCatalog{"before": i.before(), "swapped": i.swapped(), "rolled-back": i.rolledBack()}
			if err != nil || catalog != placements[test.placement] {
				t.Fatalf("placement at interruption = %v, want %v (%v)", catalog, placements[test.placement], err)
			}
			t.Logf("catalog placement at SIGKILL %s (cluster|live|incoming|previous|failed)", catalog.key())
			if test.checkpoint != "after-control" {
				if status := checkedCommand(t, "status", "INSTALL_NOT_HEALTHY", true); strings.Contains(status, ": ready") {
					t.Fatal("the writer answered before recovery")
				}
			}
			checkedCommand(t, "apply", "INSTALL_RESTORE_PENDING", true)
			checkedCommand(t, "backup", "INSTALL_RESTORE_PENDING", true, "--output", backups)
			if test.checkpoint == "before-swap" {
				different := c
				different.Project += "-different"
				if _, err := readRestoreIntent(different); err == nil || !strings.Contains(err.Error(), "INSTALL_RESTORE_SITE_CHANGED") {
					t.Fatalf("changed site configuration was accepted: %v", err)
				}
				query(t, maintenanceDatabase, fmt.Sprintf(`ALTER DATABASE "%s" RENAME TO "%s"`, i.incomingName(), i.failedName()))
				checkedCommand(t, "recover-restore", "INSTALL_RESTORE_IDENTITY_CHANGED", true)
				query(t, maintenanceDatabase, fmt.Sprintf(`ALTER DATABASE "%s" RENAME TO "%s"`, i.failedName(), i.incomingName()))
			}

			wantName, wantStatus, wantOID, kept, keptName := "Backed up before interruption", "RESTORE_COMPLETE", fmt.Sprint(i.Incoming), i.previousName(), "Original contains newer data"
			if test.placement != "swapped" {
				wantName, wantStatus, wantOID, kept, keptName = "Original contains newer data", "RESTORE_ROLLED_BACK", original, i.failedName(), "Backed up before interruption"
			}
			if test.checkpoint == "after-control" {
				wantName = "A write after promotion"
				writeName(t, wantName)
			}
			var recoveryOptions []string
			if test.rollback {
				recoveryOptions = []string{"--rollback"}
				wantName, wantStatus, wantOID, kept, keptName = "Original contains newer data", "RESTORE_ROLLED_BACK", original, i.failedName(), wantName
			} else if test.checkpoint == "after-control" {
				t.Run("keeps the promoted database through a failed retry", func(t *testing.T) {
					interruptInstallerDocker(t, "fail-control")
					checkedCommand(t, "recover-restore", "INSTALL_RESTORE_PENDING", true)
					if live, state := query(t, databaseName, oid), query(t, databaseName, name); live != wantOID || state != wantName {
						t.Fatalf("failed recovery changed the promoted database: %s %q", live, state)
					}
				})
			}
			offlineBackup := backup + "-offline"
			if err := os.Rename(backup, offlineBackup); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Rename(offlineBackup, backup); err != nil {
					t.Error(err)
				}
			})
			recovery := checkedCommand(t, "recover-restore", wantStatus, false, recoveryOptions...)
			if test.checkpoint == "after-control" && strings.Contains(recovery, "invalidate-restored-authority") {
				t.Fatal("recovery revoked authority after the writer had started")
			}
			if live, state, retained := query(t, databaseName, oid), query(t, databaseName, name), query(t, kept, name); live != wantOID || state != wantName || retained != keptName {
				t.Fatalf("after restart live OID=%s state=%q retained=%q; expected %s/%q/%q", live, state, retained, wantOID, wantName, keptName)
			}
			if wantStatus == "RESTORE_ROLLED_BACK" {
				lateForward, _ := i.renameSQL(false)
				if _, err := o.query(t.Context(), maintenanceDatabase, lateForward); err == nil || !strings.Contains(err.Error(), "INSTALL_RESTORE_IDENTITY_CHANGED") {
					t.Fatalf("a delayed forward transaction could not be fenced: %v", err)
				}
			}
			out = checkedCommand(t, "status", c.Origins.Admin+": ready", false)
			if !strings.Contains(out, c.Origins.Portal+": ready") {
				t.Fatal("portal was not ready after recovery")
			}
			checkedCommand(t, "recover-restore", "RESTORE_NO_RECOVERY_PENDING", false)
		})
	}
}

func interruptInstallerDocker(t *testing.T, checkpoint string) {
	t.Helper()
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(docker, filepath.Join(dir, "docker-real")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checkpoint"), []byte(checkpoint), 0o600); err != nil {
		t.Fatal(err)
	}
	const wrapper = `#!/bin/sh
set -eu
dir=${0%/*}
docker=$(readlink "$dir/docker-real")
if [ -f "$dir/checkpoint" ]; then
    point=$(cat "$dir/checkpoint")
    case "$point:$*" in
        before-dump:*'postgres pg_dump '*|\
        before-swap:*'ALTER DATABASE "ownsolid" RENAME TO "ownsolid_before_restore_'*)
            unlink "$dir/checkpoint"
            kill -KILL "$PPID"
            exit 125
            ;;
        before-dump-file-change:*'postgres pg_dump '*)
            file=$(cat "$dir/change-file")
            touch -r "$file" "$dir/mtime"
            sleep 0.02
            printf '%s' 'changed' > "$file"
            printf '%s' 'fixture' > "$file"
            touch -r "$dir/mtime" "$file"
            unlink "$dir/checkpoint"
            ;;
        after-rollback:*'check-schema --config'*)
            printf '%s\n' 'injected schema check failure' >&2
            exit 1
            ;;
        fail-control:*'up --detach --wait --no-build control')
            printf '%s\n' 'injected control start failure' >&2
            exit 1
            ;;
        after-swap:*'ALTER DATABASE "ownsolid" RENAME TO "ownsolid_before_restore_'*|\
        after-rollback:*'ALTER DATABASE "ownsolid" RENAME TO "ownsolid_failed_restore_'*|\
        after-control:*'up --detach --wait --no-build control')
            "$docker" "$@"
            unlink "$dir/checkpoint"
            kill -KILL "$PPID"
            exit 125
            ;;
    esac
fi
exec "$docker" "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
