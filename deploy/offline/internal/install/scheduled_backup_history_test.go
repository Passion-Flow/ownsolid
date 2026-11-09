package install

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const historyFixtureID = "b67d188a-4f8a-4b3a-8dc7-73c9f9d41955"
const historyDiagnostic = "untrusted diagnostic with private row contents"

type scheduledHistoryRunner struct {
	deploymentID string
	dumpFailure  bool
	dumpStarted  func()
}

func (scheduledHistoryRunner) Run(context.Context, string, ...string) (string, error) {
	return "", errors.New("unexpected non-stream command")
}

func (r scheduledHistoryRunner) Stream(_ context.Context, stdin io.Reader, stdout io.Writer, _ string, args ...string) (string, error) {
	switch {
	case slices.Contains(args, "pg_dump"):
		if r.dumpStarted != nil {
			r.dumpStarted()
		}
		if r.dumpFailure {
			return historyDiagnostic, errors.New("dump failed")
		}
		_, err := io.WriteString(stdout, "controlled dump fixture")
		return "", err
	case slices.Contains(args, "pg_restore"):
		_, err := io.Copy(io.Discard, stdin)
		return "", err
	case slices.Contains(args, "psql"):
		sql := args[len(args)-1]
		var answer string
		switch {
		case strings.HasPrefix(sql, "SELECT deployment_id"):
			if r.deploymentID == "" {
				return historyDiagnostic, errors.New("database unavailable")
			}
			answer = r.deploymentID
		case sql == "SELECT 1":
			answer = "1"
		case strings.HasPrefix(sql, "CREATE DATABASE"), strings.HasPrefix(sql, "DROP DATABASE"):
		case strings.HasPrefix(sql, "SELECT max(version)"):
			answer = "57"
		case strings.HasPrefix(sql, "SELECT c.relname"):
			answer = "installation_state"
		case strings.HasPrefix(sql, "SELECT 'installation_state'"):
			answer = "installation_state|1|42"
		default:
			return "", errors.New("unexpected SQL")
		}
		_, err := io.WriteString(stdout, answer)
		return "", err
	default:
		return "", errors.New("unexpected stream command")
	}
}

func historyTestConfig(t *testing.T) Config {
	t.Helper()
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("the scheduled job's pinned directory descriptor requires Linux and root")
	}
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	c := Config{
		InstallRoot: filepath.Join(shortTempDir(t), "install"), Project: "scheduled-history",
		BackupSeconds: 30, BackupSchedule: &BackupScheduleConfig{Directory: shortTempDir(t), Retain: 1},
	}
	if err := os.MkdirAll(c.Layout().Operator, 0o700); err != nil {
		t.Fatal(err)
	}
	return c
}

func historyTestRun(t *testing.T, c Config, runner scheduledHistoryRunner, second int) error {
	t.Helper()
	o := newOperation(c, runner, io.Discard)
	o.now = func() time.Time { return time.Date(2026, 11, 9, 1, 0, second, 0, time.UTC) }
	return runScheduledBackup(t.Context(), o)
}

