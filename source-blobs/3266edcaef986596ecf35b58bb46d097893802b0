package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

const derivedCatalogFile = "projectone-derived-catalog.json"

// Fixed storage ABI ceiling; retained failures are never pruned automatically.
const MaximumDerivedCopyAttempts = 16

// PreparedDerivedFiles is a verified local copy, not a registered or approved model.
// Only this owning package can create its private identity token.
type PreparedDerivedFiles struct {
	catalog wire.DerivedCatalog
	digest  string
	seals   map[string]os.FileInfo
}

func sealDerivedFiles(root *os.Root, c wire.DerivedCatalog) (map[string]os.FileInfo, error) {
	out := map[string]os.FileInfo{}
	names := []string{derivedCatalogFile}
	for _, asset := range c.Files {
		names = append(names, asset.Path)
	}
	for _, name := range names {
		info, e := root.Lstat(filepath.Join(derivedDirectory(c), name))
		if e != nil {
			return nil, derivedFailure()
		}
		out[name] = info
	}
	return out, nil
}
func preparedDerivedToken(root *os.Root, c wire.DerivedCatalog) (PreparedDerivedFiles, error) {
	var out PreparedDerivedFiles
	digest, e := c.Digest()
	if e != nil {
		return out, e
	}
	seals, e := sealDerivedFiles(root, c)
	if e != nil {
		return out, e
	}
	c.Files = append(c.Files[:0:0], c.Files...)
	return PreparedDerivedFiles{catalog: c, digest: digest, seals: seals}, nil
}
func (s *Service) verifyPreparedDerivedToken(p PreparedDerivedFiles) error {
	sha, e := p.catalog.Digest()
	if e != nil || sha != p.digest || len(p.seals) != len(p.catalog.Files)+1 {
		return derivedFailure()
	}
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return e
	}
	defer root.Close()
	if e = verifyDerivedParent(root); e != nil {
		return e
	}
	current, e := sealDerivedFiles(root, p.catalog)
	if e != nil {
		return e
	}
	for name, before := range p.seals {
		after := current[name]
		if after == nil || !after.Mode().IsRegular() || after.Mode().Perm() != 0444 || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return derivedFailure()
		}
		owner, ok := after.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
			return derivedFailure()
		}
	}
	return nil
}
func (p PreparedDerivedFiles) Catalog() wire.DerivedCatalog {
	c := p.catalog
	c.Files = append(c.Files[:0:0], c.Files...)
	return c
}
func (p PreparedDerivedFiles) Digest() string { return p.digest }

