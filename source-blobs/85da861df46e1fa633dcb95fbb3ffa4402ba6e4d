package factory

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// A prepared inventory has no legitimate writer during verification. Keep this
// stricter directory snapshot separate from the factory's publication lifecycle.
func samePreparedDirectory(before, after os.FileInfo) bool {
	if !owned(before) || !owned(after) || before.Mode() != os.ModeDir|0700 || after.Mode() != os.ModeDir|0700 || !sameVersion(before, after) || !sameRegularChangeTime(before, after) {
		return false
	}
	a, b := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	return a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink
}

func preparedDirectoryUnchanged(root *os.Root, before os.FileInfo) bool {
	if !regularParentUnchanged(root) {
		return false
	}
	opened, e := root.Stat(".")
	current, pe := os.Lstat(filepath.Clean(root.Name()))
	return e == nil && pe == nil && samePreparedDirectory(before, opened) && samePreparedDirectory(before, current)
}

// The terminal dot forces directory traversal even if a previously checked
// directory becomes a FIFO. Do not clean this suffix before OpenRoot.
func openPreparedDirectory(directory string, before os.FileInfo) (*os.Root, error) {
	if !samePreparedDirectory(before, before) || !canonical(directory) {
		return nil, fail("RELEASE_PREPARED_DIRECTORY_IDENTITY_INVALID")
	}
	root, e := os.OpenRoot(directory + string(filepath.Separator) + ".")
	if e != nil {
		return nil, fail("RELEASE_PREPARED_DIRECTORY_UNAVAILABLE")
	}
	if !preparedDirectoryUnchanged(root, before) {
		root.Close()
		return nil, fail("RELEASE_PREPARED_DIRECTORY_CHANGED")
	}
	return root, nil
}

func readPreparedDirectory(ctx context.Context, directory string, before os.FileInfo, visit func(*os.Root, os.DirEntry) error) (err error) {
	if ctx.Err() != nil {
		return fail("RELEASE_PREPARED_VERIFICATION_INTERRUPTED")
	}
	root, e := openPreparedDirectory(directory, before)
	if e != nil {
		return e
	}
	defer func() {
		if e := root.Close(); e != nil && err == nil {
			err = fail("RELEASE_PREPARED_DIRECTORY_READ_FAILED")
		}
	}()
	seen := map[string]os.FileMode{}
	if err := scanPreparedEntries(ctx, root, ".", func(entry os.DirEntry) error {
		if _, duplicate := seen[entry.Name()]; duplicate {
			return fail("RELEASE_PREPARED_DIRECTORY_CHANGED")
		}
		seen[entry.Name()] = entry.Type()
		return visit(root, entry)
	}); err != nil {
		return err
	}
	// A directory can gain an entry in the same filesystem clock tick.
	if err := scanPreparedEntries(ctx, root, ".", func(entry os.DirEntry) error {
		mode, present := seen[entry.Name()]
		if !present || mode != entry.Type() {
			return fail("RELEASE_PREPARED_DIRECTORY_CHANGED")
		}
		delete(seen, entry.Name())
		return nil
	}); err != nil {
		return err
	}
	if len(seen) != 0 || !preparedDirectoryUnchanged(root, before) {
		return fail("RELEASE_PREPARED_DIRECTORY_CHANGED")
	}
	return nil
}

func scanPreparedEntries(ctx context.Context, root *os.Root, name string, visit func(os.DirEntry) error) (err error) {
	folder, e := root.OpenFile(name, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return fail("RELEASE_PREPARED_DIRECTORY_UNAVAILABLE")
	}
	defer func() {
		if e := folder.Close(); e != nil && err == nil {
			err = fail("RELEASE_PREPARED_DIRECTORY_READ_FAILED")
		}
	}()
	for {
		if ctx.Err() != nil {
			return fail("RELEASE_PREPARED_VERIFICATION_INTERRUPTED")
		}
		entries, e := folder.ReadDir(directoryReadBatchEntries)
		if e != nil && e != io.EOF {
			return fail("RELEASE_PREPARED_DIRECTORY_READ_FAILED")
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return fail("RELEASE_PREPARED_VERIFICATION_INTERRUPTED")
			}
			if e := visit(entry); e != nil {
				return e
			}
		}
		if e == io.EOF {
			break
		}
	}
	if ctx.Err() != nil {
		return fail("RELEASE_PREPARED_VERIFICATION_INTERRUPTED")
	}
	return nil
}

