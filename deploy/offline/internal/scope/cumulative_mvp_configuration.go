package scope

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This catalog supplies existing protected payloads, never inline secrets, users
// or mounts. The Control CLI remains the authority for each capability's values.
type cumulativeMVPFile struct {
	Name       string `json:"name"`
	SourceFile string `json:"source_file"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
}
type cumulativeMVPCatalog struct {
	RegistryReleaseMountTarget string                         `json:"registry_release_mount_target"`
	RegistryMountTarget        string                         `json:"registry_mount_target"`
	FormatVersion              int                            `json:"format_version"`
	Roles                      map[string][]cumulativeMVPFile `json:"roles"`
}

func (l *locked) cumulativeMVPFiles(p CumulativeUpgradePlan) (map[string][]engine.ConfigurationFile, error) {
	out := map[string][]engine.ConfigurationFile{}
	bad := failure("OFFLINE_SCOPE_MVP_CONFIGURATION_INVALID")
	if p.Target.Profile != wire.MVPPostgreSQLCPUProfile {
		if p.Request.MVPConfigurationFiles != "" || p.MVPConfigurationSHA256 != "" || p.RegistryMountTarget != "" || p.RegistryReleaseMountTarget != "" || p.RegistryReleaseMetadataSHA256 != "" || p.RegistryReleaseProfile != nil {
			return nil, bad
		}
		return out, nil
	}
	if p.Request.MVPConfigurationFiles == "" || !wire.ValidSHA256(p.MVPConfigurationSHA256) {
		return nil, bad
	}
	raw, e := protectedInput(l.config, p.Request.MVPConfigurationFiles, l.config.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(raw) != p.MVPConfigurationSHA256 {
		return nil, bad
	}
	var c cumulativeMVPCatalog
	if wire.StrictJSON(raw, &c) != nil || c.FormatVersion != 1 || len(c.Roles) != 2 || c.RegistryReleaseMountTarget != p.RegistryReleaseMountTarget || c.RegistryMountTarget != p.RegistryMountTarget || !path.IsAbs(c.RegistryMountTarget) || path.Clean(c.RegistryMountTarget) != c.RegistryMountTarget || c.RegistryMountTarget == "/" || strings.ContainsAny(c.RegistryMountTarget, "\\\x00\r\n") {
		return nil, bad
	}
	// Reject case aliases as well as unknown fields, before reading any source.
	var fields map[string]json.RawMessage
	if wire.StrictJSON(raw, &fields) != nil || len(fields) != 4 || fields["format_version"] == nil || fields["roles"] == nil || fields["registry_mount_target"] == nil || fields["registry_release_mount_target"] == nil {
		return nil, bad
	}
	count, total := 0, int64(0)
	for _, role := range []string{"control-config", "operator-config"} {
		refs, ok := c.Roles[role]
		if l.config.Engine == nil || l.config.Engine.ResponseBytes < 1 {
			return nil, bad
		}
		roleBytes := int64(0)
		if !ok || len(refs) == 0 {
			return nil, bad
		}
		var rawRoles map[string][]json.RawMessage
		if wire.StrictJSON(fields["roles"], &rawRoles) != nil {
			return nil, bad
		}
		names := map[string]bool{}
		for i, f := range refs {
			var keys map[string]json.RawMessage
			if wire.StrictJSON(rawRoles[role][i], &keys) != nil || len(keys) != 4 || keys["name"] == nil || keys["source_file"] == nil || keys["bytes"] == nil || keys["sha256"] == nil {
				return nil, bad
			}
			count++
			total += f.Bytes
			roleBytes += f.Bytes
			// The existing SDK configuration wire bound also limits allocations
			// before private files are loaded; signed-asset limits are much larger.
			if roleBytes > l.config.Engine.ResponseBytes {
				return nil, bad
			}
			if count > l.config.Verification.MaximumAssets || f.Bytes < 1 || f.Bytes > l.config.Verification.MaximumMetadataBytes || total > l.config.Verification.MaximumTotalBytes || !wire.SafePath(f.Name) || path.Base(f.Name) != f.Name || names[f.Name] || !wire.ValidSHA256(f.SHA256) {
				return nil, bad
			}
			contents, e := protectedInput(l.config, f.SourceFile, f.Bytes)
			if e != nil || int64(len(contents)) != f.Bytes || wire.Digest(contents) != f.SHA256 {
				return nil, bad
			}
			names[f.Name] = true
			out[role] = append(out[role], engine.ConfigurationFile{Name: f.Name, Contents: contents})
		}
		if !names["mvp-capabilities.json"] {
			return nil, bad
		}
	}
	return out, nil
}

func cumulativeMVPCapabilities(raw []byte, extra, original []engine.ConfigurationFile, root string, model compose.Model) ([]byte, error) {
	bad := failure("OFFLINE_SCOPE_MVP_CAPABILITY_CLOSURE_INVALID")
	overlay, e := restoreNamedFile(extra, "mvp-capabilities.json")
	if e != nil {
		return nil, bad
	}
	var values, added map[string]json.RawMessage
	if wire.StrictJSON(raw, &values) != nil || wire.StrictJSON(overlay, &added) != nil || len(added) == 0 {
		return nil, bad
	}
	references := map[string]bool{"mongodb_config_file": true, "image_registry_config_file": true, "middleware_config_file": true, "middleware_environment_file": true, "private_cluster_config_file": true, "monitoring_config_file": true}
	objects := map[string]bool{"model_cache": true, "aws": true, "task_events": true, "object_storage": true}
	files := map[string][]byte{}
	for _, list := range [][]engine.ConfigurationFile{original, extra} {
		for _, f := range list {
			if _, ok := files[f.Name]; ok && !(root == engine.DatabaseTaskDestination && f.Name == configuration.NativePostgreSQLRecoveryDescriptorFile) {
				return nil, bad
			}
			files[f.Name] = f.Contents
		}
	}
	used := map[string]bool{"mvp-capabilities.json": true}
	if root == engine.DatabaseTaskDestination {
		descriptor, ok := files[configuration.NativePostgreSQLRecoveryDescriptorFile]
		if !ok || validateMVPNativeRegistryDescriptor(raw, descriptor, model) != nil {
			return nil, bad
		}
		used[configuration.NativePostgreSQLRecoveryDescriptorFile] = true
	}
	inspected := map[string]bool{}
	registryTarget, e := compose.MVPRegistryTarget(model)
	if e != nil {
		return nil, bad
	}
	releaseTarget, e := compose.MVPRegistryReleaseTarget(model)
	if e != nil {
		return nil, bad
	}
	var inspect func(any, string, string) error
	inspect = func(value any, purpose, parent string) error {
		switch object := value.(type) {
		case map[string]any:
			for key, v := range object {
				// New data paths require this exact independently declared data owner.
				// Never infer data ownership from a mount's existence or old role name.
				if strings.HasSuffix(key, "_root") || strings.HasSuffix(key, "_directory") {
					name, ok := v.(string)
					if !ok || purpose != "image_registry_config_file" || !(key == "store_root" && parent == "" && name == path.Join(registryTarget, "private") || key == "prepared_release_root" && parent == "" && name == path.Join(releaseTarget, "private") || key == "mount_root" && parent == "verification" && name == releaseTarget) {
						return bad
					}
					continue
				}
				if name, ok := v.(string); ok && strings.HasPrefix(name, "/") && (strings.HasSuffix(key, "_file") || strings.HasSuffix(key, "_root") || strings.HasSuffix(key, "_directory")) {
					if path.Clean(name) != name || strings.ContainsAny(name, "\\\x00\r\n") {
						return bad
					}
					prefix := path.Join(root, "private") + "/"
					if strings.HasPrefix(name, prefix) {
						base := strings.TrimPrefix(name, prefix)
						data, ok := files[base]
						if !ok || path.Base(base) != base {
							return bad
						}
						if !inspected[purpose+":"+base] {
							inspected[purpose+":"+base] = true
							used[base] = true
							if strings.HasSuffix(base, ".json") {
								var nested any
								if wire.StrictJSON(data, &nested) != nil {
									return bad
								}
								if e := inspect(nested, purpose, ""); e != nil {
									return e
								}
							}
						}
					} else {
						return bad
					}
				}
				if e := inspect(v, purpose, key); e != nil {
					return e
				}
			}
		case []any:
			for _, v := range object {
				if e := inspect(v, purpose, parent); e != nil {
					return e
				}
			}
		}
		return nil
	}
	for key, value := range added {
		if references[key] {
			var name string
			if json.Unmarshal(value, &name) != nil || name == "" || !strings.HasPrefix(name, path.Join(root, "private")+"/") {
				return nil, bad
			}
		}
		if objects[key] {
			var object map[string]json.RawMessage
			var enabled bool
			if wire.StrictJSON(value, &object) != nil || object["enabled"] == nil || json.Unmarshal(object["enabled"], &enabled) != nil {
				return nil, bad
			}
		}
		if !references[key] && !objects[key] {
			return nil, bad
		}
		values[key] = value
	}
	for _, key := range []string{"middleware_config_file", "private_cluster_config_file", "monitoring_config_file", "image_registry_config_file"} {
		if added[key] == nil {
			return nil, bad
		}
	}
	var tree map[string]any
	if wire.StrictJSON(overlay, &tree) != nil {
		return nil, bad
	}
	for purpose, value := range tree {
		if inspect(map[string]any{purpose: value}, purpose, "") != nil {
			return nil, bad
		}
	}
	for _, f := range extra {
		if !used[f.Name] {
			return nil, bad
		}
	}
	target, e := compose.MVPRegistryTarget(model)
	if e != nil {
		return nil, bad
	}
	var ref string
	if json.Unmarshal(added["image_registry_config_file"], &ref) != nil {
		return nil, bad
	}
	var registry struct {
		StoreRoot string `json:"store_root"`
	}
	if wire.UniqueJSON(files[path.Base(ref)], &registry) != nil || registry.StoreRoot != path.Join(target, "private") {
		return nil, bad
	}
	return encode(values), nil
}

// Existing operator-only authority must never be copied into the serving role.
func cumulativeMVPSecretIsolation(extra, original map[string][]engine.ConfigurationFile) bool {
	public := map[string]bool{}
	for _, f := range original["control-config"] {
		public[wire.Digest(f.Contents)] = true
	}
	operator := map[string]bool{}
	for _, f := range original["operator-config"] {
		operator[wire.Digest(f.Contents)] = true
	}
	for _, f := range extra["control-config"] {
		h := wire.Digest(f.Contents)
		if operator[h] && !public[h] {
			return false
		}
	}
	return true
}
