package inference

import (
	"context"
	"encoding/json"
	"maps"
)

type Network struct {
	ID         string `json:"Id"`
	Name       string
	Driver     string
	Internal   bool
	EnableIPv6 bool
	Options    map[string]string
	Labels     map[string]string
	Containers map[string]json.RawMessage
}

func (d *Docker) inspectNetwork(ctx context.Context, name string) (Network, error) {
	var out Network
	e := d.request(ctx, "GET", "/networks/"+name, nil, &out, 200)
	return out, e
}
func networkOptions() map[string]string {
	return map[string]string{"com.docker.network.bridge.host_binding_ipv4": "127.0.0.1", "com.docker.network.bridge.enable_icc": "false"}
}

func approvedNetworkOptions(options map[string]string) bool {
	checked := maps.Clone(options)
	// API1.45 omits this default; current Engine versions serialize it explicitly.
	if value, ok := checked["com.docker.network.enable_ipv4"]; ok {
		if value != "true" {
			return false
		}
		delete(checked, "com.docker.network.enable_ipv4")
	}
	return maps.Equal(checked, networkOptions())
}

func ownedNetwork(network Network, node string) bool {
	return containerID.MatchString(network.ID) && network.Name == "projectone-inference-"+node && network.Driver == "bridge" && !network.Internal && !network.EnableIPv6 && approvedNetworkOptions(network.Options) && network.Labels["projectone.owner"] == "node-agent" && network.Labels["projectone.node_id"] == node
}

// EnsureNetwork creates only the node's owned loopback-publishing bridge; unknown existing networks are preserved.
// Engine suppresses host port publication on internal-only networks. Offline application flags are not an egress firewall.
func (d *Docker) EnsureNetwork(ctx context.Context, node string) (string, error) {
	if !deploymentID.MatchString(node) {
		return "", fail("INFERENCE_CONFIGURATION_INVALID")
	}
	name := "projectone-inference-" + node
	value, e := d.inspectNetwork(ctx, name)
	if e == nil {
		if !ownedNetwork(value, node) {
			return "", fail("INFERENCE_NETWORK_CONFLICT")
		}
		return value.ID, nil
	}
	if e.Error() != "INFERENCE_DOCKER_NOT_FOUND" {
		return "", e
	}
	input := struct {
		Name           string
		Driver         string
		Internal       bool
		EnableIPv6     bool
		CheckDuplicate bool
		Options        map[string]string
		Labels         map[string]string
	}{name, "bridge", false, false, true, networkOptions(), map[string]string{"projectone.owner": "node-agent", "projectone.node_id": node}}
	var created struct {
		ID string `json:"Id"`
	}
	if e = d.request(ctx, "POST", "/networks/create", input, &created, 201); e != nil {
		return "", e
	}
	if !containerID.MatchString(created.ID) {
		return "", fail("INFERENCE_DOCKER_REJECTED")
	}
	value, e = d.inspectNetwork(ctx, name)
	if e != nil || !ownedNetwork(value, node) || value.ID != created.ID {
		return "", fail("INFERENCE_NETWORK_CONFLICT")
	}
	return value.ID, nil
}
func (d *Docker) RemoveUnusedNetwork(ctx context.Context, node string, expected ...string) error {
	if !deploymentID.MatchString(node) {
		return fail("INFERENCE_CONFIGURATION_INVALID")
	}
	value, e := d.inspectNetwork(ctx, "projectone-inference-"+node)
	if e != nil && e.Error() == "INFERENCE_DOCKER_NOT_FOUND" {
		return nil
	}
	if e != nil {
		return e
	}
	if len(expected) > 1 || len(expected) == 1 && expected[0] != value.ID || !ownedNetwork(value, node) || len(value.Containers) != 0 {
		return fail("INFERENCE_NETWORK_CONFLICT")
	}
	return d.request(ctx, "DELETE", "/networks/"+value.ID, nil, nil, 204)
}

func (d *Docker) VerifyNetworkEndpoint(ctx context.Context, node, id, container string) error {
	if !deploymentID.MatchString(node) || !containerID.MatchString(id) || !containerID.MatchString(container) {
		return fail("INFERENCE_NETWORK_CONFLICT")
	}
	value, e := d.inspectNetwork(ctx, id)
	if e != nil {
		return e
	}
	if !ownedNetwork(value, node) || value.ID != id {
		return fail("INFERENCE_NETWORK_CONFLICT")
	}
	for attached := range value.Containers {
		if attached != container {
			return fail("INFERENCE_NETWORK_CONFLICT")
		}
	}
	return nil
}
