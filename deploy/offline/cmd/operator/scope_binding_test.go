package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

func TestOperatorScopeBindingArgumentsRefuseBeforeIO(t *testing.T) {
	id := strings.Repeat("a", 32)
	for _, tc := range []struct {
		name string
		args []string
		code string
	}{
		{"empty", []string{"status", "--bound-scope-id="}, "OFFLINE_OPERATOR_ARGUMENT_INVALID"},
		{"short", []string{"status", "--bound-scope-id", id[:31]}, "OFFLINE_OPERATOR_ARGUMENT_INVALID"},
		{"long", []string{"status", "--bound-scope-id", id + "a"}, "OFFLINE_OPERATOR_ARGUMENT_INVALID"},
		{"uppercase", []string{"status", "--bound-scope-id", strings.ToUpper(id)}, "OFFLINE_OPERATOR_ARGUMENT_INVALID"},
		{"nonhex", []string{"status", "--bound-scope-id", strings.Repeat("g", 32)}, "OFFLINE_OPERATOR_ARGUMENT_INVALID"},
		{"duplicate", []string{"status", "--bound-scope-id", id, "--bound-scope-id", id}, "OFFLINE_OPERATOR_ARGUMENT_INVALID"},
		{"init", []string{"init-scope", "--bound-scope-id", id}, "OFFLINE_OPERATOR_SCOPE_BINDING_COMMAND_INVALID"},
		{"inspect", []string{"inspect", "--bound-scope-id", id}, "OFFLINE_OPERATOR_SCOPE_BINDING_COMMAND_INVALID"},
		{"unpack", []string{"unpack", "--bound-scope-id", id}, "OFFLINE_OPERATOR_SCOPE_BINDING_COMMAND_INVALID"},
		{"prepared", []string{"verify-prepared", "--bound-scope-id", id}, "OFFLINE_OPERATOR_SCOPE_BINDING_COMMAND_INVALID"},
		{"runtime", []string{"start-runtime", "--bound-scope-id", id}, "OFFLINE_OPERATOR_SCOPE_BINDING_COMMAND_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if e := run(tc.args); e == nil || e.Error() != tc.code {
				t.Fatalf("unexpected admission decision: %v", e)
			}
		})
	}
}

func TestOperatorScopeBindingReachesOpenedMarkerBeforeEffects(t *testing.T) {
	// Deliberately incomplete private scope: a matching/omitted fence must reach
	// context refusal, while a foreign fence refuses earlier at the actual marker.
	// No Engine, bundle or journal fixture can produce a successful operation.
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	c := configuration.Defaults()
	mount := root
	for {
		var current, parent syscall.Stat_t
		if syscall.Stat(mount, &current) != nil || syscall.Stat(filepath.Dir(mount), &parent) != nil || mount == "/" {
			t.Skip("actual mounted external TMPDIR required; run make test-offline on Linux")
		}
		if current.Dev != parent.Dev {
			break
		}
		mount = filepath.Dir(mount)
	}
	c.Verification.MountRoot = mount
	c.Verification.TrustFile = filepath.Join(root, "trust.json")
	c.BundleFile = filepath.Join(root, "bundle.tar")
	c.DestinationRoot = filepath.Join(root, "prepared")
	c.ScopeRoot = filepath.Join(root, "scope")
	c.ExpectedArchiveSHA256 = strings.Repeat("b", 64)
	c.ExpectedManifestSHA256 = strings.Repeat("c", 64)
	if e = os.Mkdir(c.ScopeRoot, 0700); e != nil {
		t.Fatal(e)
	}
	id := strings.Repeat("a", 32)
	marker := []byte(`{"scope_id":"` + id + `"}`)
	write := func(name string, raw []byte) {
		t.Helper()
		if e := os.WriteFile(name, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	markerName := filepath.Join(c.ScopeRoot, ".projectone-installation-test-scope.json")
	lockName := filepath.Join(c.ScopeRoot, ".scope.lock")
	write(markerName, marker)
	write(lockName, []byte(id+"\n"))
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = configuration.Load(raw, nil); e != nil {
		t.Fatal("invalid fixture configuration", e)
	}
	name := filepath.Join(root, "operator.json")
	write(name, raw)
	for _, tc := range []struct {
		name, bound, code string
	}{
		{"foreign", strings.Repeat("f", 32), "OFFLINE_SCOPE_BINDING_MISMATCH"},
		{"same", id, "OFFLINE_SCOPE_CONTEXT_MISMATCH"},
		{"omitted", "", "OFFLINE_SCOPE_CONTEXT_MISMATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"status", "--config", name}
			if tc.bound != "" {
				args = append(args, "--bound-scope-id", tc.bound)
			}
			if e := run(args); e == nil || e.Error() != tc.code {
				t.Fatalf("actual marker admission: %v", e)
			}
		})
	}
	for path, expected := range map[string][]byte{markerName: marker, lockName: []byte(id + "\n"), name: raw} {
		actual, e := os.ReadFile(path)
		if e != nil || string(actual) != string(expected) {
			t.Fatal("scope fixture changed", e)
		}
	}
	entries, e := os.ReadDir(c.ScopeRoot)
	if e != nil || len(entries) != 2 {
		t.Fatal("scope fixture gained unexpected state", e)
	}
}
