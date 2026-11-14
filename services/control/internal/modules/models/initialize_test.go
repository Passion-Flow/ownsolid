package models_test

import (
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"testing"
)

func TestModelStoreInitializationRefusesMissingSymlinkAndExistingData(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "explicit-empty")
	if e := os.Mkdir(empty, 0755); e != nil {
		t.Fatal(e)
	}
	if e := models.InitializeStore(empty, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(empty)
	if e != nil || info.Mode().Perm() != 0700 {
		t.Fatal("private root not initialized", e)
	}
	if e = models.InitializeStore(filepath.Join(root, "missing"), os.Geteuid(), os.Getegid()); e == nil {
		t.Fatal("missing storage silently created")
	}
	if e = os.WriteFile(filepath.Join(empty, "existing-data"), []byte("PRESERVE_EXISTING_DATA"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = models.InitializeStore(empty, os.Geteuid(), os.Getegid()); e == nil || e.Error() != "MODEL_STORE_NOT_EMPTY" {
		t.Fatal("existing store adopted", e)
	}
	link := filepath.Join(root, "linked")
	if e = os.Symlink(empty, link); e != nil {
		t.Fatal(e)
	}
	if e = models.InitializeStore(link, os.Geteuid(), os.Getegid()); e == nil {
		t.Fatal("symlink store adopted")
	}
	if data, e := os.ReadFile(filepath.Join(empty, "existing-data")); e != nil || string(data) != "PRESERVE_EXISTING_DATA" {
		t.Fatal("existing data lost", e)
	}
}
