package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPITRRetentionKeepsTransitiveDependencies(t *testing.T) {
	const timeline = 3
	bases := map[string]pitrBase{
		"old":     {Timeline: timeline, Start: 10, End: 11},
		"middle":  {Timeline: timeline, Start: 20, End: 21, Parent: "old"},
		"cutoff":  {Timeline: timeline, Start: 30, End: 31},
		"latest":  {Timeline: timeline, Start: 40, End: 41, Parent: "middle"},
		"expired": {Timeline: timeline, Start: 1, End: 2},
	}
	keep, floor, err := pitrRetainedBases(bases, "cutoff")
	if err != nil || len(keep) != 4 || !keep["old"] || !keep["middle"] || !keep["cutoff"] || !keep["latest"] || floor != 10 {
		t.Fatalf("retention = %v, WAL floor = %d, err = %v", keep, floor, err)
	}
	delete(bases, "middle")
	if _, _, err := pitrRetainedBases(bases, "cutoff"); err == nil {
		t.Fatal("retention accepted an incomplete chain")
	}
}

func TestPITRRetentionRefusesCrossTimelineAndIncrementalCutoffs(t *testing.T) {
	bases := map[string]pitrBase{
		"full":        {Timeline: 1, Start: 10, End: 11},
		"incremental": {Timeline: 1, Start: 20, End: 21, Parent: "full"},
		"foreign":     {Timeline: 2, Start: 40, End: 41},
	}
	if _, _, err := pitrRetainedBases(bases, "full"); err == nil || !strings.Contains(err.Error(), "TIMELINE_MIXED") {
		t.Fatalf("mixed timelines: %v", err)
	}
	if _, _, err := pitrRetainedBases(bases, "incremental"); err == nil || !strings.Contains(err.Error(), "CUTOFF_INVALID") {
		t.Fatalf("incremental cutoff: %v", err)
	}
}

func TestPITRWALRangeCrossesLogBoundaryAndRefusesGaps(t *testing.T) {
	const floor = "0000000100000000000000FE"
	const size = 16 << 20
	segments := []string{"000000010000000100000000", floor, "0000000100000000000000FF"}
	if err := validatePITRWALRange(floor, size, segments); err != nil {
		t.Fatal(err)
	}
	if err := validatePITRWALRange(floor, size, []string{floor, "000000010000000100000000"}); err == nil {
		t.Fatal("accepted a missing archived WAL segment")
	}
}

