package recovery

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// LoadInferenceReceipt accepts one bounded private local operator file, never a symlink or remote URL.
func LoadInferenceReceipt(path string) (wire.InferenceRemovalReceipt, error) {
	var out wire.InferenceRemovalReceipt
	bad := errors.New("RECOVERY_RECEIPT_INVALID")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return out, bad
	}
	before, e := os.Lstat(path)
	if e != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() < 1 || before.Size() > 16384 {
		return out, bad
	}
	owner, ok := before.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return out, bad
	}
	file, e := os.Open(path)
	if e != nil {
		return out, bad
	}
	defer file.Close()
	current, e := file.Stat()
	if e != nil || !os.SameFile(before, current) {
		return out, bad
	}
	raw, e := io.ReadAll(io.LimitReader(file, 16385))
	if e != nil || len(raw) > 16384 {
		return out, bad
	}
	after, e := file.Stat()
	if e != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return out, bad
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF || out.Validate() != nil {
		return out, bad
	}
	return out, nil
}
