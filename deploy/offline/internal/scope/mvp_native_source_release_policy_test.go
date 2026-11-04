package scope

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

func TestMVPNativeReleasePolicyPayloadReusesOriginalParserAndLegacySelection(t *testing.T) {
	policy := view.Configuration{Directory: view.ContainerDirectory, ScopeID: strings.Repeat("b", 32), ManifestSHA256: strings.Repeat("a", 64), ConfigurationSHA256: strings.Repeat("c", 64), PublisherUID: 1234, PageSize: 47, OperationSeconds: 7}
	filesFor := func(application string) []engine.ConfigurationFile {
		return []engine.ConfigurationFile{{Name: "runtime.yaml", Contents: encode(map[string]any{"server": map[string]any{"application_profile": application}, "release_view_config_file": "/run/projectone/control/private/view.json"})}, {Name: "view.json", Contents: encode(policy)}}
	}
	for _, selection := range []struct{ profile, application string }{{wire.MVPPostgreSQLCPUProfile, ""}, {wire.CumulativeCPUProfile, "releases"}, {wire.CumulativeCacheCPUProfile, wire.CumulativeCacheApplicationProfile}} {
		name, parsed, e := releasePolicyPayloadForProfile(filesFor(selection.application), "runtime.yaml", selection.profile)
		if e != nil || name != "view.json" || !reflect.DeepEqual(parsed, policy) {
			t.Fatal("original nondefault policy changed", selection.profile, e)
		}
	}
	if hasRecordedReleasePolicy(wire.MVPPostgreSQLCPUProfile) || !hasRecordedReleasePolicy(wire.CumulativeCPUProfile) || !hasRecordedReleasePolicy(wire.CumulativeCacheCPUProfile) {
		t.Fatal("legacy BackupPlan selection broadened")
	}
	for _, fault := range []string{"legacy-header", "alias", "unknown-policy", "missing-policy-field", "null-policy", "false-seconds"} {
		t.Run(fault, func(t *testing.T) {
			files := filesFor("")
			switch fault {
			case "legacy-header":
				files = filesFor("releases")
			case "alias":
				files[0].Contents = encode(map[string]any{"server": map[string]any{}, "release_view_config_file": "/run/projectone/control/private/view.json", "other_file": "/different/private/view.json"})
			case "null-policy":
				files[1].Contents = []byte("null\n")
			default:
				object := map[string]any{"directory": policy.Directory, "scope_id": policy.ScopeID, "manifest_sha256": policy.ManifestSHA256, "configuration_sha256": policy.ConfigurationSHA256, "publisher_uid": policy.PublisherUID, "page_size": policy.PageSize, "operation_seconds": policy.OperationSeconds}
				if fault == "unknown-policy" {
					object["foreign"] = true
				}
				if fault == "missing-policy-field" {
					delete(object, "publisher_uid")
				}
				if fault == "false-seconds" {
					object["operation_seconds"] = false
				}
				files[1].Contents = encode(object)
			}
			if _, _, e := releasePolicyPayloadForProfile(files, "runtime.yaml", wire.MVPPostgreSQLCPUProfile); e == nil {
				t.Fatal("invalid MVP policy accepted", fault)
			}
		})
	}
}

func mvpNativeReleasePolicyFixture() (mvpNativeBackupPlan, map[string][]engine.ConfigurationFile) {
	p := mvpNativeSourcePlanFixture()
	files := map[string][]engine.ConfigurationFile{}
	for _, role := range []string{"control-config", "operator-config"} {
		envelope := encode(map[string]any{"server": map[string]any{}, "release_view_config_file": "/run/projectone/" + role + "/private/view.json"})
		files[role] = []engine.ConfigurationFile{{Name: "runtime.yaml", Contents: envelope}, {Name: "view.json", Contents: encode(*p.ReleaseViewBinding)}}
		if role == "operator-config" {
			files[role] = append(files[role], engine.ConfigurationFile{Name: "control.yaml", Contents: envelope})
		}
	}
	p.ConfigurationFiles = nil
	for role, items := range files {
		for _, f := range items {
			p.ConfigurationFiles = append(p.ConfigurationFiles, BackupConfigurationFile{Role: role, File: InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)}})
		}
	}
	return p, files
}