func assertPITRIsolatedDocker(t *testing.T, installer, config string, run func(string, ...string) (string, error)) {
	t.Helper()
	c, err := LoadOperational(config)
	if err != nil {
		t.Fatal(err)
	}
	o := newOperation(c, ExecRunner{}, io.Discard)
	query := func(database, sql string) string {
		t.Helper()
		out, err := o.query(t.Context(), database, sql)
		if err != nil {
			t.Fatalf("PostgreSQL query: %v", err)
		}
		return out
	}
	checked := func(want string, failure bool, args ...string) string {
		t.Helper()
		out, err := run(installer, append(args, "--config", config)...)
		t.Logf("%s\n%s", strings.Join(args, " "), out)
		if (err != nil) != failure || !strings.Contains(out, want) {
			t.Fatalf("wanted %s (failure=%t), got %v", want, failure, err)
		}
		return out
	}
	const tenant = "SELECT name FROM tenants WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)"
	const oid = "SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()"
	setTenant := func(value string) {
		query(databaseName, "UPDATE tenants SET name='"+value+"' WHERE id=(SELECT tenant_id FROM installation_state WHERE id=1)")
	}
	assertTenant := func(want string) {
		t.Helper()
		if got := query(databaseName, tenant); got != want {
			t.Fatalf("serving tenant = %q, want %q", got, want)
		}
	}
	present := func(name string, want bool) {
		t.Helper()
		_, err := os.Lstat(name)
		if want && err != nil || !want && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("presence of %s, want %t: %v", name, want, err)
		}
	}

	// Keep spread checkpoints bounded on the small disposable database.
	query(maintenanceDatabase, "ALTER SYSTEM SET checkpoint_timeout = '30s'")
	query(maintenanceDatabase, "SELECT pg_reload_conf()")
	setTenant("Full")
	checked("PITR_BACKUP_COMPLETE", false, "pitr", "backup", "--id", "first")
	checked("PITR_BACKUP_ALREADY_COMPLETE", false, "pitr", "backup", "--id", "first")
	checked("PITR_BACKUP_COMPLETE", false, "pitr", "backup", "--id", "anchor")
	setTenant("Incremental")
	checked("PITR_BACKUP_COMPLETE", false, "pitr", "backup", "--id", "incremental", "--parent", "first")
	checked("PITR_BACKUP_ALREADY_COMPLETE", false, "pitr", "backup", "--id", "incremental", "--parent", "first")
	checked("INSTALL_PITR_ID_CONFLICT", true, "pitr", "backup", "--id", "incremental", "--parent", "anchor")
	checked("INSTALL_PITR_DIRECTORY_INVALID", true, "pitr", "backup", "--id", "bad", "--parent", "missing")
	repo := c.PITR.Directory
	waitArchive := func(base, segment string) string {
		t.Helper()
		record, err := measurePITRBase(filepath.Join(repo, pitrBases, base, pitrData))
		if err != nil {
			t.Fatal(err)
		}
		if segment == "" {
			segment = query(maintenanceDatabase, fmt.Sprintf("SELECT pg_walfile_name('%X/%X'::pg_lsn)", record.Start>>32, uint32(record.Start)))
		}
		if !pitrWALName.MatchString(segment) {
			t.Fatalf("invalid WAL segment %q", segment)
		}
		wal := filepath.Join(repo, pitrSpool, "wal", record.Cluster, segment)
		deadline := time.Now().Add(30 * time.Second)
		for {
			_, err := os.Stat(wal)
			if err == nil && query(maintenanceDatabase, "SELECT coalesce(last_archived_wal,'') FROM pg_stat_archiver") >= segment {
				return wal
			}
			if time.Now().After(deadline) {
				t.Fatalf("WAL segment %s was not archived: %v", segment, err)
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

	// A second installation cannot claim this repository or its WAL mount.
	foreign := c
	foreign.Project += "-foreign"
	foreign.InstallRoot = filepath.Join(t.TempDir(), "foreign")
	if err := Materialize(foreign); err == nil || !strings.Contains(err.Error(), "INSTALL_PITR_REPOSITORY_FOREIGN") {
		t.Fatalf("foreign installation was allowed to use the repository: %v", err)
	}
	other := filepath.Join(repo, pitrBases, "unmanaged")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(other, "keep")
	if err := os.WriteFile(marker, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	query(maintenanceDatabase, "SELECT pg_switch_wal()")
	waitArchive("first", "")
	checked("expired_bases=0", false, "pitr", "prune", "--keep-from", "anchor")
	present(filepath.Join(repo, pitrBases, "first"), true)
	present(marker, true)

	// Choose a target in WAL outside the streamed WAL carried in the base.
	query(maintenanceDatabase, "SELECT pg_switch_wal()")
	setTenant("Before target")
	target := query(maintenanceDatabase, `SELECT to_char(clock_timestamp() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')`)
	segment := query(maintenanceDatabase, "SELECT pg_walfile_name(pg_current_wal_insert_lsn())")
	setTenant("After target")
	query(maintenanceDatabase, "SELECT pg_switch_wal()")
	wal := waitArchive("incremental", segment)
	original := query(databaseName, oid)
	recoverArgs := []string{"pitr", "recover", "--id", "target", "--base", "incremental", "--target", target}
	recovered := filepath.Join(repo, pitrRecovered, "target")
	if err := os.Rename(wal, wal+".withheld"); err != nil {
		t.Fatal(err)
	}
	checked("INSTALL_PITR_REPLAY_FAILED", true, recoverArgs...)
	assertTenant("After target")
	if query(databaseName, oid) != original {
		t.Fatal("failed replay changed the active database")
	}
	present(recovered, false)
	if err := os.Rename(wal+".withheld", wal); err != nil {
		t.Fatal(err)
	}

	checked("PITR_RECOVERY_COMPLETE", false, recoverArgs...)
	checked("PITR_RECOVERY_ALREADY_COMPLETE", false, recoverArgs...)
	assertTenant("After target")
	t.Run("activation resumes after the database exchange", func(t *testing.T) {
		interruptRestoreDocker(t, "after-swap")
		out, err := run(installer, "restore", "--config", config, "--from", recovered)
		t.Logf("interrupted activation:\n%s", out)
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatalf("expected an interrupted activation, got %v", err)
		}
	})
	if t.Failed() {
		return
	}
	checked("RESTORE_COMPLETE", false, "recover-restore")
	assertTenant("Before target")
	if query(databaseName, oid) == original {
		t.Fatal("explicit activation did not switch the active database")
	}
	status := checked(c.Origins.Admin+": ready", false, "status")
	if !strings.Contains(status, c.Origins.Portal+": ready") {
		t.Fatal("portal was not ready after explicit activation")
	}
	setTenant("Write after activation")
	activated := query(databaseName, oid)
	checked("PITR_ACTIVATION_ALREADY_COMPLETE", false, "restore", "--from", recovered)
	if query(databaseName, oid) != activated {
		t.Fatal("repeating activation replaced the serving database")
	}
	checked("PITR_RECOVERY_ALREADY_COMPLETE", false, recoverArgs...)
	assertTenant("Write after activation")

	// A newer independent full backup permits expiry, but only in this cluster.
	otherArchive := filepath.Join(repo, pitrSpool, "wal", "9000000000000000000")
	if err := os.Mkdir(otherArchive, 0o700); err != nil {
		t.Fatal(err)
	}
	otherWAL := filepath.Join(otherArchive, "000000010000000000000001")
	if err := os.WriteFile(otherWAL, []byte("other cluster"), 0o600); err != nil {
		t.Fatal(err)
	}
	checked("PITR_BACKUP_COMPLETE", false, "pitr", "backup", "--id", "cutoff")
	query(maintenanceDatabase, "SELECT pg_switch_wal()")
	waitArchive("cutoff", "")
	out := checked("expired_bases=3", false, "pitr", "prune", "--keep-from", "cutoff")
	if !regexp.MustCompile(`expired_wal=[1-9][0-9]*`).MatchString(out) {
		t.Fatalf("expected old archived segments to expire: %s", out)
	}
	for _, base := range []string{"first", "anchor", "incremental"} {
		present(filepath.Join(repo, pitrBases, base), false)
	}
	for _, file := range []string{otherWAL, marker, recovered, filepath.Join(repo, pitrBases, "cutoff")} {
		present(file, true)
	}
	checked("expired_bases=0 expired_wal=0", false, "pitr", "prune", "--keep-from", "cutoff")
	checked("PITR_RECOVERY_ALREADY_COMPLETE", false, recoverArgs...)
	assertTenant("Write after activation")
	t.Log("preserved later writes and retained foreign files while pruning the current cluster")
}
