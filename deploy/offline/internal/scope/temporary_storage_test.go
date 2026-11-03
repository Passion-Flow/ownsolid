package scope

import (
	"encoding/json"
	"os"
	"syscall"
	"testing"
)

// Extra observation only. The original mounted/private scope and persistence
// assertions still run against the actual declared test filesystem.
func observeTemporaryScopeStorage(t *testing.T, directory string) {
	t.Helper()
	root := os.Getenv("PROJECT_ONE_TEST_STORAGE_ROOT")
	if root == "" {
		return
	}
	mount := os.Getenv("PROJECT_ONE_TEST_STORAGE_MOUNT")
	if !privateDirectory(root) || !mounted(mount) || !contained(root, mount) || !contained(mount, directory) || !privateDirectory(directory) {
		t.Fatal("explicit private actual internal test filesystem required")
	}
	var backing, filesystem, actual syscall.Stat_t
	if syscall.Stat(root, &backing) != nil || syscall.Stat(mount, &filesystem) != nil || syscall.Stat(directory, &actual) != nil || actual.Dev != filesystem.Dev || filesystem.Dev == backing.Dev || int(actual.Uid) != os.Geteuid() {
		t.Fatal("actual test mount/backing/device identity mismatch")
	}
	raw, e := json.Marshal(map[string]any{"path": directory, "root": root, "mount": mount, "device": actual.Dev, "backing_device": backing.Dev})
	if e != nil {
		t.Fatal(e)
	}
	t.Log("OFFLINE_TEMPORARY_STORAGE_MAPPING " + string(raw))
}
