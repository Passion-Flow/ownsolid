//go:build linux

package dameng

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func installerManifestFixture() NativeManifest {
	m := relativeManifestFixture("/private/native/bin")
	m.Files = append(m.Files, NativeFile{nativeInstallerAlias, 32, strings.Repeat("c", 64)}, NativeFile{nativeInstallerSONAME, 32, strings.Repeat("c", 64)})
	return m
}

func TestNativeInstallerAliasRequiresExactVersionedPinnedPair(t *testing.T) {
	if err := installerManifestFixture().Validate(); err != nil {
		t.Fatal("exact installer pair refused", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*NativeManifest)
	}{
		{"missing_target", func(m *NativeManifest) { m.Files = m.Files[:len(m.Files)-1] }},
		{"different_size", func(m *NativeManifest) { m.Files[len(m.Files)-1].Bytes++ }},
		{"different_hash", func(m *NativeManifest) { m.Files[len(m.Files)-1].SHA256 = strings.Repeat("d", 64) }},
		{"license_alias", func(m *NativeManifest) { m.LicenseFile = nativeInstallerAlias }},
		{"license_target", func(m *NativeManifest) { m.LicenseFile = nativeInstallerSONAME }},
		{"legacy_profile", func(m *NativeManifest) { m.FormatVersion = 1; m.LoaderProfile = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := installerManifestFixture()
			tc.change(&m)
			if m.Validate() == nil {
				t.Fatal("unbound installer alias admitted")
			}
		})
	}
}

func TestNativeInstallerAliasKeepsExactELFSONAMEClosure(t *testing.T) {
	for _, target := range []struct {
		architecture string
		machine      elf.Machine
	}{{"amd64", elf.EM_X86_64}, {"arm64", elf.EM_AARCH64}} {
		raw := nativeELFFixture(t, target.machine, nativeDynamicFixture{elf.DT_SONAME, nativeInstallerSONAME})
		files := map[string]bool{nativeInstallerAlias: true, nativeInstallerSONAME: true}
		if err := inspectNativeLibraryProfile(bytes.NewReader(raw), nativeInstallerAlias, target.architecture, files, RelativeReleaseLoaderProfile); err != nil {
			t.Fatal("exact installer SONAME refused", err)
		}
		if inspectNativeLibrary(bytes.NewReader(raw), nativeInstallerAlias, target.architecture, files) == nil {
			t.Fatal("legacy profile silently widened")
		}
		if inspectNativeLibraryProfile(bytes.NewReader(raw), nativeInstallerAlias, target.architecture, map[string]bool{nativeInstallerAlias: true}, RelativeReleaseLoaderProfile) == nil {
			t.Fatal("missing target admitted")
		}
		if inspectNativeLibraryProfile(bytes.NewReader(raw), "unreviewed.so", target.architecture, map[string]bool{"unreviewed.so": true, nativeInstallerSONAME: true}, RelativeReleaseLoaderProfile) == nil {
			t.Fatal("arbitrary SONAME alias admitted")
		}
		wrong := nativeELFFixture(t, target.machine, nativeDynamicFixture{elf.DT_SONAME, "libodbcinst.so.3"})
		if inspectNativeLibraryProfile(bytes.NewReader(wrong), nativeInstallerAlias, target.architecture, files, RelativeReleaseLoaderProfile) == nil {
			t.Fatal("wrong installer generation admitted")
		}
	}
}

func TestNativeInstallerAliasPreservesActualPrivateFileBoundary(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{root, filepath.Join(root, "bin"), filepath.Join(root, "bin", "dependencies"), filepath.Join(root, "release")} {
		if err := os.MkdirAll(n, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(n, 0700); err != nil {
			t.Fatal(err)
		}
	}
	machine := elf.EM_X86_64
	if runtime.GOARCH == "arm64" {
		machine = elf.EM_AARCH64
	}
	m := relativeManifestFixture(filepath.Join(root, "bin"))
	m.Architecture = runtime.GOARCH
	m.Files = nil
	installer := nativeELFFixture(t, machine, nativeDynamicFixture{elf.DT_SONAME, nativeInstallerSONAME})
	for _, f := range []struct {
		name string
		raw  []byte
	}{{"libdodbc.so", nativeELFFixture(t, machine)}, {"LICENSE", []byte("inert fixture terms")}, {nativeInstallerAlias, installer}, {nativeInstallerSONAME, installer}} {
		p := filepath.Join(m.LibraryDirectory, f.name)
		if err := os.WriteFile(p, f.raw, 0600); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(f.raw)
		m.Files = append(m.Files, NativeFile{f.name, int64(len(f.raw)), hex.EncodeToString(h[:])})
	}
	t.Chdir(m.LibraryDirectory)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := verifyNativeFiles(ctx, m); err != nil {
		t.Fatal("closed regular installer pair refused", err)
	}
	alias := filepath.Join(m.LibraryDirectory, nativeInstallerAlias)
	target := filepath.Join(m.LibraryDirectory, nativeInstallerSONAME)
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nativeInstallerSONAME, alias); err != nil {
		t.Fatal(err)
	}
	if verifyNativeFiles(ctx, m) == nil {
		t.Fatal("symlink installer alias admitted")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, alias); err != nil {
		t.Fatal(err)
	}
	if verifyNativeFiles(ctx, m) == nil {
		t.Fatal("hardlinked installer pair admitted")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alias, installer, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(alias, 0644); err != nil {
		t.Fatal(err)
	}
	if verifyNativeFiles(ctx, m) == nil {
		t.Fatal("nonprivate installer copy admitted")
	}
}
