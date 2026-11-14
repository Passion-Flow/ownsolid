package healthprobe

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
)

const maximumTrustBytes int64 = 1 << 20

type trustSnapshot struct {
	path   string
	name   string
	root   *os.Root
	file   *os.File
	parent os.FileInfo
	before os.FileInfo
}

func trustOwned(info os.FileInfo, directory bool) bool {
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

// Darwin and Linux expose the same change-time fact with different field names.
// Compare that stable fact rather than access time, which our read may update.
func sameChangeTime(left, right os.FileInfo) bool {
	a, aok := left.Sys().(*syscall.Stat_t)
	b, bok := right.Sys().(*syscall.Stat_t)
	if !aok || !bok {
		return false
	}
	for _, name := range []string{"Ctim", "Ctimespec"} {
		x, y := reflect.ValueOf(a).Elem().FieldByName(name), reflect.ValueOf(b).Elem().FieldByName(name)
		if x.IsValid() && y.IsValid() && x.CanInterface() && y.CanInterface() {
			return reflect.DeepEqual(x.Interface(), y.Interface())
		}
	}
	return false
}

func unchangedTrustFile(before, after os.FileInfo) bool {
	return trustOwned(before, false) && trustOwned(after, false) && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) && sameChangeTime(before, after)
}

// The literal directory suffix must reach the kernel unchanged. A parent
// replaced with a FIFO cannot then block Go's ordinary OpenRoot open call.
func openTrustDirectory(parent string) (*os.Root, error) {
	return os.OpenRoot(parent + string(filepath.Separator) + ".")
}

// A held os.Root and O_NOFOLLOW descriptor pin both the private parent and
// filename. No system-root fallback, shared trust change or path is returned.
func openTrustSnapshot(path string) (*trustSnapshot, error) {
	bad := errors.New(trustInvalid)
	if !privateReference(path) {
		return nil, bad
	}
	parentPath := filepath.Dir(path)
	canonical, e := filepath.EvalSymlinks(parentPath)
	if e != nil || canonical != parentPath {
		return nil, bad
	}
	parent, e := os.Lstat(parentPath)
	if e != nil || !trustOwned(parent, true) {
		return nil, bad
	}
	root, e := openTrustDirectory(parentPath)
	if e != nil {
		return nil, bad
	}
	snapshot := &trustSnapshot{path: path, name: filepath.Base(path), root: root, parent: parent}
	current, e := root.Stat(".")
	if e != nil || !trustOwned(current, true) || !os.SameFile(parent, current) {
		snapshot.close()
		return nil, bad
	}
	before, e := root.Lstat(snapshot.name)
	if e != nil || !trustOwned(before, false) || before.Size() < 1 || before.Size() > maximumTrustBytes {
		snapshot.close()
		return nil, bad
	}
	snapshot.before = before
	file, e := root.OpenFile(snapshot.name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		snapshot.close()
		return nil, bad
	}
	snapshot.file = file
	opened, e := file.Stat()
	if e != nil || !unchangedTrustFile(before, opened) {
		snapshot.close()
		return nil, bad
	}
	return snapshot, nil
}

func (snapshot *trustSnapshot) close() {
	if snapshot.file != nil {
		snapshot.file.Close()
	}
	if snapshot.root != nil {
		snapshot.root.Close()
	}
}

func (snapshot *trustSnapshot) read() ([]byte, error) {
	bad := errors.New(trustInvalid)
	raw, e := io.ReadAll(io.LimitReader(snapshot.file, maximumTrustBytes+1))
	after, ae := snapshot.file.Stat()
	current, ce := snapshot.root.Lstat(snapshot.name)
	parent, pe := os.Lstat(filepath.Dir(snapshot.path))
	canonical, re := filepath.EvalSymlinks(filepath.Dir(snapshot.path))
	if e != nil || ae != nil || ce != nil || pe != nil || re != nil || canonical != filepath.Dir(snapshot.path) || !trustOwned(parent, true) || !os.SameFile(snapshot.parent, parent) || int64(len(raw)) != snapshot.before.Size() || !unchangedTrustFile(snapshot.before, after) || !unchangedTrustFile(snapshot.before, current) {
		return nil, bad
	}
	return raw, nil
}

func readTrustPool(path string) (*x509.CertPool, error) {
	snapshot, e := openTrustSnapshot(path)
	if e != nil {
		return nil, errors.New(trustInvalid)
	}
	defer snapshot.close()
	raw, e := snapshot.read()
	if e != nil {
		return nil, errors.New(trustInvalid)
	}
	pool := x509.NewCertPool()
	count := 0
	for remaining := bytes.TrimSpace(raw); len(remaining) > 0; {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New(trustInvalid)
		}
		end := bytes.Index(remaining, []byte("-----END CERTIFICATE-----"))
		if end < 0 {
			return nil, errors.New(trustInvalid)
		}
		end += len("-----END CERTIFICATE-----")
		// Decode only the first framed block. pem.Decode otherwise skips a
		// malformed earlier block when a later valid certificate is present.
		block, extra := pem.Decode(remaining[:end])
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(extra)) != 0 {
			return nil, errors.New(trustInvalid)
		}
		certificate, e := x509.ParseCertificate(block.Bytes)
		if e != nil {
			return nil, errors.New(trustInvalid)
		}
		pool.AddCert(certificate)
		count++
		remaining = bytes.TrimSpace(remaining[end:])
	}
	if count == 0 {
		return nil, errors.New(trustInvalid)
	}
	return pool, nil
}
