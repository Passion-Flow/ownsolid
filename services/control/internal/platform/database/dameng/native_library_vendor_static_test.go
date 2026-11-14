//go:build linux && dameng_vendor_static

package dameng

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io"
	"os"
	"strings"
	"testing"
)

var vendorStaticLibrary = flag.String("dameng-vendor-library", "", "explicit read-only vendor ELF fixture; never loaded/executed")
var vendorStaticArchitecture = flag.String("dameng-vendor-architecture", "", "actual selected ELF architecture: amd64 or arm64")
var vendorStaticSHA256 = flag.String("dameng-vendor-sha256", "", "observed retained fixture digest, not a publisher signature")
var vendorStaticInventory = flag.String("dameng-vendor-inventory", "", "comma-separated native dependency basenames for static closure inspection")

func TestActualVendorELFRelativeProfileStaticOnly(t *testing.T) {
	if *vendorStaticLibrary == "" || *vendorStaticSHA256 == "" || *vendorStaticInventory == "" {
		t.Fatal("explicit observed vendor fixture required")
	}
	f, err := os.Open(*vendorStaticLibrary)
	if err != nil {
		t.Fatal("vendor static fixture unavailable")
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, (1<<30)+1)); err != nil || hex.EncodeToString(h.Sum(nil)) != *vendorStaticSHA256 {
		t.Fatal("observed vendor fixture digest mismatch")
	}
	files := map[string]bool{"libdodbc.so": true}
	for _, name := range strings.Split(*vendorStaticInventory, ",") {
		if !nativeFileName.MatchString(name) || files[name] {
			t.Fatal("invalid static dependency inventory")
		}
		files[name] = true
	}
	if err := inspectNativeLibraryProfile(f, "libdodbc.so", *vendorStaticArchitecture, files, RelativeReleaseLoaderProfile); err != nil {
		t.Fatal("actual vendor loader metadata refused", err)
	}
	if inspectNativeLibrary(f, "libdodbc.so", *vendorStaticArchitecture, files) == nil {
		t.Fatal("manifest1 unexpectedly admits this relative vendor layout")
	}
}