func historyTestReadback(t *testing.T, c Config) (string, []scheduledBackupRun) {
	t.Helper()
	var output bytes.Buffer
	if err := WriteScheduledBackupHistory(c, &output); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{c.InstallRoot, c.Project, c.BackupSchedule.Directory, historyDiagnostic, "ownsolid-scheduled-", "database.dump", "owner"} {
		if strings.Contains(output.String(), private) {
			t.Fatalf("query exposed a private field: %q", private)
		}
	}
	var result struct {
		FormatVersion int                  `json:"format_version"`
		DeploymentID  string               `json:"deployment_id"`
		Runs          []scheduledBackupRun `json:"runs"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.FormatVersion != 1 || result.Runs == nil {
		t.Fatalf("invalid query result: %v %s", err, output.Bytes())
	}
	return result.DeploymentID, result.Runs
}

func TestScheduledBackupHistoryTracksFailuresAndRetention(t *testing.T) {
	c := historyTestConfig(t)
	if id, runs := historyTestReadback(t, c); id != "" || len(runs) != 0 {
		t.Fatalf("initial query: %q %+v", id, runs)
	}
	if _, err := os.Stat(filepath.Join(c.InstallRoot, scheduledHistoryFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a read-only query created history")
	}
	if err := historyTestRun(t, c, scheduledHistoryRunner{}, 1); err == nil || !strings.Contains(err.Error(), "INSTALL_BACKUP_OWNER_UNAVAILABLE") {
		t.Fatalf("expected database owner failure, got %v", err)
	}
	if id, runs := historyTestReadback(t, c); id != "" || len(runs) != 1 || runs[0].ErrorCode != "INSTALL_BACKUP_OWNER_UNAVAILABLE" {
		t.Fatalf("outage before site binding: %q %+v", id, runs)
	}
	if err := historyTestRun(t, c, scheduledHistoryRunner{deploymentID: historyFixtureID}, 2); err != nil {
		t.Fatal(err)
	}
	before := scheduledArchives(t, c.BackupSchedule.Directory)
	if len(before) != 1 {
		t.Fatalf("expected one verified archive, got %v", before)
	}
	if err := historyTestRun(t, c, scheduledHistoryRunner{deploymentID: historyFixtureID, dumpFailure: true}, 3); err == nil ||
		!strings.Contains(err.Error(), "INSTALL_BACKUP_DUMP_FAILED") || strings.Contains(err.Error(), historyDiagnostic) {
		t.Fatalf("expected a redacted dump failure, got %v", err)
	}
	if after := scheduledArchives(t, c.BackupSchedule.Directory); !slices.Equal(before, after) {
		t.Fatalf("a failed backup changed retention: %v", after)
	}
	if err := historyTestRun(t, c, scheduledHistoryRunner{deploymentID: historyFixtureID}, 4); err != nil {
		t.Fatal(err)
	}
	if after := scheduledArchives(t, c.BackupSchedule.Directory); len(after) != 1 || after[0] == before[0] {
		t.Fatalf("the verified retry did not expire its predecessor: %v", after)
	}
	id, runs := historyTestReadback(t, c)
	if id != historyFixtureID || len(runs) != 4 || runs[0].Outcome != scheduledFailed ||
		runs[1].Outcome != scheduledSucceeded || !runs[1].BackupVerified || runs[1].ExpiredBackups == nil || *runs[1].ExpiredBackups != 0 ||
		runs[2].Outcome != scheduledFailed || runs[2].ErrorCode != "INSTALL_BACKUP_DUMP_FAILED" || runs[2].BackupVerified ||
		runs[3].Outcome != scheduledSucceeded || runs[3].ExpiredBackups == nil || *runs[3].ExpiredBackups != 1 {
		t.Fatalf("unexpected history after retention: %q %+v", id, runs)
	}
	raw, err := os.ReadFile(filepath.Join(c.Layout().Operator, operationLogName))
	if err != nil || bytes.Contains(raw, []byte(historyDiagnostic)) || bytes.Contains(raw, []byte(c.BackupSchedule.Directory)) {
		t.Fatalf("operator log is unreadable or contains private details: %v", err)
	}
	if err := historyTestRun(t, c, scheduledHistoryRunner{deploymentID: "0bc2c190-6fce-4136-a30a-9a32dbfc504b"}, 5); !errors.Is(err, errScheduleHistoryOwner) {
		t.Fatalf("expected changed deployment refusal, got %v", err)
	}
	id, runs = historyTestReadback(t, c)
	if id != historyFixtureID || len(runs) != 5 || runs[4].ErrorCode != "INSTALL_BACKUP_HISTORY_OWNER_MISMATCH" || runs[4].BackupVerified {
		t.Fatalf("foreign deployment changed the history binding: %q %+v", id, runs)
	}
}

func TestScheduledBackupHistoryKeepsPublicationOnRetentionFailure(t *testing.T) {
	c := historyTestConfig(t)
	runner := scheduledHistoryRunner{deploymentID: historyFixtureID}
	if err := historyTestRun(t, c, runner, 1); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(c.BackupSchedule.Directory, "ownsolid-scheduled-"+historyFixtureID+"-20261108T000000Z")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/null", filepath.Join(foreign, scheduleOwnerFile)); err != nil {
		t.Fatal(err)
	}
	if err := historyTestRun(t, c, runner, 2); err == nil || !strings.Contains(err.Error(), "INSTALL_BACKUP_RETENTION_FAILED") {
		t.Fatalf("expected retention failure, got %v", err)
	}
	_, runs := historyTestReadback(t, c)
	if len(runs) != 2 || runs[1].Outcome != scheduledFailed || !runs[1].BackupVerified ||
		runs[1].ErrorCode != "INSTALL_BACKUP_RETENTION_FAILED" || runs[1].ExpiredBackups != nil {
		t.Fatalf("publication was lost after retention failure: %+v", runs)
	}
}

func TestScheduledBackupHistorySurvivesKilledProcess(t *testing.T) {
	if root := os.Getenv("OWNSOLID_SCHEDULE_HISTORY_CHILD_ROOT"); root != "" {
		c := Config{InstallRoot: root, Project: "scheduled-history", BackupSeconds: 30,
			BackupSchedule: &BackupScheduleConfig{Directory: os.Getenv("OWNSOLID_SCHEDULE_HISTORY_CHILD_REPOSITORY"), Retain: 1}}
		runner := scheduledHistoryRunner{deploymentID: historyFixtureID, dumpStarted: func() {
			_, _ = io.WriteString(os.Stdout, "dump started\n")
			time.Sleep(time.Hour)
		}}
		t.Fatalf("the killed backup returned: %v", historyTestRun(t, c, runner, 1))
	}
	c := historyTestConfig(t)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestScheduledBackupHistorySurvivesKilledProcess$")
	child.Env = append(os.Environ(), "OWNSOLID_SCHEDULE_HISTORY_CHILD_ROOT="+c.InstallRoot,
		"OWNSOLID_SCHEDULE_HISTORY_CHILD_REPOSITORY="+c.BackupSchedule.Directory)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "dump started" {
		t.Fatalf("the child did not reach the dump: %q", scanner.Text())
	}
	_, active := historyTestReadback(t, c)
	if len(active) != 1 || active[0].Outcome != scheduledRunning || active[0].BackupVerified {
		t.Fatalf("active backup reported incorrectly: %+v", active)
	}
	if err := historyTestRun(t, c, scheduledHistoryRunner{deploymentID: historyFixtureID}, 2); err == nil || !strings.Contains(err.Error(), "INSTALL_BUSY") {
		t.Fatalf("a concurrent scheduler was admitted: %v", err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	waited = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("expected a killed process, got %v", err)
	}

	unlock, err := lockOperation(c)
	if err != nil {
		t.Fatal(err)
	}
	// A different installer operation holding the global lock is not a
	// scheduled writer and must not hide the interrupted attempt.
	_, interrupted := historyTestReadback(t, c)
	unlock()
	if len(interrupted) != 1 || interrupted[0].Outcome != scheduledInterrupted ||
		interrupted[0].ErrorCode != scheduledInterruptCode || interrupted[0].FinishedAt != nil || interrupted[0].BackupVerified {
		t.Fatalf("interrupted backup reported incorrectly: %+v", interrupted)
	}
	onDisk, err := readScheduledBackupHistory(c)
	if err != nil || onDisk.Runs[0].Outcome != scheduledRunning {
		t.Fatal("the read-only query modified the persisted record")
	}
	if err := historyTestRun(t, c, scheduledHistoryRunner{deploymentID: historyFixtureID}, 2); err != nil {
		t.Fatal(err)
	}
	_, retry := historyTestReadback(t, c)
	if len(retry) != 2 || retry[0].Outcome != scheduledInterrupted || retry[0].Sequence != 1 ||
		retry[1].Outcome != scheduledSucceeded || retry[1].Sequence != 2 {
		t.Fatalf("retry lost its interrupted predecessor: %+v", retry)
	}
}

func TestScheduledBackupHistoryBoundsAndRejectsCopiedState(t *testing.T) {
	c := Config{InstallRoot: shortTempDir(t), Project: "history-fixture"}
	h, err := readScheduledBackupHistory(c)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC)
	for i := 1; i <= scheduledHistoryLimit; i++ {
		h.Runs = append(h.Runs, scheduledBackupRun{Sequence: uint64(i), StartedAt: now,
			Outcome: scheduledInterrupted, ErrorCode: scheduledInterruptCode})
	}
	if err := h.start(c, now); err != nil {
		t.Fatal(err)
	}
	if len(h.Runs) != scheduledHistoryLimit || h.Runs[0].Sequence != 2 ||
		h.Runs[len(h.Runs)-1].Sequence != scheduledHistoryLimit+1 {
		t.Fatalf("history did not retain its sequence across compaction: %d", len(h.Runs))
	}
	copied := c
	copied.InstallRoot = shortTempDir(t)
	raw, err := os.ReadFile(filepath.Join(c.InstallRoot, scheduledHistoryFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copied.InstallRoot, scheduledHistoryFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readScheduledBackupHistory(copied); !errors.Is(err, errScheduleHistoryOwner) {
		t.Fatalf("copied history was accepted: %v", err)
	}
	path := filepath.Join(c.InstallRoot, scheduledHistoryFile)
	if err := os.WriteFile(path, []byte(`{"format_version":1,"owner":{},"runs":[],"detail":"private"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readScheduledBackupHistory(c); !errors.Is(err, errScheduleHistoryInvalid) {
		t.Fatalf("corrupt history was accepted: %v", err)
	}
}
