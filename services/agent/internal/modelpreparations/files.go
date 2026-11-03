package modelpreparations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type FileDownload func(context.Context, string, datawire.Asset, string) error

// Fixed filesystem ABI ceiling: two partitions, at most nine resume assets, request/marker and their directories.
const MaximumPreparedInputEntries = 32

func secureRoot(path string, uid int, private bool) error {
	if !cleanPath(path) {
		return fail("MODEL_PREPARATION_AGENT_PATH_INVALID")
	}
	for current := path; ; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if e != nil {
			return fail("MODEL_PREPARATION_AGENT_PATH_INVALID")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(uid) || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || current == path && private && info.Mode().Perm()&0077 != 0 {
			return fail("MODEL_PREPARATION_AGENT_PATH_INVALID")
		}
		if current == "/" {
			break
		}
	}
	return nil
}
func newPrivateFile(path string, body []byte, uid, gid int) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return fail("MODEL_PREPARATION_AGENT_IO_FAILED")
	}
	_, e = f.Write(body)
	if e == nil {
		e = f.Chown(uid, gid)
	}
	if e == nil {
		e = f.Sync()
	}
	closeError := f.Close()
	if e != nil || closeError != nil {
		return fail("MODEL_PREPARATION_AGENT_IO_FAILED")
	}
	return syncDirectory(filepath.Dir(path))
}
func rootDirectory(path string) error {
	if e := os.Mkdir(path, 0700); e != nil && !os.IsExist(e) {
		return fail("MODEL_PREPARATION_AGENT_IO_FAILED")
	}
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fail("MODEL_PREPARATION_AGENT_PATH_INVALID")
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) {
		return fail("MODEL_PREPARATION_AGENT_PATH_INVALID")
	}
	actual, e := filepath.EvalSymlinks(path)
	if e != nil || actual != path {
		return fail("MODEL_PREPARATION_AGENT_PATH_INVALID")
	}
	return nil
}
func availableSpace(path string) (uint64, error) {
	var stats syscall.Statfs_t
	if syscall.Statfs(path, &stats) != nil {
		return 0, fail("MODEL_PREPARATION_AGENT_IO_FAILED")
	}
	return uint64(stats.Bavail) * uint64(stats.Bsize), nil
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.source.Read(b)
}
func verifyModelCache(ctx context.Context, path string) error {
	info, e := os.Lstat(path)
	if e != nil || !info.IsDir() || info.Mode().Perm() != 0555 {
		return fail("MODEL_PREPARATION_INPUT_INVALID")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return fail("MODEL_PREPARATION_INPUT_INVALID")
	}
	for _, asset := range agentwire.ApprovedModel().Files {
		info, e := os.Lstat(filepath.Join(path, asset.Path))
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0444 {
			return fail("MODEL_PREPARATION_INPUT_INVALID")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
			return fail("MODEL_PREPARATION_INPUT_INVALID")
		}
	}
	if modelartifact.VerifyDirectory(ctx, path) != nil {
		return fail("MODEL_PREPARATION_INPUT_INVALID")
	}
	return nil
}
func prepareModel(ctx context.Context, config Config, j Journal, download FileDownload) error {
	parent := filepath.Join(config.RootDirectory, "models")
	if e := rootDirectory(parent); e != nil {
		return e
	}
	destination := filepath.Join(parent, j.Command.ModelCatalogSHA256)
	if _, e := os.Lstat(destination); e == nil {
		return verifyModelCache(ctx, destination)
	} else if !os.IsNotExist(e) {
		return fail("MODEL_PREPARATION_AGENT_IO_FAILED")
	}
	if download == nil {
		return fail("MODEL_PREPARATION_INPUT_INVALID")
	}
	stage := filepath.Join(parent, ".staging-"+j.JournalID)
	if os.Mkdir(stage, 0700) != nil {
		return fail("MODEL_PREPARATION_AGENT_PARTIAL_INPUT_RETAINED")
	}
	for _, asset := range agentwire.ApprovedModel().Files {
		if e := ctx.Err(); e != nil {
			return e
		}
		path := filepath.Join(stage, asset.Path)
		if e := download(ctx, "model", datawire.Asset{Path: asset.Path, SizeBytes: asset.SizeBytes, SHA256: asset.SHA256}, path); e != nil {
			return e
		}
		if os.Chmod(path, 0444) != nil {
			return fail("MODEL_PREPARATION_AGENT_IO_FAILED")
		}
	}
	if modelartifact.VerifyDirectory(ctx, stage) != nil {
		return fail("MODEL_PREPARATION_INPUT_INVALID")
	}
	if os.Chmod(stage, 0555) != nil || syncDirectory(stage) != nil {
		return fail("MODEL_PREPARATION_AGENT_IO_FAILED")
	}
	if _, e := os.Lstat(destination); !os.IsNotExist(e) {
		return fail("MODEL_PREPARATION_AGENT_PATH_INVALID")
	}
	if os.Rename(stage, destination) != nil {
		return fail("MODEL_PREPARATION_AGENT_IO_FAILED")
	}
	return syncDirectory(parent)
}
func attemptPath(config Config, j Journal) string {
	return filepath.Join(config.RootDirectory, "preparations", j.Command.Scope.TenantID, j.Command.Scope.WorkspaceID, j.Command.Scope.PreparationID, j.Command.AttemptID)
}
func openWorkerFile(path string, maximum int64, uid int) (*os.File, error) {
	actual, e := filepath.EvalSymlinks(path)
	if e != nil {
		return nil, e
	}
	if actual != path {
		return nil, fail("MODEL_PREPARATION_AGENT_PATH_INVALID")
	}
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || owner.Uid != uint32(uid) || owner.Nlink != 1 || info.Mode().Perm()&0077 != 0 || info.Size() > maximum {
		return nil, fail("MODEL_PREPARATION_OUTPUT_INVALID")
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, fail("MODEL_PREPARATION_AGENT_IO_FAILED")
	}
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) {
		f.Close()
		return nil, fail("MODEL_PREPARATION_OUTPUT_INVALID")
	}
	return f, nil
}
func verifiedAsset(ctx context.Context, path string, asset datawire.Asset, uid int) (*os.File, error) {
	f, e := openWorkerFile(path, asset.SizeBytes, uid)
	if e != nil {
		return nil, e
	}
	hash := sha256.New()
	n, e := io.Copy(hash, contextReader{ctx, io.LimitReader(f, asset.SizeBytes+1)})
	if e != nil || n != asset.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		f.Close()
		return nil, fail("MODEL_PREPARATION_OUTPUT_CHANGED")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		f.Close()
		return nil, fail("MODEL_PREPARATION_AGENT_IO_FAILED")
	}
	return f, nil
}
func workerBytes(path string, maximum int64, uid int) ([]byte, error) {
	f, e := openWorkerFile(path, maximum, uid)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, maximum+1))
	if e != nil || int64(len(b)) > maximum {
		return nil, fail("MODEL_PREPARATION_OUTPUT_INVALID")
	}
	return b, nil
}
func outputBudget(ctx context.Context, root string, resources wire.Resources, uid int) error {
	var bytes, files, entries int64
	var walk func(string, int) error
	walk = func(path string, depth int) error {
		if depth > 4 {
			return fail("MODEL_PREPARATION_OUTPUT_LIMIT")
		}
		before, e := os.Lstat(path)
		if e != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			return fail("MODEL_PREPARATION_OUTPUT_INVALID")
		}
		owner, ok := before.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(uid) || before.Mode().Perm()&0077 != 0 {
			return fail("MODEL_PREPARATION_OUTPUT_INVALID")
		}
		f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			return fail("MODEL_PREPARATION_OUTPUT_INVALID")
		}
		defer f.Close()
		opened, e := f.Stat()
		if e != nil || !os.SameFile(before, opened) {
			return fail("MODEL_PREPARATION_OUTPUT_INVALID")
		}
		for {
			if e = ctx.Err(); e != nil {
				return e
			}
			names, e := f.Readdirnames(64)
			if e != nil && e != io.EOF {
				return fail("MODEL_PREPARATION_OUTPUT_INVALID")
			}
			for _, name := range names {
				entries++
				if entries > resources.MaximumOutputFiles+16 {
					return fail("MODEL_PREPARATION_OUTPUT_LIMIT")
				}
				child := filepath.Join(path, name)
				info, e := os.Lstat(child)
				if e != nil {
					return fail("MODEL_PREPARATION_OUTPUT_INVALID")
				}
				owner, ok := info.Sys().(*syscall.Stat_t)
				if !ok || owner.Uid != uint32(uid) || info.Mode().Perm()&0077 != 0 {
					return fail("MODEL_PREPARATION_OUTPUT_INVALID")
				}
				if info.IsDir() {
					if e = walk(child, depth+1); e != nil {
						return e
					}
				} else {
					if !info.Mode().IsRegular() || owner.Nlink != 1 {
						return fail("MODEL_PREPARATION_OUTPUT_INVALID")
					}
					files++
					bytes += info.Size()
					if files > resources.MaximumOutputFiles || bytes > resources.MaximumOutputBytes {
						return fail("MODEL_PREPARATION_OUTPUT_LIMIT")
					}
				}
			}
			if e == io.EOF {
				break
			}
		}
		return nil
	}
	if !cleanPath(root) || strings.Contains(root, "\x00") {
		return fail("MODEL_PREPARATION_AGENT_PATH_INVALID")
	}
	return walk(root, 0)
}
