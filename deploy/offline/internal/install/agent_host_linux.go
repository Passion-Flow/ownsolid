package install

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func (owner agentOwner) owns(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == owner.uid && stat.Gid == owner.gid
}

func trustedAgentDirectory(path string) error {
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path {
		return errors.New("INSTALL_AGENT_DIRECTORY_UNSAFE: verify the standard Agent host directories")
	}
	for {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || !rootAgentOwner.owns(info) || info.Mode().Perm()&0o022 != 0 {
			return errors.New("INSTALL_AGENT_DIRECTORY_UNSAFE: verify the standard Agent host directories")
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}

func ensureAgentDirectory(path string, mode os.FileMode, owner agentOwner) error {
	parent := filepath.Dir(path)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		temporary, err := os.MkdirTemp(parent, ".ownsolid-agent-dir-*")
		if err != nil {
			return errors.New("INSTALL_AGENT_DIRECTORY_FAILED: check permissions and free space for Agent files")
		}
		defer os.Remove(temporary)
		if os.Chown(temporary, int(owner.uid), int(owner.gid)) != nil || os.Chmod(temporary, mode) != nil || syncDirectory(temporary) != nil {
			return errors.New("INSTALL_AGENT_DIRECTORY_FAILED: check permissions and free space for Agent files")
		}
		if err := renameAgentFile(temporary, path); err != nil && !errors.Is(err, os.ErrExist) {
			return errors.New("INSTALL_AGENT_DIRECTORY_FAILED: check permissions and free space for Agent files")
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode() != mode|os.ModeDir || !owner.owns(info) {
		return errors.New("INSTALL_AGENT_DIRECTORY_CONFLICT: inspect ownership and mode of existing Agent directories")
	}
	if err := syncDirectory(parent); err != nil {
		return errors.New("INSTALL_AGENT_DIRECTORY_FAILED: check permissions and free space for Agent files")
	}
	return nil
}

func renameAgentFile(source, target string) error {
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
}

func lockAgentInstall(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, "install.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, errors.New("INSTALL_AGENT_LOCK_FAILED: inspect the Agent installation lock")
	}
	info, err := f.Stat()
	if err != nil || info.Mode() != 0o600 || !rootAgentOwner.owns(info) {
		f.Close()
		return nil, errors.New("INSTALL_AGENT_LOCK_FAILED: inspect the Agent installation lock")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("INSTALL_AGENT_BUSY: retry when the other Agent installer has finished")
	}
	return f, nil
}

func ensureAgentAccount(ctx context.Context, runner Runner) (agentOwner, error) {
	if _, err := runner.Run(ctx, "systemd-sysusers", "--inline", `u ownsolid-agent - "Ownsolid Agent" /var/lib/ownsolid-agent`); err != nil {
		return agentOwner{}, errors.New("INSTALL_AGENT_ACCOUNT_FAILED: check systemd-sysusers and the Agent account")
	}
	account, err := user.Lookup(agentAccount)
	if err != nil {
		return agentOwner{}, errors.New("INSTALL_AGENT_ACCOUNT_FAILED: the Agent system account is unavailable")
	}
	group, err := user.LookupGroup(agentAccount)
	uid, uidErr := strconv.ParseUint(account.Uid, 10, 32)
	gid, gidErr := strconv.ParseUint(account.Gid, 10, 32)
	groups, groupsErr := account.GroupIds()
	if err != nil || uidErr != nil || gidErr != nil || groupsErr != nil || uid == 0 || gid == 0 ||
		account.HomeDir != "/var/lib/ownsolid-agent" || group.Gid != account.Gid || len(groups) != 1 || groups[0] != account.Gid {
		return agentOwner{}, errors.New("INSTALL_AGENT_ACCOUNT_INVALID: reserve an unprivileged Agent account with its own group")
	}
	return agentOwner{uint32(uid), uint32(gid)}, nil
}

func agentFilePresent(path string) (bool, error) {
	_, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, errors.New("INSTALL_AGENT_STATE_UNAVAILABLE: inspect the Agent state directory")
	default:
		return true, nil
	}
}

func stageAgentFile(dir string, data []byte, mode os.FileMode, owner agentOwner) (name string, result error) {
	file, err := os.CreateTemp(dir, ".ownsolid-agent-*")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	defer func() {
		result = errors.Join(result, file.Close())
		if result != nil {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return "", err
	}
	if err := file.Chown(int(owner.uid), int(owner.gid)); err != nil {
		return "", err
	}
	if err := file.Chmod(mode); err != nil {
		return "", err
	}
	return temporary, file.Sync()
}

func publishAgentFile(path string, data []byte, mode os.FileMode, owner agentOwner) error {
	present, err := agentFilePresent(path)
	if err != nil {
		return err
	}
	if present {
		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err == nil {
			defer f.Close()
			info, err := f.Stat()
			if err == nil && info.Mode() == mode && owner.owns(info) && info.Size() == int64(len(data)) &&
				info.Sys().(*syscall.Stat_t).Nlink == 1 {
				existing, err := io.ReadAll(io.LimitReader(f, int64(len(data))+1))
				if err == nil && bytes.Equal(existing, data) {
					if syncDirectory(filepath.Dir(path)) != nil {
						return fmt.Errorf("INSTALL_AGENT_FILE_PUBLISH_FAILED: check storage for %s", filepath.Base(path))
					}
					return nil
				}
			}
		}
		return fmt.Errorf("INSTALL_AGENT_FILE_CONFLICT: inspect existing %s", filepath.Base(path))
	}
	parent := filepath.Dir(path)
	name, err := stageAgentFile(parent, data, mode, owner)
	if err == nil {
		defer os.Remove(name)
		err = renameAgentFile(name, path)
	}
	if err != nil || syncDirectory(parent) != nil {
		return fmt.Errorf("INSTALL_AGENT_FILE_PUBLISH_FAILED: check ownership and free space for %s", filepath.Base(path))
	}
	return nil
}

// The token is read only after it is complete and durable. An interrupted
// write still leaves the same private retry fence, with no extra token copies.
func publishAgentToken(path string, data []byte, owner agentOwner) error {
	failure := errors.New("INSTALL_AGENT_TOKEN_PUBLISH_FAILED: inspect the private installed token copy before retrying")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return failure
	}
	_, writeErr := f.Write(data)
	err = errors.Join(writeErr, f.Chown(int(owner.uid), int(owner.gid)), f.Chmod(0o600), f.Sync(), f.Close())
	if err != nil || syncDirectory(filepath.Dir(path)) != nil {
		return failure
	}
	return nil
}

func removeAgentToken(path string) error {
	if err := os.Remove(path); err != nil || syncDirectory(filepath.Dir(path)) != nil {
		return errors.New("INSTALL_AGENT_TOKEN_CLEANUP_FAILED: inspect the private installed token copy and retry")
	}
	return nil
}

var (
	errAgentTokenUnusable = errors.New("INSTALL_AGENT_TOKEN_UNUSABLE: create a new Control join token and retry")
	agentUncertainRequest = regexp.MustCompile(`^STORAGE_COMMIT_OUTCOME_UNKNOWN request_id=[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
)

type agentEnrollmentUncertain string

func (e agentEnrollmentUncertain) Error() string { return string(e) }

func runAgentEnrollment(ctx context.Context, binary, config string, owner agentOwner) error {
	command := exec.CommandContext(ctx, binary, "enroll", "--if-needed", "--config", config)
	command.Env = []string{}
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: owner.uid, Gid: owner.gid}}
	output, err := command.CombinedOutput()
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		last := lines[len(lines)-1]
		if last == "TOKEN_UNUSABLE" {
			return errAgentTokenUnusable
		}
		if agentUncertainRequest.MatchString(last) {
			return agentEnrollmentUncertain(last)
		}
	}
	return err
}
