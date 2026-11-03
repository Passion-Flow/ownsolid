package scope

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

func policyRoleFiles(role string, policy view.Configuration) []engine.ConfigurationFile {
	root := "/run/projectone/control"
	if role == "operator-config" {
		root = engine.DatabaseTaskDestination
	}
	raw := []byte(`{"server":{"application_profile":"releases"},"release_view_config_file":"` + root + `/private/admin-view.json"}`)
	out := []engine.ConfigurationFile{{Name: "runtime.yaml", Contents: raw}, {Name: "admin-view.json", Contents: encode(policy)}}
	if role == "operator-config" {
		out = append(out, engine.ConfigurationFile{Name: "control.yaml", Contents: raw})
	}
	return out
}
func TestRestoreReleasePolicyRebindsOnlyActualNewScopeAndPreservesReaderPolicy(t *testing.T) {
	old := view.Configuration{Directory: view.ContainerDirectory, ScopeID: strings.Repeat("a", 32), ManifestSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), PublisherUID: 0, PageSize: 7, OperationSeconds: 2}
	source := BackupPlan{Profile: wire.CumulativeCPUProfile, ScopeID: old.ScopeID, ConfigurationSHA256: old.ConfigurationSHA256, ReleaseViewBinding: &old, Jobs: map[string]BackupJob{"models": {Store: &engine.DatabaseTask{UID: 10001}}}}
	sourceFiles := map[string][]engine.ConfigurationFile{"control-config": policyRoleFiles("control-config", old), "operator-config": policyRoleFiles("operator-config", old)}
	marker := Marker{ScopeID: strings.Repeat("d", 32), Profile: wire.CumulativeCPUProfile, ManifestSHA256: strings.Repeat("e", 64), ConfigurationSHA256: strings.Repeat("f", 64), OwnerUID: 1}
	current := old
	current.ScopeID = marker.ScopeID
	current.ManifestSHA256 = marker.ManifestSHA256
	current.ConfigurationSHA256 = marker.ConfigurationSHA256
	current.PublisherUID = marker.OwnerUID
	target := preparedInputs{descriptor: InputsDescriptor{ScopeID: marker.ScopeID, ConfigurationSHA256: marker.ConfigurationSHA256}, files: map[string][]engine.ConfigurationFile{"control-config": policyRoleFiles("control-config", current), "operator-config": policyRoleFiles("operator-config", current)}}
	model := compose.Model{Services: map[string]compose.Service{"control": {User: "10001:10001"}}}
	keys, e := restoredReleasePolicyFiles(target, model, source, sourceFiles, []Marker{marker})
	if e != nil || !reflect.DeepEqual(keys, map[string]bool{"control-config/admin-view.json": true, "operator-config/admin-view.json": true}) {
		t.Fatal(keys, e)
	}
	if _, e = restoredReleasePolicyFiles(target, model, source, sourceFiles, nil); e == nil {
		t.Fatal("target marker omitted")
	}
	for _, change := range []func(*view.Configuration){func(v *view.Configuration) { v.ScopeID = old.ScopeID }, func(v *view.Configuration) { v.ManifestSHA256 = old.ManifestSHA256 }, func(v *view.Configuration) { v.ConfigurationSHA256 = old.ConfigurationSHA256 }, func(v *view.Configuration) { v.PublisherUID = 10001 }, func(v *view.Configuration) { v.PageSize++ }, func(v *view.Configuration) { v.OperationSeconds++ }, func(v *view.Configuration) { v.Directory = "/foreign" }} {
		bad := current
		change(&bad)
		copy := target
		copy.files = map[string][]engine.ConfigurationFile{"control-config": policyRoleFiles("control-config", bad), "operator-config": policyRoleFiles("operator-config", bad)}
		if _, e = restoredReleasePolicyFiles(copy, model, source, sourceFiles, []Marker{marker}); e == nil {
			t.Fatal("wrong scope/custody/policy admitted")
		}
	}
	aliasFiles := policyRoleFiles("control-config", current)
	aliasFiles[0].Contents = []byte(strings.Replace(string(aliasFiles[0].Contents), `"server":`, `"security":{"auth_hmac_key_file":"/run/projectone/control/private/admin-view.json"},"server":`, 1))
	if _, _, e := releasePolicyPayload(aliasFiles, "runtime.yaml"); e == nil {
		t.Fatal("policy alias exempted a private authority reference")
	}
	raw, _ := json.Marshal(BackupPlan{})
	if strings.Contains(string(raw), "release_view_binding") {
		t.Fatal("historical plan encoding changed")
	}
	legacy := BackupPlan{Profile: wire.FixtureProfile}
	if keys, e = sourceReleasePolicy(legacy, nil); e != nil || len(keys) != 0 {
		t.Fatal("legacy plan changed")
	}
	legacy.ReleaseViewBinding = &old
	if _, e = sourceReleasePolicy(legacy, sourceFiles); e == nil {
		t.Fatal("legacy profile acquired new policy authority")
	}
	source.ReleaseViewBinding = &current
	if _, e = sourceReleasePolicy(source, sourceFiles); e == nil {
		t.Fatal("source declared binding ignored")
	}
}
