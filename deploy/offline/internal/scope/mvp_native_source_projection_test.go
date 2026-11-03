package scope

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func mvpNativeProjectionFixture() (preparedInputs, compose.Model, configuration.NativePostgreSQLRecoveryDescriptor) {
	r := mvpNativeSourcePlanFixture().NativeRequest
	d := configuration.NativePostgreSQLRecoveryDescriptor{FormatVersion: 1, BackupTemplate: r, StoreRoots: [4]string{"/stores/models/private", "/stores/datasets/private", "/stores/training/private", "/stores/preparations/private"}, RegistryRoot: "/artifacts/cas", PreparedReleaseRoot: "/artifacts/prepared", RegistryScaffoldRoot: "/artifacts"}
	opRoot := engine.DatabaseTaskDestination + "/private"
	controlRoot := "/run/projectone/control/private"
	localeRoot := "/run/projectone/locale/private"
	configurationFor := func(base, dsn string) []byte {
		return encode(map[string]any{"server": map[string]any{"application_profile": wire.MVPPostgreSQLCPUApplicationProfile}, "database": map[string]any{"dsn_file": dsn}, "security": map[string]any{"auth_hmac_key_file": base + "/auth-key", "mail_keyring_file": base + "/mail-keyring"}, "models": map[string]any{"artifact_root": d.StoreRoots[0]}, "datasets": map[string]any{"artifact_root": d.StoreRoots[1]}, "training": map[string]any{"artifact_root": d.StoreRoots[2]}, "model_preparation": map[string]any{"artifact_root": d.StoreRoots[3]}, "localization": map[string]any{"package_directory": localeRoot}, "image_registry_config_file": base + "/registry.json", "adjustable": map[string]any{"enabled": false, "large_integer": json.Number("9223372036854775806")}})
	}
	rtDSN := []byte("postgresql://runtime:" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{43}, 32)) + "@postgres:5432/source_db?sslmode=disable\n")
	opDSN := []byte("postgresql://operator:operator-secret@postgres:5432/source_db?sslmode=disable\n")
	filesFor := func(base string) []engine.ConfigurationFile {
		return []engine.ConfigurationFile{{Name: "auth-key", Contents: []byte("secret HMAC bytes unchanged")}, {Name: "mail-keyring", Contents: []byte("keyring bytes unchanged")}, {Name: "registry.json", Contents: encode(map[string]any{"store_root": d.RegistryRoot, "prepared_release_root": d.PreparedReleaseRoot, "ca_file": base + "/ca.pem", "credential_file": base + "/registry-credential", "verification": map[string]any{"trust_file": base + "/registry-trust", "mount_root": d.RegistryScaffoldRoot}})}, {Name: "ca.pem", Contents: []byte("synthetic CA")}, {Name: "registry-credential", Contents: []byte("synthetic registry secret")}, {Name: "registry-trust", Contents: []byte("synthetic signing trust")}}
	}
	p := preparedInputs{files: map[string][]engine.ConfigurationFile{"operator-config": filesFor(opRoot), "control-config": filesFor(controlRoot), "postgres-config": {{Name: "password", Contents: []byte("operator-secret\n")}}, localizationConfigurationRole: {{Name: "inventory.json", Contents: []byte("locale inventory exact\n")}, {Name: "en.json", Contents: []byte("locale payload exact\n")}}}}
	p.files["operator-config"] = append(p.files["operator-config"], engine.ConfigurationFile{Name: "control.yaml", Contents: configurationFor(opRoot, opRoot+"/operator-dsn")}, engine.ConfigurationFile{Name: "runtime.yaml", Contents: configurationFor(opRoot, opRoot+"/runtime-dsn")}, engine.ConfigurationFile{Name: "operator-dsn", Contents: opDSN}, engine.ConfigurationFile{Name: "runtime-dsn", Contents: rtDSN})
	p.files["control-config"] = append(p.files["control-config"], engine.ConfigurationFile{Name: "runtime.yaml", Contents: configurationFor(controlRoot, controlRoot+"/runtime-dsn")}, engine.ConfigurationFile{Name: "runtime-dsn", Contents: rtDSN})
	model := compose.Model{Services: map[string]compose.Service{"control": {Mounts: []compose.Mount{{Type: "volume", Source: "control-config", Target: "/run/projectone/control", ReadOnly: true}, {Type: "volume", Source: localizationConfigurationRole, Target: "/run/projectone/locale", ReadOnly: true}}}, "postgres": {Environment: map[string]string{"POSTGRES_DB": "source_db", "POSTGRES_USER": "operator", "POSTGRES_PASSWORD_FILE": "/run/postgres/private/password"}, Mounts: []compose.Mount{{Type: "volume", Source: "postgres-config", Target: "/run/postgres", ReadOnly: true}}}}}
	return p, model, d
}

