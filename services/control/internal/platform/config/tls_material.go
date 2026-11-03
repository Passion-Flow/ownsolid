package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// TLS material is a bounded immutable startup input, not a deployment tuning
// policy. This wire bound applies separately to the certificate and private key.
const maximumTLSMaterialBytes int64 = 1 << 20

type tlsMaterialSnapshot struct {
	root      *os.Root
	parent    string
	directory os.FileInfo
	name      string
	before    os.FileInfo
	file      *os.File
	private   bool
}

func tlsMaterialError(private bool) error {
	if private {
		return errors.New("SECRET_FILE_INVALID")
	}
	return errors.New("TLS_KEYPAIR_INVALID")
}

func tlsMaterialAdmitted(info os.FileInfo, private bool) bool {
	if info == nil || !info.Mode().IsRegular() || info.Mode()&^os.ModePerm != 0 || info.Mode().Perm()&0111 != 0 || info.Mode().Perm()&0022 != 0 || private && info.Mode().Perm()&0077 != 0 || info.Size() < 1 || info.Size() > maximumTLSMaterialBytes {
		return false
	}
	facts, ok := info.Sys().(*syscall.Stat_t)
	// Certificate ownership need not equal the runtime UID. Private-key access
	// is also subject to actual OS permissions; compare its observed identity.
	return ok && facts != nil && facts.Nlink == 1
}

func sameTLSMaterialSnapshot(before, after os.FileInfo, private bool) bool {
	if !tlsMaterialAdmitted(before, private) || !tlsMaterialAdmitted(after, private) || !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || !samePrivateChangeTime(before, after) {
		return false
	}
	a, b := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	return a.Uid == b.Uid && a.Gid == b.Gid
}

func sameTLSParentSnapshot(before, after os.FileInfo) bool {
	if before == nil || after == nil || !before.IsDir() || !after.IsDir() {
		return false
	}
	a, aok := before.Sys().(*syscall.Stat_t)
	b, bok := after.Sys().(*syscall.Stat_t)
	if !aok || !bok || a == nil || b == nil {
		return false
	}
	return os.SameFile(before, after) && before.Mode() == after.Mode() &&
		a.Uid == b.Uid && a.Gid == b.Gid && a.Nlink == b.Nlink &&
		before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) &&
		samePrivateChangeTime(before, after)
}

func (snapshot *tlsMaterialSnapshot) parentUnchanged() bool {
	if snapshot == nil || snapshot.root == nil || snapshot.directory == nil {
		return false
	}
	canonical, e := filepath.EvalSymlinks(snapshot.parent)
	if e != nil || canonical != snapshot.parent {
		return false
	}
	pinned, e := snapshot.root.Stat(".")
	current, pe := os.Lstat(snapshot.parent)
	return e == nil && pe == nil && sameTLSParentSnapshot(snapshot.directory, pinned) && sameTLSParentSnapshot(snapshot.directory, current)
}

// Pin the prechecked root and file without opening the selected file yet. The
// separate open boundary is used by both production and deterministic mutation
// tests; no mutable production hook controls an input's admission.
func pinTLSMaterial(path string, private bool) (*tlsMaterialSnapshot, error) {
	bad := tlsMaterialError(private)
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, bad
	}
	parent := filepath.Dir(path)
	canonical, e := filepath.EvalSymlinks(parent)
	if e != nil || canonical != parent {
		return nil, bad
	}
	directory, e := os.Lstat(parent)
	if e != nil || !directory.IsDir() {
		return nil, bad
	}
	// Retain the literal terminal dot: even a parent replaced by a FIFO must
	// require directory traversal rather than a blocking regular pathname open.
	root, e := os.OpenRoot(parent + string(filepath.Separator) + ".")
	if e != nil {
		return nil, bad
	}
	snapshot := &tlsMaterialSnapshot{root: root, parent: parent, directory: directory, name: filepath.Base(path), private: private}
	if !snapshot.parentUnchanged() {
		snapshot.close()
		return nil, bad
	}
	before, e := root.Lstat(snapshot.name)
	if e != nil || !tlsMaterialAdmitted(before, private) || !sameTLSMaterialSnapshot(before, before, private) {
		snapshot.close()
		return nil, bad
	}
	snapshot.before = before
	return snapshot, nil
}

func (snapshot *tlsMaterialSnapshot) open() error {
	if snapshot == nil {
		return errors.New("TLS_KEYPAIR_INVALID")
	}
	bad := tlsMaterialError(snapshot.private)
	if snapshot.file != nil || !sameTLSMaterialSnapshot(snapshot.before, snapshot.before, snapshot.private) || !snapshot.parentUnchanged() {
		return bad
	}
	return openPinnedTLSMaterial(snapshot)
}

