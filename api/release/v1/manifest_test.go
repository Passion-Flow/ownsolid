package release

import (
	"strings"
	"testing"
)

func TestStrictFirstPartyJSON(t *testing.T) {
	for name, raw := range map[string]string{"duplicate": "{\"format_version\":1,\"format_version\":1}", "nested": "{\"assets\":[{\"id\":\"x\",\"id\":\"y\"}]}", "unknown": "{\"success\":true}", "nonfinite": "{\"format_version\":NaN}", "trailing": "{} {}", "null": "{\"profile\":null}", "type": "{\"format_version\":\"1\"}"} {
		t.Run(name, func(t *testing.T) {
			var m Manifest
			if StrictJSON([]byte(raw), &m) == nil {
				t.Fatal("invalid JSON admitted")
			}
		})
	}
	var third map[string]any
	if UniqueJSON([]byte(`{"config":{"Cmd":null},"extension":true}`), &third) != nil {
		t.Fatal("valid nullable OCI rejected")
	}
	if UniqueJSON([]byte(`{"config":{"Cmd":null,"Cmd":[]}}`), &third) == nil {
		t.Fatal("duplicate OCI accepted")
	}
}
func fixtureManifest() Manifest {
	return Manifest{FormatVersion: 1, ReleaseID: "synthetic-1", Profile: FixtureProfile, SourceSHA256: strings.Repeat("a", 64), SchemaVersion: 16, MinimumPredecessor: 15, MaximumPredecessor: 16, Capabilities: []string{"M01"}, Prerequisites: []string{}, Assets: []Asset{{ID: "license", Path: "licenses/test.txt", Kind: "license", Size: 1, SHA256: strings.Repeat("b", 64), Licenses: []string{}}, {ID: "tool", Path: "tools/fixture", Kind: "operator", Size: 2, SHA256: strings.Repeat("c", 64), Licenses: []string{"license"}}}}
}
func TestInventoryAndProfileGuards(t *testing.T) {
	good := fixtureManifest()
	if good.Validate(10, 100, 1000, true) != nil {
		t.Fatal("fixture rejected")
	}
	if good.Validate(10, 100, 1000, false) == nil {
		t.Fatal("fixture flag bypass")
	}
	changes := map[string]func(*Manifest){"required-platform-capabilities": func(m *Manifest) { m.Profile = PlatformProfile }, "unknown-profile": func(m *Manifest) { m.Profile = "generic" }, "duplicate-id": func(m *Manifest) { m.Assets[1].ID = "license" }, "duplicate-path": func(m *Manifest) { m.Assets[1].Path = m.Assets[0].Path }, "traversal": func(m *Manifest) { m.Assets[1].Path = "../fixture" }, "absolute": func(m *Manifest) { m.Assets[1].Path = "/fixture" }, "reserved": func(m *Manifest) { m.Assets[1].Path = "manifest.json" }, "reserved-parent": func(m *Manifest) { m.Assets[1].Path = "manifest.json/child" }, "reserved-signature-parent": func(m *Manifest) { m.Assets[1].Path = "signature.json/child" }, "windows-path": func(m *Manifest) { m.Assets[1].Path = `c:\fixture` }, "digest": func(m *Manifest) { m.Assets[1].SHA256 = "a" }, "license-missing": func(m *Manifest) { m.Assets[1].Licenses = []string{} }, "license-wrong-kind": func(m *Manifest) { m.Assets[1].Licenses = []string{"tool"} }, "license-duplicate": func(m *Manifest) { m.Assets[1].Licenses = []string{"license", "license"} }, "license-chain": func(m *Manifest) { m.Assets[0].Licenses = []string{"tool"} }, "image-id": func(m *Manifest) { m.Assets[1].Kind = "image_archive" }, "nonimage-identity": func(m *Manifest) { m.Assets[1].ImageID = "x" }, "size": func(m *Manifest) { m.Assets[1].Size = 101 }, "predecessor": func(m *Manifest) { m.MaximumPredecessor = 17 }, "capability-duplicate": func(m *Manifest) { m.Capabilities = []string{"M01", "M01"} }}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			m := fixtureManifest()
			change(&m)
			if m.Validate(10, 100, 1000, true) == nil {
				t.Fatal("unsafe inventory accepted")
			}
		})
	}
	if good.Validate(1, 100, 1000, true) == nil || good.Validate(10, 100, 2, true) == nil {
		t.Fatal("count/total bound bypass")
	}
}