func derivedDirectory(c wire.DerivedCatalog) string { return filepath.Join("derived", c.ModelID) }
func derivedFailure() error                         { return fail("MODEL_DERIVED_STORAGE_INVALID") }
func derivedRegular(root *os.Root, path string, size int64, mode os.FileMode) (*os.File, error) {
	info, e := root.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() != size || info.Mode().Perm() != mode {
		return nil, derivedFailure()
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		return nil, derivedFailure()
	}
	file, e := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, derivedFailure()
	}
	opened, e := file.Stat()
	if e != nil || !os.SameFile(info, opened) || info.Size() != opened.Size() || !info.ModTime().Equal(opened.ModTime()) {
		file.Close()
		return nil, derivedFailure()
	}
	return file, nil
}
func verifyDerivedParent(root *os.Root) error {
	info, e := root.Lstat("derived")
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return derivedFailure()
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return derivedFailure()
	}
	return nil
}
func verifyDerivedMetadata(root *os.Root, c wire.DerivedCatalog) error {
	if e := verifyDerivedParent(root); e != nil {
		return e
	}
	raw, e := c.CanonicalBytes()
	if e != nil {
		return e
	}
	path := derivedDirectory(c)
	info, e := root.Lstat(path)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0555 || info.Mode()&os.ModeSymlink != 0 {
		return derivedFailure()
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return derivedFailure()
	}
	directory, e := root.Open(path)
	if e != nil {
		return derivedFailure()
	}
	names, e := directory.Readdirnames(len(c.Files) + 2)
	directory.Close()
	if e != nil && e != io.EOF || len(names) != len(c.Files)+1 {
		return derivedFailure()
	}
	allowed := map[string]bool{derivedCatalogFile: true}
	for _, asset := range c.Files {
		allowed[asset.Path] = true
	}
	for _, name := range names {
		if !allowed[name] {
			return derivedFailure()
		}
	}
	receipt, e := derivedRegular(root, filepath.Join(path, derivedCatalogFile), int64(len(raw)), 0444)
	if e != nil {
		return e
	}
	actual, e := io.ReadAll(io.LimitReader(receipt, int64(len(raw))+1))
	receipt.Close()
	if e != nil || !bytes.Equal(actual, raw) {
		return derivedFailure()
	}
	return nil
}
func verifyDerivedDirectory(ctx context.Context, root *os.Root, c wire.DerivedCatalog) error {
	if e := verifyDerivedMetadata(root, c); e != nil {
		return e
	}
	path := derivedDirectory(c)
	for _, asset := range c.Files {
		file, e := derivedRegular(root, filepath.Join(path, asset.Path), asset.SizeBytes, 0444)
		if e != nil {
			return e
		}
		before, e := file.Stat()
		if e != nil {
			file.Close()
			return derivedFailure()
		}
		sum := sha256.New()
		n, e := io.Copy(sum, contextReader{ctx, io.LimitReader(file, asset.SizeBytes+1)})
		after, statError := file.Stat()
		file.Close()
		if e != nil {
			return e
		}
		if statError != nil || n != asset.SizeBytes || hex.EncodeToString(sum.Sum(nil)) != asset.SHA256 || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
			return derivedFailure()
		}
	}
	return nil
}

// PrepareDerivedFiles consumes no pathname supplied by another module. The caller
// retains ownership of the pinned source FDs and must authorize again before registration.
func (s *Service) PrepareDerivedFiles(ctx context.Context, c wire.DerivedCatalog, source map[string]*os.File, minimumFreeBytes int64) (PreparedDerivedFiles, error) {
	return s.prepareDerivedFiles(ctx, c, source, minimumFreeBytes, false)
}