func TestMVPNativeJSONProjectionKeepsUnknownValuesAndRefusesCredentialAliases(t *testing.T) {
	raw := []byte(`{"database":{"dsn_file":"/original/runtime-dsn"},"large":9223372036854775806,"enabled":false,"unknown":["unchanged","/original/key"]}`)
	transformed, e := projectMVPNativeJSON(raw, map[string]string{"/original/key": "/projection/key"}, map[string]bool{"/original/runtime-dsn": true}, "/operator/runtime-dsn")
	if e != nil || !bytes.Contains(transformed, []byte("9223372036854775806")) || !bytes.Contains(transformed, []byte(`"enabled":false`)) || !bytes.Contains(transformed, []byte("/projection/key")) {
		t.Fatal(string(transformed), e)
	}
	for _, bad := range []string{`{"database":{"dsn_file":"/original/runtime-dsn"},"alias":"/original/runtime-dsn"}`, `{"database":{"dsn_file":"/foreign"}}`, `database: {dsn_file: /original/runtime-dsn}`, `{"x":1,"x":1}`} {
		if _, e := projectMVPNativeJSON([]byte(bad), nil, map[string]bool{"/original/runtime-dsn": true}, "/operator/runtime-dsn"); e == nil {
			t.Fatal("unsafe JSON/reference admitted")
		}
	}
}

func TestMVPNativeConfigurationProjectionPreservesKeysLocalesAndOriginalInputs(t *testing.T) {
	p, model, d := mvpNativeProjectionFixture()
	// Native nested registry references differ between the same source role roots;
	// they normalize to the same flat projection, not bytewise source-file aliases.
	// Use the original shared nested configuration path for both prepared configs.
	for i, f := range p.files["operator-config"] {
		if f.Name == "registry.json" {
			for _, c := range p.files["control-config"] {
				if c.Name == "registry.json" {
					p.files["operator-config"][i].Contents = append([]byte(nil), c.Contents...)
				}
			}
		}
	}
	before := map[string][]byte{}
	for role, files := range p.files {
		for _, f := range files {
			before[role+"/"+f.Name] = append([]byte(nil), f.Contents...)
		}
	}
	out, e := projectMVPNativeConfiguration(p, model, d)
	if e != nil {
		t.Fatal(e)
	}
	for role, files := range p.files {
		for _, f := range files {
			if !bytes.Equal(before[role+"/"+f.Name], f.Contents) {
				t.Fatal("original input changed")
			}
		}
	}
	if !wire.ValidSHA256(out.OperatorSHA256) || !wire.ValidSHA256(out.ConfigurationSHA256) || out.OperatorSHA256 == out.ConfigurationSHA256 {
		t.Fatal("payload fingerprints")
	}
	for _, f := range out.ConfigurationFiles {
		if bytes.Contains(f.Contents, []byte("postgresql://")) || f.Name == "operator-dsn" || f.Name == "runtime-dsn" {
			t.Fatal("DSN entered archived projection")
		}
	}
	for _, name := range []string{"auth-key", "mail-keyring", "inventory.json", "en.json"} {
		raw, e := restoreNamedFile(out.ConfigurationFiles, name)
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, original := range before {
			found = found || bytes.Equal(original, raw)
		}
		if !found {
			t.Fatal("keyring/locale bytes changed")
		}
	}
	var cfg map[string]json.RawMessage
	raw, _ := restoreNamedFile(out.ConfigurationFiles, "runtime.yaml")
	if wire.UniqueJSON(raw, &cfg) != nil || !bytes.Contains(raw, []byte(d.BackupTemplate.ConfigurationRoot+"/auth-key")) {
		t.Fatal("reference projection")
	}
	serialized, _ := json.Marshal(out)
	if strings.Contains(string(serialized), "secret") || strings.Contains(string(serialized), "postgresql") || strings.Contains(string(serialized), "Contents") {
		t.Fatal("private payload serialized")
	}
}

