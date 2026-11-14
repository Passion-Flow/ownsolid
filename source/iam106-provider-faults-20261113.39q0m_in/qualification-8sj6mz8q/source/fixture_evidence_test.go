package boundary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fixtureEvidence struct {
	mu        sync.Mutex
	directory string
	values    map[string]bool
	children  []*childProcess
}

func newFixtureEvidence(t *testing.T, label string) *fixtureEvidence {
	t.Helper()
	root := os.Getenv("IAM_OP_ARTIFACT_DIRECTORY")
	resolved, err := filepath.EvalSymlinks(root)
	info, statErr := os.Stat(root)
	if root == "" || err != nil || statErr != nil || resolved != root || !filepath.IsAbs(root) || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("private protocol artifact directory required")
	}
	directory := filepath.Join(root, label)
	if os.Mkdir(directory, 0700) != nil {
		t.Fatal("private protocol artifact scope unavailable")
	}
	return &fixtureEvidence{directory: directory, values: map[string]bool{}}
}
func (e *fixtureEvidence) record(values ...string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, value := range values {
		if len(value) >= 16 && len(value) <= 32768 {
			e.values[value] = true
		}
	}
}
func (e *fixtureEvidence) child(child *childProcess) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.children = append(e.children, child)
}
func (e *fixtureEvidence) save(t *testing.T, folder string) {
	t.Helper()
	if e == nil {
		return
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Error("private child log inventory failed")
		return
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "child-") && strings.HasSuffix(entry.Name(), ".log") {
			raw, err := readPrivateFile(filepath.Join(folder, entry.Name()), 1<<20)
			if err != nil {
				t.Error("bounded private child log read failed")
				continue
			}
			privateWrite(t, filepath.Join(e.directory, entry.Name()), raw)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	values := make([]string, 0, len(e.values))
	for value := range e.values {
		values = append(values, value)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Error("private redaction input serialization failed")
		return
	}
	privateWrite(t, filepath.Join(e.directory, "sensitive-inputs.json"), raw)
	var processes []map[string]any
	for _, child := range e.children {
		processes = append(processes, map[string]any{"pid": child.pid, "exited": child.exited, "success": child.success})
	}
	raw, err = json.Marshal(map[string]any{"test": t.Name(), "processes": processes})
	if err != nil {
		t.Error("private process result serialization failed")
		return
	}
	privateWrite(t, filepath.Join(e.directory, "processes.json"), raw)
}