func TestMVPNativeSourceReleasePolicyBindsActualOriginalPublisherAndThreeFiles(t *testing.T) {
	p, files := mvpNativeReleasePolicyFixture()
	keys, policy, e := mvpNativeSourceReleasePolicy(p, files)
	if e != nil || len(keys) != 2 || !keys["control-config/view.json"] || !keys["operator-config/view.json"] || !reflect.DeepEqual(policy, *p.ReleaseViewBinding) {
		t.Fatal("actual original source policy not retained", e)
	}
	l := &locked{marker: Marker{Profile: p.Profile, ScopeID: p.ScopeID, ManifestSHA256: p.ReleaseManifestSHA256, ConfigurationSHA256: p.ConfigurationSHA256, OwnerUID: policy.PublisherUID}}
	p.ReleaseViewBinding = nil
	if e := l.bindMVPNativeBackupReleasePolicy(&p, files); e != nil {
		t.Fatal(e)
	}
	l.marker.OwnerUID++
	if e := l.bindMVPNativeBackupReleasePolicy(&p, files); e == nil {
		t.Fatal("guessed publisher UID accepted")
	}
	for _, fault := range []string{"missing-binding", "source-scope", "source-manifest", "source-configuration", "reader-as-publisher", "invalid-reader", "operator-drift", "missing-original-file", "unretained-original", "changed-retained-hash"} {
		t.Run(fault, func(t *testing.T) {
			p, files := mvpNativeReleasePolicyFixture()
			switch fault {
			case "missing-binding":
				p.ReleaseViewBinding = nil
			case "source-scope":
				p.ReleaseViewBinding.ScopeID = strings.Repeat("d", 32)
			case "source-manifest":
				p.ReleaseViewBinding.ManifestSHA256 = strings.Repeat("d", 64)
			case "source-configuration":
				p.ReleaseViewBinding.ConfigurationSHA256 = strings.Repeat("d", 64)
			case "reader-as-publisher":
				p.ReleaseViewBinding.PublisherUID = p.NativeUID
			case "invalid-reader":
				p.NativeUID = -1
			case "operator-drift":
				changed := *p.ReleaseViewBinding
				changed.PageSize++
				files["operator-config"][1].Contents = encode(changed)
			case "missing-original-file":
				files["operator-config"] = files["operator-config"][:2]
			case "unretained-original":
				p.ConfigurationFiles = nil
			case "changed-retained-hash":
				p.ConfigurationFiles[0].File.SHA256 = strings.Repeat("d", 64)
			}
			if _, _, e := mvpNativeSourceReleasePolicy(p, files); e == nil {
				t.Fatal("invalid original source binding accepted", fault)
			}
		})
	}
}

func TestMVPNativeSourceReleasePolicyReadonlyMissingBytesNeverRepair(t *testing.T) {
	p, files := mvpNativeReleasePolicyFixture()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e := root.Mkdir("configuration", 0700); e != nil {
		t.Fatal(e)
	}
	for role, items := range files {
		if e := root.Mkdir("configuration/"+role, 0700); e != nil {
			t.Fatal(e)
		}
		for _, item := range items {
			if e := writeBackupFile(root, "configuration/"+role+"/"+item.Name, item.Contents); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := readMVPNativeReleasePolicy(root, configuration.Defaults(), p); e != nil {
		t.Fatal(e)
	}
	if e := root.Remove("configuration/operator-config/view.json"); e != nil {
		t.Fatal(e)
	}
	if e := readMVPNativeReleasePolicy(root, configuration.Defaults(), p); e == nil {
		t.Fatal("missing original policy accepted")
	}
	if _, e := root.Lstat("configuration/operator-config/view.json"); !os.IsNotExist(e) {
		t.Fatal("readonly policy reader repaired missing bytes")
	}
}
