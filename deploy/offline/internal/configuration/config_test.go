package configuration

import (
	"bytes"
	"encoding/json"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"strings"
	"testing"
)

func TestExplicitOperatorConfigurationAndFalse(t *testing.T) {
	raw := []byte(`{"expected_archive_sha256":"` + strings.Repeat("a", 64) + `","expected_manifest_sha256":"` + strings.Repeat("b", 64) + `","verification":{"allow_test_fixture":true}}`)
	t.Setenv("PROJECT_ONE_OFFLINE_OPERATOR_ALLOW_TEST_FIXTURE", "true")
	c, e := Load(raw, []byte("PROJECT_ONE_OFFLINE_OPERATOR_ALLOW_TEST_FIXTURE=false\nPROJECT_ONE_OFFLINE_OPERATOR_TIMEOUT_SECONDS=17\nPROJECT_ONE_OFFLINE_OPERATOR_MAXIMUM_JOURNAL_BYTES=131072\nPROJECT_ONE_OFFLINE_OPERATOR_MOUNT_ROOT=/test-explicit-reference\n"))
	if e != nil || c.Verification.AllowTestFixture || c.TimeoutSeconds != 17 || c.MaximumJournalBytes != 131072 || c.Verification.MountRoot != "/test-explicit-reference" {
		t.Fatal(c, e)
	}
	for name, environment := range map[string]string{"unknown": "PRIVATE_KEY_FILE=forbidden", "private-key": "PROJECT_ONE_OFFLINE_OPERATOR_PRIVATE_KEY_FILE=forbidden", "duplicate": "PROJECT_ONE_OFFLINE_OPERATOR_TIMEOUT_SECONDS=1\nPROJECT_ONE_OFFLINE_OPERATOR_TIMEOUT_SECONDS=2", "empty": "PROJECT_ONE_OFFLINE_OPERATOR_TIMEOUT_SECONDS=", "boolean": "PROJECT_ONE_OFFLINE_OPERATOR_ALLOW_TEST_FIXTURE=1", "shell": "PROJECT_ONE_OFFLINE_OPERATOR_BUNDLE_FILE=$(pwd)", "range": "PROJECT_ONE_OFFLINE_OPERATOR_TIMEOUT_SECONDS=0", "journal-low": "PROJECT_ONE_OFFLINE_OPERATOR_MAXIMUM_JOURNAL_BYTES=16383", "journal-high": "PROJECT_ONE_OFFLINE_OPERATOR_MAXIMUM_JOURNAL_BYTES=16777217", "scope-identity-override": "PROJECT_ONE_OFFLINE_OPERATOR_SCOPE_ID=forbidden"} {
		t.Run(name, func(t *testing.T) {
			if _, e := Load(raw, []byte(environment)); e == nil {
				t.Fatal("invalid env accepted")
			}
		})
	}
	for name, raw := range map[string]string{"missing": "{}", "null": "{\"verification\":null}", "duplicate": "{\"bundle_file\":\"a\",\"bundle_file\":\"b\"}", "unknown": "{\"private_key_file\":\"forbidden\"}", "nested-unknown": "{\"verification\":{\"private_key_file\":\"forbidden\"}}"} {
		t.Run(name, func(t *testing.T) {
			if _, e := Load([]byte(raw), nil); e == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestOptionalEngineDefaultsAndLegacyScopeFingerprint(t *testing.T) {
	c := Defaults()
	c.ExpectedArchiveSHA256 = strings.Repeat("a", 64)
	c.ExpectedManifestSHA256 = strings.Repeat("b", 64)
	c.ScopeRoot = "/explicit-test-scope"
	legacy := struct {
		Verification           bundle.Verification `json:"verification"`
		BundleFile             string              `json:"bundle_file"`
		ExpectedArchiveSHA256  string              `json:"expected_archive_sha256"`
		ExpectedManifestSHA256 string              `json:"expected_manifest_sha256"`
		DestinationRoot        string              `json:"destination_root"`
		TimeoutSeconds         int                 `json:"timeout_seconds"`
		ScopeRoot              string              `json:"scope_root,omitempty"`
		MaximumJournalBytes    int64               `json:"maximum_journal_bytes"`
	}{c.Verification, c.BundleFile, c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256, c.DestinationRoot, c.TimeoutSeconds, c.ScopeRoot, c.MaximumJournalBytes}
	raw, _ := json.Marshal(legacy)
	if c.Engine != nil || c.Fingerprint() != wire.Digest(raw) {
		t.Fatal("inactive engine changed old effective configuration")
	}
	base := []byte(`{"expected_archive_sha256":"` + strings.Repeat("a", 64) + `","expected_manifest_sha256":"` + strings.Repeat("b", 64) + `"}`)
	env := []byte("PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_SOCKET_FILE=/explicit-test-engine.sock\nPROJECT_ONE_OFFLINE_OPERATOR_ENGINE_PROFILE=local-macos-amd64-emulation\nPROJECT_ONE_OFFLINE_OPERATOR_ENGINE_DIAL_SECONDS=1\nPROJECT_ONE_OFFLINE_OPERATOR_ENGINE_REQUEST_SECONDS=5\nPROJECT_ONE_OFFLINE_OPERATOR_ENGINE_LOAD_SECONDS=20\nPROJECT_ONE_OFFLINE_OPERATOR_ENGINE_MAXIMUM_EXPANDED_IMAGE_BYTES=67108864\n")
	configured, e := Load(base, env)
	if e != nil || configured.Engine == nil || configured.Engine.LoadSeconds != 20 || configured.Engine.MaximumExpandedImageBytes != 67108864 {
		t.Fatal(configured, e)
	}
	for _, invalid := range []string{"PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_PROFILE=unknown\n", "PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_DIAL_SECONDS=31\n", "PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_RESPONSE_BYTES=4095\n", "PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_MAXIMUM_EXPANDED_IMAGE_BYTES=1048575\n", "PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_RETRY=true\n"} {
		if _, e := Load(base, []byte(invalid)); e == nil {
			t.Fatal("incomplete or unsafe optional engine group admitted")
		}
	}
	for _, invalid := range []string{`{"engine":null}`, `{"engine":{"private_key_file":"forbidden"}}`} {
		if _, e := Load([]byte(invalid), nil); e == nil {
			t.Fatal("unknown/null engine group accepted")
		}
	}
}

func TestOptionalComposeNondefaultEnvStrictBoundsAndLegacyEngineFingerprint(t *testing.T) {
	c := Defaults()
	c.ExpectedArchiveSHA256 = strings.Repeat("a", 64)
	c.ExpectedManifestSHA256 = strings.Repeat("b", 64)
	c.Engine = &Engine{SocketFile: "/explicit/test/socket", Profile: "local-macos-amd64-emulation", DialSeconds: 2, RequestSeconds: 5, LoadSeconds: 30, ResponseBytes: 4096, MaximumExpandedImageBytes: 1048576}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), `"compose"`) || c.Fingerprint() != wire.Digest(raw) {
		t.Fatal("absent compose altered old engine fingerprint")
	}
	env := "PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_DOCKER_BINARY=/explicit/test/docker\nPROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_PLUGIN_DIRECTORY=/explicit/test/plugins\n"
	loaded, e := Load(raw, []byte(env+"PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_COMMAND_SECONDS=7\nPROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_OUTPUT_BYTES=8192\nPROJECT_ONE_OFFLINE_OPERATOR_ALLOW_TEST_FIXTURE=false\n"))
	if e != nil || loaded.Compose == nil || loaded.Compose.CommandSeconds != 7 || loaded.Compose.OutputBytes != 8192 || loaded.Verification.AllowTestFixture {
		t.Fatal(loaded, e)
	}
	if configured, e := Load(raw, []byte(env+"PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_MODEL_FILE=/explicit/test/model.json\n")); e != nil || configured.Compose.ModelFile != "/explicit/test/model.json" {
		t.Fatal("explicit model reference override failed", e)
	}
	if _, e := Load(raw, []byte(env+"PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_MODEL_FILE=relative/model.json\n")); e == nil {
		t.Fatal("relative model reference admitted")
	}
	encoded, _ := json.Marshal(loaded.Compose)
	if bytes.Contains(encoded, []byte("model_file")) {
		t.Fatal("absent model reference changed prior compose serialization")
	}
	for _, invalid := range []string{"PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_COMMAND_SECONDS=1\n", "PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_OUTPUT_BYTES=4095\n", "PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_COMMAND_SECONDS=false\n", "PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_RETRY=true\n"} {
		if _, e = Load(raw, []byte(env+invalid)); e == nil {
			t.Fatal("unsafe compose env accepted")
		}
	}
	for _, invalid := range []string{`"compose":null`, `"compose":{"docker_binary":"/explicit/test/docker","plugin_directory":"/explicit/test/plugins","secret":"forbidden"}`} {
		input := bytes.TrimSuffix(raw, []byte("}"))
		input = append(input, []byte(","+invalid+"}")...)
		if _, e = Load(input, nil); e == nil {
			t.Fatal("unsafe compose JSON accepted")
		}
	}
	c.Engine = nil
	raw, _ = json.Marshal(c)
	if _, e = Load(raw, []byte(env)); e == nil {
		t.Fatal("compose admitted without explicit engine")
	}
}

func TestOptionalUtilityResourcesAreExplicitBoundedAndPreserveLegacyEngineBytes(t *testing.T) {
	c := Defaults()
	c.ExpectedArchiveSHA256 = strings.Repeat("a", 64)
	c.ExpectedManifestSHA256 = strings.Repeat("b", 64)
	c.Engine = &Engine{SocketFile: "/explicit/test/socket", Profile: "local-macos-amd64-emulation", DialSeconds: 2, RequestSeconds: 5, LoadSeconds: 30, ResponseBytes: 65536, MaximumExpandedImageBytes: 67108864}
	raw, _ := json.Marshal(c)
	if bytes.Contains(raw, []byte(`"utility"`)) {
		t.Fatal("inactive utility altered legacy JSON")
	}
	env := "PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_UTILITY_MEMORY_BYTES=25165824\nPROJECT_ONE_OFFLINE_OPERATOR_ENGINE_UTILITY_MILLI_CPU=50\nPROJECT_ONE_OFFLINE_OPERATOR_ENGINE_UTILITY_PIDS=12\nPROJECT_ONE_OFFLINE_OPERATOR_ALLOW_TEST_FIXTURE=false\n"
	configured, e := Load(raw, []byte(env))
	if e != nil || configured.Engine.Utility == nil || configured.Engine.Utility.MemoryBytes != 25165824 || configured.Engine.Utility.MilliCPU != 50 || configured.Engine.Utility.PIDs != 12 || configured.Verification.AllowTestFixture {
		t.Fatal(configured, e)
	}
	for _, invalid := range []string{"PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_UTILITY_MEMORY_BYTES=1\n", "PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_UTILITY_MILLI_CPU=1001\n", "PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_UTILITY_PIDS=false\n", "PROJECT_ONE_OFFLINE_OPERATOR_ENGINE_UTILITY_START=true\n"} {
		if _, e := Load(raw, []byte(invalid)); e == nil {
			t.Fatal("unsafe utility env accepted")
		}
	}
	for _, invalid := range []string{`{"utility":null}`, `{"utility":{"start":true}}`} {
		var base map[string]json.RawMessage
		json.Unmarshal(raw, &base)
		var engineFields map[string]json.RawMessage
		json.Unmarshal(base["engine"], &engineFields)
		var changes map[string]json.RawMessage
		json.Unmarshal([]byte(invalid), &changes)
		for k, v := range changes {
			engineFields[k] = v
		}
		base["engine"], _ = json.Marshal(engineFields)
		input, _ := json.Marshal(base)
		if _, e := Load(input, nil); e == nil {
			t.Fatal("unsafe utility JSON accepted")
		}
	}
}

func TestOptionalProcessPolicyPrecedenceBoundsAndLegacyFingerprint(t *testing.T) {
	c := Defaults()
	c.ExpectedArchiveSHA256 = strings.Repeat("a", 64)
	c.ExpectedManifestSHA256 = strings.Repeat("b", 64)
	c.Engine = &Engine{SocketFile: "/explicit/socket", Profile: "local-macos-amd64-emulation", DialSeconds: 2, RequestSeconds: 5, LoadSeconds: 30, ResponseBytes: 8192, MaximumExpandedImageBytes: 1048576}
	c.Compose = &Compose{DockerBinary: "/explicit/docker", PluginDirectory: "/explicit/plugins", CommandSeconds: 30, OutputBytes: 8192}
	raw, _ := json.Marshal(c)
	legacy := c.Fingerprint()
	loaded, e := Load(raw, nil)
	if e != nil || loaded.Compose.Process != nil || loaded.Fingerprint() != legacy {
		t.Fatal("legacy group changed", e)
	}
	env := "PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_PROCESS_STOP_SECONDS=3\nPROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_PROCESS_OBSERVE_SECONDS=2\nPROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_PROCESS_POLL_MILLISECONDS=100\n"
	loaded, e = Load(raw, []byte(env))
	if e != nil || loaded.Compose.Process == nil || loaded.Compose.Process.StopSeconds != 3 || loaded.Compose.Process.ObserveSeconds != 2 || loaded.Compose.Process.PollMilliseconds != 100 {
		t.Fatal(loaded.Compose, e)
	}
	c.Compose.Process = &wire.ProcessSettings{StopSeconds: 5, ObserveSeconds: 4, PollMilliseconds: 250}
	raw, _ = json.Marshal(c)
	loaded, e = Load(raw, []byte(env))
	if e != nil || loaded.Compose.Process.StopSeconds != 3 {
		t.Fatal("env precedence", e)
	}
	nondefault, e := Load(raw, []byte("PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_PROCESS_RESERVE_PERCENT=40\n"))
	if e != nil || nondefault.Compose.Process.ReservePercent != 40 {
		t.Fatal("reserve policy override", e)
	}
	for _, invalid := range []string{"RESERVE_PERCENT=29", "RESERVE_PERCENT=91", "RESERVE_PERCENT=false", "STOP_SECONDS=0", "STOP_SECONDS=121", "STOP_SECONDS=30", "STOP_SECONDS=false", "OBSERVE_SECONDS=0", "OBSERVE_SECONDS=121", "POLL_MILLISECONDS=49", "POLL_MILLISECONDS=5001", "POLL_MILLISECONDS=5000", "UNKNOWN=true"} {
		t.Run(invalid, func(t *testing.T) {
			if _, e := Load(raw, []byte("PROJECT_ONE_OFFLINE_OPERATOR_COMPOSE_PROCESS_"+invalid+"\n")); e == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}
