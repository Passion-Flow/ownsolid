package scope

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path"
	"strings"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const postgresBackupArchiveEntries = 6

func backupHostEntries(p BackupPlan) int {
	directories := map[string]bool{".": true}
	for _, name := range backupExpectedFiles(p) {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			directories[parent] = true
		}
	}
	return len(backupExpectedFiles(p)) + 1 + len(directories)
}

// Counting existing immutable archives uses the real file's bounded Seek
// support, so tar.Next need not reread large model payloads. File/hash authority
// remains the independently verified manifest and SDK stream, not this count.
func backupTarEntries(ctx context.Context, root *os.Root, name string, maximumBytes int64, maximumEntries int) (int, error) {
	before, e := root.Lstat(name)
	if e != nil || !owned(before) || before.Mode() != 0600 || before.Size() < 1024 || before.Size() > maximumBytes {
		return 0, failure("OFFLINE_SCOPE_BACKUP_ARCHIVE_FILE_REQUIRED")
	}
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink != 1 {
		return 0, failure("OFFLINE_SCOPE_BACKUP_FILE_LINK_REFUSED")
	}
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return 0, failure("OFFLINE_SCOPE_BACKUP_FILE_UNAVAILABLE")
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return 0, failure("OFFLINE_SCOPE_BACKUP_FILE_CHANGED")
	}
	r := tar.NewReader(f)
	count := 0
	for {
		if ctx.Err() != nil {
			return 0, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		h, e := r.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return 0, failure("OFFLINE_SCOPE_BACKUP_ARCHIVE_INVALID")
		}
		count++
		if count > maximumEntries {
			return 0, failure("OFFLINE_SCOPE_BACKUP_ENTRY_LIMIT")
		}
		position, e := f.Seek(0, io.SeekCurrent)
		if e != nil || h.Size < 0 || position > before.Size()-h.Size || !wire.SafePath(strings.TrimSuffix(h.Name, "/")) || h.Linkname != "" || h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg {
			return 0, failure("OFFLINE_SCOPE_BACKUP_ARCHIVE_INVALID")
		}
	}
	after, e := f.Stat()
	named, ne := root.Lstat(name)
	if e != nil || ne != nil || !os.SameFile(before, after) || !os.SameFile(before, named) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || after.Mode() != before.Mode() {
		return 0, failure("OFFLINE_SCOPE_BACKUP_FILE_CHANGED")
	}
	return count, nil
}

func remainingBackupEntries(ctx context.Context, root *os.Root, p BackupPlan) (int, error) {
	remaining := p.Request.MaximumEntries - backupHostEntries(p)
	if remaining < 0 {
		return 0, failure("OFFLINE_SCOPE_BACKUP_ENTRY_LIMIT")
	}
	for role := range p.Jobs {
		name := role + ".tar"
		if _, e := root.Lstat(name); os.IsNotExist(e) {
			continue
		}
		count, e := backupTarEntries(ctx, root, name, p.Request.MaximumBytes, remaining)
		if e != nil {
			return 0, e
		}
		remaining -= count
	}
	return remaining, nil
}
