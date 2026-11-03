package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Runtime metadata remains internal; callers return only selected secret-free fields.
type RuntimeContainer struct {
	ID       string `json:"Id"`
	Name     string
	Image    string
	Platform string
	Config   struct {
		Image      string
		User       string
		Cmd        []string
		Entrypoint []string
		Env        []string
		Labels     map[string]string
		WorkingDir string
		Volumes    map[string]json.RawMessage
	}
	State struct {
		Status                                       string
		Running, Restarting, Dead, Paused, OOMKilled bool
		PID                                          int `json:"Pid"`
		ExitCode                                     int
		StartedAt                                    string
	}
	HostConfig struct {
		NetworkMode                                                                string
		IpcMode, PidMode, UTSMode, UsernsMode, CgroupnsMode, CgroupParent, Runtime string
		ReadonlyRootfs, Privileged, AutoRemove, PublishAllPorts                    bool
		Memory, MemorySwap, NanoCPUs                                               int64
		PIDs                                                                       int64 `json:"PidsLimit"`
		CapDrop, CapAdd, SecurityOpt, Binds, VolumesFrom, DNS, ExtraHosts, Links   []string
		Devices, DeviceRequests                                                    []json.RawMessage
		PortBindings                                                               map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string
		}
		Tmpfs     map[string]string
		LogConfig struct {
			Type   string
			Config map[string]string
		}
		RestartPolicy struct {
			Name              string
			MaximumRetryCount int
		}
		Mounts []struct {
			Type, Source, Target string
			ReadOnly             bool
			VolumeOptions        *struct {
				NoCopy  bool
				Subpath string
			}
		}
	}
	Mounts []struct {
		Type, Name, Source, Destination, Driver string
		RW                                      bool
	}
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string
		}
		Networks map[string]struct{ NetworkID string }
	}
}
type RuntimeNetwork struct {
	ID                            string `json:"Id"`
	Name, Driver, Scope           string
	Internal, Attachable, Ingress bool
	Labels, Options               map[string]string
	Containers                    map[string]struct{ Name string }
}
type RuntimeObject struct {
	ID     string `json:"Id"`
	Name   string
	Names  []string
	Labels map[string]string
}

