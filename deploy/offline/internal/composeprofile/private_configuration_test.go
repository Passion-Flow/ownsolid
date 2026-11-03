package composeprofile

import (
	"encoding/json"
	"strings"
	"testing"
)

func privateProfileFixture() PrivateConfiguration {
	return PrivateConfiguration{FormatVersion: 1, ScopeID: strings.Repeat("a", 32), OperationID: strings.Repeat("b", 32), VolumeName: "projectone-" + strings.Repeat("a", 32) + "-control-config", SourceRoot: "/input", TargetRoot: "/target", SourceUID: 0, SourceGID: 0, UID: 10001, GID: 10002, MaximumFileBytes: 1024, MaximumTotalBytes: 4096, TimeoutSeconds: 30, Files: []PrivateConfigurationFile{{Name: "control.yaml", Bytes: 8, SHA256: strings.Repeat("c", 64)}}}
}

func TestComposePrivateConfigurationStrictIntent(t *testing.T) {
	p := privateProfileFixture()
	raw, _ := json.Marshal(p)
	if _, e := LoadPrivateConfiguration(raw, nil); e != nil {
		t.Fatal(e)
	}
	for _, fault := range []string{"null-owner", "unknown", "duplicate", "wrong-volume", "null-files", "unordered", "nested", "empty-hash", "over-budget", "source-target-overlap"} {
		t.Run(fault, func(t *testing.T) {
			var value map[string]any
			json.Unmarshal(raw, &value)
			switch fault {
			case "null-owner":
				value["source_uid"] = nil
			case "unknown":
				value["enabled"] = false
			case "wrong-volume":
				value["volume_name"] = "foreign"
			case "null-files":
				value["files"] = nil
			case "unordered":
				value["files"] = []PrivateConfigurationFile{p.Files[0], p.Files[0]}
			case "nested":
				value["files"] = []PrivateConfigurationFile{{Name: "../control.yaml", Bytes: 8, SHA256: p.Files[0].SHA256}}
			case "empty-hash":
				value["files"] = []PrivateConfigurationFile{{Name: "control.yaml", Bytes: 8}}
			case "over-budget":
				value["maximum_total_bytes"] = 1
			case "source-target-overlap":
				value["source_root"] = "/target/inside"
			}
			input, _ := json.Marshal(value)
			if fault == "duplicate" {
				input = []byte(strings.Replace(string(input), `"source_uid":0`, `"source_uid":0,"source_uid":0`, 1))
			}
			if _, e := LoadPrivateConfiguration(input, nil); e == nil {
				t.Fatal("accepted invalid intent")
			}
		})
	}
}

func TestComposePrivateConfigurationExplicitEnvironment(t *testing.T) {
	raw, _ := json.Marshal(privateProfileFixture())
	p, e := LoadPrivateConfiguration(raw, []byte("PROJECT_ONE_PRIVATE_SOURCE_UID=501\nPROJECT_ONE_PRIVATE_SOURCE_GID=20\nPROJECT_ONE_PRIVATE_UID=10003\nPROJECT_ONE_PRIVATE_TARGET_ROOT=\"/native target\"\nPROJECT_ONE_PRIVATE_TIMEOUT_SECONDS=2\n"))
	if e != nil || p.SourceUID != 501 || p.SourceGID != 20 || p.UID != 10003 || p.TargetRoot != "/native target" || p.TimeoutSeconds != 2 {
		t.Fatal(p, e)
	}
	for _, overlay := range []string{"PROJECT_ONE_PRIVATE_UID=false", "PROJECT_ONE_PRIVATE_UID=01", "PROJECT_ONE_PRIVATE_UID=0", "PROJECT_ONE_PRIVATE_UID=1\nPROJECT_ONE_PRIVATE_UID=2", "PROJECT_ONE_PRIVATE_SCOPE_ID=other", "PROJECT_ONE_PRIVATE_TARGET_ROOT=/input/inside", "PROJECT_ONE_PRIVATE_TIMEOUT_SECONDS=0", "PROJECT_ONE_PRIVATE_MAXIMUM_TOTAL_BYTES=1"} {
		if _, e := LoadPrivateConfiguration(raw, []byte(overlay)); e == nil {
			t.Fatal("accepted", overlay)
		}
	}
}
