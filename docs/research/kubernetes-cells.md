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

### Bootstrap ordering for the packaged Canal chart

RKE2 `v1.36.6+rke2r1` embeds Canal
[`v3.32.2-build2026102200`](https://rke2-charts.rancher.io/assets/rke2-canal/rke2-canal-v3.32.2-build2026102200.tgz).
The exact release chart has no Helm value for `FELIX_USAGEREPORTINGENABLED`
or an arbitrary container environment entry. Felix v3.32.2 reads
[`FelixConfiguration/default`](https://docs.tigera.io/calico/latest/reference/resources/felixconfig)
from its datastore before starting the reporter. With
`spec.usageReportingEnabled: false` the
[reporter is not constructed](https://github.com/projectcalico/calico/blob/v3.32.2/felix/daemon/daemon.go).
Calico's startup fills missing logging defaults without resetting the
usage-reporting field. Environment, file, selector and node settings
retain their native precedence.

A regular AddOn for the setting can race the first Canal Pod because the
chart installs the CRD. Use the [RKE2 AddOn skip mechanism](https://docs.rke2.io/install/packaged_components)
to hold only the packaged Canal manifest on a fresh server.
[RKE2 stages the manifest from its verified runtime image](https://github.com/rancher/rke2/blob/v1.36.6%2Brke2r1/pkg/bootstrap/bootstrap.go)
before the AddOn controller applies it. Extract only the Felix CRD
from the staged HelmChart's inline archive, create it with
[Helm release ownership metadata](https://github.com/helm/helm/blob/v4.0.0/pkg/action/validate.go),
and set the default through the local API before releasing the hold.
The pinned [K3s deploy controller](https://github.com/k3s-io/k3s/blob/c2fdd7c56c2c/pkg/deploy/controller.go)
rescans the directory and applies the original chart when the hold is
removed. The disconnected prototype extracted the same CRD bytes as the
release chart and installed the untouched packaged HelmChart successfully.
The first Canal Pod logged the disabled setting.

RKE2 briefly stages charts that the site has explicitly disabled. File
presence alone cannot establish that Canal was selected. Once its
manifest appears, publish an Agent-owned comment-only AddOn after it
in the deploy controller's sorted manifest order. Wait for the checksum
of this attempt before inspecting the Canal file again. A disabled
chart is removed by that scan, while an enabled chart is still held
by the skip file. This prevents the Agent from creating a Canal-owned
CRD in a cluster that uses a site-selected CNI. The marker declares no
application resources and is removed when bootstrap completes.

The Agent owns its temporary hold and resumes it after a failed join.
Create the CRD only if absent. Create the default resource if absent, or
set only an unset usage-reporting field with a resource-version check.
This keeps site fields and explicit boolean settings, including
concurrent updates. A native skip file and existing Helm chart and
registry configuration remain site-owned. A healthy pre-existing
cluster has no bootstrap hold. Existing v1 workers use the same join
protocol and pick up the server's persisted Felix configuration. This
adds no dependency, model or data set. The Agent reads the chart from
the pinned [RKE2 distribution](https://github.com/rancher/rke2/blob/v1.36.6%2Brke2r1/LICENSE).
[RKE2's K3s deploy controller](https://github.com/k3s-io/k3s/blob/c2fdd7c56c2c/LICENSE),
[Helm](https://github.com/helm/helm/blob/v4.0.0/LICENSE) and
[Calico](https://github.com/projectcalico/calico/blob/v3.32.2/LICENSE.md)
use Apache-2.0, which permits commercial and offline use and
redistribution with applicable notices.

A missing API, unestablished CRD or refused write must leave the hold
in place and report a bounded readiness failure. Verify the ordering
on a fresh server and worker using the Agent join path. Run wrong-CA
refusal, retry with a distinct token, healthy replay and a cached Pod
scheduled to the worker. The disconnected site resolver and the IPv4
and IPv6 capture must cover startup and at least 330 seconds with both
Canal Pods ready.

### Agent startup regression

The final implementation ran on two fresh Linux amd64 systemd nodes
on `ownsolid-cpu`, with the pinned RKE2 archives and the Agent join
manager. Neither host had a pre-existing Canal hold, scan marker or
Felix configuration. The server reached Ready in 90.94 seconds. The
Agent-created Felix configuration preceded the packaged Canal DaemonSet
by 15 seconds. The original chart deployed and adopted the Felix CRD.
The temporary marker and hold were removed, and a healthy server replay
kept the original PID and Felix resource.

The fresh worker refused the wrong CA in 76.31 seconds including
installation. The journal identified the CA mismatch, and the worker
did not register. The next join used a distinct bootstrap token,
replaced the activating PID, and reached Ready in 41.67 seconds. An
active replay retained the PID. Kubernetes scheduled a cached pause
Pod to the worker and it reached Ready with a pod-network IP.
Both first `calico-node` containers reported zero restarts when the
observation window began.

Packet capture started before provisioning after each node passed a
deliberate IPv4 and IPv6 probe. The non-forwarding site resolver also
logged deliberate Docker Hub and Calico questions before the product
window. Through startup and 331 seconds with both Canal Pods and the
worker workload ready, each node recorded zero external packets. The
resolver recorded two CoreDNS HINFO loop probes and zero public-name
questions. Both first Felix containers logged the disabled setting and
neither started the reporter. The packet and DNS evidence is retained
on the test host under
`/root/lane-arc-felix-final.v1xoN1/evidence`. The shared host reached
1,428 MiB free memory during the run. No scale load was started.

## Signed release and managed API acceptance

Use the [signed delivery handoff](rke2-signed-delivery.md), the
[cell-clusters API](../../api/openapi/v1/cell-clusters/openapi.json),
and the [Agent gateway arrangement](agent-gateway-tier.md). The
acceptance fixture uses the release verifier and Compose installer,
real Control and PostgreSQL, a separate TLS Agent Gateway, and two real
Kubernetes-profile Agents. It leaves provisioning to the API and the
Agent tasks. The node containers start with clean systemd state and
receive only the Agent executable and enrollment inputs. This exercises
artifact authorization and installation together. A direct Agent manager
probe remains useful for first-start diagnostics and cannot exercise
the control-plane task authority.

On `ownsolid-cpu`, the repository factory manufactured an isolated
test release with an ephemeral signer under the `cb83eb64` delivery
contract. The installed Control, installer and Agents came from
`4a3f0aba`. The Control image reused the cached amd64 base's static
assets. The test used the release's packaged installer after
`prepare-bundle` with `require_rke2: true` verified the signature and
payloads on a separate filesystem. `apply-bundle` installed PostgreSQL,
telemetry and Control from the verified archive. The release signing key
was outside the runtime mounts. The generated cell join-token key was
outside the release. Control and the Agent Gateway had a read-only
mount for the approved artifact directory. This fixture verifies the
original RKE2 archive contract. The later reviewed component inventory
is tracked separately in [DEL-101](rke2-component-inventory.md).

| Verified identity | SHA-256 |
| --- | --- |
| Signed test archive | `3b02a9934f2adec70cdc614b2a9764ddb111a226609ec1e92c0aac2284d9bb90` |
| Signed manifest | `b4127041ce56f7517aa8492306387c87f10a9eecaf5e617418d1f3d64a29823f` |

- Both real Agents opened TLS sessions on the separate gateway. Admin
  API creation assigned the server and the server join settled Ready
  after 112 seconds. The stored enrollment address matched the real
  site interface. Its returned CA hash matched the RKE2 server CA.
- Temporarily remove one approved file from the fixture's artifact
  directory and add the worker through the members API. Its first join
  settled with `KUBERNETES_ASSET_UNAVAILABLE` after 13 seconds, and the
  task log identified the missing binary archive. Restoring the original
  verified bytes and invoking member retry yielded a distinct join task
  and a Ready worker after 71 seconds. The API retained both attempts.
- Both successful join task logs named exactly the three approved
  RKE2 files. The two installed image archives on each node matched
  the upstream SHA-256 values, and the installed binary reported the
  pinned release. With a real Agent certificate and credential, the
  scoped artifact route refused the signed but unapproved checksum
  filename with `400 VALIDATION_FAILED`. It refused an approved filename
  for a task that did not exist with `404 RESOURCE_NOT_FOUND`.
- The observations API refreshed both members' Ready timestamps and
  returned to two Ready members. Kubernetes also reported both real
  Linux amd64 nodes Ready. Felix's offline configuration preceded the
  Canal DaemonSet by 14 seconds. The real scheduler placed the cached
  pause Pod on the worker, where it reached Ready with a pod-network IP.

The captures ran from before signed preparation through 334 seconds
with the worker workload and both Canal Pods Ready. The two
`calico-node` containers kept their original start times and each
logged the disabled reporting setting. The site resolver received two
internal CoreDNS HINFO questions and zero public-name questions.
The server and worker packet logs were empty. The Control host log
contained exactly the two deliberate IPv4 and IPv6 probes.

The managed Go test body passed. The generic e2e process exited 1
because its `TestMain` recognizes the IPv4 test subnet only and
classified the deliberate IPv6 probe as outbound product traffic.
An independent replay checked the unchanged raw captures, allowing
exactly one packet to each deliberate probe destination. It rejected
additional IPv4, IPv6 and duplicate probe packets. The API test log,
the nonzero wrapper result and both egress checks remain in
`/root/lane-arc-managed.JFUrp5/capture` on `ownsolid-cpu`.
A query escaping issue in the scratch Pod check was corrected against
the same running nodes before the timed observation. The capture
files and their start boundaries were retained across that correction.

The shared host was checked before manufacture and before the
isolated run, with no DEL test containers running. Free memory
dropped below 2 GiB during staging and testing, so only ARC-owned
file cache was reclaimed before subsequent heavy steps. This run
did not include fleet load. The managed console remains a separate
ARC-104 acceptance gate.

## Signed site Registry consumption by a managed worker

The [DEL-108 signed Registry](signed-registry-bootstrap.md) verifies and
serves original OCI manifests and blobs from private IPv4 TLS. Its
cold Docker test does not exercise the RKE2 kubelet or the Agent's
site-supplied registry configuration. Reuse the existing signed API
cell fixture and install a separate signed Registry test release in
its isolated site. Keep the RKE2 runtime and system image sets on
their approved artifact path. Require the worker's containerd to
start without the signed workload image and let kubelet fetch it.
A cached image, a Docker pull, or a successful registry health check
cannot prove that path.

[RKE2 private registry configuration](https://docs.rke2.io/install/private_registry)
places endpoint authentication and the trust file in the native
`registries.yaml`. Leave this site file and its permissions intact
during Agent provisioning. Use a site-owned
[RKE2 configuration drop-in](https://docs.rke2.io/install/configuration#multiple-config-files)
to disable default endpoint fallback for configured mirrors. Test a
Docker Hub image name through a configured mirror and require the
local manifest refusal without any public DNS lookup or external
packet. RKE2 limits the switch to configured mirrors, so this test
does not establish a policy for arbitrary unconfigured registry names.

Use the digest reference printed by the verified Registry installer
and [OCI digest semantics](https://github.com/opencontainers/distribution-spec/blob/v1.1.1/spec.md#pulling-manifests).
With the target absent, reject a wrong site CA, an anonymous request,
and a missing manifest by digest. Check the actual containerd errors
and the absence of the signed workload in the store after each
attempt. Restore the protected site configuration and let the real
scheduler bind a Pod on the intended worker. Require the Pod to reach
Ready with the workload's [HTTP readiness endpoint](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/master/docs/victoriametrics/url-examples.md#-ready),
and match its running image to the signed manifest and CRI config identity. Keep the raw DNS
and packet captures and validate the exact deliberate probes.

RKE2, containerd and CNCF Distribution use Apache-2.0. The Registry
image's transitive notices and conditions for commercial redistribution
and offline use remain in the [DEL-108 inventory](signed-registry-bootstrap.md).
Use the existing signed workload images and notices without adding
a product dependency, model, dataset, protocol field, delivery change
or Helm change. The test signer's key stays outside the release mount.
Supply Registry credentials and the TLS key only through site-owned
private files. Do not put either in a Pod, command line or retained log.

### Registry consumer fixture and first refusal

The private runner on `ownsolid-cpu` is
`/root/lane-arc104-registry.JFfDHO`. It reused the earlier signed RKE2
test archive and manufactured a distinct Registry test release with
the `8d02a017` factory. The Registry archive SHA-256 is
`f325071c67b59b78cf4cacaf5cce5761b1a01e7d97e90dfcddd305630a42f626`
and its signed manifest SHA-256 is
`16052a9220114f7916c2be623aef90ca2d9b74ee9e690299d64ce14a6cfced47`.
The first factory attempt refused the staging directory permissions.
After changing the private staging directory to mode 0700, the factory
signed the archive and passed a separate archive inspection. The Registry
installer subsequently published four signed image references in the
isolated site. The target was the metrics image at digest
`sha256:37677492c6e08c70b91df8d0661ec14bd8da9163a4ed8e2bebb3f9a84cd64b84`.
These test-signed inputs do not qualify production release inventory.

The consumer fixture reserves 18 GiB MemAvailable for the 6 GiB
Control and Registry container, two 5 GiB node containers, and a 2 GiB
host reserve. It also requires 2 GiB free memory, 35 GiB free disk,
full I/O PSI avg10 at most 5%, and no competing heavy work. The
ARC-105 22 GiB gate and scale fixture are separate. The first consumer
start refused 5.55% I/O PSI. The next attempt exposed a fixture bug
that used systemd child cgroups as container budget boundaries.
After changing the monitor to check the Docker scope's actual cap,
the Registry published and the RKE2 archive verified. The live
monitor then detected a DEL test entering its private namespace and
stopped the ARC fixture before the worker pull phase.

The final recovery started after DEL was absent. SSH initially
worked, MemAvailable was about 20.50 GiB, free memory about 4.46 GiB,
and full I/O PSI avg10 was 0.03%. The first test failure was
`RELEASE_PREPARATION_OCCUPIED_OR_UNCERTAIN` from
`prepare-bundle` at `/release/arc-prepared`. The interrupted
attempt had already prepared that path. The Go process and launcher
returned 1. The strict Control recorder check passed for its two
exact probes. The pull proof had not started, so this run supplies no
wrong-CA, anonymous, unknown-digest, fallback, or Pod result. Preserve
the earlier prepared files and use a distinct preparation path on the
next permitted run. Subsequent SSH evidence collection timed out
during the banner exchange, with exit 255. No further test was started.

The private run directory is `cell.QShAJf` under the runner. Keep
its separate attempt captures and the actual nonzero wrapper result.
The managed worker path, the ARC-104 console, and DEL-108's remaining
consumer and release gates are open.

### Cold worker retest

Short-timeout SSH and the host gate passed with about 20.50 GiB
MemAvailable, 4.24 GiB free memory, 816 GiB free disk and zero full
I/O PSI avg10. The new Control, server and worker scopes had
6.00, 4.98 and 4.98 GiB of effective memory headroom, respectively,
before releasing the installer. The 54 live resource samples showed
no gate refusal or fixture OOM kill.

The retest reused the verified signed media and allocated an exclusive
preparation output under
`/release/arc104-consumer-arc104-registry-cold-1ntmgu-3171725157/approved`.
It verified the RKE2 archive, completed the signed Compose install
and enrolled two Agents on the TLS gateway. After successful cell
creation, the first failing operation was the member-list read
`GET /api/v1/sites/default/cells/default/admin/cell-clusters/{id}/members`
while waiting for server readiness. It returned HTTP 409
`STORAGE_COMMIT_OUTCOME_UNKNOWN`, request ID
`73e99a41-76f4-4d49-9234-642ee3a8ef78`. The test and launcher both
exited 1. The strict Control packet check accepted its two exact
probes. The worker addition, cold pull, readiness and no-fallback
checks had not started.

The raw evidence is retained on `ownsolid-cpu` at
`/root/lane-arc104-registry.JFfDHO/cold-retest.IqKiOM/run.1ntmgu/capture`.
The prior packet and DNS checksums still matched and the old
`/release/arc-prepared` directory retained its inode and mode.
The isolated containers and network were removed and the media
unmounted. No retry or scale run was launched.

### Member-list transaction diagnosis

For request `73e99a41-76f4-4d49-9234-642ee3a8ef78`, Control logged
HTTPS transport admission at 03:00:11 UTC. The client received the
HTTP 409 problem with that request ID. The gateway logged
`SERVICE_NOT_READY` at 03:00:21 UTC. Control also emitted an uncorrelated
process failure with the same storage code. The test exit was written
at 03:00:35 UTC. The fixture HTTP client had a 30-second timeout and
received the problem before its transport returned an error. The
packaged Control defaults to a 15-second request policy, and the fixture
provided no override. The capture
has no per-phase request timing, PostgreSQL backend PID, original
PostgreSQL log, or underlying pgx commit error. It cannot establish
which transaction failed or whether a SQL connection closed, a server
deadline expired, or a reply was lost.

[Members](../../services/control/internal/modules/cellclusters/reads.go)
runs its two reads through
[`adminWithin`](../../services/control/internal/modules/cellclusters/service.go).
The authority recheck takes locks on the user and session, so the
wrapper uses a read-write transaction even for this GET. The
[PostgreSQL adapter](../../services/control/internal/platform/database/transaction/postgres.go)
checks the context after a successful callback, then maps commit
errors other than a known rollback to the same unknown outcome.
The HTTP response preserves the category and loses the underlying cause.

The small fixture called the actual member GET handler over local
HTTPS with a real synthetic administrator session. It seeded a
single joining member with SQL in a disposable PostgreSQL 18.6
container on Linux arm64. It did not call the create route or run
the full Control router and lifecycle. Three repetitions produced
the following results.

| Fault point | PostgreSQL or driver observation | HTTP result |
| --- | --- | --- |
| No fault | Member read and COMMIT completed | 200, one joining member |
| Member query waiting for a lock | SQLSTATE `55P03` before COMMIT | 503 `STORAGE_UNAVAILABLE` |
| Request deadline during the member query | Driver deadline before COMMIT | 503 `STORAGE_TIMEOUT` |
| Connection cut before COMMIT reached PostgreSQL | One COMMIT intercepted, zero forwarded | 409 `STORAGE_COMMIT_OUTCOME_UNKNOWN` |
| Successful COMMIT reply suppressed | One COMMIT forwarded, server completion observed | 409 `STORAGE_COMMIT_OUTCOME_UNKNOWN` |
| Context cancelled during the COMMIT call | Driver cancellation after the pre-commit check | 409 `STORAGE_COMMIT_OUTCOME_UNKNOWN` |
| Request deadline during the COMMIT call | Driver deadline after the pre-commit check | 409 `STORAGE_COMMIT_OUTCOME_UNKNOWN` |

The [PostgreSQL lock timeout](https://www.postgresql.org/docs/18/runtime-config-client.html#GUC-LOCK-TIMEOUT)
and [CommandComplete](https://www.postgresql.org/docs/18/protocol-message-formats.html#PROTOCOL-MESSAGE-FORMATS-COMMANDCOMPLETE)
controls were observed on real protocol traffic. Every GET kept the
persisted member unchanged. The fault proxy saw a single COMMIT in
each wire-fault case. The fixture source, script, request timeline,
PostgreSQL log and test log are under
`lane-arc-diagnostics/arc104-member-read-73e99a41` beside the lane worktree.
Run `bash reproduce.sh /absolute/path/to/lane-arc` from that directory.

For the next isolated read, the cell-cluster and database owners should
correlate the request ID with authorization, member query and commit
phases, the PostgreSQL backend PID and matching server logs, remaining
context budget, and the sanitized commit error type or SQLSTATE. Track
the Control process failure separately from the HTTP request until the
two share a traced cause. The controlled results distinguish query-stage
failures from commit-stage failures, while the original trigger remains
unknown. No production or cold-run test code was changed and no cluster
write was replayed.
