package compose

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RuntimeReader interface {
	Inspect(context.Context, string) (engine.Image, bool, error)
	InspectRuntime(context.Context, string, string) (engine.RuntimeContainer, bool, error)
	InspectRuntimeNetwork(context.Context, string) (engine.RuntimeNetwork, bool, error)
	InspectRuntimeIngress(context.Context, string) (engine.RuntimeNetwork, bool, error)
	RuntimeCandidates(context.Context, string, bool) ([]engine.RuntimeObject, error)
}
type RuntimeServiceStatus struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	ExitCode int    `json:"exit_code"`
}
type RuntimeStatus struct {
	Status           string                          `json:"status"`
	NetworkID        string                          `json:"network_id,omitempty"`
	IngressNetworkID string                          `json:"ingress_network_id,omitempty"`
	Services         map[string]RuntimeServiceStatus `json:"services"`
	Qualification    string                          `json:"qualification"`
}

func scopeLabels(labels map[string]string, scopeID string) bool {
	return labels[scopeLabel] == scopeID && labels[purposeLabel] == "isolated-validation" && labels["com.docker.compose.project"] == engine.RuntimeProject(scopeID)
}
func environment(values []string) (map[string]string, bool) {
	result := map[string]string{}
	for _, v := range values {
		key, value, ok := strings.Cut(v, "=")
		if !ok || key == "" {
			return nil, false
		}
		if _, exists := result[key]; exists {
			return nil, false
		}
		result[key] = value
	}
	return result, true
}
func (c *Client) ObserveRuntime(ctx context.Context, reader RuntimeReader, scope RenderContext, model Model) (RuntimeStatus, error) {
	result := RuntimeStatus{Status: "observed_absent_compose_resources", Services: map[string]RuntimeServiceStatus{}, Qualification: "process_resource_observation_not_application_health_installation_or_target"}
	if _, e := c.document(scope, model); e != nil {
		return result, e
	}
	network, networkFound, e := reader.InspectRuntimeNetwork(ctx, scope.ScopeID)
	if e != nil {
		return result, e
	}
	if networkFound {
		if !scopeLabels(network.Labels, scope.ScopeID) || network.Labels["com.docker.compose.network"] != "isolated" || network.Driver != "bridge" || network.Scope != "local" || !network.Internal || network.Attachable || network.Ingress || !networkOptions(network.Options) {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_NETWORK_MISMATCH")
		}
		result.NetworkID = network.ID
	}
	ingress, ingressFound, e := reader.InspectRuntimeIngress(ctx, scope.ScopeID)
	if e != nil {
		return result, e
	}
	needsIngress := RequiresIngress(model)
	if ingressFound {
		if !needsIngress || !scopeLabels(ingress.Labels, scope.ScopeID) || ingress.Labels["com.docker.compose.network"] != "ingress" || ingress.Driver != "bridge" || ingress.Scope != "local" || ingress.Internal || ingress.Attachable || ingress.Ingress || !networkOptions(ingress.Options) || ingress.ID == network.ID {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_INGRESS_MISMATCH")
		}
		result.IngressNetworkID = ingress.ID
	}
	ingressIDs := map[string]bool{}
	expectedIDs := map[string]bool{}
	for name, service := range model.Services {
		object, found, e := reader.InspectRuntime(ctx, scope.ScopeID, name)
		if e != nil {
			return result, e
		}
		if !found {
			continue
		}
		if !networkFound {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_NETWORK_MISSING")
		}
		image, found, e := reader.Inspect(ctx, service.Image)
		if e != nil {
			return result, e
		}
		if !found || image.Config == nil || !imageVolumeCoverage(service, image.Config.Volumes) || image.OS != "linux" || image.Architecture != "amd64" || image.ID != service.Image {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_IMAGE_MISMATCH")
		}
		expectedCommand := image.Config.Cmd
		if len(service.Command) > 0 {
			expectedCommand = service.Command
		}
		expectedEnv, ok := environment(image.Config.Env)
		if !ok {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_IMAGE_ENVIRONMENT_INVALID")
		}
		for key, value := range service.Environment {
			expectedEnv[key] = value
		}
		actualEnv, ok := environment(object.Config.Env)
		h := object.HostConfig
		if !ok || !reflect.DeepEqual(expectedEnv, actualEnv) || object.Image != service.Image || object.Config.Image != service.Image || object.Config.User != service.User || !slices.Equal(object.Config.Cmd, expectedCommand) || !slices.Equal(object.Config.Entrypoint, image.Config.Entrypoint) || object.Config.WorkingDir != image.Config.WorkingDir || !scopeLabels(object.Config.Labels, scope.ScopeID) || object.Config.Labels["com.docker.compose.service"] != name || object.Config.Labels["com.docker.compose.container-number"] != "1" || object.Config.Labels["com.docker.compose.oneoff"] != "False" || object.Platform != "linux" || h.NetworkMode != engine.RuntimeNetworkName(scope.ScopeID) || h.IpcMode != "private" || h.PidMode != "" || h.UTSMode != "" || h.UsernsMode != "" || h.CgroupParent != "" || h.CgroupnsMode != "private" || h.Runtime != "runc" || h.ReadonlyRootfs != service.ReadOnly || h.Memory != service.MemoryBytes || h.NanoCPUs != service.MilliCPU*nanoCPUPerMilliCPU || h.PIDs != service.PIDs || h.Privileged || h.AutoRemove || h.PublishAllPorts || len(h.CapAdd) > 0 || !slices.Equal(h.CapDrop, []string{"ALL"}) || !slices.Equal(h.SecurityOpt, []string{"no-new-privileges:true"}) || len(h.Binds) > 0 || len(h.VolumesFrom) > 0 || len(h.Devices) > 0 || len(h.DeviceRequests) > 0 || len(h.DNS) > 0 || len(h.ExtraHosts) > 0 || len(h.Links) > 0 || h.RestartPolicy.Name != service.Restart || h.RestartPolicy.MaximumRetryCount != 0 {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_CONTAINER_MISMATCH")
		}
		if e = runtimeMounts(scope, service, object); e != nil {
			return result, e
		}
		if !sameImageVolumes(image.Config.Volumes, object.Config.Volumes) {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_IMAGE_VOLUME_DECLARATION_MISMATCH")
		}
		if len(h.Tmpfs) != len(service.Tmpfs) {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_PORT_OR_TMPFS_MISMATCH")
		}
		for _, tmp := range service.Tmpfs {
			expected := "rw,noexec,nosuid,nodev,size=" + strconv.FormatInt(tmp.SizeBytes, 10) + ",uid=" + strconv.Itoa(tmp.UID) + ",gid=" + strconv.Itoa(tmp.GID) + ",mode=0700"
			if h.Tmpfs[tmp.Target] != expected {
				return result, failure("OFFLINE_COMPOSE_RUNTIME_PORT_OR_TMPFS_MISMATCH")
			}
		}
		expectedPorts := map[string]map[string]bool{}
		for _, port := range service.Ports {
			key := strconv.Itoa(port.Target) + "/tcp"
			if expectedPorts[key] == nil {
				expectedPorts[key] = map[string]bool{}
			}
			expectedPorts[key][port.HostIP+"|"+strconv.Itoa(port.Published)] = true
		}
		if len(h.PortBindings) != len(expectedPorts) {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_PORT_OR_TMPFS_MISMATCH")
		}
		for key, bindings := range h.PortBindings {
			want, ok := expectedPorts[key]
			if !ok || len(want) != len(bindings) {
				return result, failure("OFFLINE_COMPOSE_RUNTIME_PORT_OR_TMPFS_MISMATCH")
			}
			seen := map[string]bool{}
			for _, binding := range bindings {
				value := binding.HostIP + "|" + binding.HostPort
				if !want[value] || seen[value] {
					return result, failure("OFFLINE_COMPOSE_RUNTIME_PORT_OR_TMPFS_MISMATCH")
				}
				seen[value] = true
			}
		}
		expectedNetworkIDs := map[string]string{engine.RuntimeNetworkName(scope.ScopeID): network.ID}
		if len(service.Ports) > 0 {
			if !ingressFound {
				return result, failure("OFFLINE_COMPOSE_RUNTIME_INGRESS_MISSING")
			}
			expectedNetworkIDs[engine.RuntimeIngressName(scope.ScopeID)] = ingress.ID
			ingressIDs[object.ID] = true
		}
		if len(object.NetworkSettings.Networks) != len(expectedNetworkIDs) {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_ENDPOINT_MISMATCH")
		}
		for networkName, id := range expectedNetworkIDs {
			endpoint, ok := object.NetworkSettings.Networks[networkName]
			if !ok || endpoint.NetworkID != "" && endpoint.NetworkID != id || object.State.Running && endpoint.NetworkID != id {
				return result, failure("OFFLINE_COMPOSE_RUNTIME_ENDPOINT_MISMATCH")
			}
		}
		if object.State.Running {
			published := map[string]map[string]bool{}
			for key, bindings := range object.NetworkSettings.Ports {
				if len(bindings) == 0 {
					continue
				}
				published[key] = map[string]bool{}
				for _, binding := range bindings {
					value := binding.HostIP + "|" + binding.HostPort
					if published[key][value] {
						return result, failure("OFFLINE_COMPOSE_ACTUAL_PORT_MAPPING_MISMATCH")
					}
					published[key][value] = true
				}
			}
			if !reflect.DeepEqual(published, expectedPorts) {
				return result, failure("OFFLINE_COMPOSE_ACTUAL_PORT_MAPPING_MISMATCH")
			}
		}
		if object.State.Restarting || object.State.Dead || object.State.Paused || object.State.OOMKilled {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_PROCESS_FAILED")
		}
		switch object.State.Status {
		case "created":
			if object.State.Running || object.State.PID != 0 {
				return result, failure("OFFLINE_COMPOSE_RUNTIME_STATE_MISMATCH")
			}
		case "running":
			if !object.State.Running || object.State.PID < 1 {
				return result, failure("OFFLINE_COMPOSE_RUNTIME_STATE_MISMATCH")
			}
		case "exited":
			if object.State.Running || object.State.PID != 0 {
				return result, failure("OFFLINE_COMPOSE_RUNTIME_STATE_MISMATCH")
			}
		default:
			return result, failure("OFFLINE_COMPOSE_RUNTIME_STATE_MISMATCH")
		}
		result.Services[name] = RuntimeServiceStatus{ID: object.ID, State: object.State.Status, ExitCode: object.State.ExitCode}
		expectedIDs[object.ID] = true
	}
	for id, endpoint := range network.Containers {
		if !expectedIDs[id] {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_FOREIGN_ENDPOINT")
		}
		if endpoint.Name == "" {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_ENDPOINT_MISMATCH")
		}
	}
	for id, endpoint := range ingress.Containers {
		if !ingressIDs[id] || endpoint.Name == "" {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_FOREIGN_INGRESS_ENDPOINT")
		}
	}
	for _, networks := range []bool{false, true} {
		objects, e := reader.RuntimeCandidates(ctx, scope.ScopeID, networks)
		if e != nil {
			return result, e
		}
		wanted := expectedIDs
		if networks {
			wanted = map[string]bool{}
			if networkFound {
				wanted[network.ID] = true
			}
			if ingressFound {
				wanted[ingress.ID] = true
			}
		}
		if len(objects) != len(wanted) {
			return result, failure("OFFLINE_COMPOSE_RUNTIME_FOREIGN_RESOURCES")
		}
		for _, object := range objects {
			if !wanted[object.ID] {
				return result, failure("OFFLINE_COMPOSE_RUNTIME_FOREIGN_RESOURCES")
			}
		}
	}
	if networkFound || ingressFound || len(result.Services) > 0 {
		result.Status = "observed_partial_compose_resources"
	}
	if networkFound && (!needsIngress || ingressFound) && len(result.Services) == len(model.Services) {
		result.Status = "observed_complete_compose_resources"
	}
	return result, nil
}
func runtimeMounts(scope RenderContext, service Service, object engine.RuntimeContainer) error {
	bad := func() error { return failure("OFFLINE_COMPOSE_RUNTIME_MOUNT_MISMATCH") }
	if len(object.HostConfig.Mounts) != len(service.Mounts) || len(object.Mounts) != len(service.Mounts) {
		return bad()
	}
	expected := map[string]Mount{}
	for _, mount := range service.Mounts {
		expected[mount.Target] = mount
	}
	for target := range object.Config.Volumes {
		mount, ok := expected[target]
		if !ok || mount.Type != "volume" {
			return bad()
		}
	}
	seen := map[string]bool{}
	for _, mount := range object.HostConfig.Mounts {
		want, ok := expected[mount.Target]
		if !ok || seen[mount.Target] || mount.Type != want.Type || mount.ReadOnly != want.ReadOnly {
			return bad()
		}
		seen[mount.Target] = true
		source := want.Source
		if want.Type == "volume" {
			source = "projectone-" + scope.ScopeID + "-" + want.Source
			if mount.VolumeOptions == nil || !mount.VolumeOptions.NoCopy || mount.VolumeOptions.Subpath != "" {
				return bad()
			}
		} else if mount.VolumeOptions != nil {
			return bad()
		}
		if mount.Source != source {
			return bad()
		}
	}
	seen = map[string]bool{}
	for _, mount := range object.Mounts {
		want, ok := expected[mount.Destination]
		if !ok || seen[mount.Destination] || mount.Type != want.Type || mount.RW == want.ReadOnly {
			return bad()
		}
		seen[mount.Destination] = true
		if want.Type == "volume" {
			if mount.Name != "projectone-"+scope.ScopeID+"-"+want.Source || mount.Driver != "local" {
				return bad()
			}
		} else if mount.Source != want.Source {
			return bad()
		}
	}
	return nil
}

