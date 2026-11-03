package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const registryConfigurationKey = "PROJECT_ONE_AGENT_REGISTRY_CONFIG_FILE"

// LoadConfigWithEnvironment reads only an explicitly selected protected file.
// JSON remains the base authority; invalid base input is never repaired by env.
func LoadConfigWithEnvironment(path, environment string) (Config, error) {
	c, e := loadConfig(path)
	if e != nil || environment == "" {
		return c, e
	}
	raw, e := state.ProtectedFile(environment)
	if e != nil {
		return Config{}, e
	}
	values, e := registryEnvironment(raw)
	if e != nil {
		return Config{}, e
	}
	if e = applyAgentEnvironment(&c, values); e != nil {
		return Config{}, e
	}
	if reference, exists := values[registryConfigurationKey]; exists {
		component, err := imageregistry.LoadEnvironment(map[string]string{registryConfigurationKey: reference})
		if err != nil {
			return Config{}, err
		}
		c.ImageRegistry = &component
	}
	if c, e = validateConfig(c); e != nil {
		return Config{}, e
	}
	if e = validateRegistryConfiguration(c); e != nil {
		return Config{}, e
	}
	return c, nil
}

func registryEnvironment(raw []byte) (map[string]string, error) {
	bad := errors.New("AGENT_REGISTRY_ENVIRONMENT_INVALID")
	values := make(map[string]string)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if _, duplicate := values[key]; !ok || key == "" || duplicate {
			return nil, bad
		}
		if strings.HasPrefix(value, "\"") {
			var decoded string
			if json.Unmarshal([]byte(value), &decoded) != nil {
				return nil, bad
			}
			value = decoded
		} else if strings.HasPrefix(value, "'") {
			if len(value) < 2 || !strings.HasSuffix(value, "'") {
				return nil, bad
			}
			value = value[1 : len(value)-1]
		}
		if value == "" || strings.ContainsAny(value, "\x00\r\n$`") {
			return nil, bad
		}
		values[key] = value
	}
	if len(values) == 0 {
		return nil, bad
	}
	return values, nil
}

func validateRegistryConfiguration(c Config) error {
	if c.ImageRegistry == nil {
		return nil
	}
	bad := errors.New("AGENT_REGISTRY_CONFIGURATION_INVALID")
	if !wire.IsInferenceProfile(profile(c)) || c.Inference.RootDirectory == "" || c.Inference.DockerSocket == "" || c.ImageRegistry.Validate() != nil {
		return bad
	}
	for _, root := range []string{c.StateDirectory, c.Inference.RootDirectory, c.Training.RootDirectory, c.Preparation.RootDirectory} {
		if root != "" && overlap(root, c.ImageRegistry.Root) {
			return bad
		}
	}
	return nil
}

type taskImageLoader interface {
	EnsureTask(context.Context, string, string, string, wire.InferenceCommand) (wire.RegistryManifest, error)
}

func registryLoader(c Config, connection *client.Client) (taskImageLoader, func(), error) {
	if e := validateRegistryConfiguration(c); e != nil {
		return nil, nil, e
	}
	if c.ImageRegistry == nil {
		return nil, func() {}, nil
	}
	docker, e := inference.NewDocker(c.Inference.DockerSocket, c.Policy())
	if e != nil {
		return nil, nil, e
	}
	loader, e := imageregistry.New(*c.ImageRegistry, connection, docker)
	if e != nil {
		docker.Close()
		return nil, nil, e
	}
	return loader, docker.Close, nil
}

func registryObservation(observation wire.InferenceObservation, loader taskImageLoader) wire.InferenceObservation {
	observation.RegistryImageDelivery = wire.RegistryDeliveryCapability(loader != nil)
	return observation
}

func ensureRegistryTaskImage(ctx context.Context, node string, lease wire.Lease, loader taskImageLoader) error {
	if lease.Inference == nil || lease.Inference.Validate(lease.Operation) != nil {
		return errors.New("AGENT_TASK_UNSUPPORTED")
	}
	if lease.Inference.RegistryPinID == "" {
		return nil
	}
	if _, bounded := ctx.Deadline(); !bounded || !idPattern.MatchString(node) || !idPattern.MatchString(lease.TaskID) || !token(lease.LeaseToken) {
		return errors.New("AGENT_TASK_UNSUPPORTED")
	}
	if ctx.Err() != nil {
		return errors.New("INFERENCE_CANCELLED")
	}
	if loader == nil {
		return errors.New("REGISTRY_DISABLED")
	}
	if _, e := loader.EnsureTask(ctx, node, lease.TaskID, lease.LeaseToken, *lease.Inference); e != nil {
		// Preserve its local journal and withhold task success. Private file/provider
		// causes are never copied into the runtime's structured log template.
		return errors.New("REGISTRY_RECOVERY_REQUIRED")
	}
	return nil
}
