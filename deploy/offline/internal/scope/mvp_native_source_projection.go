package scope

import (
	"bytes"
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Only copied native-task payloads are transformed. Original prepared inputs,
// effective Control generations, credentials and signed release bytes stay exact.
type mvpNativeConfigurationProjection struct {
	RegistryManifestMaximumBytes int64                      `json:"-"`
	OperatorFiles                []engine.ConfigurationFile `json:"-"`
	ConfigurationFiles           []engine.ConfigurationFile `json:"-"`
	OperatorSHA256               string                     `json:"operator_sha256"`
	ConfigurationSHA256          string                     `json:"configuration_sha256"`
}

func mvpNativePayloadSHA(files []engine.ConfigurationFile) string {
	index := []InputFile{}
	for _, f := range files {
		index = append(index, InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)})
	}
	sort.Slice(index, func(i, j int) bool { return index[i].Name < index[j].Name })
	return wire.Digest(encode(index))
}

func projectMVPNativeConfiguration(p preparedInputs, model compose.Model, d configuration.NativePostgreSQLRecoveryDescriptor) (mvpNativeConfigurationProjection, error) {
	var out mvpNativeConfigurationProjection
	bad := failure("OFFLINE_SCOPE_NATIVE_CONFIGURATION_PROJECTION_INVALID")
	if d.Validate() != nil {
		return out, bad
	}
	// Reuse the original connection, separate-runtime credential and model guards.
	if _, e := restoreConnections(p, model); e != nil {
		return out, e
	}
	runtime, e := restoreNamedFile(p.files["control-config"], "runtime.yaml")
	if e != nil {
		return out, e
	}
	operator, e := restoreNamedFile(p.files["operator-config"], "control.yaml")
	if e != nil {
		return out, e
	}
	operatorRuntime, e := restoreNamedFile(p.files["operator-config"], "runtime.yaml")
	if e != nil {
		return out, e
	}
	var envelope struct {
		Server struct {
			Profile string `json:"application_profile"`
		} `json:"server"`
		Database struct {
			DSNFile string `json:"dsn_file"`
		} `json:"database"`
		Models struct {
			Root string `json:"artifact_root"`
		} `json:"models"`
		Datasets struct {
			Root string `json:"artifact_root"`
		} `json:"datasets"`
		Training struct {
			Root string `json:"artifact_root"`
		} `json:"training"`
		Preparations struct {
			Root string `json:"artifact_root"`
		} `json:"model_preparation"`
		RegistryFile string `json:"image_registry_config_file"`
	}
	if wire.UniqueJSON(runtime, &envelope) != nil || envelope.Server.Profile != "" || envelope.RegistryFile == "" || [4]string{envelope.Models.Root, envelope.Datasets.Root, envelope.Training.Root, envelope.Preparations.Root} != d.StoreRoots {
		return out, bad
	}
	roots := map[string]string{"operator-config": engine.DatabaseTaskDestination}
	for _, m := range model.Services["control"].Mounts {
		if m.Type == "volume" && (m.Source == "control-config" || m.Source == localizationConfigurationRole) {
			if !m.ReadOnly || roots[m.Source] != "" {
				return out, bad
			}
			roots[m.Source] = m.Target
		}
	}
	if roots["control-config"] == "" || roots[localizationConfigurationRole] == "" {
		return out, bad
	}
	forbidden := map[string]bool{}
	for _, item := range []struct{ role, name string }{{"operator-config", "control.yaml"}, {"operator-config", "runtime.yaml"}, {"control-config", "runtime.yaml"}} {
		env, e := restoreEnvelope(p.files[item.role], item.name)
		if e != nil {
			return out, e
		}
		forbidden[env.Database.DSNFile] = true
	}
	opEnv, e := restoreEnvelope(p.files["operator-config"], "control.yaml")
	if e != nil {
		return out, e
	}
	rtEnv, e := restoreEnvelope(p.files["operator-config"], "runtime.yaml")
	if e != nil {
		return out, e
	}
	opDSN, e := restorePayloadAt(p, model, "operator-config", opEnv.Database.DSNFile)
	if e != nil {
		return out, e
	}
	rtDSN, e := restorePayloadAt(p, model, "operator-config", rtEnv.Database.DSNFile)
	if e != nil {
		return out, e
	}
	inputRoot := engine.DatabaseTaskDestination + "/private"
	mapping := map[string]string{}
	files := map[string][]byte{}
	registryCopies := [][]byte{}
	registryName := path.Base(envelope.RegistryFile)
	for _, role := range []string{"operator-config", "control-config", localizationConfigurationRole} {
		base := roots[role] + "/private"
		if role == localizationConfigurationRole {
			mapping[base] = d.BackupTemplate.ConfigurationRoot
		}
		for _, f := range p.files[role] {
			original := base + "/" + f.Name
			if !wire.SafePath(f.Name) || strings.Contains(f.Name, "/") || len(f.Contents) == 0 {
				return out, bad
			}
			if forbidden[original] {
				continue
			}
			if role == "operator-config" && (f.Name == "control.yaml" || f.Name == "runtime.yaml" || f.Name == configuration.NativePostgreSQLRecoveryDescriptorFile) {
				continue
			}
			if role == "control-config" && f.Name == "runtime.yaml" {
				continue
			}
			if f.Name == "runtime.yaml" || f.Name == "control.yaml" || f.Name == "operator-dsn" || f.Name == "runtime-dsn" || f.Name == "native-request.json" || f.Name == "operator.env" {
				return out, bad
			}
			if f.Name == registryName {
				registryCopies = append(registryCopies, append([]byte(nil), f.Contents...))
			}
			if prior, exists := files[f.Name]; exists && !bytes.Equal(prior, f.Contents) && f.Name != registryName {
				return out, failure("OFFLINE_SCOPE_NATIVE_CONFIGURATION_FLAT_NAME_CONFLICT")
			}
			files[f.Name] = append([]byte(nil), f.Contents...)
			mapping[original] = path.Join(d.BackupTemplate.ConfigurationRoot, f.Name)
		}
	}
	if path.Clean(envelope.RegistryFile) != envelope.RegistryFile || mapping[envelope.RegistryFile] == "" {
		return out, bad
	}
	registryName = path.Base(envelope.RegistryFile)
	var registry struct {
		StoreRoot           string `json:"store_root"`
		PreparedReleaseRoot string `json:"prepared_release_root"`
		Verification        struct {
			MountRoot            string `json:"mount_root"`
			MaximumManifestBytes int64  `json:"maximum_manifest_bytes"`
		} `json:"verification"`
	}
	// The original registry owner seeds this same factory-owned default before
	// applying its verification input. Do not substitute native manifest policy.
	registry.Verification.MaximumManifestBytes = bundle.Defaults().MaximumManifestBytes
	if wire.UniqueJSON(files[registryName], &registry) != nil || registry.StoreRoot != d.RegistryRoot || registry.PreparedReleaseRoot != d.PreparedReleaseRoot || registry.Verification.MountRoot != d.RegistryScaffoldRoot {
		return out, bad
	}
	if registry.Verification.MaximumManifestBytes < 1024 || registry.Verification.MaximumManifestBytes > 4<<20 {
		return out, bad
	}
	out.RegistryManifestMaximumBytes = registry.Verification.MaximumManifestBytes
	transformed, e := projectMVPNativeJSON(files[registryName], mapping, forbidden, "")
	if e != nil {
		return out, e
	}
	for _, original := range registryCopies {
		candidate, e := projectMVPNativeJSON(original, mapping, forbidden, "")
		if e != nil || !bytes.Equal(candidate, transformed) {
			return out, failure("OFFLINE_SCOPE_NATIVE_CONFIGURATION_FLAT_NAME_CONFLICT")
		}
	}
	files[registryName] = transformed
	// Native owner compares these two complete typed configurations. Scope only
	// changes admitted reference paths and the existing separate DSN reference.
	nativeRuntime, e := projectMVPNativeJSON(runtime, mapping, forbidden, inputRoot+"/runtime-dsn")
	if e != nil {
		return out, e
	}
	nativeOperator, e := projectMVPNativeJSON(operator, mapping, forbidden, inputRoot+"/operator-dsn")
	if e != nil {
		return out, e
	}
	nativeOperatorRuntime, e := projectMVPNativeJSON(operatorRuntime, mapping, forbidden, inputRoot+"/runtime-dsn")
	if e != nil {
		return out, e
	}
	if !bytes.Equal(nativeRuntime, nativeOperatorRuntime) {
		return out, failure("OFFLINE_SCOPE_NATIVE_RUNTIME_CONFIGURATION_CHANGED")
	}
	files["runtime.yaml"] = nativeRuntime
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out.ConfigurationFiles = append(out.ConfigurationFiles, engine.ConfigurationFile{Name: name, Contents: files[name]})
	}
	out.OperatorFiles = []engine.ConfigurationFile{{Name: "control.yaml", Contents: nativeOperator}, {Name: "runtime-dsn", Contents: append([]byte(nil), rtDSN...)}, {Name: "operator-dsn", Contents: append([]byte(nil), opDSN...)}}
	out.OperatorSHA256 = mvpNativePayloadSHA(out.OperatorFiles)
	out.ConfigurationSHA256 = mvpNativePayloadSHA(out.ConfigurationFiles)
	return out, nil
}