// CreateRuntime is a low-level actual effect. The scope caller must sync intent first.
func (c *Client) CreateRuntime(ctx context.Context, reader RuntimeReader, scope RenderContext, model Model) (RuntimeStatus, error) {
	if _, e := c.Version(ctx); e != nil {
		return RuntimeStatus{}, e
	}
	before, e := c.ObserveRuntime(ctx, reader, scope, model)
	if e != nil {
		return before, e
	}
	if before.Status != "observed_absent_compose_resources" {
		return before, failure("OFFLINE_COMPOSE_EXISTING_RESOURCES_PRESERVED")
	}
	for _, service := range model.Services {
		image, found, e := reader.Inspect(ctx, service.Image)
		if e != nil {
			return before, e
		}
		if !found || image.Config == nil || !imageVolumeCoverage(service, image.Config.Volumes) || image.OS != "linux" || image.Architecture != "amd64" || image.ID != service.Image {
			return before, failure("OFFLINE_COMPOSE_RUNTIME_IMAGE_MISMATCH")
		}
	}
	document, e := c.document(scope, model)
	if e != nil {
		return before, e
	}
	// Compose interpolates string values when reading a model; escape every literal dollar.
	var literal any
	if json.Unmarshal(document, &literal) != nil {
		return before, failure("OFFLINE_COMPOSE_MODEL_INPUT_INVALID")
	}
	document, _ = json.Marshal(escapeInterpolation(literal))
	args := []string{"--project-name", engine.RuntimeProject(scope.ScopeID), "--project-directory", scope.ScopeRoot, "-f", "-", "create", "--no-build", "--pull", "never", "--no-recreate"}
	if _, e = c.execute(ctx, args, document); e != nil {
		return before, failure("OFFLINE_COMPOSE_CREATE_UNCERTAIN")
	}
	after, e := c.ObserveRuntime(ctx, reader, scope, model)
	if e != nil {
		return after, e
	}
	if after.Status != "observed_complete_compose_resources" {
		return after, failure("OFFLINE_COMPOSE_CREATE_UNCERTAIN")
	}
	for _, service := range after.Services {
		if service.State != "created" {
			return after, failure("OFFLINE_COMPOSE_CREATE_UNCERTAIN")
		}
	}
	return after, nil
}

