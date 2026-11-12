package install

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// supportAccess uses the existing operator log. It is not a server audit actor.
type supportAccess struct {
	root      *os.Root
	file      *os.File
	event     supportAccessEvent
	priorSize int64
}

type supportAccessEvent struct {
	Version         int       `json:"format_version"`
	PriorIncomplete bool      `json:"prior_tail_incomplete,omitempty"`
	Time            time.Time `json:"time"`
	Action          string    `json:"action"`
	Outcome         string    `json:"outcome"`
	Attempt         string    `json:"attempt_id"`
	Kind            string    `json:"collector"`
	UID             int       `json:"uid"`
	EffectiveUID    int       `json:"effective_uid"`
	InputSHA256     string    `json:"input_sha256"`
	ArchiveSHA256   string    `json:"archive_sha256,omitempty"`
	ArchiveBytes    int64     `json:"archive_bytes,omitempty"`
	Failure         string    `json:"failure_code,omitempty"`
}

type supportArchive struct {
	sha256 string
	bytes  int64
	info   os.FileInfo
}

func supportAccessError() error { return supportAccessErrorAt("admission") }

func supportAccessErrorAt(stage string) error {
	return errors.New("INSTALL_SUPPORT_ACCESS_UNAVAILABLE: cannot durably record diagnostic access (" + stage + "); preserve the operator log and check ownership, permissions and free space before retrying")
}

func supportOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func installationSupportAccess(ctx context.Context, c Config) (*supportAccess, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, supportAccessErrorAt("encode input")
	}
	if !canonicalPath(c.InstallRoot) || c.InstallRoot == "/" {
		return nil, supportAccessErrorAt("installation root selection")
	}
	if err := os.MkdirAll(c.InstallRoot, 0o700); err != nil {
		return nil, supportAccessErrorAt("create installation root")
	}
	root, err := os.OpenRoot(c.InstallRoot)
	if err != nil {
		return nil, supportAccessErrorAt("open installation root")
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil || !supportOwned(info) || info.Mode().Perm()&0o022 != 0 {
		return nil, supportAccessErrorAt("installation directory owner or mode")
	}
	if err := root.Mkdir(operatorDirectoryName, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, supportAccessErrorAt("create operator directory")
	}
	info, err = root.Lstat(operatorDirectoryName)
	if err != nil || !info.IsDir() || !supportOwned(info) || info.Mode().Perm() != 0o700 {
		return nil, supportAccessErrorAt("operator directory owner or mode")
	}
	// Persist a newly created operator directory before admitting any read.
	directory, err := root.Open(".")
	if err != nil {
		return nil, supportAccessErrorAt("open installation directory")
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return nil, supportAccessErrorAt("sync installation directory")
	}
	operator, err := root.OpenRoot(operatorDirectoryName)
	if err != nil {
		return nil, supportAccessErrorAt("open operator directory")
	}
	return beginSupportAccess(ctx, operator, "local", raw)
}

func clusterSupportAccess(ctx context.Context, input string, raw []byte) (*supportAccess, error) {
	root, err := os.OpenRoot(filepath.Dir(input))
	if err != nil {
		return nil, supportAccessError()
	}
	return beginSupportAccess(ctx, root, "managed_cluster", raw)
}

// beginSupportAccess owns root, including on failure.
func beginSupportAccess(ctx context.Context, root *os.Root, kind string, input []byte) (*supportAccess, error) {
	a := &supportAccess{root: root}
	info, err := root.Stat(".")
	if err == nil && (!info.IsDir() || !supportOwned(info) || info.Mode().Perm()&0o022 != 0) {
		err = supportAccessError()
	}
	if err == nil {
		flags := os.O_APPEND | os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
		a.file, err = root.OpenFile(operationLogName, os.O_CREATE|os.O_EXCL|flags, 0o600)
		if errors.Is(err, os.ErrExist) {
			info, err = root.Lstat(operationLogName)
			if err == nil && (!info.Mode().IsRegular() || !supportOwned(info) || info.Mode().Perm() != 0o600) {
				err = supportAccessErrorAt("existing log identity or mode")
			}
			if err == nil {
				// Do not recreate a carrier that disappears after an observed EEXIST.
				a.file, err = root.OpenFile(operationLogName, flags, 0)
			}
		}
	}
	if err != nil {
		a.close()
		var errno syscall.Errno
		if errors.As(err, &errno) {
			var detail *os.PathError
			operation := "open"
			if errors.As(err, &detail) {
				operation = detail.Op
			}
			return nil, supportAccessErrorAt("operator log " + operation + " errno " + strconv.Itoa(int(errno)))
		}
		return nil, supportAccessErrorAt("open operator log")
	}
	sum := sha256.Sum256(input)
	a.event = supportAccessEvent{Version: 1, Action: "diagnostics", Attempt: rand.Text(), Kind: kind,
		UID: os.Getuid(), EffectiveUID: os.Geteuid(), InputSHA256: hex.EncodeToString(sum[:])}
	if err := a.append(ctx, "admitted", supportArchive{}, ""); err != nil {
		a.close()
		return nil, err
	}
	return a, nil
}

