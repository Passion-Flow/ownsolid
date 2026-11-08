package install

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"
)

const scheduleOwnerFile = "schedule-owner.json"

var deploymentIDPattern = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)

// BackupScheduleConfig selects a dedicated directory and the number of
// successful scheduled backups to keep. The host timer selects the calendar.
type BackupScheduleConfig struct {
	Directory string `yaml:"directory"`
	Retain    int    `yaml:"retain"`
}

func (s BackupScheduleConfig) validate() error {
	if !canonicalPath(s.Directory) {
		return invalid("backup_schedule.directory", "must be a clean absolute path")
	}
	if s.Retain < 1 || s.Retain > 1000 {
		return invalid("backup_schedule.retain", "must be 1 to 1000")
	}
	return nil
}

type scheduleOwner struct {
	DeploymentID string `json:"deployment_id"`
	Project      string `json:"project"`
	InstallRoot  string `json:"install_root"`
}

// ScheduledBackup makes a verified backup, then expires older archives
// created by this installation's scheduled job.
func ScheduledBackup(ctx context.Context, c Config, runner StreamRunner, out io.Writer) error {
	if runtime.GOOS != "linux" {
		return errors.New("INSTALL_BACKUP_SCHEDULE_UNSUPPORTED: run scheduled backups on the Linux installation host")
	}
	if os.Geteuid() != 0 {
		return errors.New("INSTALL_ROOT_REQUIRED: run the scheduled backup as root")
	}
	if c.BackupSchedule == nil {
		return errors.New("INSTALL_BACKUP_SCHEDULE_REQUIRED: configure backup_schedule before running the scheduled job")
	}
	schedule := *c.BackupSchedule
	if err := schedule.validate(); err != nil {
		return err
	}
	if err := admitBackupOutput(c, schedule.Directory); err != nil {
		return err
	}
	unlock, err := lockOperation(c)
	if err != nil {
		return err
	}
	defer unlock()
	if err := checkBoundOperation(c); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.BackupSeconds)*time.Second)
	defer cancel()
	if err := checkLocalDocker(ctx, runner); err != nil {
		return err
	}
	o := newOperation(c, runner, out)
	id, err := o.query(ctx, databaseName, "SELECT deployment_id FROM public.platform_license_state WHERE id = 1")
	if err != nil || !deploymentIDPattern.MatchString(id) {
		return errors.New("INSTALL_BACKUP_OWNER_UNAVAILABLE: verify that the installation's PostgreSQL database is running")
	}
	root, lock, err := openScheduleDirectory(c, schedule.Directory)
	if err != nil {
		return err
	}
	defer root.Close()
	defer lock.Close()
	owner := scheduleOwner{DeploymentID: id, Project: c.Project, InstallRoot: c.InstallRoot}
	ownerRecord, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	// Keep publication tied to the locked directory even if an ancestor is renamed.
	output := fmt.Sprintf("/proc/self/fd/%d", lock.Fd())
	if err := claimScheduleDirectory(c, root, lock, output, ownerRecord); err != nil {
		return err
	}

	prefix := "ownsolid-scheduled-" + id + "-"
	published, err := o.backupWithPrefix(ctx, output, prefix, func(partial string) error {
		return writeFile(filepath.Join(partial, scheduleOwnerFile), ownerRecord, c, false)
	})
	if err != nil {
		err = retryWith(err, "scheduled-backup")
		o.record("backup", "failed", err.Error())
		return err
	}
	path := filepath.Join(schedule.Directory, filepath.Base(published))
	o.record("backup", "complete", path)
	fmt.Fprintf(out, "BACKUP_COMPLETE path=%s\n", path)
	removed, err := expireScheduledBackups(ctx, root, prefix, filepath.Base(path), schedule.Retain, ownerRecord)
	if err != nil {
		return fmt.Errorf("INSTALL_BACKUP_RETENTION_FAILED: the new backup is available, but expiry did not finish: %w", err)
	}
	fmt.Fprintf(out, "INSTALL_SCHEDULED_BACKUP_COMPLETE expired=%d\n", removed)
	return nil
}

func openScheduleDirectory(c Config, path string) (*os.Root, *os.File, error) {
	invalidDirectory := errors.New("INSTALL_BACKUP_SCHEDULE_DIRECTORY_INVALID: use a root-owned private directory outside the installation with no symlinks")
	actual, err := filepath.EvalSymlinks(path)
	installed, installErr := filepath.EvalSymlinks(c.InstallRoot)
	if err != nil || installErr != nil || actual != path ||
		actual == installed || strings.HasPrefix(actual, installed+string(os.PathSeparator)) {
		return nil, nil, invalidDirectory
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, invalidDirectory
	}
	dir, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, nil, invalidDirectory
	}
	info, err := dir.Stat()
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Sys().(*syscall.Stat_t).Uid != 0 {
		dir.Close()
		root.Close()
		return nil, nil, invalidDirectory
	}
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		dir.Close()
		root.Close()
		return nil, nil, errors.New("INSTALL_BACKUP_SCHEDULE_BUSY: another job holds the scheduled backup directory")
	}
	return root, dir, nil
}

func claimScheduleDirectory(c Config, root *os.Root, dir *os.File, output string, owner []byte) error {
	matches, err := scheduleOwnerMatches(root, scheduleOwnerFile, owner)
	if errors.Is(err, os.ErrNotExist) {
		if entries, err := dir.Readdirnames(1); len(entries) != 0 || !errors.Is(err, io.EOF) {
			return errors.New("INSTALL_BACKUP_SCHEDULE_DIRECTORY_OCCUPIED: select a new empty directory")
		}
		if err := writeFile(filepath.Join(output, scheduleOwnerFile), owner, c, false); err != nil {
			return err
		}
		return dir.Sync()
	}
	if err != nil || !matches {
		return errors.New("INSTALL_BACKUP_SCHEDULE_OWNER_MISMATCH: select the private backup directory for this installation")
	}
	return nil
}

func scheduleOwnerMatches(root *os.Root, path string, expected []byte) (bool, error) {
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return false, nil
	}
	actual, err := io.ReadAll(io.LimitReader(f, int64(len(expected)+1)))
	return err == nil && bytes.Equal(actual, expected), err
}

func expireScheduledBackups(ctx context.Context, root *os.Root, prefix, newBackup string, retain int, owner []byte) (int, error) {
	dir, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return 0, err
	}
	var candidates []string
	for _, entry := range entries {
		stamp, ok := strings.CutPrefix(entry.Name(), prefix)
		if !entry.IsDir() || !ok || entry.Name() == newBackup {
			continue
		}
		when, err := time.Parse(backupTimeLayout, stamp)
		if err != nil || when.Format(backupTimeLayout) != stamp {
			continue
		}
		owned, err := scheduleOwnerMatches(root, filepath.Join(entry.Name(), scheduleOwnerFile), owner)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		if owned {
			candidates = append(candidates, entry.Name())
		}
	}
	// Always keep the new verified backup, even if the system clock moved back.
	slices.Sort(candidates)
	expired := max(0, len(candidates)-retain+1)
	for i, name := range candidates[:expired] {
		if err := ctx.Err(); err != nil {
			return i, err
		}
		if err := root.RemoveAll(name); err != nil {
			return i, err
		}
	}
	if expired > 0 {
		return expired, dir.Sync()
	}
	return 0, nil
}
