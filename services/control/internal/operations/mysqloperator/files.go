// Package mysqloperator owns protected, installation-locked MySQL operator input.
package mysqloperator

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const MaximumIntentBytes int64 = 16 << 10
const maximumPrivateBytes int64 = 1 << 20

func SHA256(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func owned(info os.FileInfo, directory bool) bool {
	if info == nil {
		return false
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.Mode() == os.ModeDir|0700
	}
	return info.Mode() == 0600 && s.Nlink == 1
}
func privateParent(path string) (*os.Root, string, error) {
	bad := errors.New("MYSQL_OPERATOR_PRIVATE_PATH_INVALID")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) == string(filepath.Separator) {
		return nil, "", bad
	}
	parent := filepath.Dir(path)
	resolved, e := filepath.EvalSymlinks(parent)
	if e != nil || resolved != parent {
		return nil, "", bad
	}
	before, e := os.Lstat(parent)
	if e != nil || !owned(before, true) {
		return nil, "", bad
	}
	r, e := os.OpenRoot(parent)
	if e != nil {
		return nil, "", bad
	}
	current, e := r.Stat(".")
	if e != nil || !os.SameFile(before, current) || !owned(current, true) {
		r.Close()
		return nil, "", bad
	}
	return r, filepath.Base(path), nil
}

// ReadPrivateBytes pins the same private descriptor/name/parent through bounded
// IO. Its result is credential-bearing and must never be diagnostic output.
func ReadPrivateBytes(path string, maximum int64) ([]byte, error) {
	bad := errors.New("MYSQL_OPERATOR_PRIVATE_FILE_INVALID")
	if maximum < 1 || maximum > maximumPrivateBytes {
		return nil, bad
	}
	r, name, e := privateParent(path)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	parent, e := r.Stat(".")
	if e != nil {
		return nil, bad
	}
	before, e := r.Lstat(name)
	if e != nil || !owned(before, false) || before.Size() < 1 || before.Size() > maximum {
		return nil, bad
	}
	f, e := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, bad
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !owned(opened, false) || !os.SameFile(before, opened) {
		return nil, bad
	}
	raw, e := io.ReadAll(io.LimitReader(f, maximum+1))
	after, ae := f.Stat()
	current, ce := r.Lstat(name)
	directory, de := os.Lstat(filepath.Dir(path))
	if e != nil || ae != nil || ce != nil || de != nil || !owned(after, false) || !owned(current, false) || !owned(directory, true) || !os.SameFile(parent, directory) || !os.SameFile(before, after) || !os.SameFile(before, current) || int64(len(raw)) != before.Size() || after.Size() != before.Size() || current.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || !current.ModTime().Equal(before.ModTime()) {
		return nil, errors.New("MYSQL_OPERATOR_PRIVATE_FILE_CHANGED")
	}
	return raw, nil
}
func ReadDocument(path string, maximum int64, out any) ([]byte, error) {
	raw, e := ReadPrivateBytes(path, maximum)
	if e != nil {
		return nil, e
	}
	if strict.DecodeStrict(raw, out) != nil {
		return nil, errors.New("MYSQL_OPERATOR_DOCUMENT_INVALID")
	}
	return raw, nil
}

// WriteDocument never overwrites or removes uncertain/partial evidence.
func WriteDocument(path string, raw []byte, out any) error {
	if int64(len(raw)) < 1 || int64(len(raw)) > MaximumIntentBytes {
		return errors.New("MYSQL_OPERATOR_DOCUMENT_INVALID")
	}
	r, name, e := privateParent(path)
	if e != nil {
		return e
	}
	defer r.Close()
	f, e := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return errors.New("MYSQL_OPERATOR_INTENT_CREATE_FAILED")
	}
	_, e = io.Copy(f, bytes.NewReader(raw))
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return errors.New("MYSQL_OPERATOR_INTENT_WRITE_UNCERTAIN")
	}
	d, e := r.Open(".")
	if e != nil {
		return errors.New("MYSQL_OPERATOR_INTENT_SYNC_UNCERTAIN")
	}
	e = d.Sync()
	ce = d.Close()
	if e != nil || ce != nil {
		return errors.New("MYSQL_OPERATOR_INTENT_SYNC_UNCERTAIN")
	}
	actual, e := ReadDocument(path, MaximumIntentBytes, out)
	if e != nil || !bytes.Equal(actual, raw) {
		return errors.New("MYSQL_OPERATOR_INTENT_READBACK_FAILED")
	}
	return nil
}
