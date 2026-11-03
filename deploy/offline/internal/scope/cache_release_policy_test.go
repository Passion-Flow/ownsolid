package scope

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

func selectedPolicyRoleFiles(role string, policy view.Configuration, profile string) []engine.ConfigurationFile {
	files := policyRoleFiles(role, policy)
	if profile == wire.CumulativeCacheCPUProfile {
		for i := range files {
			if files[i].Name == "runtime.yaml" || files[i].Name == "control.yaml" {
				files[i].Contents = []byte(strings.Replace(string(files[i].Contents), `"application_profile":"releases"`, `"application_profile":"`+wire.CumulativeCacheApplicationProfile+`"`, 1))
			}
		}
	}
	return files
}

// These are exact policy/identity fixtures, not an installed cache or SQL restore.
func TestCacheCPUBackupAndSameProfileRestoreKeepRecordedReleaseAuthority(t *testing.T) {
	for _, profile := range []string{wire.CumulativeCPUProfile, wire.CumulativeCacheCPUProfile} {
		t.Run(profile, func(t *testing.T) {
			old := view.Configuration{Directory: view.ContainerDirectory, ScopeID: strings.Repeat("a", 32), ManifestSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), PublisherUID: 0, PageSize: 7, OperationSeconds: 2}
			original := &locked{marker: Marker{Profile: profile, ScopeID: old.ScopeID, ManifestSHA256: old.ManifestSHA256, ConfigurationSHA256: old.ConfigurationSHA256, OwnerUID: old.PublisherUID}}
			source := BackupPlan{Profile: profile, ScopeID: old.ScopeID, ConfigurationSHA256: old.ConfigurationSHA256, Jobs: map[string]BackupJob{"models": {Store: &engine.DatabaseTask{UID: 10001}}}}
			files := map[string][]engine.ConfigurationFile{"control-config": selectedPolicyRoleFiles("control-config", old, profile), "operator-config": selectedPolicyRoleFiles("operator-config", old, profile)}
			if e := original.bindBackupReleasePolicy(&source, files); e != nil || !reflect.DeepEqual(source.ReleaseViewBinding, &old) {
				t.Fatal("backup lost actual original recorded policy", e)
			}
			marker := Marker{Profile: profile, ScopeID: strings.Repeat("d", 32), ManifestSHA256: strings.Repeat("e", 64), ConfigurationSHA256: strings.Repeat("f", 64), OwnerUID: 1}
			current := old
			current.ScopeID, current.ManifestSHA256, current.ConfigurationSHA256, current.PublisherUID = marker.ScopeID, marker.ManifestSHA256, marker.ConfigurationSHA256, marker.OwnerUID
			target := preparedInputs{descriptor: InputsDescriptor{ScopeID: marker.ScopeID, ConfigurationSHA256: marker.ConfigurationSHA256}, files: map[string][]engine.ConfigurationFile{"control-config": selectedPolicyRoleFiles("control-config", current, profile), "operator-config": selectedPolicyRoleFiles("operator-config", current, profile)}}
			model := compose.Model{Services: map[string]compose.Service{"control": {User: "10001:10001"}}}
			keys, e := restoredReleasePolicyFiles(target, model, source, files, []Marker{marker})
			if e != nil || !reflect.DeepEqual(keys, map[string]bool{"control-config/admin-view.json": true, "operator-config/admin-view.json": true}) {
				t.Fatal("same-profile restore did not bind only new recorded policy", keys, e)
			}
			other := wire.CumulativeCPUProfile
			if profile == other {
				other = wire.CumulativeCacheCPUProfile
			}
			wrong := marker
			wrong.Profile = other
			if _, e = restoredReleasePolicyFiles(target, model, source, files, []Marker{wrong}); e == nil {
				t.Fatal("ordinary restore silently performed a profile upgrade")
			}
			missing := source
			missing.ReleaseViewBinding = nil
			if _, e = sourceReleasePolicy(missing, files); e == nil {
				t.Fatal("recorded policy omitted from cache/release backup")
			}
			badOriginal := *original
			badOriginal.marker.Profile = other
			if e = badOriginal.bindBackupReleasePolicy(&source, files); e == nil {
				t.Fatal("backup profile differs from locked installation")
			}
		})
	}
}

func TestCacheCPUPolicyCannotAdoptM17RuntimeOrUnsupportedViewProfiles(t *testing.T) {
	policy := view.Configuration{Directory: view.ContainerDirectory, ScopeID: strings.Repeat("a", 32), ManifestSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), PublisherUID: 1, PageSize: 7, OperationSeconds: 2}
	files := selectedPolicyRoleFiles("control-config", policy, wire.CumulativeCacheCPUProfile)
	if _, _, e := releasePolicyPayloadForProfile(files, "runtime.yaml", wire.CumulativeCacheCPUProfile); e != nil {
		t.Fatal(e)
	}
	for _, profile := range []string{wire.CumulativeCPUProfile, wire.CumulativePredecessorCPUProfile, wire.CumulativeKubernetesCPUProfile, wire.PlatformProfile, wire.FixtureProfile} {
		if _, _, e := releasePolicyPayloadForProfile(files, "runtime.yaml", profile); e == nil {
			t.Fatal("new cache runtime relabeled under another signed profile", profile)
		}
	}
	if _, _, e := releasePolicyPayloadForProfile(policyRoleFiles("control-config", policy), "runtime.yaml", wire.CumulativeCacheCPUProfile); e == nil {
		t.Fatal("M17 runtime relabeled as cache runtime")
	}
	for _, profile := range []string{wire.CumulativePredecessorCPUProfile, wire.CumulativeKubernetesCPUProfile, wire.FixtureProfile} {
		if _, e := sourceReleasePolicy(BackupPlan{Profile: profile, ReleaseViewBinding: &policy}, nil); e == nil {
			t.Fatal("unsupported profile gained a recorded release policy", profile)
		}
	}
}

func TestCacheCPUStoreSelectionKeepsFourOwnersAndExactRuntime(t *testing.T) {
	raw := strings.Replace(string(cumulativeStoreConfiguration()), `"releases"`, `"`+wire.CumulativeCacheApplicationProfile+`"`, 1)
	files := []engine.ConfigurationFile{{Name: "runtime.yaml", Contents: []byte(raw)}}
	if e := currentSelectedStoreProfile(files, wire.CumulativeCacheCPUProfile); e != nil {
		t.Fatal(e)
	}
	if e := currentSelectedStoreProfile(files, wire.CumulativeCPUProfile); e == nil {
		t.Fatal("old M17 store selector silently accepted cache runtime")
	}
	if e := currentSelectedStoreProfile([]engine.ConfigurationFile{{Name: "runtime.yaml", Contents: cumulativeStoreConfiguration()}}, wire.CumulativeCacheCPUProfile); e == nil {
		t.Fatal("cache store selector accepted old runtime")
	}
	l, stores, current, model := cumulativeStoreTaskFixture()
	l.marker.Profile = wire.CumulativeCacheCPUProfile
	tasks, e := assembleCurrentStoreTasks(l, stores, current, model)
	if e != nil || len(tasks) != 4 {
		t.Fatal("cache graph lost retained store owners", len(tasks), e)
	}
}
