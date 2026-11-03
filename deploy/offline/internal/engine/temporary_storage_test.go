package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Record the real Unix fixture backing, without relaxing protocol assertions.
func observeTemporaryEngineStorage(t *testing.T, directory string) {
	t.Helper()
	root := os.Getenv("PROJECT_ONE_TEST_STORAGE_ROOT")
	if root == "" {
		return
	}
	mount := os.Getenv("PROJECT_ONE_TEST_STORAGE_MOUNT")
	for _, pair := range [][2]string{{root, mount}, {mount, directory}} {
		relative, e := filepath.Rel(pair[0], pair[1])
		resolved, re := filepath.EvalSymlinks(pair[1])
		info, se := os.Stat(pair[1])
		if e != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || re != nil || resolved != pair[1] || se != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			t.Fatal("private actual internal test mount required")
		}
	}
	var backing, filesystem, actual syscall.Stat_t
	if syscall.Stat(root, &backing) != nil || syscall.Stat(mount, &filesystem) != nil || syscall.Stat(directory, &actual) != nil || actual.Dev != filesystem.Dev || filesystem.Dev == backing.Dev || int(backing.Uid) != os.Geteuid() || int(filesystem.Uid) != os.Geteuid() || int(actual.Uid) != os.Geteuid() {
		t.Fatal("actual Engine fixture mount/backing/device mismatch")
	}
	raw, e := json.Marshal(map[string]any{"path": directory, "root": root, "mount": mount, "device": actual.Dev, "backing_device": backing.Dev})
	if e != nil {
		t.Fatal(e)
	}
	t.Log("OFFLINE_TEMPORARY_STORAGE_MAPPING " + string(raw))
}
