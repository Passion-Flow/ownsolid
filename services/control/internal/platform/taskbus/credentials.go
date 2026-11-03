package taskbus

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/nats-io/nkeys"
)

const maximumPEMBytes int64 = 1 << 20
const maximumSeedBytes int64 = 256
const maximumServerNonceBytes = 1024

func wipe(raw []byte) {
	for n := range raw {
		raw[n] = 0
	}
}
func privateFileInfo(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	details, ok := info.Sys().(*syscall.Stat_t)
	if !ok || details.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && details.Nlink == 1
}
func samePrivateFile(before, after os.FileInfo) bool {
	return privateFileInfo(before, false) && privateFileInfo(after, false) && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime() == after.ModTime() && before.Mode() == after.Mode()
}

// readPrivateFile never expands HOME/environment or follows a substituted link.
// The bound is a protocol file envelope, not a deployment-selected secret value.
func readPrivateFile(path string, maximum int64) ([]byte, error) {
	bad := errors.New("TASK_BUS_SECRET_INVALID")
	if !protectedPath(path) || maximum < 1 {
		return nil, bad
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, bad
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !privateFileInfo(parent, true) {
		return nil, bad
	}
	before, err := os.Lstat(path)
	if err != nil || !privateFileInfo(before, false) || before.Size() < 1 || before.Size() > maximum {
		return nil, bad
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, bad
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !samePrivateFile(before, opened) {
		return nil, bad
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(raw)) != before.Size() {
		wipe(raw)
		return nil, bad
	}
	after, err := file.Stat()
	linked, linkErr := os.Lstat(path)
	currentParent, parentErr := os.Lstat(filepath.Dir(path))
	resolved, resolveErr := filepath.EvalSymlinks(path)
	if err != nil || linkErr != nil || parentErr != nil || resolveErr != nil || resolved != path || !samePrivateFile(before, after) || !samePrivateFile(before, linked) || !privateFileInfo(currentParent, true) || !os.SameFile(parent, currentParent) {
		wipe(raw)
		return nil, bad
	}
	return raw, nil
}

func tlsConfiguration(c Configuration) (*tls.Config, error) {
	bad := errors.New("TASK_BUS_SECRET_INVALID")
	ca, err := readPrivateFile(c.CAFile, maximumPEMBytes)
	if err != nil {
		return nil, err
	}
	defer wipe(ca)
	certificate, err := readPrivateFile(c.ClientCertificateFile, maximumPEMBytes)
	if err != nil {
		return nil, err
	}
	defer wipe(certificate)
	key, err := readPrivateFile(c.ClientKeyFile, maximumPEMBytes)
	if err != nil {
		return nil, err
	}
	defer wipe(key)
	roots := x509.NewCertPool()
	remaining := ca
	count := 0
	for len(bytes.TrimSpace(remaining)) > 0 {
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, bad
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !parsed.IsCA {
			return nil, bad
		}
		roots.AddCert(parsed)
		count++
		remaining = rest
	}
	if count == 0 {
		return nil, bad
	}
	pair, err := tls.X509KeyPair(certificate, key)
	if err != nil || len(pair.Certificate) == 0 {
		return nil, bad
	}
	pair.Leaf, err = x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, bad
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{pair}}, nil
}

func readUserKey(path string) (nkeys.KeyPair, error) {
	raw, err := readPrivateFile(path, maximumSeedBytes)
	if err != nil {
		return nil, err
	}
	defer wipe(raw)
	seed := bytes.TrimSuffix(raw, []byte{'\n'})
	seed = bytes.TrimSuffix(seed, []byte{'\r'})
	key, err := nkeys.FromSeed(seed)
	if err != nil {
		return nil, errors.New("TASK_BUS_SECRET_INVALID")
	}
	public, err := key.PublicKey()
	if err != nil || !nkeys.IsValidPublicUserKey(public) {
		key.Wipe()
		return nil, errors.New("TASK_BUS_SECRET_INVALID")
	}
	return key, nil
}
func userPublicKey(path string) (string, error) {
	key, err := readUserKey(path)
	if err != nil {
		return "", err
	}
	defer key.Wipe()
	public, err := key.PublicKey()
	if err != nil {
		return "", errors.New("TASK_BUS_SECRET_INVALID")
	}
	return public, nil
}

// signUserNonce repeats the protected file check for every challenge. A replaced
// seed cannot silently change the already admitted publisher/consumer identity.
func signUserNonce(path, expectedPublic string) func([]byte) ([]byte, error) {
	return func(nonce []byte) ([]byte, error) {
		bad := errors.New("TASK_BUS_AUTHENTICATION_FAILED")
		if len(nonce) < 1 || len(nonce) > maximumServerNonceBytes || !nkeys.IsValidPublicUserKey(expectedPublic) {
			return nil, bad
		}
		key, err := readUserKey(path)
		if err != nil {
			return nil, bad
		}
		defer key.Wipe()
		public, err := key.PublicKey()
		if err != nil || public != expectedPublic {
			return nil, bad
		}
		signature, err := key.Sign(nonce)
		if err != nil {
			wipe(signature)
			return nil, bad
		}
		return signature, nil
	}
}
