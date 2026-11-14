//go:build linux

package dameng

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNativeInventoryChecksSupportLibrariesBeforeLoading(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	machine, wrong := elf.EM_X86_64, elf.EM_AARCH64
	if runtime.GOARCH == "arm64" {
		machine, wrong = wrong, machine
	}
	manifest := NativeManifest{FormatVersion: 1, Architecture: runtime.GOARCH, LibraryDirectory: root, DriverFile: "libdodbc.so", LicenseFile: "LICENSE", DriverVersion: "fixture", DBMSName: "fixture", DBMSVersion: "fixture"}
	write := func(name string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		entry := NativeFile{Name: name, Bytes: int64(len(raw)), SHA256: hex.EncodeToString(digest[:])}
		for i, file := range manifest.Files {
			if file.Name == name {
				manifest.Files[i] = entry
				return
			}
		}
		manifest.Files = append(manifest.Files, entry)
	}
	write("libdodbc.so", nativeELFFixture(t, machine, nativeDynamicFixture{elf.DT_NEEDED, "libdmclient.so"}))
	write("libdmclient.so", nativeELFFixture(t, machine, nativeDynamicFixture{elf.DT_NEEDED, "libc.so.6"}))
	write("LICENSE", []byte("Synthetic metadata fixture only; no vendor license or binary.\n"))
	if err := verifyNativeFiles(context.Background(), manifest); err != nil {
		t.Fatal("complete private metadata inventory refused", err)
	}
	// Keep the declared digest current: the failure must be the unsupported
	// support-library ABI, not an incidental checksum mismatch.
	write("libdmclient.so", nativeELFFixture(t, wrong, nativeDynamicFixture{elf.DT_NEEDED, "libc.so.6"}))
	if err := verifyNativeFiles(context.Background(), manifest); FailureCode(err) != "DAMENG_NATIVE_ARTIFACT_INVALID" {
		t.Fatal("foreign-architecture support library admitted", err)
	}
	write("libdmclient.so", []byte("a pinned hash alone is not a shared library"))
	if err := verifyNativeFiles(context.Background(), manifest); FailureCode(err) != "DAMENG_NATIVE_ARTIFACT_INVALID" {
		t.Fatal("non-ELF support file admitted", err)
	}
}
