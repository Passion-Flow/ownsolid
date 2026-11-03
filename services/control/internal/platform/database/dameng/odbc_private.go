package dameng

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// MaximumConfigurationBytes is a bounded document-format invariant, not an
// ambient deployment setting. Individual callers may require a smaller bound.
const MaximumConfigurationBytes int64 = 1 << 20
const odbcPrivateInputInvalid = "DAMENG_ODBC_PRIVATE_INPUT_INVALID"

type inputError struct{ code string }

func (e *inputError) Error() string { return e.code }
func fail(code string) error        { return &inputError{code: code} }
func canonicalPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != string(filepath.Separator) && !strings.ContainsAny(path, "\x00\r\n")
}
func odbcPrivateOwned(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky|os.ModeSymlink) != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.Mode() == os.ModeDir|0700
	}
	return info.Mode() == 0600 && stat.Nlink == 1
}
func odbcPrivateSameFile(before, after os.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) && odbcPrivateOwned(after, false) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

type odbcPrivateSnapshot struct {
	parent, name      string
	directory, before os.FileInfo
	root              *os.Root
	file              *os.File
	maximum           int64
}

func (*odbcPrivateSnapshot) String() string   { return "DAMENG_ODBC_PRIVATE_SNAPSHOT_REDACTED" }
func (*odbcPrivateSnapshot) GoString() string { return "DAMENG_ODBC_PRIVATE_SNAPSHOT_REDACTED" }

// openODBCPrivateInput pins the actual owned parent and file. It does not accept
// a configurable filesystem root, path substitution, fixture or native driver.
func openODBCPrivateInput(path string, maximum int64) (*odbcPrivateSnapshot, error) {
	bad := fail(odbcPrivateInputInvalid)
	if maximum < 1 || maximum > MaximumConfigurationBytes || !canonicalPath(path) {
		return nil, bad
	}
	parent := filepath.Dir(path)
	resolved, e := filepath.EvalSymlinks(parent)
	if e != nil || resolved != parent {
		return nil, bad
	}
	directory, e := os.Lstat(parent)
	if e != nil || !odbcPrivateOwned(directory, true) {
		return nil, bad
	}
	root, e := os.OpenRoot(parent)
	if e != nil {
		return nil, bad
	}
	s := &odbcPrivateSnapshot{parent: parent, name: filepath.Base(path), directory: directory, root: root, maximum: maximum}
	keep := false
	defer func() {
		if !keep {
			_ = s.close()
		}
	}()
	openedDirectory, e := root.Stat(".")
	if e != nil || !odbcPrivateOwned(openedDirectory, true) || !os.SameFile(directory, openedDirectory) {
		return nil, bad
	}
	before, e := root.Lstat(s.name)
	if e != nil || !odbcPrivateOwned(before, false) || before.Size() < 1 || before.Size() > maximum {
		return nil, bad
	}
	s.before = before
	s.file, e = root.OpenFile(s.name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, bad
	}
	opened, e := s.file.Stat()
	if e != nil || !odbcPrivateSameFile(before, opened) {
		return nil, bad
	}
	keep = true
	return s, nil
}
func (s *odbcPrivateSnapshot) verify(raw []byte) error {
	bad := fail(odbcPrivateInputInvalid)
	if s == nil || s.file == nil || s.root == nil || s.before == nil || int64(len(raw)) != s.before.Size() || int64(len(raw)) > s.maximum {
		return bad
	}
	after, e := s.file.Stat()
	if e != nil || !odbcPrivateSameFile(s.before, after) {
		return bad
	}
	current, e := s.root.Lstat(s.name)
	if e != nil || !odbcPrivateSameFile(s.before, current) {
		return bad
	}
	openedDirectory, e := s.root.Stat(".")
	if e != nil || !odbcPrivateOwned(openedDirectory, true) || !os.SameFile(s.directory, openedDirectory) {
		return bad
	}
	currentDirectory, e := os.Lstat(s.parent)
	if e != nil || !odbcPrivateOwned(currentDirectory, true) || !os.SameFile(s.directory, currentDirectory) {
		return bad
	}
	resolved, e := filepath.EvalSymlinks(s.parent)
	if e != nil || resolved != s.parent {
		return bad
	}
	return nil
}
func (s *odbcPrivateSnapshot) read() ([]byte, error) {
	if s == nil || s.file == nil || s.maximum < 1 || s.maximum > MaximumConfigurationBytes {
		return nil, fail(odbcPrivateInputInvalid)
	}
	raw, e := io.ReadAll(io.LimitReader(s.file, s.maximum+1))
	if e != nil {
		clear(raw)
		return nil, fail(odbcPrivateInputInvalid)
	}
	if e = s.verify(raw); e != nil {
		clear(raw)
		return nil, fail(odbcPrivateInputInvalid)
	}
	return raw, nil
}
func (s *odbcPrivateSnapshot) close() error {
	if s == nil {
		return nil
	}
	failed := false
	if s.file != nil {
		if e := s.file.Close(); e != nil {
			failed = true
		}
		s.file = nil
	}
	if s.root != nil {
		if e := s.root.Close(); e != nil {
			failed = true
		}
		s.root = nil
	}
	if failed {
		return fail(odbcPrivateInputInvalid)
	}
	return nil
}

// readODBCPrivateInput publishes bytes only after both identity verification and
// owned-resource closure. OS errors, credential paths and partial bytes stay private.
func readODBCPrivateInput(path string, maximum int64) (out []byte, result error) {
	snapshot, e := openODBCPrivateInput(path, maximum)
	if e != nil {
		return nil, fail(odbcPrivateInputInvalid)
	}
	defer func() {
		if e := snapshot.close(); e != nil {
			clear(out)
			out = nil
			result = fail(odbcPrivateInputInvalid)
		}
	}()
	out, result = snapshot.read()
	return out, result
}