// Snapshot sources are pinned and freshly verified by this owning module before allowing their immutable0444 mode.
func (s *Service) prepareDerivedFiles(ctx context.Context, c wire.DerivedCatalog, source map[string]*os.File, minimumFreeBytes int64, immutableSources bool) (PreparedDerivedFiles, error) {
	var out PreparedDerivedFiles
	if e := c.Validate(); e != nil {
		return out, e
	}
	if minimumFreeBytes < 1048576 || minimumFreeBytes > 1099511627776 {
		return out, fail("MODEL_DERIVED_COPY_POLICY_INVALID")
	}
	if len(source) != len(c.Files) {
		return out, derivedFailure()
	}
	for _, asset := range c.Files {
		if source[asset.Path] == nil {
			return out, derivedFailure()
		}
	}
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return out, e
	}
	defer root.Close()
	if e = directory(root, "derived"); e != nil {
		return out, e
	}
	if e = verifyDerivedParent(root); e != nil {
		return out, e
	}
	// One fixed identity serializes only its own copy. Retain the protected lock file.
	lockName := filepath.Join("derived", ".lock-"+c.ModelID)
	lock, e := root.OpenFile(lockName, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return out, derivedFailure()
	}
	defer lock.Close()
	info, e := lock.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != 0 || info.Mode().Perm() != 0600 {
		return out, derivedFailure()
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		return out, derivedFailure()
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return out, fail("MODEL_DERIVED_COPY_BUSY")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if e = ctx.Err(); e != nil {
		return out, e
	}
	destination := derivedDirectory(c)
	if _, e = root.Lstat(destination); e == nil {
		if e = verifyDerivedDirectory(ctx, root, c); e != nil {
			return out, e
		}
		return preparedDerivedToken(root, c)
	} else if !os.IsNotExist(e) {
		return out, derivedFailure()
	}
	directoryFile, e := root.Open("derived")
	if e != nil {
		return out, derivedFailure()
	}
	count, entries := 0, 0
	for {
		names, readError := directoryFile.Readdirnames(64)
		for _, name := range names {
			entries++
			if strings.HasPrefix(name, ".staging-"+c.ModelID+"-") {
				count++
			}
		}
		if entries > 65536 || count >= MaximumDerivedCopyAttempts {
			directoryFile.Close()
			return out, fail("MODEL_DERIVED_COPY_ATTEMPT_LIMIT")
		}
		if readError == io.EOF {
			break
		}
		if readError != nil {
			directoryFile.Close()
			return out, derivedFailure()
		}
	}
	directoryFile.Close()
	var stats syscall.Statfs_t
	if syscall.Statfs(s.RootDirectory, &stats) != nil {
		return out, derivedFailure()
	}
	required := minimumFreeBytes
	for _, asset := range c.Files {
		required += asset.SizeBytes
	}
	if uint64(stats.Bavail)*uint64(stats.Bsize) < uint64(required) {
		return out, fail("MODEL_DERIVED_DISK_SPACE_LOW")
	}
	stage := filepath.Join("derived", ".staging-"+c.ModelID+"-"+uuid.NewString())
	if root.Mkdir(stage, 0700) != nil {
		return out, fail("MODEL_DERIVED_PARTIAL_COPY_RETAINED")
	}
	for _, asset := range c.Files {
		if e = ctx.Err(); e != nil {
			return out, e
		}
		input := source[asset.Path]
		before, e := input.Stat()
		if e != nil || !before.Mode().IsRegular() || before.Size() != asset.SizeBytes || before.Mode().Perm()&0077 != 0 && !(immutableSources && before.Mode().Perm() == 0444) {
			return out, derivedFailure()
		}
		owner, ok := before.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
			return out, derivedFailure()
		}
		if _, e = input.Seek(0, io.SeekStart); e != nil {
			return out, derivedFailure()
		}
		file, e := root.OpenFile(filepath.Join(stage, asset.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
		if e != nil {
			return out, derivedFailure()
		}
		sum := sha256.New()
		n, copyError := io.CopyBuffer(io.MultiWriter(file, sum), contextReader{ctx, io.LimitReader(input, asset.SizeBytes+1)}, make([]byte, 1048576))
		after, statError := input.Stat()
		if copyError == nil {
			copyError = file.Chmod(0444)
		}
		if copyError == nil {
			copyError = file.Sync()
		}
		closeError := file.Close()
		if copyError != nil {
			return out, copyError
		}
		if closeError != nil || statError != nil || n != asset.SizeBytes || hex.EncodeToString(sum.Sum(nil)) != asset.SHA256 || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			return out, derivedFailure()
		}
	}
	raw, _ := c.CanonicalBytes()
	receipt, e := root.OpenFile(filepath.Join(stage, derivedCatalogFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return out, derivedFailure()
	}
	_, e = receipt.Write(raw)
	if e == nil {
		e = receipt.Chmod(0444)
	}
	if e == nil {
		e = receipt.Sync()
	}
	closeError := receipt.Close()
	if e != nil || closeError != nil {
		return out, derivedFailure()
	}
	if e = syncDirectory(root, stage); e != nil {
		return out, e
	}
	if e = root.Chmod(stage, 0555); e != nil {
		return out, derivedFailure()
	}
	if _, e = root.Lstat(destination); !os.IsNotExist(e) {
		return out, derivedFailure()
	}
	if e = root.Rename(stage, destination); e != nil {
		return out, derivedFailure()
	}
	if e = syncDirectory(root, "derived"); e != nil {
		return out, e
	}
	if e = verifyDerivedDirectory(ctx, root, c); e != nil {
		return out, e
	}
	return preparedDerivedToken(root, c)
}
func (s *Service) VerifyDerivedFiles(ctx context.Context, c wire.DerivedCatalog) error {
	if e := c.Validate(); e != nil {
		return e
	}
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return e
	}
	defer root.Close()
	return verifyDerivedDirectory(ctx, root, c)
}
