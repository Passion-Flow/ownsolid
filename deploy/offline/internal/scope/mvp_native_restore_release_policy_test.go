package scope

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
)

func mvpNativeRestorePolicyFixture() (*locked, preparedInputs, compose.Model, mvpNativeBackupPlan, view.Configuration, map[string]bool) {
	source := mvpNativeSourcePlanFixture()
	original := *source.ReleaseViewBinding
	m := Marker{ScopeID: strings.Repeat("d", 32), Profile: wire.MVPPostgreSQLCPUProfile, ReleaseID: source.ReleaseID, ManifestSHA256: source.ReleaseManifestSHA256, ConfigurationSHA256: strings.Repeat("f", 64), OwnerUID: 8}
	l := &locked{marker: m, config: configuration.Config{ScopeRoot: "/synthetic/target-scope"}}
	current := original
	current.ScopeID = m.ScopeID
	current.ConfigurationSHA256 = m.ConfigurationSHA256
	current.PublisherUID = m.OwnerUID
	files := func(role string) []engine.ConfigurationFile {
		out := policyRoleFiles(role, current)
		for i := range out {
			out[i].Contents = bytes.ReplaceAll(out[i].Contents, []byte(`"application_profile":"releases"`), []byte(`"application_profile":""`))
		}
		return out
	}
	p := preparedInputs{descriptor: InputsDescriptor{ScopeID: m.ScopeID, ConfigurationSHA256: m.ConfigurationSHA256, Roles: []InputRole{{Key: "control-config", UID: 10001, GID: 10002}, {Key: "operator-config", UID: 10001, GID: 10002}}}, files: map[string][]engine.ConfigurationFile{"control-config": files("control-config"), "operator-config": files("operator-config")}}
	mvpNativeFreezePolicyFixture(&p)
	model := compose.Model{Services: map[string]compose.Service{"control": {User: "10001:10002", Mounts: []compose.Mount{{Type: "bind", Source: filepath.Join(l.config.ScopeRoot, view.RelativeDirectory), Target: view.ContainerDirectory, ReadOnly: true}}}}}
	return l, p, model, source, original, map[string]bool{"control-config/admin-view.json": true, "operator-config/admin-view.json": true}
}

func mvpNativeFreezePolicyFixture(p *preparedInputs) {
	for i := range p.descriptor.Roles {
		role := &p.descriptor.Roles[i]
		role.Files = nil
		for _, file := range p.files[role.Key] {
			role.Files = append(role.Files, InputFile{Name: file.Name, Bytes: int64(len(file.Contents)), SHA256: wire.Digest(file.Contents)})
		}
	}
}

func TestMVPNativeRestoreTargetPolicyBindsAlreadyFrozenActualScopeAndReader(t *testing.T) {
	l, p, model, source, original, names := mvpNativeRestorePolicyFixture()
	before := encode(p.files)
	keys, e := l.mvpNativeRestoreTargetReleasePolicy(p, model, source, original, names)
	if e != nil || !reflect.DeepEqual(keys, names) || !bytes.Equal(before, encode(p.files)) {
		t.Fatal("actual frozen policy binding failed or wrote input", e)
	}
	if keys["control-config/auth-hmac"] || keys["operator-config/native-postgres.json"] {
		t.Fatal("unrelated authority file exempted")
	}
	keys["control-config/added"] = true
	if names["control-config/added"] {
		t.Fatal("returned key set aliases source admission")
	}
}

func TestMVPNativeRestoreTargetPolicyRefusesChangedScopePolicyReaderAndMount(t *testing.T) {
	for _, name := range []string{"source-scope", "source-config", "source-manifest", "target-scope", "target-config", "publisher", "reader", "reader-group", "missing-role", "duplicate-role", "profile", "policy-page", "policy-seconds", "policy-missing", "policy-null", "policy-alias", "names", "writable", "missing-mount", "duplicate-mount", "foreign-service", "ancestor", "wrong-target", "envelope-not-frozen", "policy-not-frozen"} {
		t.Run(name, func(t *testing.T) {
			l, p, model, source, original, names := mvpNativeRestorePolicyFixture()
			service := model.Services["control"]
			changePolicy := func(field string, value any) {
				for role, files := range p.files {
					for i, f := range files {
						if f.Name == "admin-view.json" {
							var fields map[string]any
							if wire.StrictJSON(f.Contents, &fields) != nil {
								t.Fatal("fixture")
							}
							fields[field] = value
							files[i].Contents = encode(fields)
						}
					}
					p.files[role] = files
				}
				// This represents a bad policy that was genuinely frozen as input;
				// its semantic rejection must not rely on a stale fixture digest.
				mvpNativeFreezePolicyFixture(&p)
			}
			switch name {
			case "source-scope":
				original.ScopeID = strings.Repeat("e", 32)
			case "source-config":
				original.ConfigurationSHA256 = strings.Repeat("e", 64)
			case "source-manifest":
				original.ManifestSHA256 = strings.Repeat("e", 64)
			case "target-scope":
				p.descriptor.ScopeID = source.ScopeID
			case "target-config":
				p.descriptor.ConfigurationSHA256 = source.ConfigurationSHA256
			case "publisher":
				changePolicy("publisher_uid", 9)
			case "reader":
				service.User = "8:10002"
			case "reader-group":
				p.descriptor.Roles[0].GID = 10003
			case "missing-role":
				p.descriptor.Roles = p.descriptor.Roles[:1]
			case "duplicate-role":
				p.descriptor.Roles = append(p.descriptor.Roles, p.descriptor.Roles[0])
			case "profile":
				l.marker.Profile = wire.CumulativeCPUProfile
			case "policy-page":
				changePolicy("page_size", 8)
			case "policy-seconds":
				changePolicy("operation_seconds", 3)
			case "policy-missing":
				p.files["control-config"] = p.files["control-config"][:1]
			case "policy-null":
				p.files["control-config"][1].Contents = []byte("null\n")
			case "policy-alias":
				p.files["control-config"][0].Contents = bytes.Replace(p.files["control-config"][0].Contents, []byte(`"server":`), []byte(`"security":{"auth_hmac_key_file":"/run/projectone/control/private/admin-view.json"},"server":`), 1)
				mvpNativeFreezePolicyFixture(&p)
			case "envelope-not-frozen":
				p.files["control-config"][0].Contents = append([]byte(" \n"), p.files["control-config"][0].Contents...)
			case "policy-not-frozen":
				p.files["control-config"][1].Contents = append([]byte(" \n"), p.files["control-config"][1].Contents...)
			case "names":
				delete(names, "control-config/admin-view.json")
				names["control-config/auth-hmac"] = true
			case "writable":
				service.Mounts[0].ReadOnly = false
			case "missing-mount":
				service.Mounts = nil
			case "duplicate-mount":
				service.Mounts = append(service.Mounts, service.Mounts[0])
			case "foreign-service":
				model.Services["gateway"] = service
				delete(model.Services, "control")
			case "ancestor":
				service.Mounts[0].Source = l.config.ScopeRoot
			case "wrong-target":
				service.Mounts[0].Target = "/synthetic/wrong-view"
			}
			if name != "foreign-service" {
				model.Services["control"] = service
			}
			if keys, e := l.mvpNativeRestoreTargetReleasePolicy(p, model, source, original, names); e == nil || keys != nil {
				t.Fatal("changed target policy admitted")
			}
		})
	}
}