// Exact explicit coverage prevents the daemon from allocating anonymous image volumes.
func imageVolumeCoverage(service Service, declarations map[string]json.RawMessage) bool {
	for target, value := range declarations {
		if !containerTarget(target) || !emptyVolumeDeclaration(value) {
			return false
		}
		matches := 0
		for _, mount := range service.Mounts {
			if mount.Target == target {
				if mount.Type != "volume" {
					return false
				}
				matches++
			}
		}
		if matches != 1 {
			return false
		}
	}
	return true
}
func sameImageVolumes(expected, actual map[string]json.RawMessage) bool {
	if len(expected) != len(actual) {
		return false
	}
	for key, value := range actual {
		if _, ok := expected[key]; !ok || !emptyVolumeDeclaration(value) {
			return false
		}
	}
	return true
}
func escapeInterpolation(value any) any {
	switch v := value.(type) {
	case string:
		return strings.ReplaceAll(v, "$", "$$")
	case []any:
		for i, item := range v {
			v[i] = escapeInterpolation(item)
		}
	case map[string]any:
		for key, item := range v {
			v[key] = escapeInterpolation(item)
		}
	}
	return value
}

func networkOptions(options map[string]string) bool {
	for key, value := range options {
		switch key {
		case "com.docker.network.enable_ipv4":
			if value != "true" {
				return false
			}
		case "com.docker.network.enable_ipv6":
			if value != "false" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func RequiresIngress(model Model) bool {
	for _, service := range model.Services {
		if len(service.Ports) > 0 {
			return true
		}
	}
	return false
}

func emptyVolumeDeclaration(value json.RawMessage) bool {
	var object map[string]json.RawMessage
	return wire.UniqueJSON(value, &object) == nil && object != nil && len(object) == 0
}
