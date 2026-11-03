package training

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/google/uuid"
)

func verificationLockAccess(t *testing.T, file *os.File, expected int) {
	t.Helper()
	flags, _, e := syscall.Syscall(syscall.SYS_FCNTL, file.Fd(), uintptr(syscall.F_GETFL), 0)
	if e != 0 || int(flags)&syscall.O_ACCMODE != expected {
		t.Fatal("ownership lock descriptor access changed", e)
	}
}

func TestVerificationStoreRetainsExclusiveOwnershipWithReadOnlyLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	tenant := uuid.NewString()
	if e := InitializeStore(root, tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	writer, e := OpenStore(root, tenant, 4096, 67108864, 64)
	if e != nil {
		t.Fatal(e)
	}
	verificationLockAccess(t, writer.lock, syscall.O_RDWR)
	if _, e := OpenStoreForVerification(root, tenant, 4096, 67108864, 64); e == nil {
		t.Fatal("verification bypassed held ordinary writer")
	}
	if e = writer.Close(); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(root, ownerLock))
	if e != nil {
		t.Fatal(e)
	}
	reader, e := OpenStoreForVerification(root, tenant, 4096, 67108864, 64)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	verificationLockAccess(t, reader.lock, syscall.O_RDONLY)
	if _, e = reader.lock.Write([]byte("must not write ownership lock")); e == nil {
		t.Fatal("verification ownership descriptor permits writing")
	}
	if _, e = OpenStore(root, tenant, 4096, 67108864, 64); e == nil {
		t.Fatal("ordinary writer bypassed held verifier")
	}
	if _, e = OpenStoreForVerification(root, tenant, 4096, 67108864, 64); e == nil {
		t.Fatal("second verifier weakened exclusive locking")
	}
	if _, e = OpenStoreForVerification(root, uuid.NewString(), 4096, 67108864, 64); e == nil {
		t.Fatal("foreign namespace adopted")
	}
	if e = reader.Close(); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(root, ownerLock))
	if e != nil || string(before) != string(after) {
		t.Fatal("verification changed ownership bytes", e)
	}
	writer, e = OpenStore(root, tenant, 4096, 67108864, 64)
	if e != nil {
		t.Fatal("writer cannot reopen after verification", e)
	}
	verificationLockAccess(t, writer.lock, syscall.O_RDWR)
	if e = writer.Close(); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(root, ownerLock)); e != nil {
		t.Fatal(e)
	}
	if _, e = OpenStoreForVerification(root, tenant, 4096, 67108864, 64); e == nil {
		t.Fatal("missing lock silently initialized")
	}
	if _, e = os.Lstat(filepath.Join(root, ownerLock)); !os.IsNotExist(e) {
		t.Fatal("verification recreated missing lock")
	}
	missing := filepath.Join(root, "never-created")
	if _, e = OpenStoreForVerification(missing, tenant, 4096, 67108864, 64); e == nil {
		t.Fatal("missing root silently initialized")
	}
	if _, e = os.Lstat(missing); !os.IsNotExist(e) {
		t.Fatal("verification created missing root")
	}
}