// This is also the deterministic checked-open boundary exercised by the tests.
func readPreparedFile(ctx context.Context, root *os.Root, name string, before os.FileInfo, size int64, digest string, license wire.Asset) error {
	if ctx.Err() != nil {
		return fail("RELEASE_PREPARED_VERIFICATION_INTERRUPTED")
	}
	if !regularInput(before, size, true, 1) || before.Mode() != 0600 || before.Size() != size {
		return fail("RELEASE_PREPARED_UNKNOWN_OR_UNSAFE_FILE")
	}
	input, e := openPinnedRegular(root, name, before, size, true, 1)
	if e != nil {
		return fail("RELEASE_PREPARED_FILE_CHANGED")
	}
	var actual string
	var readError error
	if license.Kind == wire.LicenseTrustAssetKind {
		actual, readError = checkLicenseTrust(ctx, input, nil, license)
	} else {
		actual, readError = streamHash(ctx, input, nil, size)
	}
	after, statError := input.Stat()
	closeError := input.Close()
	current, pathError := root.Lstat(name)
	if readError != nil || statError != nil || closeError != nil || pathError != nil || !sameRegularSnapshot(before, after, size, true, 1) || !sameRegularSnapshot(before, current, size, true, 1) || !regularParentUnchanged(root) || actual != digest {
		return fail("RELEASE_PREPARED_FILE_CHANGED")
	}
	return nil
}

// Revisit earlier payloads after all bytes and authority files were read. Hashing
// each file once is insufficient when an earlier file changes later in the scan.
func verifyPreparedSnapshot(ctx context.Context, root *os.Root, files, directories map[string]os.FileInfo) error {
	if ctx.Err() != nil {
		return fail("RELEASE_PREPARED_VERIFICATION_INTERRUPTED")
	}
	if !preparedDirectoryUnchanged(root, directories["."]) {
		return fail("RELEASE_PREPARED_DIRECTORY_CHANGED")
	}
	entries := make(map[string]struct{}, len(files)+len(directories)-1)
	for name, before := range directories {
		if ctx.Err() != nil {
			return fail("RELEASE_PREPARED_VERIFICATION_INTERRUPTED")
		}
		current, e := root.Lstat(filepath.FromSlash(name))
		if e != nil || !samePreparedDirectory(before, current) {
			return fail("RELEASE_PREPARED_DIRECTORY_CHANGED")
		}
		if err := scanPreparedEntries(ctx, root, filepath.FromSlash(name), func(entry os.DirEntry) error {
			path := filepath.ToSlash(filepath.Join(filepath.FromSlash(name), entry.Name()))
			_, knownDirectory := directories[path]
			_, knownFile := files[path]
			_, duplicate := entries[path]
			if duplicate || !(entry.IsDir() && knownDirectory || entry.Type().IsRegular() && knownFile) {
				return fail("RELEASE_PREPARED_DIRECTORY_CHANGED")
			}
			entries[path] = struct{}{}
			return nil
		}); err != nil {
			return err
		}
	}
	if len(entries) != len(files)+len(directories)-1 {
		return fail("RELEASE_PREPARED_DIRECTORY_CHANGED")
	}
	for name, before := range files {
		if ctx.Err() != nil {
			return fail("RELEASE_PREPARED_VERIFICATION_INTERRUPTED")
		}
		current, e := root.Lstat(filepath.FromSlash(name))
		if e != nil || before == nil || before.Mode() != 0600 || !sameRegularSnapshot(before, current, before.Size(), true, 1) {
			return fail("RELEASE_PREPARED_FILE_CHANGED")
		}
	}
	if ctx.Err() != nil {
		return fail("RELEASE_PREPARED_VERIFICATION_INTERRUPTED")
	}
	if !preparedDirectoryUnchanged(root, directories["."]) {
		return fail("RELEASE_PREPARED_DIRECTORY_CHANGED")
	}
	return nil
}
