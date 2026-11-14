package install

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"golang.org/x/sys/unix"
)

const (
	scheduledHistoryFile    = "scheduled-backups.json"
	scheduledHistoryVersion = 1
	scheduledHistoryLimit   = 256
	scheduledHistoryBytes   = 1 << 20

	scheduledRunning     = "running"
	scheduledSucceeded   = "succeeded"
	scheduledFailed      = "failed"
	scheduledInterrupted = "interrupted"

	scheduledInterruptCode = "INSTALL_BACKUP_INTERRUPTED"
	scheduledFailureCode   = "INSTALL_SCHEDULED_BACKUP_FAILED"
)

var (
	errScheduleHistoryInvalid = errors.New("INSTALL_BACKUP_HISTORY_INVALID: verify the private scheduled backup history")
	errScheduleHistoryOwner   = errors.New("INSTALL_BACKUP_HISTORY_OWNER_MISMATCH: use the history belonging to this installation")
	errScheduleHistoryWrite   = errors.New("INSTALL_BACKUP_HISTORY_WRITE_FAILED: check the installation root's permissions and free space")
	errScheduleHistoryRoot    = errors.New("INSTALL_BACKUP_HISTORY_ROOT_INVALID: use the installation's private root without symlinks")
)

// The same run shape is used on disk and in the redacted readback.
type scheduledBackupRun struct {
	Sequence       uint64     `json:"sequence"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Outcome        string     `json:"outcome"`
	ErrorCode      string     `json:"error_code,omitempty"`
	BackupVerified bool       `json:"backup_verified"`
	ExpiredBackups *int       `json:"expired_backups,omitempty"`
}

type scheduledBackupHistory struct {
	FormatVersion int                  `json:"format_version"`
	Owner         scheduleOwner        `json:"owner"`
	Runs          []scheduledBackupRun `json:"runs"`
}

// WriteScheduledBackupHistory reports local scheduled attempts without contacting
// Docker or PostgreSQL. The writer's lock distinguishes a live attempt from a
// start record left by a stopped process. An expected deployment ID requires
// the snapshot to carry that verified binding before any JSON is emitted.
func WriteScheduledBackupHistory(c Config, expectedDeploymentID string, out io.Writer) error {
	if os.Geteuid() != 0 {
		return errors.New("INSTALL_ROOT_REQUIRED: read scheduled backup history as root on the installation host")
	}
	if expectedDeploymentID != "" && !deploymentIDPattern.MatchString(expectedDeploymentID) {
		return errors.New("INSTALL_BACKUP_HISTORY_DEPLOYMENT_INVALID: use the installation's lowercase deployment UUID")
	}
	dir, err := openScheduleHistoryRoot(c)
	if err != nil {
		return err
	}
	defer dir.Close()
	// If the scheduled writer holds this lock, an atomic snapshot can still be
	// read. A successful shared lock excludes a new start during classification.
	locked := syscall.Flock(int(dir.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if locked != nil && !errors.Is(locked, syscall.EWOULDBLOCK) {
		return errScheduleHistoryInvalid
	}
	history, err := readScheduledBackupHistory(c, dir)
	if err != nil {
		return err
	}
	if expectedDeploymentID != "" {
		if history.Owner.DeploymentID == "" {
			return errors.New("INSTALL_BACKUP_HISTORY_UNBOUND: run a scheduled job against the installation's PostgreSQL database")
		}
		if history.Owner.DeploymentID != expectedDeploymentID {
			return errScheduleHistoryOwner
		}
	}
	if locked == nil {
		history.interrupt()
		if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_UN); err != nil {
			return errScheduleHistoryInvalid
		}
	}
	return json.NewEncoder(out).Encode(struct {
		FormatVersion int                  `json:"format_version"`
		DeploymentID  string               `json:"deployment_id,omitempty"`
		Runs          []scheduledBackupRun `json:"runs"`
	}{history.FormatVersion, history.Owner.DeploymentID, history.Runs})
}

func openScheduleHistoryRoot(c Config) (*os.File, error) {
	resolved, err := filepath.EvalSymlinks(c.InstallRoot)
	if err != nil || resolved != c.InstallRoot {
		return nil, errScheduleHistoryRoot
	}
	dir, err := os.OpenFile(c.InstallRoot, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errScheduleHistoryRoot
	}
	if err := checkScheduleHistoryRoot(c, dir); err != nil {
		dir.Close()
		return nil, err
	}
	return dir, nil
}

func checkScheduleHistoryRoot(c Config, dir *os.File) error {
	opened, err := dir.Stat()
	current, statErr := os.Lstat(c.InstallRoot)
	if err != nil || statErr != nil || !os.SameFile(opened, current) || opened.Mode() != os.ModeDir|0o700 ||
		opened.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return errScheduleHistoryRoot
	}
	return nil
}

func readScheduledBackupHistory(c Config, dir *os.File) (scheduledBackupHistory, error) {
	var h scheduledBackupHistory
	// Open only the named file in the directory whose lock was checked.
	fd, err := unix.Openat(int(dir.Fd()), scheduledHistoryFile, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, os.ErrNotExist) {
		return scheduledBackupHistory{
			FormatVersion: scheduledHistoryVersion,
			Owner:         scheduleOwner{InstallRoot: c.InstallRoot, Project: c.Project},
			Runs:          []scheduledBackupRun{},
		}, checkScheduleHistoryRoot(c, dir)
	}
	if err != nil {
		return h, errScheduleHistoryInvalid
	}
	file := os.NewFile(uintptr(fd), scheduledHistoryFile)
	defer file.Close()
	var before, after unix.Stat_t
	if err := unix.Fstat(fd, &before); err != nil || !validScheduleHistoryFile(before) {
		return h, errScheduleHistoryInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, scheduledHistoryBytes+1))
	if err != nil || unix.Fstat(fd, &after) != nil || !validScheduleHistoryFile(after) ||
		int64(len(raw)) != before.Size || before.Size != after.Size ||
		before.Mtim != after.Mtim || before.Ctim != after.Ctim || wire.StrictJSON(raw, &h) != nil || !h.valid() {
		return h, errScheduleHistoryInvalid
	}
	if err := checkScheduleHistoryRoot(c, dir); err != nil {
		return h, err
	}
	if h.Owner.InstallRoot != c.InstallRoot || h.Owner.Project != c.Project {
		return h, errScheduleHistoryOwner
	}
	return h, nil
}

func validScheduleHistoryFile(info unix.Stat_t) bool {
	return info.Mode&unix.S_IFMT == unix.S_IFREG && info.Mode&0o077 == 0 &&
		info.Size > 0 && info.Size <= scheduledHistoryBytes &&
		info.Uid == uint32(os.Geteuid()) && info.Nlink == 1
}

func (h scheduledBackupHistory) valid() bool {
	if h.FormatVersion != scheduledHistoryVersion || h.Runs == nil || len(h.Runs) > scheduledHistoryLimit ||
		h.Owner.DeploymentID != "" && !deploymentIDPattern.MatchString(h.Owner.DeploymentID) {
		return false
	}
	for i, run := range h.Runs {
		if run.Sequence == 0 || run.StartedAt.IsZero() || run.FinishedAt != nil && run.FinishedAt.IsZero() ||
			i > 0 && run.Sequence != h.Runs[i-1].Sequence+1 ||
			run.BackupVerified && h.Owner.DeploymentID == "" {
			return false
		}
		switch run.Outcome {
		case scheduledRunning:
			if i != len(h.Runs)-1 || run.FinishedAt != nil || run.ErrorCode != "" || run.ExpiredBackups != nil {
				return false
			}
		case scheduledInterrupted:
			if run.FinishedAt != nil || run.ErrorCode != scheduledInterruptCode || run.ExpiredBackups != nil {
				return false
			}
		case scheduledFailed:
			if run.FinishedAt == nil || !validScheduleFailureCode(run.ErrorCode) || run.ExpiredBackups != nil {
				return false
			}
		case scheduledSucceeded:
			if run.FinishedAt == nil || run.ErrorCode != "" || !run.BackupVerified || run.ExpiredBackups == nil || *run.ExpiredBackups < 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (h *scheduledBackupHistory) interrupt() {
	if len(h.Runs) > 0 && h.Runs[len(h.Runs)-1].Outcome == scheduledRunning {
		h.Runs[len(h.Runs)-1].Outcome = scheduledInterrupted
		h.Runs[len(h.Runs)-1].ErrorCode = scheduledInterruptCode
	}
}

func (h *scheduledBackupHistory) start(c Config, now time.Time) error {
	h.interrupt()
	var sequence uint64 = 1
	if len(h.Runs) > 0 {
		sequence = h.Runs[len(h.Runs)-1].Sequence + 1
		if sequence == 0 {
			return errScheduleHistoryInvalid
		}
	}
	h.Runs = append(h.Runs, scheduledBackupRun{Sequence: sequence, StartedAt: now.UTC(), Outcome: scheduledRunning})
	h.Runs = h.Runs[max(0, len(h.Runs)-scheduledHistoryLimit):]
	return h.save(c)
}

func (h *scheduledBackupHistory) bind(c Config, id string) error {
	if h.Owner.DeploymentID == id {
		return nil
	}
	if h.Owner.DeploymentID != "" {
		return errScheduleHistoryOwner
	}
	h.Owner.DeploymentID = id
	return h.save(c)
}

func (h *scheduledBackupHistory) finish(c Config, now time.Time, result error, expired int) error {
	run := &h.Runs[len(h.Runs)-1]
	finished := now.UTC()
	run.FinishedAt = &finished
	if result == nil {
		run.Outcome = scheduledSucceeded
		run.ExpiredBackups = &expired
	} else {
		run.Outcome = scheduledFailed
		run.ErrorCode = scheduleFailureCode(result)
	}
	return h.save(c)
}

func (h scheduledBackupHistory) save(c Config) error {
	if !h.valid() {
		return errScheduleHistoryInvalid
	}
	if err := writeReleaseRecord(c, scheduledHistoryFile, h); err != nil {
		return errScheduleHistoryWrite
	}
	return nil
}

func scheduleFailureCode(err error) string {
	var step *StepError
	code, _, _ := strings.Cut(err.Error(), ":")
	if errors.As(err, &step) {
		code = step.Code
	}
	if validScheduleFailureCode(code) {
		return code
	}
	return scheduledFailureCode
}

func validScheduleFailureCode(code string) bool {
	switch code {
	case scheduledFailureCode, "INSTALL_BACKUP_OUTPUT_INVALID", "INSTALL_PREFLIGHT_DOCKER_ENDPOINT",
		"INSTALL_BACKUP_OWNER_UNAVAILABLE", "INSTALL_BACKUP_HISTORY_OWNER_MISMATCH", "INSTALL_BACKUP_HISTORY_WRITE_FAILED",
		"INSTALL_BACKUP_SCHEDULE_DIRECTORY_INVALID", "INSTALL_BACKUP_SCHEDULE_DIRECTORY_OCCUPIED",
		"INSTALL_BACKUP_SCHEDULE_BUSY", "INSTALL_BACKUP_SCHEDULE_OWNER_MISMATCH", "INSTALL_DATABASE_UNAVAILABLE",
		"INSTALL_BACKUP_EXISTS", "INSTALL_BACKUP_DUMP_FAILED", "INSTALL_BACKUP_VERIFY_FAILED",
		"INSTALL_BACKUP_FILES_FAILED", "INSTALL_BACKUP_OUTPUT_FAILED", "INSTALL_BACKUP_RETENTION_FAILED":
		return true
	default:
		return false
	}
}
