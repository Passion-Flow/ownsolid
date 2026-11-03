package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func workspaceConfigInput(t *testing.T) []byte {
	t.Helper()
	raw, e := yaml.Marshal(validConfig())
	if e != nil {
		t.Fatal(e)
	}
	// validConfig's nil runtime marshals as null; omission selects owned defaults.
	return []byte(strings.Replace(string(raw), "runtime: null\n", "", 1))
}

func workspaceEnvironmentFixture(t *testing.T, contents string) string {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(root, 0700) != nil {
		t.Fatal("private fixture")
	}
	path := filepath.Join(root, "policy.env")
	if os.WriteFile(path, []byte(contents), 0600) != nil {
		t.Fatal("private fixture")
	}
	return path
}

func TestWorkspacePolicyDefaultsPartialAndEnvironmentPrecedence(t *testing.T) {
	raw := workspaceConfigInput(t)
	c, e := Decode(raw)
	if e != nil || c.Policy().Workspaces != DefaultWorkspacePolicy() {
		t.Fatal("owned defaults", e)
	}
	raw = append(raw, []byte("runtime:\n  workspaces:\n    maximum_count: 7\n")...)
	c, e = Decode(raw)
	if e != nil || c.Policy().Workspaces.MaximumCount != 7 || c.Policy().Workspaces.MaximumLiveNotes != DefaultWorkspacePolicy().MaximumLiveNotes {
		t.Fatal("partial YAML", e)
	}
	t.Setenv(workspaceCountEnvironment, "9999")
	c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, "# explicit private input\n"+workspaceNotesEnvironment+"='13'\n"))
	if e != nil || c.Policy().Workspaces != (WorkspacePolicy{MaximumCount: 7, MaximumLiveNotes: 13}) {
		t.Fatal("partial explicit override and no ambient fallback", e)
	}
	c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, workspaceCountEnvironment+"=\"1\"\n"+workspaceNotesEnvironment+"=1000000\n"))
	if e != nil || c.Policy().Workspaces != (WorkspacePolicy{MaximumCount: 1, MaximumLiveNotes: 1000000}) {
		t.Fatal("inclusive bounds", e)
	}
	c, e = decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, workspaceCountEnvironment+"=10000\n"+workspaceNotesEnvironment+"=1\n"))
	if e != nil || c.Policy().Workspaces != (WorkspacePolicy{MaximumCount: 10000, MaximumLiveNotes: 1}) {
		t.Fatal("opposite inclusive bounds", e)
	}
}

func TestWorkspacePolicyMalformedInputsAndSafeDiagnostics(t *testing.T) {
	raw := workspaceConfigInput(t)
	path := workspaceEnvironmentFixture(t, workspaceCountEnvironment+"=2\n")
	for _, body := range []string{"null", "false", "{maximum_count: null}", "{maximum_count: false}", "{maximum_count: '7'}", "{maximum_count: 0}", "{maximum_count: 10001}", "{maximum_live_notes: 1000001}", "{maximum_live_notes: -1}", "{unknown: sensitive-value}", "{maximum_count: 2, maximum_count: 3}", "{maximum_count: &count 2, maximum_live_notes: *count}"} {
		if _, e := decodeWithEnvironment(append(append([]byte{}, raw...), []byte("runtime:\n  workspaces: "+body+"\n")...), path); e == nil || strings.Contains(e.Error(), "sensitive-value") {
			t.Fatal("invalid YAML accepted or leaked", body, e)
		}
	}
	for _, body := range []string{"UNKNOWN=sensitive-value", workspaceCountEnvironment + "=1\n" + workspaceCountEnvironment + "=2", workspaceCountEnvironment + "=", workspaceCountEnvironment + "=false", workspaceCountEnvironment + "=0", workspaceCountEnvironment + "=10001", workspaceNotesEnvironment + "=1000001", workspaceCountEnvironment + "=-1", workspaceCountEnvironment + "=1.5", workspaceCountEnvironment + "=$COUNT", workspaceCountEnvironment + "=$(touch sensitive-value)", workspaceCountEnvironment + "=\"2\" trailing", workspaceCountEnvironment + "='2' # comment", workspaceCountEnvironment + "=9999999999999999999999999999"} {
		if os.WriteFile(path, []byte(body), 0600) != nil {
			t.Fatal("private fixture")
		}
		if _, e := decodeWithEnvironment(raw, path); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" {
			t.Fatal("environment must fail safely", e)
		}
	}
	if c, e := Decode(append(raw, []byte("runtime: null\n")...)); e != nil || c.Policy().Workspaces != DefaultWorkspacePolicy() {
		t.Fatal("legacy omitted operator runtime compatibility", e)
	}
}

func TestWorkspacePolicyPrivateFileBoundaryAndLicenseCompatibility(t *testing.T) {
	raw := workspaceConfigInput(t)
	path := workspaceEnvironmentFixture(t, workspaceCountEnvironment+"=3\n")
	reject := func(p string) {
		t.Helper()
		if _, e := decodeWithEnvironment(raw, p); e == nil || e.Error() != "CONFIG_POLICY_PRIVATE_FILE_INVALID" {
			t.Fatal("private boundary", e)
		}
		if _, e := readLicenseFile(p, MaximumPolicyEnvironmentBytes); e == nil || e.Error() != "CONFIG_LICENSING_PRIVATE_FILE_INVALID" {
			t.Fatal("licensing diagnostic compatibility", e)
		}
	}
	reject("relative-secret-input")
	reject(filepath.Dir(path))
	if os.Chmod(path, 0640) != nil {
		t.Fatal("mode")
	}
	reject(path)
	if os.Chmod(path, 0600) != nil {
		t.Fatal("mode")
	}
	link := filepath.Join(filepath.Dir(path), "link")
	if os.Symlink(path, link) != nil {
		t.Fatal("symlink")
	}
	reject(link)
	if os.Remove(link) != nil || os.Link(path, link) != nil {
		t.Fatal("hardlink")
	}
	reject(path)
	if os.Remove(link) != nil {
		t.Fatal("remove owned link")
	}
	if os.Chmod(filepath.Dir(path), 0750) != nil {
		t.Fatal("directory mode")
	}
	reject(path)
	if os.Chmod(filepath.Dir(path), 0700) != nil {
		t.Fatal("directory mode")
	}
	if os.WriteFile(path, nil, 0600) != nil {
		t.Fatal("empty")
	}
	reject(path)
	if os.WriteFile(path, []byte(strings.Repeat("x", int(MaximumPolicyEnvironmentBytes)+1)), 0600) != nil {
		t.Fatal("oversize")
	}
	reject(path)
}
