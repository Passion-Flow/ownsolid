//go:build offlineintegration

package prepare

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This executable journey uses only synthetic signed bytes and disposable isolated files.
func TestActualOperatorCLIInspectUnpackRecoveryAndFalse(t *testing.T) {
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private scope unavailable")
	}
	mount := root
	for {
		var a, b syscall.Stat_t
		if syscall.Stat(mount, &a) != nil || syscall.Stat(filepath.Dir(mount), &b) != nil {
			t.Fatal("mount unavailable")
		}
		if a.Dev != b.Dev {
			break
		}
		parent := filepath.Dir(mount)
		if parent == mount {
			t.Fatal("actual external mounted TMPDIR required")
		}
		mount = parent
	}
	put := func(name string, raw []byte) {
		if e := os.WriteFile(name, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	encode := func(v any) []byte {
		raw, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return append(raw, '\n')
	}
	assets := filepath.Join(root, "assets")
	os.Mkdir(assets, 0700)
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{31}, 32))
	public := key.Public().(ed25519.PublicKey)
	keyPath := filepath.Join(root, "synthetic-key")
	trustPath := filepath.Join(root, "trust.json")
	put(keyPath, []byte(base64.StdEncoding.EncodeToString(key)))
	put(trustPath, encode(map[string]any{"keys": map[string]string{wire.Digest(public): base64.StdEncoding.EncodeToString(public)}}))
	m := wire.Manifest{FormatVersion: 1, ReleaseID: "synthetic-operator-1", Profile: wire.FixtureProfile, SourceSHA256: strings.Repeat("a", 64), SchemaVersion: 16, MinimumPredecessor: 15, MaximumPredecessor: 16, Capabilities: []string{"M01"}, Prerequisites: []string{}, Assets: []wire.Asset{}}
	for _, s := range []struct{ id, kind, value string }{{"license", "license", "Explicit synthetic license"}, {"payload", "operator", "Explicit synthetic payload, never installed or executed"}} {
		raw := []byte(s.value)
		a := wire.Asset{ID: s.id, Path: s.id, Kind: s.kind, Size: int64(len(raw)), SHA256: wire.Digest(raw), Licenses: []string{}}
		if s.kind != "license" {
			a.Licenses = []string{"license"}
		}
		m.Assets = append(m.Assets, a)
		put(filepath.Join(assets, a.Path), raw)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	manifestRaw := encode(m)
	put(manifestPath, manifestRaw)
	bundlePath := filepath.Join(root, "fixture.tar.gz")
	factoryConfig := filepath.Join(root, "factory.json")
	put(factoryConfig, encode(map[string]any{"mount_root": mount, "artifact_root": assets, "manifest_file": manifestPath, "trust_file": trustPath, "private_key_file": keyPath, "output_file": bundlePath, "allow_test_fixture": true, "maximum_asset_bytes": 1048576, "maximum_total_bytes": 4194304}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	run := func(binary, command, config, environment string, success bool) []byte {
		args := []string{command, "--config", config}
		if environment != "" {
			args = append(args, "--env-file", environment)
		}
		cmd := exec.CommandContext(ctx, binary, args...)
		output, e := cmd.CombinedOutput()
		if success && e != nil || !success && e == nil {
			t.Fatalf("actual CLI %s outcome: %v %s", command, e, output)
		}
		return output
	}
	factoryBinary := os.Getenv("PROJECT_ONE_TEST_FACTORY_BINARY")
	operatorBinary := os.Getenv("PROJECT_ONE_TEST_OPERATOR_BINARY")
	records := []map[string]any{}
	factoryOutput := run(factoryBinary, "build", factoryConfig, "", true)
	var built bundle.Result
	if json.Unmarshal(factoryOutput, &built) != nil {
		t.Fatal("factory result invalid")
	}
	c := configuration.Defaults()
	c.Verification.MountRoot = mount
	c.Verification.TrustFile = trustPath
	c.Verification.AllowTestFixture = true
	c.BundleFile = bundlePath
	c.ExpectedArchiveSHA256 = built.ArchiveSHA256
	c.ExpectedManifestSHA256 = wire.Digest(manifestRaw)
	c.DestinationRoot = filepath.Join(root, "prepared")
	c.ScopeRoot = filepath.Join(root, "installation-scope")
	operatorConfig := filepath.Join(root, "operator.json")
	put(operatorConfig, encode(c))
	environment := filepath.Join(root, "operator.env")
	put(environment, []byte("PROJECT_ONE_OFFLINE_OPERATOR_TIMEOUT_SECONDS=17\nPROJECT_ONE_OFFLINE_OPERATOR_MAXIMUM_JOURNAL_BYTES=131072\n"))
	for _, operation := range []string{"inspect", "unpack", "verify-prepared"} {
		output := run(operatorBinary, operation, operatorConfig, environment, true)
		var result bundle.Result
		expectedQualification := "synthetic_test_fixture_only_never_complete_platform_bundle"
		if operation == "verify-prepared" {
			expectedQualification = "synthetic_prepared_fixture_only_never_complete_platform_installation"
		}
		if json.Unmarshal(output, &result) != nil || result.Qualification != expectedQualification {
			t.Fatal("incorrect fixture result", string(output))
		}
		records = append(records, map[string]any{"operation": operation, "result": result})
	}
	for _, a := range m.Assets {
		raw, e := os.ReadFile(filepath.Join(c.DestinationRoot, a.Path))
		if e != nil || wire.Digest(raw) != a.SHA256 {
			t.Fatal("persisted actual output mismatch", e)
		}
	}
	journalBefore, e := os.ReadFile(c.DestinationRoot + ".journal.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	output := run(operatorBinary, "unpack", operatorConfig, environment, false)
	if !bytes.Contains(output, []byte("RELEASE_PREPARATION_OCCUPIED_OR_UNCERTAIN")) {
		t.Fatal("repeat not refused", string(output))
	}
	journalAfter, _ := os.ReadFile(c.DestinationRoot + ".journal.jsonl")
	if !bytes.Equal(journalBefore, journalAfter) {
		t.Fatal("repeat modified known scope")
	}
	var initialScope scope.Status
	for _, operation := range []string{"init-scope", "status"} {
		output := run(operatorBinary, operation, operatorConfig, environment, true)
		var result scope.Status
		if json.Unmarshal(output, &result) != nil || result.Status != "isolated_scope_prepared" || result.Events != 3 || result.Qualification != "synthetic_isolated_scope_preparation_only_never_application_installation_or_target" {
			t.Fatal("scope result invalid", string(output))
		}
		if initialScope.ScopeID == "" {
			initialScope = result
		} else if initialScope.ScopeID != result.ScopeID {
			t.Fatal("identity changed across CLI restart")
		}
		records = append(records, map[string]any{"operation": operation, "result": result})
	}
	scopeJournalPath := filepath.Join(c.ScopeRoot, ".scope.journal.jsonl")
	scopeBefore, e := os.ReadFile(scopeJournalPath)
	if e != nil {
		t.Fatal(e)
	}
	output = run(operatorBinary, "init-scope", operatorConfig, environment, false)
	if !bytes.Contains(output, []byte("OFFLINE_SCOPE_EXISTING_ROOT_PRESERVED")) {
		t.Fatal("repeat scope initialization not refused", string(output))
	}
	scopeAfter, _ := os.ReadFile(scopeJournalPath)
	if !bytes.Equal(scopeBefore, scopeAfter) {
		t.Fatal("scope repeat modified journal")
	}
	foreign := c
	foreign.ScopeRoot = filepath.Join(root, "unknown-existing-scope")
	os.Mkdir(foreign.ScopeRoot, 0700)
	unknown := filepath.Join(foreign.ScopeRoot, "synthetic-customer-file")
	put(unknown, []byte("Preserve explicitly synthetic existing data"))
	put(operatorConfig, encode(foreign))
	output = run(operatorBinary, "init-scope", operatorConfig, environment, false)
	if !bytes.Contains(output, []byte("OFFLINE_SCOPE_EXISTING_ROOT_PRESERVED")) {
		t.Fatal("unknown scope not refused", string(output))
	}
	preserved, e := os.ReadFile(unknown)
	if e != nil || string(preserved) != "Preserve explicitly synthetic existing data" {
		t.Fatal("unknown target data changed")
	}
	put(operatorConfig, encode(c))
	put(environment, []byte("PROJECT_ONE_OFFLINE_OPERATOR_ALLOW_TEST_FIXTURE=false\n"))
	output = run(operatorBinary, "inspect", operatorConfig, environment, false)
	if !bytes.Contains(output, []byte("RELEASE_ARCHIVE_MANIFEST_INVALID")) {
		t.Fatal("explicit false ignored", string(output))
	}
	evidence := os.Getenv("PROJECT_ONE_TEST_OPERATOR_EVIDENCE")
	f, e := os.OpenFile(evidence, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal("new private evidence required", e)
	}
	defer f.Close()
	raw := encode(map[string]any{"status": "passed_actual_operator_CLI_inspect_unpack_verify_scope_init_status_restart_and_preservation", "qualification": "Synthetic isolated scope/file preparation only, never a full release/installation/target claim", "operations": records, "archive_sha256": built.ArchiveSHA256, "manifest_sha256": built.ManifestSHA256, "payloads_verified": len(m.Assets), "existing_prepared_scope_preserved": true, "existing_scope_preserved": true, "unknown_scope_preserved": true, "fixture_false_refused": true})
	if _, e = f.Write(raw); e != nil || f.Sync() != nil {
		t.Fatal("evidence durability failed", e)
	}
}
