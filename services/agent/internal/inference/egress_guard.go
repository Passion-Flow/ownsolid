package inference

import "regexp"

// egressGuardUnavailable is the failure when the node cannot install or confirm the
// firewall rules that keep a workload container off the network. Inference never starts
// without them: offline flags inside the image are not a firewall.
const egressGuardUnavailable = "INFERENCE_EGRESS_GUARD_UNAVAILABLE"

// egressGuard denies network access to workload containers on an owned bridge. A
// container on a guarded bridge cannot open a connection to anything: not routed off the
// host, not to a host service on the bridge address, and not through Docker's embedded
// resolver, whose forwarded queries leave from the container's own network namespace.
// Replies to connections the host opens, such as the loopback port publication that the
// gateway and the readiness probe use, still pass.
//
// The rules live in the kernel, so they survive an agent restart; they do not survive a
// reboot, and the agent re-installs them before it starts an owned container again.
type egressGuard interface {
	// Ensure installs the rules for bridge, or repairs them when they are missing or were
	// changed, and confirms them by reading them back.
	Ensure(bridge string) error
	// Remove deletes the rules for bridge. It succeeds when there are none.
	Remove(bridge string) error
}

// bridgeName matches the host interface Docker creates for a bridge network that sets no
// explicit name: "br-" and the first twelve characters of the network ID.
var bridgeName = regexp.MustCompile(`^br-[a-f0-9]{12}$`)

// bridgeInterface returns the host interface of the owned bridge network id. The owned
// network sets no com.docker.network.bridge.name option (approvedNetworkOptions refuses
// one), so Docker derives the name from the ID.
func bridgeInterface(networkID string) string {
	return "br-" + networkID[:12]
}
