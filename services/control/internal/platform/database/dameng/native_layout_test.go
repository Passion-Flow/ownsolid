package dameng

import (
	"bytes"
	"debug/elf"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func relativeManifestFixture(directory string) NativeManifest {
	return NativeManifest{FormatVersion: 2, Architecture: "amd64", LibraryDirectory: directory,
		DriverFile: "libdodbc.so", LicenseFile: "LICENSE", DriverVersion: "fixture", DBMSName: "fixture", DBMSVersion: "fixture",
		LoaderProfile: RelativeReleaseLoaderProfile, Files: []NativeFile{{"libdodbc.so", 1, strings.Repeat("a", 64)}, {"LICENSE", 1, strings.Repeat("b", 64)}}}
}

func TestRelativeNativeLayoutRequiresActualPrivateDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{root, filepath.Join(root, "bin"), filepath.Join(root, "bin", "dependencies"), filepath.Join(root, "release")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	m := relativeManifestFixture(filepath.Join(root, "bin"))
	info, err := os.Lstat(m.LibraryDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyNativeLayout(m, info); FailureCode(err) != "DAMENG_NATIVE_WORKING_DIRECTORY_INVALID" {
		t.Fatal("inherited arbitrary working directory admitted", err)
	}
	t.Chdir(m.LibraryDirectory)
	if err := verifyNativeLayout(m, info); err != nil {
		t.Fatal("closed private lookup topology refused", err)
	}
	release := filepath.Join(root, "release")
	for _, directory := range []string{release, filepath.Join(m.LibraryDirectory, "dependencies")} {
		entry := filepath.Join(directory, "libc.so.6")
		if err := os.WriteFile(entry, []byte("not a runtime library"), 0600); err != nil {
			t.Fatal(err)
		}
		if verifyNativeLayout(m, info) == nil {
			t.Fatal("relative lookup can shadow the base runtime")
		}
		if err := os.Remove(entry); err != nil {
			t.Fatal(err)
		}
	}
	for _, directory := range []string{release, filepath.Join(m.LibraryDirectory, "dependencies"), root} {
		if err := os.Chmod(directory, 0755); err != nil {
			t.Fatal(err)
		}
		if verifyNativeLayout(m, info) == nil {
			t.Fatal("nonprivate lookup ancestor admitted")
		}
		if err := os.Chmod(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	retained := filepath.Join(root, "retained-release")
	if err := os.Rename(release, retained); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(retained, release); err != nil {
		t.Fatal(err)
	}
	if verifyNativeLayout(m, info) == nil {
		t.Fatal("symlink lookup alias admitted")
	}
}

func TestRelativeManifestAndLaunchSettingsStayVersioned(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	c := connectorConfigurationFixture()
	c.NativeManifestFile = filepath.Join(root, "native.json")
	m := relativeManifestFixture(filepath.Join(root, "bin"))
	write := func(raw []byte) {
		t.Helper()
		if err := os.WriteFile(c.NativeManifestFile, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(m)
	write(raw)
	settings, err := LaunchSettings(c)
	if err != nil || settings.FormatVersion != 1 || settings.WorkingDirectory != m.LibraryDirectory || len(settings.Environment) != 5 {
		t.Fatal("typed child working directory unavailable", err)
	}
	if _, err := LaunchEnvironment(c); FailureCode(err) != "DAMENG_NATIVE_LAUNCH_SETTINGS_REQUIRED" {
		t.Fatal("legacy environment-only launcher silently admitted relative lookup", err)
	}
	for name, mutate := range map[string]func(*NativeManifest){
		"old_version":     func(m *NativeManifest) { m.FormatVersion = 1 },
		"unknown_version": func(m *NativeManifest) { m.FormatVersion = 3 },
		"missing_profile": func(m *NativeManifest) { m.LoaderProfile = "" },
		"unknown_profile": func(m *NativeManifest) { m.LoaderProfile = "arbitrary_paths" },
		"wrong_topology":  func(m *NativeManifest) { m.LibraryDirectory = filepath.Join(root, "lib") },
	} {
		t.Run(name, func(t *testing.T) {
			changed := m
			mutate(&changed)
			if changed.Validate() == nil {
				t.Fatal("invalid versioned layout accepted")
			}
		})
	}
	for _, invalid := range []string{
		strings.Replace(string(raw), `"loader_profile":"dm_relative_release_v1"`, `"loader_profile":false`, 1),
		strings.Replace(string(raw), `"loader_profile":"dm_relative_release_v1"`, `"loader_profile":null`, 1),
		strings.TrimSuffix(string(raw), "}") + `,"working_directory":"/arbitrary"}`,
	} {
		write([]byte(invalid))
		if _, _, err := loadNativeManifest(c.NativeManifestFile); err == nil {
			t.Fatal("untyped or independent working-directory override accepted")
		}
	}
}

func TestRelativeLibraryProfileKeepsExactSearchPathClosure(t *testing.T) {
	files := map[string]bool{"libdodbc.so": true}
	raw := nativeELFFixture(t, elf.EM_X86_64, nativeDynamicFixture{elf.DT_RPATH, "./:../release"})
	if inspectNativeLibrary(bytes.NewReader(raw), "libdodbc.so", "amd64", files) == nil {
		t.Fatal("manifest1 relative lookup behavior changed")
	}
	if err := inspectNativeLibraryProfile(bytes.NewReader(raw), "libdodbc.so", "amd64", files, RelativeReleaseLoaderProfile); err != nil {
		t.Fatal("explicit vendor layout metadata refused", err)
	}
	current := nativeELFFixture(t, elf.EM_X86_64, nativeDynamicFixture{elf.DT_RPATH, "./:../release:$ORIGIN:$ORIGIN/dependencies"})
	if err := inspectNativeLibraryProfile(bytes.NewReader(current), "libdodbc.so", "amd64", files, RelativeReleaseLoaderProfile); err != nil {
		t.Fatal("current observed vendor layout refused", err)
	}
	for _, path := range []string{"./", "../release", "./:../release:", "../release:./", "/opt/dmdbms/bin", "./:../lib", "${ORIGIN}/../release"} {
		raw := nativeELFFixture(t, elf.EM_X86_64, nativeDynamicFixture{elf.DT_RPATH, path})
		if inspectNativeLibraryProfile(bytes.NewReader(raw), "libdodbc.so", "amd64", files, RelativeReleaseLoaderProfile) == nil {
			t.Fatal("unreviewed relative lookup accepted", path)
		}
	}
}
