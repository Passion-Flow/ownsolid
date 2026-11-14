//go:build linux && public_manager_elf_integration

package dameng

import (
	"flag"
	"os"
	"runtime"
	"testing"
)

var publicManagerELFLibrary = flag.String("dameng-public-manager-library", "", "explicit retained Linux unixODBC library for ELF parser integration; no DM driver is loaded")
var publicInstallerELFLibrary = flag.String("dameng-public-installer-library", "", "explicit retained unixODBC installer library for static ELF alias verification; never loaded")

func TestActualPublicManagerELFDependencyInspection(t *testing.T) {
	if *publicManagerELFLibrary == "" {
		t.Fatal("explicit retained public manager library required")
	}
	f, err := os.Open(*publicManagerELFLibrary)
	if err != nil {
		t.Fatal("public manager library unavailable")
	}
	defer f.Close()
	if err := inspectNativeLibrary(f, "libodbc.so.2", runtime.GOARCH, map[string]bool{"libodbc.so.2": true}); err != nil {
		t.Fatal("actual built public-manager ELF refused", err)
	}
}

func TestActualPublicInstallerELFUsesOnlyClosedAliasPair(t *testing.T) {
	if *publicInstallerELFLibrary == "" {
		t.Fatal("explicit retained public installer library required")
	}
	f, err := os.Open(*publicInstallerELFLibrary)
	if err != nil {
		t.Fatal("public installer ELF unavailable")
	}
	defer f.Close()
	files := map[string]bool{nativeInstallerAlias: true, nativeInstallerSONAME: true}
	if err := inspectNativeLibraryProfile(f, nativeInstallerAlias, runtime.GOARCH, files, RelativeReleaseLoaderProfile); err != nil {
		t.Fatal("actual installer pair ELF refused", err)
	}
	if inspectNativeLibrary(f, nativeInstallerAlias, runtime.GOARCH, files) == nil {
		t.Fatal("legacy SONAME behavior changed")
	}
}
