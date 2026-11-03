// Package state persists the agent identity without exposing credentials in arguments or logs.
package state

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Identity struct {
	NodeID           string `json:"node_id"`
	Credential       string `json:"credential"`
	ControlOrigin    string `json:"control_origin"`
	ProtocolVersion  string `json:"protocol_version"`
	ExecutionProfile string `json:"execution_profile,omitempty"`
}

func owned(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid())
}
func ProtectedFile(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !owned(info) || info.Size() > 65536 {
		return nil, errors.New("PROTECTED_FILE_INVALID")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("PROTECTED_FILE_UNAVAILABLE")
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return nil, errors.New("PROTECTED_FILE_CHANGED")
	}
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(b) > 65536 {
		return nil, errors.New("PROTECTED_FILE_INVALID")
	}
	return b, nil
}
func Directory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("STATE_DIRECTORY_INVALID")
	}
	info, e := os.Lstat(path)
	if e != nil {
		return errors.New("STATE_DIRECTORY_UNAVAILABLE")
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("STATE_DIRECTORY_INVALID")
	}
	if info.Mode().Perm()&0077 != 0 {
		return errors.New("STATE_DIRECTORY_PERMISSIONS_INVALID")
	}
	if !owned(info) {
		return errors.New("STATE_DIRECTORY_OWNER_INVALID")
	}
	actual, e := filepath.EvalSymlinks(path)
	if e != nil || actual != path {
		return errors.New("STATE_DIRECTORY_SYMLINK_REJECTED")
	}
	return nil
}
func Load(dir string) (Identity, error) {
	var value Identity
	if e := Directory(dir); e != nil {
		return value, e
	}
	b, e := ProtectedFile(filepath.Join(dir, "identity.json"))
	if e != nil {
		return value, e
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF {
		return value, errors.New("IDENTITY_INVALID")
	}
	return value, nil
}
func Exists(dir string) bool {
	_, e := os.Lstat(filepath.Join(dir, "identity.json"))
	return !os.IsNotExist(e)
}

func Lock(dir string) (*os.File, error) {
	if e := Directory(dir); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, "agent.lock")
	if info, e := os.Lstat(path); e == nil && (!info.Mode().IsRegular() || !owned(info) || info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("AGENT_LOCK_INVALID")
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, errors.New("AGENT_LOCK_UNAVAILABLE")
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, errors.New("AGENT_ALREADY_RUNNING")
	}
	return f, nil
}
func SaveNew(dir string, value Identity) error {
	if e := Directory(dir); e != nil {
		return e
	}
	if Exists(dir) {
		return errors.New("IDENTITY_ALREADY_EXISTS")
	}
	b, e := json.Marshal(value)
	if e != nil {
		return errors.New("IDENTITY_INVALID")
	}
	temp, e := os.CreateTemp(dir, ".identity-*")
	if e != nil {
		return errors.New("IDENTITY_WRITE_FAILED")
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, e = temp.Write(b); e == nil {
		e = temp.Sync()
	}
	closeError := temp.Close()
	if e != nil || closeError != nil {
		return errors.New("IDENTITY_WRITE_FAILED")
	}
	// Link is atomic and refuses an existing destination; the durable temporary inode has mode 0600.
	if e = os.Link(name, filepath.Join(dir, "identity.json")); e != nil {
		return errors.New("IDENTITY_WRITE_FAILED")
	}
	d, e := os.Open(dir)
	if e != nil {
		return errors.New("IDENTITY_DIRECTORY_SYNC_FAILED")
	}
	defer d.Close()
	if d.Sync() != nil {
		return errors.New("IDENTITY_DIRECTORY_SYNC_FAILED")
	}
	return nil
}
