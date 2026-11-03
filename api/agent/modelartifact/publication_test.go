package modelartifact

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDirectoryPublicationNeverReplacesAndSerializes(t *testing.T) {
	base := t.TempDir()
	root, e := os.OpenRoot(base)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	for _, name := range []string{"first", "second", "empty", "kept"} {
		if e = os.Mkdir(filepath.Join(base, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	for _, target := range []string{"empty", "kept"} {
		if e = PublishDirectory(root, "first", target); e == nil {
			t.Fatal("existing destination replaced", target)
		}
	}
	if e = os.Symlink("kept", filepath.Join(base, "link")); e != nil {
		t.Fatal(e)
	}
	if e = PublishDirectory(root, "first", "link"); e == nil {
		t.Fatal("symlink replaced")
	}
	if target, e := os.Readlink(filepath.Join(base, "link")); e != nil || target != "kept" {
		t.Fatal("symlink changed", e)
	}
	var group sync.WaitGroup
	result := make(chan error, 2)
	for _, source := range []string{"first", "second"} {
		group.Go(func() { result <- PublishDirectory(root, source, "published") })
	}
	group.Wait()
	close(result)
	passed := 0
	for e := range result {
		if e == nil {
			passed++
		}
	}
	if passed != 1 {
		t.Fatal("publication winners", passed)
	}
	for _, bad := range []string{"../outside", "/outside", "."} {
		if PublishDirectory(root, bad, "next") == nil || PublishDirectory(root, "published", bad) == nil {
			t.Fatal("path admitted", bad)
		}
	}
	if _, e = os.Stat(filepath.Join(base, "published")); e != nil {
		t.Fatal(e)
	}
}
