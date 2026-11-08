# One Kubernetes cluster per cell

Research for ARC-104. Decision: [ADR 0051](../decisions/0051-kubernetes-cells-with-rke2.md).

## The problem

The product decision is one Kubernetes cluster per cell of 2,000 to 5,000 nodes, with the
ownsolid agent handling the host underneath (roadmap index, "Fleet substrate"). Scheduling
(SCH-107, Kueue and Volcano), device allocation (GPU-105), workload egress (NET-106) and the
cell lifecycle (FLT-111) all need that cluster to exist. Today nothing installs Kubernetes:
the inherited `privateclusters` module only runs a proof Job in a cluster an operator built
and connected by hand.

ARC-104 has to: install a cluster on nodes ownsolid already manages, fully offline, from
artifacts in the offline bundle; put every cluster node under an ownsolid agent; and let
control see what the cluster is doing.

## Limits that size the design

- Upstream Kubernetes supports up to 5,000 nodes, 110 pods per node, 150,000 pods and
  300,000 containers per cluster ([Considerations for large clusters](https://kubernetes.io/docs/setup/best-practices/cluster-large/)).
  A cell of 2,000 to 5,000 nodes is inside the tested envelope, at its edge.
- Large clusters scale the control plane vertically first, one or two instances per failure
  zone, and may move Events to a separate etcd (same page).
- RKE2 sizes its control-plane and etcd nodes as 2 CPU and 4 GB for up to 225 agents, 4 and
  8 for 450, 8 and 16 for 1,300, 16 and 32 above that ([RKE2 requirements](https://docs.rke2.io/install/requirements)).
  The k3s page gives the same shape and adds: join agents "in batches of 50 or less" because
  each join spikes server CPU ([K3s requirements](https://docs.k3s.io/installation/requirements)).

## How others install Kubernetes

| Option | What it is | Offline install | Licence |
| --- | --- | --- | --- |
| [kubeadm](https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/) | Upstream bootstrapper. The operator supplies containerd, runc, CNI plugins, crictl, kubelet units and every image | Possible, but the whole package set is ours to assemble, pin and keep current | Apache-2.0 |
| [RKE2](https://docs.rke2.io/) (SUSE) | A distribution of upstream components as static pods, with containerd, embedded etcd, a CNI and a Helm controller in one binary and one image archive. Hardened to the CIS benchmark, FIPS builds | [Documented air-gap install](https://docs.rke2.io/install/airgap): the binary tarball plus image archives placed in `/var/lib/rancher/rke2/agent/images/` | Apache-2.0 |
| [K3s](https://docs.k3s.io/) | The same project family as RKE2, aimed at edge: single binary, SQLite or external datastore by default, Flannel | Documented, smaller archives | Apache-2.0 |
| [k0s](https://docs.k0sproject.io/stable/airgap-install/) | Single-binary distribution by Mirantis | Documented air-gap bundle | Apache-2.0 |
| [Kubespray](https://github.com/kubernetes-sigs/kubespray) | Ansible playbooks | Offline mode exists | Apache-2.0 |

## Licences

RKE2 is Apache-2.0 (`LICENSE` in [rancher/rke2](https://github.com/rancher/rke2)); its tarball
carries `share/rke2/LICENSE.txt`, which the bundle keeps. The images it ships are containerd,
runc, etcd, the Kubernetes components, CoreDNS, Canal (Calico and Flannel), metrics-server,
the snapshot controller and the Helm controller, all Apache-2.0, and Traefik (MIT), which
ownsolid disables but the core archive still carries. Commercial use, redistribution in the
offline bundle and offline use are all allowed; the bundle must carry the licence texts.

## Chosen approach

**RKE2, stable channel, pinned.** RKE2 v1.36.6+rke2r1 is the `stable` channel release on
2026-11-08 ([channels](https://update.rke2.io/v1-release/channels)). The bundle carries
`rke2.linux-amd64.tar.gz` (41,496,182 bytes), `rke2-images-core.linux-amd64.tar.zst`
(567,518,406) and `rke2-images-canal.linux-amd64.tar.zst` (241,211,711), each pinned by the
SHA-256 in the release's `sha256sum-amd64.txt`. RKE2 rather than kubeadm because a
distribution that already pins and tests containerd, CNI and the control plane together is
what a regulated customer expects, and building that ourselves is a product of its own;
rather than K3s because RKE2 runs the upstream components unchanged and is the family's
datacenter and government build; rather than k0s because the RKE2 air-gap path, its sizing
and its GPU operator support are documented in more depth; rather than Kubespray because it
needs SSH from an Ansible host, and the agent already sits on every node.

**The agent installs it; control orchestrates.** Control picks the server node, then asks
each worker's agent to join. The agent verifies the artifacts by size and SHA-256, unpacks
the binary into `/usr/local`, places the image archives, writes `/etc/rancher/rke2/config.yaml`
(mode 0600) and starts `rke2-server` or `rke2-agent` with systemd. Nothing reaches outside
the bundle.

**No long-lived cluster secret leaves the server.** The server token is generated by RKE2 on
the server node and never sent anywhere. To join workers, the server's agent runs
`rke2 token create --ttl` and returns a bootstrap token in the full format
`K10<CA hash>::<id>.<secret>`, which pins the cluster CA: a joining node downloads `/cacerts`,
compares its SHA-256 with the hash, and sends its credentials only on a match
([RKE2 tokens](https://docs.rke2.io/security/token)). Control hands the token to the joining
agents and it expires on its own.

**Control reads the cluster through the agents.** Each agent reports its own `Node`'s
`Ready` condition, read with the kubelet's kubeconfig, which the node authorizer limits to the
node's own object. The cluster admin kubeconfig stays on the server. Later items that need to
install into the cluster (SCH-107, GPU-105, NET-106) place manifests in the server's
`/var/lib/rancher/rke2/server/manifests`, which RKE2's deploy controller applies.

**Artifacts travel over the existing agent download call**, in 8 MiB ranges at bulk
priority, and control joins workers in batches of at most 50, the figure K3s gives.

## Control side, as built

- **One table per concept.** `cell_clusters` holds a cell's cluster (one per cell, enforced by a
  unique index), `cell_cluster_members` its nodes (a node in at most one cluster, one server per
  cluster). `managed_nodes.enrolled_address` keeps the address an agent enrolled from; a node of
  the Kubernetes profile cannot enroll without a unicast one.
- **Results record; one reconciler writes.** A task result only updates its member. The
  reconciler, every `reconcile_seconds`, locks a cluster (skipping one another process holds),
  settles members whose task ended without a result, asks the server for a token and joins
  workers. Locks go cluster first, then nodes, so a result never waits on another node.
- **Join tokens are sealed at rest.** The token is stored only on the cluster row, encrypted with
  AES-256-GCM under a key derived (HKDF-SHA256) from a key file of its own, with the cluster ID as
  associated data, and added to a worker's lease at claim. Task rows never hold it. The auth HMAC
  key was rejected for this: agent gateways must open tokens and must not hold the session key.
- **Busy and offline nodes.** A node with another task stays pending and joins on a later pass;
  one that is offline or gone fails at once with `NODE_UNAVAILABLE`, so an administrator sees it.
- **Control verifies the RKE2 files** once per file version (SHA-256 of the whole file) before it
  serves any range, so a damaged bundle is refused at control rather than by every worker.

## Prototype

Run on `ownsolid-cpu` on 2026-11-08, outside the product code, with nodes as privileged
Ubuntu 24.04 systemd containers on a Docker network with no route out (`--internal`) and the
pinned artifacts copied in:

- The server became `Ready` 20 to 30 seconds after start; a worker joined in 50 to 70.
  Canal, CoreDNS, metrics-server and the snapshot controller ran on both nodes, offline.
- A worker joined with a bootstrap token of ten minutes. After the token was deleted, both
  the worker and the server restarted and returned to `Ready`: the token is needed once.
- A token whose CA hash was wrong was refused: `CA cert validation failed ... certificate
  signed by unknown authority`. RKE2 then retries forever, so the agent must bound its wait.
- From a worker, the kubelet kubeconfig read the node's own `Ready` condition and was
  refused every other node.
- Each node used about 3 to 4 GB of memory including the unpacked images (in tmpfs there).

## First-start sandbox pull in the pinned release

The Linux amd64 baseline used the repository's [TUN recorder](../decisions/0008-zero-egress-harness.md)
as the only IPv4 and IPv6 default route and tested both routes before starting
RKE2. It recorded six UDP packets to the test site resolver, three pairs of
A and AAAA queries for `registry-1.docker.io`. A syscall trace of containerd
matched the times and the resolver address. The first failed manifest HEAD
for `rancher/mirrored-pause:3.10.2` was about 11 seconds before the core archive
import logged that image. The failed requests came from creating the etcd
and kube-proxy sandboxes. containerd started the sandbox from the local
image after import. The image is shipped as
`docker.io/rancher/mirrored-pause:3.10.2`; the
[RKE2 executor](https://github.com/rancher/rke2/blob/v1.36.6%2Brke2r1/pkg/executor/staticpod/staticpod.go)
uses `index.docker.io`. The earlier experiment with matching spellings
still made a public request. Changing the alias or retagging the store
cannot close the window before the image is imported.

Use [RKE2's default endpoint control](https://docs.rke2.io/install/private_registry)
with an explicit `docker.io` mirror in an Agent-owned registry file. The
[pinned K3s renderer](https://github.com/k3s-io/k3s/blob/c2fdd7c56c2c/pkg/agent/containerd/config.go)
omits an empty mirror, so the offline entry names HTTPS on IPv4 loopback
port zero. The address needs no DNS. On Linux,
[binding port zero selects an ephemeral port](https://man7.org/linux/man-pages/man7/ip.7.html),
so no listener can be assigned to TCP port zero. The built-in Docker Hub endpoint is disabled, and
containerd uses the imported image when it becomes available. A fresh
prototype server using those two RKE2 options reached Ready in 140 seconds
with zero packets in the TUN capture. Its generated `hosts.toml` had only
the loopback mirror. The alternative of blocking traffic at the host
firewall would affect workloads and would still permit DNS attempts inside
the product. Repacking the archive would invalidate its pinned digest.

A node with a site-supplied native `registries.yaml` keeps that explicit
policy. The Agent owns the default fallback configuration and does not
rewrite the site's mirrors or their credentials. A site's custom policy
needs its own offline validation when it is integrated with the installer.
The generated files are written before starting either RKE2 unit. Existing
managed nodes without a native policy pick it up at their next RKE2 start.
A healthy unit is left running on join replay. No artifact, dependency,
model, data set, or agent protocol field changes. The pinned RKE2, K3s
and containerd code use Apache-2.0 with commercial use, redistribution
with notices, and offline use permitted.

The disconnected intranet still needs a site resolver. A longer run with
the resolver address deliberately beyond the TUN recorded CoreDNS's
random HINFO probes and root NS health checks after the server became
Ready. It recorded no Docker Hub lookups. These are the documented
[CoreDNS loop checks](https://coredns.io/plugins/loop/) and
[forwarder health checks](https://coredns.io/plugins/forward/). Keep both
DNS protections and test with a non-forwarding resolver on the isolated
site network. The site resolver logs all questions and returns NXDOMAIN.
A deliberate Docker Hub lookup proves that the resolver log can still
detect a public-name lookup. The external TUN logs remain unfiltered.

The regression checks both roles for the Agent-owned policy and preserves
an explicit native configuration. On real Linux, the recorder must see
zero attempts from a fresh server and worker through wrong-CA refusal,
valid-token retry, and replay. A locally cached test workload must run on
the worker.

With the generated policy on fresh Linux amd64 server and worker nodes,
the server reached Ready in 142 seconds. The worker refused the wrong CA
with `KUBERNETES_NOT_READY`, then used a distinct bootstrap token minted
after the refusal and reached Ready in 86 seconds. A replay kept the
worker PID. The Kubernetes scheduler placed a workload using the locally
cached pause image on the worker. Both IPv4 and IPv6 recorders were
verified with deliberate packets before the run. Each node recorded zero
external packets through the full sequence. After its deliberate Docker
Hub positive control, the isolated site resolver received one CoreDNS
HINFO probe and no other question. Both nodes had only the loopback
endpoint in containerd's Docker Hub host file.

## Reloading a failed join

With a deliberately wrong CA hash, the RKE2 worker refused the token and
continued retrying inside its systemd start job. The next agent join wrote
the correct token to disk but left the same activating PID in place. Its
journal recorded further CA refusals after the new file was written. Queuing
a restart replaced the PID and the worker registered with the same cluster.

[RKE2 reads changed configuration at restart](https://docs.rke2.io/install/configuration).
[systemctl](https://manpages.ubuntu.com/manpages/noble/man1/systemctl.1.html)
can report the active state and restart an existing job. On an installed
node, restart an activating unit when retrying a join. Start an inactive
unit normally and leave an active unit running. Always restarting would
interrupt a healthy worker after a lost result. Leaving an activating unit
alone keeps the old token in memory. This uses the already required
systemd (LGPL-2.1-or-later, commercial and offline use permitted, with
licence terms applying on redistribution). The agent v1 commands, token
validation, journal format and membership constraints remain compatible.

On `ownsolid-cpu`, two isolated systemd nodes shared a Docker internal
network with a dummy default route. The test used the pinned RKE2 Linux
amd64 artifacts, checked their SHA-256, and invoked the agent's Kubernetes
manager directly. A fresh worker with the wrong CA returned
`KUBERNETES_NOT_READY` in 154 seconds including staging and a 60-second
readiness window. A new join with a valid token replaced the systemd PID
and reached Ready in 132 seconds. A replay completed in under one second
and kept the same PID. Kubernetes scheduled a locally available test
image on the worker with image pulling disabled. The worker's kubelet
credential was refused when it tried to read the server node.

This verifies node join and retry. The control-plane artifact transfer
from an installed bundle and the console create, observe and retry flow
still need an end-to-end run.

## Failure modes and how tests cover them

| Failure | Found | Handling | Test |
| --- | --- | --- | --- |
| No default route: RKE2 refuses to start | [Air-gap docs](https://docs.rke2.io/install/airgap) | Agent preflight refuses with `KUBERNETES_DEFAULT_ROUTE_MISSING` before changing anything | Unit test on route parsing; e2e harness gives nodes a black-hole route |
| Default route on another interface: RKE2 advertises the wrong address | Prototype: InternalIP was the dummy interface | Control always sends `node-ip`, the address the node enrolled from | Unit test on the written config; e2e checks InternalIP |
| `node-ip` changed after the server first started: etcd membership no longer matches and the server never comes up | Prototype: `this server is not a member of the etcd cluster` | The address is fixed when the cluster is created and never changed; a server whose config differs is refused, not rewritten | Unit test: a second join with another address fails `KUBERNETES_CONFIG_CONFLICT` |
| Loopback resolver in `/etc/resolv.conf`: kubelet falls back to 8.8.8.8 | Prototype warning | Agent sets `resolv-conf` to `/run/systemd/resolve/resolv.conf` when systemd-resolved is in use, otherwise to `/etc/resolv.conf`, and refuses when the chosen file has only loopback servers | Unit test on resolver selection |
| Wrong CA hash, unreachable server, expired token: RKE2 retries forever | Disconnected Linux run | Join reports a bounded readiness failure with a safe diagnosis. A later join restarts a still-activating unit so it loads the new token | Unit test for retry and active replay; Linux worker join with a wrong CA, then a valid token |
| First sandbox creation can precede the archive import | Disconnected Linux run | The Agent's default registry policy suppresses Docker Hub fallback; containerd uses the cached image after import | Fresh server and worker with dual-stack external packet capture and a separately logged site resolver |
| Artifact corrupted or short | Download | Size and SHA-256 checked before anything is unpacked | Unit tests |
| A node already runs RKE2 that ownsolid did not install | Operator mistake | Refused with `KUBERNETES_CONFLICT`; nothing is overwritten | Unit test |
| Repeated join after a crash or a lost result | Agent restart | Join is idempotent: same config means "observe and report" | Unit test |
| A server takes 1,000 joins at once | K3s sizing note | Control joins in batches of at most 50 | Service test |

## What done means

- From the console, an administrator creates the cell's cluster by choosing a server node
  and worker nodes among the enrolled `kubernetes_node` nodes; the page shows each node's
  join state, and a failed node shows its error and can be retried.
- Control stores the cluster and each member, enforces administrator permission on the
  server, and audits the create and every join.
- On a network with no route out, the e2e test installs the server and a worker from the
  offline bundle, both reach `Ready`, workloads schedule on the worker, and control reports
  both as ready.
- A wrong server address fails the join within the bounded wait with a clear error, and a
  retry after correcting it succeeds.

## Open questions, now roadmap items

- Three servers per cell for a highly available control plane: ARC-115.
- At 1,000 nodes, sending 830 MB to every worker through control is 830 GB per cell.
  Workers should pull images from the cluster's [embedded registry mirror](https://docs.rke2.io/install/registry_mirror)
  instead: ARC-116.
- Upgrade and certificate rotation of the cluster stay with FLT-111.
## Delayed Canal usage report

The earlier server and worker run covered registry fallback and the
join sequence. A longer [ARC-105 test fixture](cell-scale-validation.md#delayed-felix-reporting)
observed the pinned Canal component asking the site resolver for
`usage.projectcalico.org` after the first reporting delay. The resolver
returned NXDOMAIN and forwarded nothing. A zero-egress acceptance run
must cover the delayed window as well as startup, with a site DNS
positive control and separate IPv4 and IPv6 packet recording.