func (a *supportAccess) close() {
	if a.file != nil {
		a.file.Close()
	}
	if a.root != nil {
		a.root.Close()
	}
}

// append serializes one bounded append and durable sync, without replacing the carrier.
func (a *supportAccess) append(ctx context.Context, outcome string, archive supportArchive, failure string) error {
	for {
		if ctx.Err() != nil {
			return supportAccessErrorAt("lock")
		}
		err := syscall.Flock(int(a.file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return supportAccessErrorAt("lock")
		}
		// Poll a nonblocking file lock so caller cancellation still bounds admission.
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return supportAccessErrorAt("cancelled")
		case <-timer.C:
		}
	}
	defer syscall.Flock(int(a.file.Fd()), syscall.LOCK_UN)
	info, err := a.file.Stat()
	current, pathErr := a.root.Lstat(operationLogName)
	if err != nil || pathErr != nil || !info.Mode().IsRegular() || !supportOwned(info) ||
		info.Mode().Perm() != 0o600 || !os.SameFile(info, current) ||
		info.Sys().(*syscall.Stat_t).Nlink != 1 {
		return supportAccessErrorAt("log identity or mode")
	}
	priorIncomplete := false
	if info.Size() > 0 {
		var last [1]byte
		if _, err := a.file.ReadAt(last[:], info.Size()-1); err != nil {
			return supportAccessErrorAt("log tail")
		}
		priorIncomplete = last[0] != '\n'
	}
	if outcome == "admitted" {
		a.priorSize = info.Size()
	}
	event := a.event
	event.PriorIncomplete = priorIncomplete
	event.Time, event.Outcome = time.Now().UTC(), outcome
	event.ArchiveSHA256, event.ArchiveBytes, event.Failure = archive.sha256, archive.bytes, failure
	raw, err := json.Marshal(event)
	if err != nil || ctx.Err() != nil {
		return supportAccessErrorAt("encoding or cancellation")
	}
	raw = append(raw, '\n')
	if priorIncomplete {
		// Preserve every legacy byte and separate this new attempt without adopting the old one.
		raw = append([]byte{'\n'}, raw...)
	}
	n, err := a.file.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return supportAccessErrorAt("log write")
	}
	directory, err := a.root.Open(".")
	if err != nil {
		return supportAccessErrorAt("directory handle")
	}
	if err := errors.Join(a.file.Sync(), directory.Sync(), directory.Close()); err != nil {
		return supportAccessErrorAt("durability sync")
	}
	return nil
}

// removeSupportArchive removes only the exact inode published by this attempt.
func removeSupportArchive(parent *os.Root, name string, archive supportArchive) error {
	if archive.info == nil {
		return nil
	}
	info, err := parent.Lstat(name)
	if err != nil || !os.SameFile(info, archive.info) {
		return errors.New("INSTALL_SUPPORT_CLEANUP_REQUIRED: preserve the operator log and inspect the uncertain output path")
	}
	if err := parent.Remove(name); err != nil {
		return errors.New("INSTALL_SUPPORT_CLEANUP_REQUIRED: the private archive could not be withdrawn after an unconfirmed outcome")
	}
	directory, err := parent.Open(".")
	if err != nil {
		return errors.New("INSTALL_SUPPORT_CLEANUP_REQUIRED: archive withdrawal requires directory synchronization")
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return errors.New("INSTALL_SUPPORT_CLEANUP_REQUIRED: archive withdrawal synchronization is unconfirmed")
	}
	return nil
}
