package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestModelLifecycleOutputVerificationBudgetDefaultsOverridesAndBounds(t *testing.T) {
	p := DefaultModelLifecyclePolicy()
	if p.OutputSeconds != 300 || p.Validate() != nil {
		t.Fatal("bounded output default")
	}
	for _, value := range []int{1, 45, 3600} {
		p.OutputSeconds = value
		if e := p.Validate(); e != nil {
			t.Fatal("valid nondefault output deadline", value, e)
		}
	}
	for _, value := range []int{0, -1, 3601} {
		p.OutputSeconds = value
		if e := p.Validate(); e == nil {
			t.Fatal("invalid output deadline", value)
		}
	}
}

func TestModelLifecyclePolicyStrictSourcesDefaultsOverridesAndBounds(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/control.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	base := strings.Split(string(raw), "\nruntime:")[0]
	load := func(policy string) (Config, error) {
		path := filepath.Join(t.TempDir(), "control.yaml")
		if e := os.WriteFile(path, []byte(base+"\nruntime:\n  model_lifecycle:\n"+policy), 0600); e != nil {
			t.Fatal(e)
		}
		return Load(path)
	}
	valid, e := load("    scan_seconds: 12\n    scan_maximum_bytes: 2097152\n    scan_maximum_rows: 512\n    scan_maximum_versions: 10\n    scan_concurrency: 2\n    transaction_seconds: 3\n    max_holdouts_per_workspace: 5\n")
	if e != nil || valid.Policy().ModelLifecycle.ScanConcurrency != 2 || valid.Policy().ModelLifecycle.ScanMaximumRows != 512 {
		t.Fatal("nondefault policy", e)
	}
	for name, value := range map[string]string{
		"null": "    scan_seconds: null\n", "fraction": "    scan_seconds: 5.5\n", "quoted": "    scan_seconds: \"5\"\n", "boolean": "    scan_seconds: true\n",
		"unknown": "    unknown_secret: synthetic-sensitive-value\n", "zero": "    scan_seconds: 0\n", "memory": "    scan_maximum_bytes: 1073741825\n", "rows": "    scan_maximum_rows: 2\n", "concurrency": "    scan_concurrency: 5\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, e := load(value)
			if e == nil {
				t.Fatal("invalid new policy silently accepted")
			}
			if strings.Contains(e.Error(), "synthetic-sensitive-value") {
				t.Fatal("configuration leaked value")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "control.yaml")
	if e := os.WriteFile(path, []byte(base+"\nruntime:\n  model_lifecycle: null\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Load(path); e == nil {
		t.Fatal("explicit null policy silently defaulted")
	}
	if DefaultModelLifecyclePolicy().Validate() != nil {
		t.Fatal("invalid defaults")
	}
	alias, e := load("    scan_seconds: &seconds 12\n    transaction_seconds: *seconds\n")
	if e != nil || alias.Policy().ModelLifecycle.TransactionSeconds != 12 {
		t.Fatal("typed integer alias", e)
	}
	if _, e := load("    <<: {scan_seconds: null}\n"); e == nil {
		t.Fatal("merge bypassed typed new-policy validation")
	}
	if e := os.WriteFile(path, []byte(base+"\nruntime:\n  <<: {model_lifecycle: {scan_seconds: null}}\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Load(path); e == nil {
		t.Fatal("ancestor merge hid invalid new-policy value")
	}

}

func TestModelLifecycleSnapshotBoundsAndStrictScalars(t *testing.T) {
	p := DefaultModelLifecyclePolicy()
	if p.SnapshotSeconds != 300 || p.SnapshotManifestBytes != 33554432 || p.SnapshotMaximumBytes != 107374182400 || p.SnapshotMaximumFiles != 100000 {
		t.Fatal("snapshot defaults")
	}
	for _, change := range []func(*ModelLifecyclePolicy){func(p *ModelLifecyclePolicy) { p.SnapshotSeconds = 0 }, func(p *ModelLifecyclePolicy) { p.SnapshotSeconds = 3601 }, func(p *ModelLifecyclePolicy) { p.SnapshotManifestBytes = 4095 }, func(p *ModelLifecyclePolicy) { p.SnapshotManifestBytes = 67108865 }, func(p *ModelLifecyclePolicy) { p.SnapshotMaximumBytes = 2147483647 }, func(p *ModelLifecyclePolicy) { p.SnapshotMaximumBytes = 10995116277761 }, func(p *ModelLifecyclePolicy) { p.SnapshotMaximumFiles = 15 }, func(p *ModelLifecyclePolicy) { p.SnapshotMaximumFiles = 1000001 }} {
		bad := p
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid snapshot budget accepted")
		}
	}
	for _, body := range []string{"snapshot_seconds: null", "snapshot_maximum_bytes: 2147483648.5", "snapshot_maximum_files: \"16\"", "snapshot_manifest_bytes: true"} {
		if validateModelLifecycleYAML([]byte("runtime:\n  model_lifecycle:\n    "+body+"\n")) == nil {
			t.Fatal("snapshot scalar silently coerced", body)
		}
	}
}
