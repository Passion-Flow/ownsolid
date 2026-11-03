// Package teststorage observes explicit synthetic fixture storage without changing it.
package teststorage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Observe preserves normal fixture behavior when no temporary storage profile
// is selected. Selected profiles must prove every positive store's actual host
// filesystem, including a not-yet-created leaf's closest existing ancestor.
func Observe(t testing.TB, role, path string) {
	t.Helper()
	root := os.Getenv("PROJECT_ONE_TEST_STORAGE_ROOT")
	if root == "" {
		return
	}
	canonical, e := filepath.EvalSymlinks(root)
	info, se := os.Lstat(root)
	if e != nil || se != nil || canonical != root || !filepath.IsAbs(root) || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("TEMPORARY_TEST_STORAGE_ROOT_UNSAFE")
	}
	rootStat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(rootStat.Uid) != os.Geteuid() {
		t.Fatal("TEMPORARY_TEST_STORAGE_OWNER_INVALID")
	}
	relative, e := filepath.Rel(root, path)
	if e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.Clean(path) != path {
		t.Fatal("TEMPORARY_TEST_STORE_OUTSIDE_SELECTED_ROOT", role)
	}
	observed := path
	for {
		if _, e = os.Lstat(observed); e == nil {
			break
		}
		if !os.IsNotExist(e) || observed == root {
			t.Fatal("TEMPORARY_TEST_STORE_PARENT_UNAVAILABLE", role)
		}
		observed = filepath.Dir(observed)
	}
	resolved, e := filepath.EvalSymlinks(observed)
	store, e2 := os.Lstat(observed)
	if e != nil || e2 != nil || resolved != observed || !store.IsDir() {
		t.Fatal("TEMPORARY_TEST_STORE_UNSAFE", role)
	}
	actual, ok := store.Sys().(*syscall.Stat_t)
	if !ok || actual.Dev != rootStat.Dev || int(actual.Uid) != os.Geteuid() {
		t.Fatal("TEMPORARY_TEST_STORE_FILESYSTEM_OR_OWNER_CHANGED", role)
	}
	raw, e := json.Marshal(struct {
		Role           string `json:"role"`
		Path           string `json:"path"`
		ObservedParent string `json:"observed_parent"`
		Device         uint64 `json:"device"`
	}{role, path, observed, uint64(actual.Dev)})
	if e != nil {
		t.Fatal("TEMPORARY_TEST_STORE_OBSERVATION_INVALID")
	}
	t.Logf("TEMPORARY_STORAGE_MAPPING %s", raw)
}
