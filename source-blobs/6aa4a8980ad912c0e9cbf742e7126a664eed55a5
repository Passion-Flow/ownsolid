// Package bootstrap implements protected local installation bootstrap intents.
package bootstrap

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// MaximumDocumentBytes is the version1 wire-document bound, not a deployment policy.
const MaximumDocumentBytes = 16 * 1024

func privateParent(path string) (*os.Root, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, "", errors.New("BOOTSTRAP_FILE_PATH_INVALID")
	}
	parent := filepath.Dir(path)
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || canonical != parent {
		return nil, "", errors.New("BOOTSTRAP_FILE_PARENT_INVALID")
	}
	r, e := os.OpenRoot(parent)
	if e != nil {
		return nil, "", errors.New("BOOTSTRAP_FILE_PARENT_INVALID")
	}
	i, e := r.Stat(".")
	if e != nil || !i.IsDir() || i.Mode() != os.ModeDir|0700 || !owned(i, false) {
		r.Close()
		return nil, "", errors.New("BOOTSTRAP_FILE_PARENT_INVALID")
	}
	return r, filepath.Base(path), nil
}

func owned(i os.FileInfo, single bool) bool {
	s, ok := i.Sys().(*syscall.Stat_t)
	return ok && uint64(s.Uid) == uint64(os.Geteuid()) && (!single || s.Nlink == 1)
}

func readPrivate(path string, out any) ([]byte, error) {
	b, e := readPrivateBytes(path)
	if e != nil {
		return nil, e
	}
	if e = strict.DecodeStrict(b, out); e != nil {
		return nil, errors.New("BOOTSTRAP_DOCUMENT_INVALID")
	}
	return b, nil
}
func readPrivateBytes(path string) ([]byte, error) {
	return readProtectedBytes(path, false)
}

func readProtectedBytes(path string, durable bool) ([]byte, error) {
	r, name, e := privateParent(path)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	f, e := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, errors.New("BOOTSTRAP_FILE_INVALID")
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() || i.Mode() != 0600 || !owned(i, true) || i.Size() < 1 || i.Size() > MaximumDocumentBytes {
		return nil, errors.New("BOOTSTRAP_FILE_INVALID")
	}
	b, e := io.ReadAll(io.LimitReader(f, MaximumDocumentBytes+1))
	j, se := f.Stat()
	current, le := r.Lstat(name)
	if e != nil || se != nil || le != nil || !os.SameFile(i, j) || !os.SameFile(i, current) || current.Mode()&os.ModeSymlink != 0 || i.Size() != int64(len(b)) || j.Size() != i.Size() || !j.ModTime().Equal(i.ModTime()) || !owned(j, true) {
		return nil, errors.New("BOOTSTRAP_FILE_CHANGED")
	}
	if durable {
		d, e := r.Open(".")
		if e != nil {
			return nil, errors.New("BOOTSTRAP_INTENT_SYNC_UNCERTAIN")
		}
		defer d.Close()
		if f.Sync() != nil || d.Sync() != nil {
			return nil, errors.New("BOOTSTRAP_INTENT_SYNC_UNCERTAIN")
		}
	}
	return b, nil
}

func syncRetainedIntent(path string, expected []byte) error {
	actual, e := readProtectedBytes(path, true)
	if e != nil {
		return e
	}
	if !bytes.Equal(actual, expected) {
		return errors.New("BOOTSTRAP_INTENT_CHANGED")
	}
	return nil
}

func writeIntent(path string, b []byte) error {
	var check Intent
	return writeDocument(path, b, &check)
}

func writeDocument(path string, b []byte, check any) error {
	r, name, e := privateParent(path)
	if e != nil {
		return e
	}
	defer r.Close()
	d, e := r.Open(".")
	if e != nil {
		return errors.New("BOOTSTRAP_INTENT_SYNC_UNCERTAIN")
	}
	defer d.Close()
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return errors.New("BOOTSTRAP_RANDOM_UNAVAILABLE")
	}
	stage := ".bootstrap-" + hex.EncodeToString(nonce[:])
	f, e := r.OpenFile(stage, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return errors.New("BOOTSTRAP_INTENT_CREATE_FAILED")
	}
	defer r.Remove(stage)
	_, e = io.Copy(f, bytes.NewReader(b))
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return errors.New("BOOTSTRAP_INTENT_WRITE_UNCERTAIN")
	}
	// Publish only the complete, synced document. The final name is never
	// overwritten, even when two offline preparations choose the same output.
	if e = renameExclusive(int(d.Fd()), stage, name); e != nil {
		return errors.New("BOOTSTRAP_INTENT_CREATE_FAILED")
	}
	if e = d.Sync(); e != nil {
		return errors.New("BOOTSTRAP_INTENT_SYNC_UNCERTAIN")
	}
	actual, e := readPrivate(path, check)
	if e != nil || !bytes.Equal(actual, b) {
		return errors.New("BOOTSTRAP_INTENT_READBACK_FAILED")
	}
	return nil
}
