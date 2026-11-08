# Kubernetes node

Installs RKE2 on a node of the `kubernetes_node` profile and joins it to its cell's
cluster as the server or a worker ([ADR 0051](../../../../docs/decisions/0051-kubernetes-cells-with-rke2.md),
[research](../../../../docs/research/kubernetes-cells.md)).

[manager.go](manager.go) runs the three operations:

- `kubernetes.join.v1`: preflight (root, systemd, the enrolled address on an interface, a
  default route, a resolver file naming a real DNS server, free space), download and verify
  the pinned artifacts, unpack the allowed files of the binary tarball into `/usr/local`,
  place the image archives, write `/etc/rancher/rke2/config.yaml` (0600), start the unit and
  wait at most `ready_seconds` for the node's own `Node` to be `Ready`. A server also reports
  the SHA-256 of its CA, which join tokens pin.
- `kubernetes.token.v1`: on the server, `rke2 token create --ttl`, checked to pin this
  cluster's CA. The server token itself never leaves the node.
- `kubernetes.observe.v1`: the unit's state and the node's readiness.

The journal `kubernetes.json` in the agent's state directory records the membership the agent
installed. A join that disagrees with it (another cluster, role, address or resolver) is
refused with `KUBERNETES_CONFIG_CONFLICT`, because a server whose address changes loses its
etcd membership. A worker's retry may bring a fresh token. The agent restarts a unit still
activating so it reads the new configuration. It keeps an active unit running.
RKE2 files ownsolid did not install are never overwritten (`KUBERNETES_CONFLICT`).
After a crash, the agent keeps verified staged files and
[client.KubernetesArtifact](../client/kubernetes.go) resumes a partial download
from its last whole range.

Without a site-supplied `/etc/rancher/rke2/registries.yaml`, the Agent writes
`ownsolid-registries.yaml` beside the RKE2 configuration and disables Docker
Hub fallback. A sandbox that starts before the bundled image imports fails
locally and can retry from the cache. A native registry file remains the
site's policy. For already running nodes the default takes effect at the
next RKE2 start.

Readiness is read with the kubelet's own kubeconfig, which the node authorizer limits to the
node's own object; the cluster admin kubeconfig stays on the server. When a join times out,
the task log gets a diagnosis the agent recognises in the RKE2 journal (wrong CA, server
unreachable, token refused), never the journal itself.

Check: `go -C services/agent test ./internal/kubernetes`. The unit tests use a fake host and a
synthetic release. The ARC-104 research note records the real Linux node runs
and the remaining cell acceptance tests.
