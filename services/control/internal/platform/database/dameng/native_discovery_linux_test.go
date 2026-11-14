//go:build linux

package dameng

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func nativeDiscoveryFixture(t *testing.T) (NativeManifest, func(string, []byte)) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{root, filepath.Join(root, "bin"), filepath.Join(root, "release"), filepath.Join(root, "bin", "dependencies")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	m := relativeManifestFixture(filepath.Join(root, "bin"))
	m.Architecture = runtime.GOARCH
	m.DriverVersion, m.DBMSName, m.DBMSVersion = "", "", ""
	m.Files = nil
	write := func(name string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(m.LibraryDirectory, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(raw)
		entry := NativeFile{name, int64(len(raw)), hex.EncodeToString(digest[:])}
		for i := range m.Files {
			if m.Files[i].Name == name {
				m.Files[i] = entry
				return
			}
		}
		m.Files = append(m.Files, entry)
	}
	machine := elf.EM_X86_64
	if runtime.GOARCH == "arm64" {
		machine = elf.EM_AARCH64
	}
	write("libdodbc.so", nativeELFFixture(t, machine, nativeDynamicFixture{elf.DT_NEEDED, "libdmclient.so"}))
	write("libdmclient.so", nativeELFFixture(t, machine, nativeDynamicFixture{elf.DT_NEEDED, "libc.so.6"}))
	write("LICENSE", []byte("Inert static fixture; no vendor code or license.\n"))
	t.Chdir(m.LibraryDirectory)
	return m, write
}

func TestNativeDiscoveryVerifiesInertFilesWithoutAdmittingConnection(t *testing.T) {
	m, _ := nativeDiscoveryFixture(t)
	if err := VerifyNativeInventoryForValidation(t.Context(), m); err != nil {
		t.Fatal("complete inert static inventory refused", err)
	}
	if m.Validate() == nil || verifyNativeFiles(t.Context(), m) == nil {
		t.Fatal("discovery input admitted through normal manifest path")
	}
	c := connectorConfigurationFixture()
	c.NativeManifestFile = filepath.Join(filepath.Dir(m.LibraryDirectory), "native.json")
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.NativeManifestFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if binding, err := bindConnectionInputs(t.Context(), c); binding != nil || FailureCode(err) != "DAMENG_NATIVE_MANIFEST_INVALID" {
		t.Fatal("normal connection binding accepted discovery identity", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := VerifyNativeInventoryForValidation(ctx, m); !errors.Is(err, context.Canceled) {
		t.Fatal("static discovery ignored cancellation", err)
	}
}

func TestNativeDiscoveryPreservesArtifactAndRelativeLookupRefusals(t *testing.T) {
	for _, name := range []string{"hash", "abi", "permission", "unlisted", "release", "dependencies", "cwd"} {
		t.Run(name, func(t *testing.T) {
			m, write := nativeDiscoveryFixture(t)
			if err := VerifyNativeInventoryForValidation(t.Context(), m); err != nil {
				t.Fatal("fixture refused before mutation", err)
			}
			switch name {
			case "hash":
				license := filepath.Join(m.LibraryDirectory, "LICENSE")
				raw, err := os.ReadFile(license)
				if err != nil {
					t.Fatal(err)
				}
				raw[0] ^= 1 // Same size; only the declared content hash changes.
				if err := os.WriteFile(license, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "abi":
				wrong := elf.EM_AARCH64
				if runtime.GOARCH == "arm64" {
					wrong = elf.EM_X86_64
				}
				// Refresh the declared digest so this isolates ABI refusal.
				write("libdmclient.so", nativeELFFixture(t, wrong, nativeDynamicFixture{elf.DT_NEEDED, "libc.so.6"}))
			case "permission":
				if err := os.Chmod(filepath.Join(m.LibraryDirectory, "libdodbc.so"), 0644); err != nil {
					t.Fatal(err)
				}
			case "unlisted":
				if err := os.WriteFile(filepath.Join(m.LibraryDirectory, "unlisted.so"), []byte("shadow"), 0600); err != nil {
					t.Fatal(err)
				}
			case "release":
				if err := os.WriteFile(filepath.Join(filepath.Dir(m.LibraryDirectory), "release", "shadow.so"), []byte("shadow"), 0600); err != nil {
					t.Fatal(err)
				}
			case "dependencies":
				if err := os.WriteFile(filepath.Join(m.LibraryDirectory, "dependencies", "shadow.so"), []byte("shadow"), 0600); err != nil {
					t.Fatal(err)
				}
			case "cwd":
				t.Chdir(filepath.Dir(m.LibraryDirectory))
			}
			if err := VerifyNativeInventoryForValidation(t.Context(), m); err == nil {
				t.Fatal("discovery bypassed the artifact or lookup boundary")
			}
		})
	}
}