func RuntimeProject(scopeID string) string { return "projectone-" + scopeID }
func RuntimeContainerName(scopeID, service string) string {
	return RuntimeProject(scopeID) + "-" + service + "-1"
}
func RuntimeNetworkName(scopeID string) string { return RuntimeProject(scopeID) + "_isolated" }
func RuntimeIngressName(scopeID string) string { return RuntimeProject(scopeID) + "_ingress" }
func runtimeService(service string) bool {
	return service == "control" || service == "postgres" || service == "gateway"
}
func (c *Client) InspectRuntime(ctx context.Context, scopeID, service string) (RuntimeContainer, bool, error) {
	var result RuntimeContainer
	if !objectIdentity.MatchString(scopeID) || !runtimeService(service) {
		return result, false, failure("OFFLINE_ENGINE_RUNTIME_CONTEXT_INVALID")
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+url.PathEscape(RuntimeContainerName(scopeID, service))+"/json", nil, nil, c.config.RequestSeconds)
	if e != nil {
		return result, false, e
	}
	if status == http.StatusNotFound {
		return result, false, nil
	}
	if status != http.StatusOK || wire.UniqueJSON(raw, &result) != nil || !containerIdentity.MatchString(result.ID) || result.Name != "/"+RuntimeContainerName(scopeID, service) {
		return RuntimeContainer{}, false, failure("OFFLINE_ENGINE_RUNTIME_RESPONSE_INVALID")
	}
	return result, true, nil
}
func (c *Client) InspectRuntimeNetwork(ctx context.Context, scopeID string) (RuntimeNetwork, bool, error) {
	return c.inspectRuntimeNetwork(ctx, scopeID, false)
}
func (c *Client) InspectRuntimeIngress(ctx context.Context, scopeID string) (RuntimeNetwork, bool, error) {
	return c.inspectRuntimeNetwork(ctx, scopeID, true)
}
func (c *Client) inspectRuntimeNetwork(ctx context.Context, scopeID string, ingress bool) (RuntimeNetwork, bool, error) {
	var result RuntimeNetwork
	if !objectIdentity.MatchString(scopeID) {
		return result, false, failure("OFFLINE_ENGINE_RUNTIME_CONTEXT_INVALID")
	}
	name := RuntimeNetworkName(scopeID)
	if ingress {
		name = RuntimeIngressName(scopeID)
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/networks/"+url.PathEscape(name), nil, nil, c.config.RequestSeconds)
	if e != nil {
		return result, false, e
	}
	if status == http.StatusNotFound {
		return result, false, nil
	}
	if status != http.StatusOK || wire.UniqueJSON(raw, &result) != nil || !containerIdentity.MatchString(result.ID) || result.Name != name {
		return RuntimeNetwork{}, false, failure("OFFLINE_ENGINE_RUNTIME_NETWORK_RESPONSE_INVALID")
	}
	return result, true, nil
}

// Query both independent labels so a foreign object cannot hide behind a changed label.
func (c *Client) RuntimeCandidates(ctx context.Context, scopeID string, networks bool) ([]RuntimeObject, error) {
	if !objectIdentity.MatchString(scopeID) {
		return nil, failure("OFFLINE_ENGINE_RUNTIME_CONTEXT_INVALID")
	}
	objects := map[string]RuntimeObject{}
	for _, label := range []string{"io.projectone.isolated-scope=" + scopeID, "com.docker.compose.project=" + RuntimeProject(scopeID)} {
		filters, _ := json.Marshal(map[string][]string{"label": {label}})
		query := url.Values{"filters": {string(filters)}}
		path := "/containers/json"
		if networks {
			path = "/networks"
		} else {
			query.Set("all", "1")
		}
		raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+path, query, nil, c.config.RequestSeconds)
		if e != nil {
			return nil, e
		}
		var page []RuntimeObject
		if status != http.StatusOK || wire.UniqueJSON(raw, &page) != nil || page == nil {
			return nil, failure("OFFLINE_ENGINE_RUNTIME_INVENTORY_INVALID")
		}
		for _, object := range page {
			if !containerIdentity.MatchString(object.ID) {
				return nil, failure("OFFLINE_ENGINE_RUNTIME_INVENTORY_INVALID")
			}
			if prior, ok := objects[object.ID]; ok {
				a, _ := json.Marshal(prior)
				b, _ := json.Marshal(object)
				if string(a) != string(b) {
					return nil, failure("OFFLINE_ENGINE_RUNTIME_INVENTORY_CHANGED")
				}
			}
			objects[object.ID] = object
		}
	}
	result := make([]RuntimeObject, 0, len(objects))
	for _, object := range objects {
		result = append(result, object)
	}
	return result, nil
}

func (c *Client) VerifyVolumeConsumers(ctx context.Context, owner Ownership, allowed []string) error {
	if _, found, e := c.InspectVolume(ctx, owner); e != nil {
		return e
	} else if !found {
		return failure("OFFLINE_ENGINE_OWNED_VOLUME_REQUIRED")
	}
	ids := map[string]bool{}
	for _, id := range allowed {
		if !containerIdentity.MatchString(id) || ids[id] {
			return failure("OFFLINE_ENGINE_RUNTIME_CONTEXT_INVALID")
		}
		ids[id] = true
	}
	filters, _ := json.Marshal(map[string][]string{"volume": {owner.VolumeName()}})
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/json", url.Values{"all": {"1"}, "filters": {string(filters)}}, nil, c.config.RequestSeconds)
	if e != nil {
		return e
	}
	var consumers []RuntimeObject
	if status != http.StatusOK || wire.UniqueJSON(raw, &consumers) != nil || consumers == nil {
		return failure("OFFLINE_ENGINE_VOLUME_CONSUMERS_INVALID")
	}
	seen := map[string]bool{}
	for _, consumer := range consumers {
		if !ids[consumer.ID] || seen[consumer.ID] {
			return failure("OFFLINE_ENGINE_FOREIGN_VOLUME_CONSUMER")
		}
		seen[consumer.ID] = true
	}
	return nil
}
