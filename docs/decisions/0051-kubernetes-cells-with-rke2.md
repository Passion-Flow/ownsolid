# 0051: Kubernetes cells with RKE2

- Status: accepted (ARC-104)
- Date: 2026-11-08

## Context

Each cell runs one Kubernetes cluster that ownsolid installs and manages, with the ownsolid
agent on every node. Scheduling, device allocation, workload egress and the cell lifecycle
all build on it. The install must work with no route out. Research:
[Kubernetes cells](../research/kubernetes-cells.md).

## Decision

**RKE2 is the distribution, pinned to its stable release.** The offline bundle carries the
RKE2 binary tarball and the core and Canal image archives of v1.36.6+rke2r1, each pinned by
size and SHA-256. Linux amd64 first. Traefik is disabled: no ownsolid entrance runs in the
cluster yet.

**A cluster node is a node with the `kubernetes_node` execution profile.** The profile is new
and adds operations to join, to create a join token and to observe; existing profiles and
their agents are unchanged.

**Control orchestrates; the agent installs.** Control chooses a server node and writes a
join task for it, then, once it is ready, a token task and join tasks for workers in batches
of at most 50. The agent verifies the artifacts, writes the RKE2 configuration with the
node's enrolled address as `node-ip`, starts the systemd unit and waits a bounded time for
its own `Node` to be `Ready`.

**No long-lived cluster secret leaves the server.** RKE2 generates the server token on the
server node. Workers join with a bootstrap token that the server's agent creates with a short
lifetime, in the format that pins the cluster CA by hash.

**Control reads the cluster through the agents.** Each agent reports its own node's
readiness through the kubelet's credential. The cluster admin credential stays on the server;
later items install into the cluster by placing manifests in the server's auto-deploy
directory.

**The address and the resolver are fixed at creation.** A server's address cannot change
after its first start without breaking etcd membership, so a join whose configuration
differs from what is installed is refused, never rewritten. The agent points the kubelet at
a resolver file that holds real upstream servers, so pods never fall back to a public one.

## Consequences

- A node is either a Docker node or a Kubernetes node; moving a node between them is a
  re-enrollment until a later item makes it an operation.
- One server per cell until ARC-115 adds three. A server failure stops scheduling in the
  cell, not running workloads.
- Every worker downloads about 850 MB from control until ARC-116 moves images to the
  cluster's registry mirror.
- Upgrade and certificate rotation are FLT-111. RKE2 rotates its certificates on restart
  when fewer than 90 days remain.
