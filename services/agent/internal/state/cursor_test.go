package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTaskCursorIsDurableAndProtected(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	if got, e := LoadCursor(dir); e != nil || got != 0 {
		t.Fatal("missing cursor", got, e)
	}
	for _, sequence := range []uint64{1, 7, 7} {
		if e := SaveCursor(dir, sequence); e != nil {
			t.Fatal(e)
		}
		if got, e := LoadCursor(dir); e != nil || got != sequence {
			t.Fatal("cursor not durable", got, e)
		}
	}
	if e := SaveCursor(dir, 1<<53); e == nil {
		t.Fatal("cursor beyond the JSON range saved")
	}
	path := filepath.Join(dir, cursorFile)
	if info, e := os.Stat(path); e != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("cursor mode", e)
	}
	for _, bad := range []string{`{"acknowledged_sequence":-1}`, `{"acknowledged_sequence":1,"x":2}`, `{"acknowledged_sequence":9007199254740992}`, `{}{}`} {
		if e := os.WriteFile(path, []byte(bad), 0o600); e != nil {
			t.Fatal(e)
		}
		if _, e := LoadCursor(dir); e == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	if e := os.WriteFile(path, []byte(`{"acknowledged_sequence":3}`), 0o644); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(path, 0o644); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadCursor(dir); e == nil {
		t.Fatal("world-readable cursor accepted")
	}
}
