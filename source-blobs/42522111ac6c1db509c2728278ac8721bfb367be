//go:build repository_integration

package repositoryfixture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
)

const defaultSetupJSON = `{"format_version":1,"setup_seconds":30,"mysql":{"lock_wait_seconds":1,"step_seconds":5,"total_seconds":20,"cleanup_seconds":2}}`
const slowSetupJSON = `{"format_version":1,"setup_seconds":180,"mysql":{"lock_wait_seconds":1,"step_seconds":30,"total_seconds":120,"cleanup_seconds":5}}`

func TestRepositorySetupUnspecifiedKeepsCanonicalDefaults(t *testing.T) {
	t.Setenv(setupPolicyEnvironment, "")
	if e := os.Unsetenv(setupPolicyEnvironment); e != nil {
		t.Fatal(e)
	}
	p, e := loadSetupPolicy()
	want := mysqlmigrate.Options{LockWaitSeconds: 1, StepSeconds: 5, TotalSeconds: 20, CleanupSeconds: 2, MaximumSteps: 97, MaximumMetadataBytes: 1 << 20}
	if e != nil || p.SetupSeconds != 30 || p.FormatVersion != 1 || p.mysqlOptions(97) != want {
		t.Fatal("legacy fixture setup changed", p, e)
	}
	decoded, e := decodeSetupPolicy([]byte(defaultSetupJSON))
	if e != nil || decoded != p {
		t.Fatal("explicit canonical policy differs", e)
	}
}
func TestRepositorySetupExplicitSlowDiskAndRelations(t *testing.T) {
	p, e := decodeSetupPolicy([]byte(slowSetupJSON))
	if e != nil || p.SetupSeconds != 180 || p.MySQL.StepSeconds != 30 || p.MySQL.TotalSeconds != 120 || p.MySQL.CleanupSeconds != 5 {
		t.Fatal("slow setup profile refused", e)
	}
	var document map[string]any
	if e := json.Unmarshal([]byte(slowSetupJSON), &document); e != nil {
		t.Fatal(e)
	}
	document["setup_seconds"] = 900
	database := document["mysql"].(map[string]any)
	database["lock_wait_seconds"], database["step_seconds"], database["total_seconds"], database["cleanup_seconds"] = 60, 300, 810, 30
	raw, e := json.Marshal(document)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := decodeSetupPolicy(raw); e != nil {
		t.Fatal("bounded exact setup relationship refused", e)
	}
}
func TestRepositorySetupRejectsInvalidJSONTypesBoundsAndAliases(t *testing.T) {
	cases := map[string]string{
		"empty": "", "null": "null", "array": "[]", "empty-object": "{}",
		"unknown":           strings.Replace(slowSetupJSON, `"setup_seconds":180`, `"setup_seconds":180,"business_seconds":999`, 1),
		"duplicate":         strings.Replace(slowSetupJSON, `"setup_seconds":180`, `"setup_seconds":180,"setup_seconds":180`, 1),
		"nested-duplicate":  strings.Replace(slowSetupJSON, `"step_seconds":30`, `"step_seconds":30,"step_seconds":30`, 1),
		"nested-unknown":    strings.Replace(slowSetupJSON, `"step_seconds":30`, `"step_seconds":30,"skip_migration":true`, 1),
		"case-alias":        strings.Replace(slowSetupJSON, `"setup_seconds"`, `"Setup_Seconds"`, 1),
		"nested-case-alias": strings.Replace(slowSetupJSON, `"step_seconds"`, `"Step_Seconds"`, 1),
		"boolean":           strings.Replace(slowSetupJSON, `"setup_seconds":180`, `"setup_seconds":true`, 1),
		"string-number":     strings.Replace(slowSetupJSON, `"setup_seconds":180`, `"setup_seconds":"180"`, 1),
		"float":             strings.Replace(slowSetupJSON, `"setup_seconds":180`, `"setup_seconds":180.0`, 1),
		"exponent":          strings.Replace(slowSetupJSON, `"setup_seconds":180`, `"setup_seconds":18e1`, 1),
		"field-null":        strings.Replace(slowSetupJSON, `"setup_seconds":180`, `"setup_seconds":null`, 1),
		"missing":           strings.Replace(slowSetupJSON, `"setup_seconds":180,`, ``, 1),
		"version":           strings.Replace(slowSetupJSON, `"format_version":1`, `"format_version":2`, 1),
		"setup-small":       strings.Replace(slowSetupJSON, `"setup_seconds":180`, `"setup_seconds":29`, 1),
		"setup-large":       strings.Replace(slowSetupJSON, `"setup_seconds":180`, `"setup_seconds":901`, 1),
		"no-cleanup-budget": strings.Replace(slowSetupJSON, `"setup_seconds":180`, `"setup_seconds":125`, 1),
		"step-zero":         strings.Replace(slowSetupJSON, `"step_seconds":30`, `"step_seconds":0`, 1),
		"step-large":        strings.Replace(slowSetupJSON, `"step_seconds":30`, `"step_seconds":301`, 1),
		"total-relation":    strings.Replace(slowSetupJSON, `"total_seconds":120`, `"total_seconds":30`, 1),
		"lock-large":        strings.Replace(slowSetupJSON, `"lock_wait_seconds":1`, `"lock_wait_seconds":61`, 1),
		"cleanup-zero":      strings.Replace(slowSetupJSON, `"cleanup_seconds":5`, `"cleanup_seconds":0`, 1),
		"overflow":          strings.Replace(slowSetupJSON, `"setup_seconds":180`, `"setup_seconds":9999999999999999999999`, 1),
		"trailing":          slowSetupJSON + `{}`, "oversize": strings.Repeat(" ", maximumSetupPolicyBytes) + slowSetupJSON,
		"invalid-utf8": string([]byte{0xff}),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			p, e := decodeSetupPolicy([]byte(raw))
			if e == nil || e.Error() != "REPOSITORY_SETUP_POLICY_INVALID" || p != (setupPolicy{}) {
				t.Fatal("unsafe setup policy accepted or exposed partial values", e)
			}
		})
	}
}
func privateSetupFile(t *testing.T) string {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	name := filepath.Join(root, "setup.json")
	if e = os.WriteFile(name, []byte(slowSetupJSON), 0600); e != nil {
		t.Fatal(e)
	}
	return name
}
func TestRepositorySetupProtectedReference(t *testing.T) {
	name := privateSetupFile(t)
	t.Setenv(setupPolicyEnvironment, name)
	p, e := loadSetupPolicy()
	if e != nil || p.SetupSeconds != 180 || p.MySQL.TotalSeconds != 120 {
		t.Fatal("protected actual file refused", e)
	}
	for _, mode := range []string{"empty-reference", "relative", "alias", "file-mode", "parent-mode", "symlink", "hardlink", "directory"} {
		t.Run(mode, func(t *testing.T) {
			file := privateSetupFile(t)
			switch mode {
			case "empty-reference":
				file = ""
			case "relative":
				file = "setup.json"
			case "alias":
				file = filepath.Dir(file) + "/./setup.json"
			case "file-mode":
				if e := os.Chmod(file, 0644); e != nil {
					t.Fatal(e)
				}
			case "parent-mode":
				if e := os.Chmod(filepath.Dir(file), 0755); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				link := filepath.Join(filepath.Dir(file), "link.json")
				if e := os.Symlink(file, link); e != nil {
					t.Fatal(e)
				}
				file = link
			case "hardlink":
				if e := os.Link(file, filepath.Join(filepath.Dir(file), "second.json")); e != nil {
					t.Fatal(e)
				}
			case "directory":
				file = filepath.Dir(file)
			}
			t.Setenv(setupPolicyEnvironment, file)
			p, e := loadSetupPolicy()
			if e == nil || e.Error() != "REPOSITORY_SETUP_POLICY_INVALID" || p != (setupPolicy{}) {
				t.Fatal("unsafe setup reference accepted or leaked", e)
			}
		})
	}
}
