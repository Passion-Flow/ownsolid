package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func preparationOperatorPath(path string, directory bool) error {
	actual, e := filepath.EvalSymlinks(path)
	if e != nil || actual != path || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("RECOVERY_RECEIPT_PATH_INVALID")
	}
	info, e := os.Lstat(path)
	if e != nil {
		return errors.New("RECOVERY_RECEIPT_PATH_INVALID")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 || directory && !info.IsDir() || !directory && (!info.Mode().IsRegular() || owner.Nlink != 1 || info.Size() < 1 || info.Size() > wire.MaximumRemovalBytes) {
		return errors.New("RECOVERY_RECEIPT_PATH_INVALID")
	}
	return nil
}
func PreparePreparationRemovalFile(ctx context.Context, accounts *identity.Service, node, job, reason, path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || preparationOperatorPath(filepath.Dir(path), true) != nil {
		return errors.New("RECOVERY_RECEIPT_PATH_INVALID")
	}
	file, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return errors.New("RECOVERY_RECEIPT_PATH_INVALID")
	}
	defer file.Close()
	challenge, e := PreparePreparationRemoval(ctx, accounts, node, job, reason)
	if e != nil {
		return e
	}
	if json.NewEncoder(file).Encode(challenge) != nil || file.Sync() != nil {
		return errors.New("RECOVERY_RECEIPT_WRITE_FAILED")
	}
	parent, e := os.Open(filepath.Dir(path))
	if e != nil {
		return errors.New("RECOVERY_RECEIPT_WRITE_FAILED")
	}
	defer parent.Close()
	if parent.Sync() != nil {
		return errors.New("RECOVERY_RECEIPT_WRITE_FAILED")
	}
	return nil
}
func LoadPreparationRemovalReceipt(path string) (wire.RemovalReceipt, error) {
	var out wire.RemovalReceipt
	if preparationOperatorPath(path, false) != nil {
		return out, errors.New("RECOVERY_RECEIPT_INVALID")
	}
	before, e := os.Lstat(path)
	if e != nil {
		return out, errors.New("RECOVERY_RECEIPT_INVALID")
	}
	file, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return out, errors.New("RECOVERY_RECEIPT_INVALID")
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return out, errors.New("RECOVERY_RECEIPT_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(file, wire.MaximumRemovalBytes+1))
	if e != nil {
		return out, errors.New("RECOVERY_RECEIPT_INVALID")
	}
	after, e := file.Stat()
	if e != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return out, errors.New("RECOVERY_RECEIPT_INVALID")
	}
	return wire.DecodeRemovalReceipt(raw, time.Now())
}
