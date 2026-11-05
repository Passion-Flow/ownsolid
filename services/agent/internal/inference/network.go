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
// unreachableResolver is the only upstream the embedded DNS resolver of an owned workload
// container may forward to. Docker forwards to a loopback resolver on the host, such as
// systemd-resolved, from the host's own network namespace, where no rule on the bridge
// sees the query; an explicit upstream is dialed from the container's namespace instead,
// and the egress guard refuses it there. 203.0.113.53 is in TEST-NET-3 (RFC 5737) and
// never belongs to a real host, so even without the guard no resolver would answer.
const unreachableResolver = "203.0.113.53"

// workloadResolvers is the DNS configuration of every owned workload container.
func workloadResolvers() []string { return []string{unreachableResolver} }

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

// EnsureNetwork creates only the node's owned loopback-publishing bridge, and guards it
// against egress before returning it; unknown existing networks are preserved.
// The bridge cannot be internal: Engine suppresses host port publication on internal
// networks, and the gateway reaches the model only through that loopback port. The
// egress guard therefore denies every connection the container opens, whatever the
// application's offline flags say.
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
		return d.guarded(value.ID)
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
	return d.guarded(value.ID)
}

// guarded returns the network ID once its egress guard is in place, and no ID otherwise.
func (d *Docker) guarded(id string) (string, error) {
	if e := d.guard.Ensure(bridgeInterface(id)); e != nil {
		return "", e
	}
	return id, nil
}

// RemoveUnusedNetwork deletes the node's owned network when no container uses it, then
// its egress guard rules. A retry after the network is gone still removes the rules of
// the expected network.
func (d *Docker) RemoveUnusedNetwork(ctx context.Context, node string, expected ...string) error {
	if !deploymentID.MatchString(node) {
		return fail("INFERENCE_CONFIGURATION_INVALID")
	}
	value, e := d.inspectNetwork(ctx, "projectone-inference-"+node)
	if e != nil && e.Error() == "INFERENCE_DOCKER_NOT_FOUND" {
		if len(expected) == 1 && containerID.MatchString(expected[0]) {
			return d.guard.Remove(bridgeInterface(expected[0]))
		}
		return nil
	}
	if e != nil {
		return e
	}
	if len(expected) > 1 || len(expected) == 1 && expected[0] != value.ID || !ownedNetwork(value, node) || len(value.Containers) != 0 {
		return fail("INFERENCE_NETWORK_CONFLICT")
	}
	if e = d.request(ctx, "DELETE", "/networks/"+value.ID, nil, nil, 204); e != nil {
		return e
	}
	return d.guard.Remove(bridgeInterface(value.ID))
}

// VerifyNetworkEndpoint confirms that the owned network holds only the owned container
// and that its egress guard is in place, re-installing the guard when a reboot or a
// firewall reload removed it. Every start of an owned container passes through here.
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
	return d.guard.Ensure(bridgeInterface(value.ID))
}
