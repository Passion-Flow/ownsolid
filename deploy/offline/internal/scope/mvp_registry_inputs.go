package scope

import (
	"encoding/json"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This exact MVP-only addition never changes the legacy global role maps or
// their catalogue cardinality. Preparation remains the ordinary real SDK path.
func inputProfileTag(profile string) string {
	if profile == wire.MVPPostgreSQLCPUProfile {
		return profile
	}
	return ""
}
func inputRoleCount(profile string) int {
	n := len(roleServices)
	if profile == wire.MVPPostgreSQLCPUProfile {
		n += 2
	}
	return n
}
func inputRoleAllowed(profile, key string) bool {
	_, ok := roleServices[key]
	return ok || profile == wire.MVPPostgreSQLCPUProfile && (key == engine.NativePostgreSQLRegistryRole || key == compose.MVPRegistryReleaseRole)
}
func inputRoleUser(profile string, model compose.Model, key string) (int, int, error) {
	if key == engine.NativePostgreSQLRegistryRole || key == compose.MVPRegistryReleaseRole {
		if profile != wire.MVPPostgreSQLCPUProfile {
			return 0, 0, failure("OFFLINE_SCOPE_MVP_REGISTRY_PROFILE_REQUIRED")
		}
		return roleUser(model, "control-config")
	}
	return roleUser(model, key)
}
func inputServiceMountAllowed(profile, service string, m compose.Mount) bool {
	if m.Source == engine.NativePostgreSQLRegistryRole {
		return profile == wire.MVPPostgreSQLCPUProfile && service == "control" && m.Type == "volume" && !m.ReadOnly
	}
	if m.Source == compose.MVPRegistryReleaseRole {
		return profile == wire.MVPPostgreSQLCPUProfile && service == "control" && m.Type == "volume" && m.ReadOnly
	}
	return serviceVolumeMountAllowed(service, m)
}

func validateMVPInitialCapabilities(files map[string][]engine.ConfigurationFile, model compose.Model) error {
	root := ""
	for _, m := range model.Services["control"].Mounts {
		if m.Source == "control-config" {
			root = m.Target
		}
	}
	if root == "" {
		return failure("OFFLINE_SCOPE_MVP_CONFIGURATION_INVALID")
	}
	for _, role := range []string{"control-config", "operator-config"} {
		target := root
		names := []string{"runtime.yaml"}
		if role == "operator-config" {
			target = engine.DatabaseTaskDestination
			names = append(names, "control.yaml")
		}
		for _, name := range names {
			raw, e := restoreNamedFile(files[role], name)
			if e != nil {
				return e
			}
			var values map[string]json.RawMessage
			if wire.StrictJSON(raw, &values) != nil {
				return failure("OFFLINE_SCOPE_MVP_CONFIGURATION_INVALID")
			}
			caps := map[string]json.RawMessage{}
			for _, key := range []string{"mongodb_config_file", "image_registry_config_file", "middleware_config_file", "middleware_environment_file", "private_cluster_config_file", "monitoring_config_file", "model_cache", "aws", "task_events", "object_storage"} {
				if v, ok := values[key]; ok {
					caps[key] = v
				}
			}
			for _, f := range files[role] {
				if f.Name == "mvp-capabilities.json" {
					return failure("OFFLINE_SCOPE_MVP_CONFIGURATION_COLLISION")
				}
			}
			extra := []engine.ConfigurationFile{{Name: "mvp-capabilities.json", Contents: encode(caps)}}
			if _, e := cumulativeMVPCapabilities(raw, extra, files[role], target, model); e != nil {
				return e
			}
			var server struct {
				Server struct {
					Profile string `json:"application_profile"`
				}
			}
			if wire.UniqueJSON(raw, &server) != nil || server.Server.Profile != "" {
				return failure("OFFLINE_SCOPE_MVP_CONFIGURATION_INVALID")
			}
		}
	}
	return nil
}