// This native-open boundary deliberately has no new parent/path stat before the
// open: production has already prechecked them. A deterministic test can replace
// the checked file here and exercise the actual nonblocking descriptor open. The
// complete strict parent/path/descriptor validation still precedes every reader.
func openPinnedTLSMaterial(snapshot *tlsMaterialSnapshot) error {
	if snapshot == nil {
		return errors.New("TLS_KEYPAIR_INVALID")
	}
	bad := tlsMaterialError(snapshot.private)
	if snapshot.root == nil || snapshot.file != nil || !sameTLSMaterialSnapshot(snapshot.before, snapshot.before, snapshot.private) {
		return bad
	}
	file, e := snapshot.root.OpenFile(snapshot.name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return bad
	}
	snapshot.file = file
	// Go Root can resolve an in-root relative symlink despite O_NOFOLLOW. Both
	// the named Lstat and opened descriptor must pass before any bytes are read.
	if e = snapshot.validate(); e != nil {
		file.Close()
		snapshot.file = nil
		return e
	}
	return nil
}

func (snapshot *tlsMaterialSnapshot) validate() error {
	if snapshot == nil {
		return errors.New("TLS_KEYPAIR_INVALID")
	}
	bad := tlsMaterialError(snapshot.private)
	if snapshot.file == nil || snapshot.root == nil || !snapshot.parentUnchanged() {
		return bad
	}
	opened, e := snapshot.file.Stat()
	current, pe := snapshot.root.Lstat(snapshot.name)
	if e != nil || pe != nil || !sameTLSMaterialSnapshot(snapshot.before, opened, snapshot.private) || !sameTLSMaterialSnapshot(snapshot.before, current, snapshot.private) || !snapshot.parentUnchanged() {
		return bad
	}
	return nil
}

func (snapshot *tlsMaterialSnapshot) read() ([]byte, error) {
	if e := snapshot.validate(); e != nil {
		return nil, e
	}
	raw, e := io.ReadAll(io.LimitReader(snapshot.file, maximumTLSMaterialBytes+1))
	if e != nil || int64(len(raw)) != snapshot.before.Size() {
		return nil, tlsMaterialError(snapshot.private)
	}
	if e = snapshot.validate(); e != nil {
		return nil, e
	}
	return raw, nil
}

func (snapshot *tlsMaterialSnapshot) close() error {
	if snapshot == nil {
		return nil
	}
	var err error
	if snapshot.file != nil {
		if e := snapshot.file.Close(); e != nil {
			err = tlsMaterialError(snapshot.private)
		}
		snapshot.file = nil
	}
	if snapshot.root != nil {
		if e := snapshot.root.Close(); e != nil {
			err = tlsMaterialError(snapshot.private)
		}
		snapshot.root = nil
	}
	return err
}

func readTLSMaterialPair(certificatePath, privateKeyPath string) (certificatePEM, privateKeyPEM []byte, err error) {
	// Preserve the historical key admission error priority. Neither descriptor
	// is read until both roots and file observations have been pinned.
	key, e := pinTLSMaterial(privateKeyPath, true)
	if e != nil {
		return nil, nil, e
	}
	defer func() {
		if e := key.close(); e != nil && err == nil {
			certificatePEM, privateKeyPEM, err = nil, nil, e
		}
	}()
	certificate, e := pinTLSMaterial(certificatePath, false)
	if e != nil {
		return nil, nil, e
	}
	defer func() {
		if e := certificate.close(); e != nil && err == nil {
			certificatePEM, privateKeyPEM, err = nil, nil, e
		}
	}()
	if os.SameFile(key.before, certificate.before) {
		return nil, nil, errors.New("TLS_KEYPAIR_INVALID")
	}
	if e = key.open(); e != nil {
		return nil, nil, e
	}
	if e = certificate.open(); e != nil {
		return nil, nil, e
	}
	privateKeyPEM, e = key.read()
	if e != nil {
		return nil, nil, e
	}
	certificatePEM, e = certificate.read()
	if e != nil {
		return nil, nil, e
	}
	// Keep both files and parents alive through the last pair checks. This is a
	// verified byte pair, not an atomic multi-file filesystem transaction or a
	// hard cancellation guarantee for stalled native filesystem syscalls.
	if e = key.validate(); e != nil {
		return nil, nil, e
	}
	if e = certificate.validate(); e != nil {
		return nil, nil, e
	}
	return certificatePEM, privateKeyPEM, nil
}