func TestMVPNativeConfigurationProjectionRejectsConflictsMissingClosureAndChangedRuntime(t *testing.T) {
	for _, fault := range []string{"flat-conflict", "source-root", "registry-root", "locale-mount", "credential-alias", "missing-key", "runtime-drift"} {
		t.Run(fault, func(t *testing.T) {
			p, model, d := mvpNativeProjectionFixture()
			// Normalize the ordinary shared config input for all controls except the
			// deliberate conflicting nested registry source file.
			for i, f := range p.files["operator-config"] {
				if f.Name == "registry.json" {
					for _, c := range p.files["control-config"] {
						if c.Name == "registry.json" {
							p.files["operator-config"][i].Contents = append([]byte(nil), c.Contents...)
						}
					}
				}
			}
			switch fault {
			case "flat-conflict":
				p.files[localizationConfigurationRole] = append(p.files[localizationConfigurationRole], engine.ConfigurationFile{Name: "auth-key", Contents: []byte("different owner bytes")})
			case "source-root":
				d.StoreRoots[0] = "/foreign/models"
			case "registry-root":
				d.RegistryRoot = "/artifacts/foreign"
			case "locale-mount":
				svc := model.Services["control"]
				svc.Mounts = svc.Mounts[:1]
				model.Services["control"] = svc
			case "credential-alias":
				for i, f := range p.files["control-config"] {
					if f.Name == "runtime.yaml" {
						var obj map[string]any
						json.Unmarshal(f.Contents, &obj)
						obj["alias"] = "/run/projectone/control/private/runtime-dsn"
						p.files["control-config"][i].Contents = encode(obj)
					}
				}
			case "missing-key":
				for i, f := range p.files["control-config"] {
					if f.Name == "registry.json" {
						p.files["control-config"] = append(p.files["control-config"][:i], p.files["control-config"][i+1:]...)
						break
					}
				}
				for i, f := range p.files["operator-config"] {
					if f.Name == "registry.json" {
						p.files["operator-config"] = append(p.files["operator-config"][:i], p.files["operator-config"][i+1:]...)
						break
					}
				}
			case "runtime-drift":
				for i, f := range p.files["operator-config"] {
					if f.Name == "runtime.yaml" {
						var obj map[string]any
						json.Unmarshal(f.Contents, &obj)
						obj["changed"] = true
						p.files["operator-config"][i].Contents = encode(obj)
					}
				}
			}
			if _, e := projectMVPNativeConfiguration(p, model, d); e == nil {
				t.Fatal("incomplete/changed projection admitted", fault, reflect.TypeOf(p))
			}
		})
	}
}

func TestMVPNativeConfigurationProjectionKeepsOriginalRegistryManifestPolicy(t *testing.T) {
	for _, limit := range []int64{1024, 2 << 20, 4 << 20, 0, (4 << 20) + 1} {
		p, model, d := mvpNativeProjectionFixture()
		for _, role := range []string{"control-config", "operator-config"} {
			for i, file := range p.files[role] {
				if file.Name != "registry.json" {
					continue
				}
				var object map[string]any
				if e := json.Unmarshal(file.Contents, &object); e != nil {
					t.Fatal(e)
				}
				object["verification"].(map[string]any)["maximum_manifest_bytes"] = limit
				p.files[role][i].Contents = encode(object)
			}
		}
		projection, e := projectMVPNativeConfiguration(p, model, d)
		if limit < 1024 || limit > 4<<20 {
			if e == nil {
				t.Fatal("invalid original registry limit accepted", limit)
			}
		} else if e != nil || projection.RegistryManifestMaximumBytes != limit {
			t.Fatal("original nondefault registry limit replaced", limit, e)
		}
	}
}
