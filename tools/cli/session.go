package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

type sessionStore struct {
	root *os.Root
	name string
}

func openSessionStore(cfg settings, create bool) (sessionStore, error) {
	if create {
		if err := os.MkdirAll(cfg.dir, 0o700); err != nil {
			return sessionStore{}, fmt.Errorf("create the CLI config directory: %w", err)
		}
	}
	root, err := os.OpenRoot(cfg.dir)
	if err != nil {
		return sessionStore{}, fmt.Errorf("open the CLI config directory: %w", err)
	}
	info, err := root.Stat(".")
	if err != nil {
		root.Close()
		return sessionStore{}, fmt.Errorf("inspect the CLI config directory: %w", err)
	}
	if !ownedMode(info, 0o700) {
		root.Close()
		return sessionStore{}, fmt.Errorf("CLI config directory %q must be owned by you with mode 0700; fix its permissions or set OWNSOLID_CONFIG_DIR", cfg.dir)
	}
	name := fmt.Sprintf("session-%x", sha256.Sum256([]byte(cfg.origin.String())))
	return sessionStore{root: root, name: name}, nil
}

func ownedMode(info os.FileInfo, mode os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	const modeBits = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	return ok && int(stat.Uid) == os.Geteuid() && info.Mode()&modeBits == mode
}

func privateFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || !ownedMode(info, 0o600) {
		return errors.New("file must be regular, owned by you, and have mode 0600")
	}
	return nil
}

func (s sessionStore) lock() (*os.File, error) {
	f, err := s.root.OpenFile(s.name+".lock", os.O_CREATE|os.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the session lock: %w", err)
	}
	if err := privateFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("inspect the session lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errors.New("another login or logout is running for this server; wait for it to finish and retry")
		}
		return nil, fmt.Errorf("lock the session: %w", err)
	}
	return f, nil
}

func validSession(value string) bool {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}

func (s sessionStore) read() (string, error) {
	f, err := s.root.OpenFile(s.name, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("open the session credential: %w", err)
	}
	defer f.Close()
	if err := privateFile(f); err != nil {
		return "", fmt.Errorf("inspect session credential %q: %w", f.Name(), err)
	}
	data, err := io.ReadAll(io.LimitReader(f, 44))
	if err != nil {
		return "", fmt.Errorf("read the session credential: %w", err)
	}
	if !validSession(string(data)) {
		return "", fmt.Errorf("session credential %q is invalid; move it aside and run ownsolid login", f.Name())
	}
	return string(data), nil
}

func (s sessionStore) save(value string) error {
	tmp := ".session-" + rand.Text()
	f, err := s.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer s.root.Remove(tmp)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	_, writeErr := io.WriteString(f, value)
	var syncErr error
	if writeErr == nil {
		syncErr = f.Sync()
	}
	if err := errors.Join(writeErr, syncErr, f.Close()); err != nil {
		return err
	}
	return s.root.Rename(tmp, s.name)
}

func (s sessionStore) remove() error {
	if err := s.root.Remove(s.name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove the local session credential: %w", err)
	}
	return nil
}
