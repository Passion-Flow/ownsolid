package mysqlbackup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func owned(info os.FileInfo, directory bool) bool {
	if info == nil {
		return false
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.Mode() == os.ModeDir|0700
	}
	return info.Mode() == 0600 && owner.Nlink == 1
}
func openPrivateRoot(path string) (*os.Root, error) {
	bad := errors.New("MYSQL_BACKUP_PRIVATE_PATH_INVALID")
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
		return nil, bad
	}
	before, e := os.Lstat(path)
	if e != nil || !owned(before, true) {
		return nil, bad
	}
	root, e := os.OpenRoot(path)
	if e != nil {
		return nil, bad
	}
	after, e := root.Stat(".")
	if e != nil || !owned(after, true) || !os.SameFile(before, after) {
		root.Close()
		return nil, bad
	}
	return root, nil
}

func readPrivateDocument(path string, maximum int64, value any) ([]byte, error) {
	if maximum < 1 || maximum > 16<<20 {
		return nil, errors.New("MYSQL_BACKUP_DOCUMENT_LIMIT_INVALID")
	}
	root, e := openPrivateRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer root.Close()
	name := filepath.Base(path)
	before, e := root.Lstat(name)
	if e != nil || !owned(before, false) || before.Size() < 1 || before.Size() > maximum {
		return nil, errors.New("MYSQL_BACKUP_PRIVATE_FILE_INVALID")
	}
	file, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, errors.New("MYSQL_BACKUP_PRIVATE_FILE_INVALID")
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return nil, errors.New("MYSQL_BACKUP_PRIVATE_FILE_CHANGED")
	}
	raw, e := io.ReadAll(io.LimitReader(file, maximum+1))
	after, ae := file.Stat()
	current, ce := root.Lstat(name)
	parent, pe := os.Lstat(filepath.Dir(path))
	openedParent, oe := root.Stat(".")
	if e != nil || ae != nil || ce != nil || pe != nil || oe != nil || !owned(after, false) || !owned(current, false) || !owned(parent, true) || !os.SameFile(parent, openedParent) || !os.SameFile(before, after) || !os.SameFile(before, current) || int64(len(raw)) != before.Size() || after.Size() != before.Size() || current.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || !current.ModTime().Equal(before.ModTime()) {
		return nil, errors.New("MYSQL_BACKUP_PRIVATE_FILE_CHANGED")
	}
	if strict.DecodeStrict(raw, value) != nil {
		return nil, errors.New("MYSQL_BACKUP_DOCUMENT_INVALID")
	}
	return raw, nil
}

func syncRoot(root *os.Root) error {
	file, e := root.Open(".")
	if e != nil {
		return errors.New("MYSQL_BACKUP_DIRECTORY_SYNC_FAILED")
	}
	e = file.Sync()
	ce := file.Close()
	if e != nil || ce != nil {
		return errors.New("MYSQL_BACKUP_DIRECTORY_SYNC_FAILED")
	}
	return nil
}
func writeNew(root *os.Root, name string, content []byte) error {
	file, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return errors.New("MYSQL_BACKUP_EXCLUSIVE_CREATE_FAILED")
	}
	n, e := file.Write(content)
	if e == nil && n == len(content) {
		e = file.Sync()
	}
	ce := file.Close()
	if e != nil || ce != nil || n != len(content) {
		return errors.New("MYSQL_BACKUP_WRITE_UNCERTAIN")
	}
	return syncRoot(root)
}

func requireSpace(path string, additional, minimum int64) error {
	if additional < 0 || minimum < 0 {
		return errors.New("MYSQL_BACKUP_SPACE_INVALID")
	}
	file, e := os.Open(path)
	if e != nil {
		return errors.New("MYSQL_BACKUP_SPACE_UNAVAILABLE")
	}
	defer file.Close()
	return requireSpaceFile(file, additional, minimum)
}

func requireSpaceFile(file *os.File, additional, minimum int64) error {
	if file == nil || additional < 0 || minimum < 0 {
		return errors.New("MYSQL_BACKUP_SPACE_INVALID")
	}
	var stat syscall.Statfs_t
	if syscall.Fstatfs(int(file.Fd()), &stat) != nil || stat.Bsize <= 0 {
		return errors.New("MYSQL_BACKUP_SPACE_UNAVAILABLE")
	}
	available, capacity := uint64(stat.Bavail)*uint64(stat.Bsize), uint64(stat.Blocks)*uint64(stat.Bsize)
	if uint64(additional) > available || uint64(minimum) > available-uint64(additional) || available-uint64(additional) < capacity/10*3+((capacity%10)*3+9)/10 {
		return errors.New("MYSQL_BACKUP_SPACE_HEADROOM_REQUIRED")
	}
	return nil
}

func privateArchive(ctx context.Context, path string, maximum int64, expected string, consume func(*os.File) error) (result error) {
	if maximum < 1 || !digestPattern.MatchString(expected) || consume == nil {
		return errors.New("MYSQL_BACKUP_ARCHIVE_INVALID")
	}
	root, e := openPrivateRoot(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer root.Close()
	before, e := root.Lstat(filepath.Base(path))
	if e != nil || !owned(before, false) || before.Size() < 1 || before.Size() > maximum {
		return errors.New("MYSQL_BACKUP_ARCHIVE_INVALID")
	}
	file, e := root.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return errors.New("MYSQL_BACKUP_ARCHIVE_INVALID")
	}
	defer func() {
		if file.Close() != nil {
			result = errors.Join(result, errors.New("MYSQL_BACKUP_ARCHIVE_CLOSE_FAILED"))
		}
	}()
	check := func() error {
		current, e := file.Stat()
		named, ne := root.Lstat(filepath.Base(path))
		parent, pe := os.Lstat(filepath.Dir(path))
		actualParent, ae := root.Stat(".")
		if e != nil || ne != nil || pe != nil || ae != nil || !owned(current, false) || !owned(named, false) || !owned(parent, true) || !os.SameFile(parent, actualParent) || !os.SameFile(before, current) || !os.SameFile(before, named) || current.Size() != before.Size() || named.Size() != before.Size() || !current.ModTime().Equal(before.ModTime()) || !named.ModTime().Equal(before.ModTime()) {
			return errors.New("MYSQL_BACKUP_ARCHIVE_CHANGED")
		}
		if _, e = file.Seek(0, io.SeekStart); e != nil {
			return errors.New("MYSQL_BACKUP_ARCHIVE_CHANGED")
		}
		sha, size, e := hashStream(ctx, nil, file, maximum)
		if e != nil || sha != expected || size != before.Size() {
			return errors.New("MYSQL_BACKUP_ARCHIVE_CHANGED")
		}
		if _, e = file.Seek(0, io.SeekStart); e != nil {
			return errors.New("MYSQL_BACKUP_ARCHIVE_CHANGED")
		}
		return nil
	}
	if e = check(); e != nil {
		return e
	}
	result = consume(file)
	return errors.Join(result, check())
}