// JSON-compatible YAML is the original offline Scope input profile. Keep unknown
// settings and scalar bytes; transform only exact admitted references, never a
// prefix search through credential contents, source data or user strings.
func projectMVPNativeJSON(raw []byte, mapping map[string]string, forbidden map[string]bool, dsn string) ([]byte, error) {
	bad := failure("OFFLINE_SCOPE_NATIVE_JSON_CONFIGURATION_REQUIRED")
	var obj map[string]json.RawMessage
	if wire.UniqueJSON(raw, &obj) != nil || obj == nil {
		return nil, bad
	}
	if dsn != "" {
		var database map[string]json.RawMessage
		if wire.UniqueJSON(obj["database"], &database) != nil || database == nil {
			return nil, bad
		}
		var original string
		if json.Unmarshal(database["dsn_file"], &original) != nil || !forbidden[original] {
			return nil, bad
		}
		database["dsn_file"] = json.RawMessage(`""`)
		obj["database"] = encode(database)
	}
	var visit func(json.RawMessage) (json.RawMessage, error)
	visit = func(value json.RawMessage) (json.RawMessage, error) {
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) == 0 {
			return nil, bad
		}
		if trimmed[0] == '"' {
			var s string
			if json.Unmarshal(trimmed, &s) != nil {
				return nil, bad
			}
			if forbidden[s] {
				return nil, failure("OFFLINE_SCOPE_NATIVE_CREDENTIAL_REFERENCE_ALIAS")
			}
			if next, ok := mapping[s]; ok {
				return json.Marshal(next)
			}
			return append(json.RawMessage(nil), value...), nil
		}
		if trimmed[0] == '{' {
			var members map[string]json.RawMessage
			if wire.UniqueJSON(value, &members) != nil {
				return nil, bad
			}
			for k, v := range members {
				next, e := visit(v)
				if e != nil {
					return nil, e
				}
				members[k] = next
			}
			return json.Marshal(members)
		}
		if trimmed[0] == '[' {
			var members []json.RawMessage
			if wire.UniqueJSON(value, &members) != nil {
				return nil, bad
			}
			for i, v := range members {
				next, e := visit(v)
				if e != nil {
					return nil, e
				}
				members[i] = next
			}
			return json.Marshal(members)
		}
		return append(json.RawMessage(nil), value...), nil
	}
	result, e := visit(encode(obj))
	if e != nil {
		return nil, e
	}
	if dsn != "" {
		var resultObject map[string]json.RawMessage
		var database map[string]json.RawMessage
		if wire.UniqueJSON(result, &resultObject) != nil || wire.UniqueJSON(resultObject["database"], &database) != nil {
			return nil, bad
		}
		database["dsn_file"], _ = json.Marshal(dsn)
		resultObject["database"] = encode(database)
		result, e = json.Marshal(resultObject)
		if e != nil {
			return nil, bad
		}
	}
	return append(result, '\n'), nil
}
