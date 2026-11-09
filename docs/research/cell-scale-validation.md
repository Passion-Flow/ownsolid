# Managed cell scale validation

Research for ARC-105 and ARC-106. The Agent-side installer and signed
test release ran with two real RKE2 nodes on disconnected Linux
([ARC-104](kubernetes-cells.md#signed-release-and-managed-api-acceptance)).
The managed console remains a separate acceptance gate. Record developer
installations separately from the signed managed API run.

## Simulation and proof boundaries

[KWOK in an existing cluster](https://kwok.sigs.k8s.io/docs/user/kwok-in-cluster/)
runs a controller and custom lifecycle stages for synthetic Node and Pod
objects. It does not exercise the ownsolid Agent protocol. Use KWOK alongside
the [ownsolid simulator](agent-simulator.md), which enrolls simulated agents,
opens authenticated HTTP/2 sessions and sends actual heartbeats and claims.
The two populations must map to the same cell. Running either tool alone
cannot establish the combined workload. KWOK is Apache-2.0 and its pinned
controller image, deployment manifests, stages and notices must be staged
before disconnecting the test environment.

Begin with a bounded fixture using the existing Docker-in-Docker developer
installer and egress recorder. It runs two actual Agent gateway processes
against one control plane and PostgreSQL. The simulator uses one connection
per agent. Check persisted scope and rejection of wrong site or cell metadata
over the actual TLS gRPC method. Record complete heartbeat windows, session
counts on both gateways, task completion and recovery after losing and
restoring a gateway. A listener stub or the HTTPS fallback alone cannot
establish these properties.

The historical 500-node trial ended before the load started. Isolate the
[Docker daemon startup](https://docs.docker.com/reference/cli/dockerd/) and
capture the container status and managed containerd output before changing any
load parameter. The harness uses local pinned images, an isolated network
namespace and the repository's [egress recorder](../decisions/0008-zero-egress-harness.md).
[Docker Engine](https://github.com/moby/moby/blob/master/LICENSE),
[containerd](https://github.com/containerd/containerd/blob/main/LICENSE),
[VictoriaMetrics](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/master/LICENSE)
and [VictoriaLogs](https://github.com/VictoriaMetrics/VictoriaLogs/blob/master/LICENSE)
use Apache-2.0. [PostgreSQL](https://www.postgresql.org/about/licence/) and
[pgvector](https://github.com/pgvector/pgvector/blob/master/LICENSE) use the
PostgreSQL License. These licences permit commercial use, redistribution
with the applicable notices, and offline use. No new product dependency,
model, data set or protocol field is needed for this smoke run.

## Installation fixture initialization

On the Linux amd64 test host, an isolated Docker 29.1.3 container with the
stock entrypoint and its default data volume exited with code 1, before
the installer started. dockerd had spawned its managed containerd and
logged `timeout waiting for containerd to start`. Docker reported no
OOM kill. The earlier 500-node trial has no retained daemon trace, so
this identifies a reproduced failure stage without assigning its exit
an unverified cause. A Unix-only dockerd started with both a default
data volume and an explicit data directory. The stock entrypoint also
started with an explicit data directory. Those comparisons do not
isolate a single cause of the containerd readiness timeout.

The first run that reached the installer passed preflight, configuration
and image import but failed to start PostgreSQL. The outer test container's cgroup
reported `domain threaded`. Its inner cgroupfs runtime could not create
a child domain with resource controllers. The
[Linux cgroup v2 rules](https://docs.kernel.org/admin-guide/cgroup-v2.html#threads)
explain how an occupied parent with enabled threaded controllers becomes
a threaded domain. In a new private cgroup namespace the fixture placed
its supervisor in a child, enabled CPU, memory and PID controllers on
the empty domain parent, and gave the nested daemon a separate workload
branch. A real nested container started before the installer was retried.
This was scratch fixture configuration and required no product changes.

## Bounded real gateway smoke

On `ownsolid-cpu` (Linux amd64), the developer installer applied a local
Compose configuration and an unsigned image archive. The control binary,
simulator and installer came from `738bcaa2`; the test control image reused
the static assets from local image
`sha256:420fba061485235ef31d367428e2d15beeb56d22a8fb8eb1a710e2662a591fea`.
The fixture ran one control process, PostgreSQL 18 with pgvector 0.8.7,
and two separate `serve-agent-gateway` processes on one host.
No RKE2 or KWOK cluster participated in this smoke. The disconnected RKE2
node experiment is recorded separately in [ARC-104](kubernetes-cells.md).

- All 24 persisted node records and simulator identities had site and
  cell `default/default`. The real TLS HTTP/2 gateway refused independently
  wrong site and cell metadata with gRPC NotFound.
- Every agent connected within 13 seconds from simulator start. The two
  live gateways held 10 and 14 sessions. Two successive fresh-heartbeat
  checks reached all 24 rows in 11.2 and 11.3 seconds with the same
  session distribution. The simulator's first two 15-second windows had
  73 successful heartbeats, zero heartbeat or claim failures, and ended
  with 24 active streams each. The first includes ramp-up.
- Killing the first gateway moved every session to the survivor within
  2.826 seconds measured from the kill request. The following database
  check took 7.5 seconds to see a post-kill heartbeat from every node.
  The fault window recorded
  10 failed opens, nine failed claims and one failed heartbeat.
- After the first gateway returned Ready, all sessions returned to their
  original gateway within 10.532 seconds. Fresh post-restore heartbeats
  from all 24 reached PostgreSQL within 10 seconds. Two steady-state and
  three post-failover facts tasks were acknowledged and completed.
- The run lasted 101.4 seconds including the fixture setup, with 70.7
  seconds in the simulator. The control log contained zero direct
  Agent session opens. The egress recorder saw one deliberate harness
  probe and zero product outbound packets.

## Completion gate

The [ARC-015 baseline](fleet-load-test.md) defines twice the planned
per-node load as a five-second interval, versus the ten-second planning
interval. At 1,000 agents this offers about 200 heartbeats per second
per cell, with claims on the same pacing. Keep the published steady
window criteria and state the actual task cadence, gateway and database
resources, and concurrent Kubernetes workload before the run.

Stage a digest-pinned Linux amd64 KWOK controller image, its manifests
and CRDs, lifecycle stages, licence notices, and the RKE2 release inputs.
Use the [upstream KWOK node management controls](https://kwok.sigs.k8s.io/docs/user/kwok-manage-nodes-and-pods/)
to select only the synthetic Node objects. Use the enrolled simulator
node IDs to correlate those objects with the measured site and cell.
Exercise scheduling and simulated Pod transitions alongside the gateway
load without counting simulated Pod status as real container execution.
Use the existing database counter deltas and dispatch measurement from
the ARC-015 fixture, separating ramp-up, steady load, and failure windows.

At the time of the original gateway smoke, the shared test host had 16
logical CPUs and 31 GiB RAM, with about 20 GiB available. Its Docker cache
and test staging directories held no KWOK image or pinned stages. The
subsequent fixture below stages these inputs separately from the product.

## KWOK offline fixture

Select [KWOK v0.8.0](https://github.com/kubernetes-sigs/kwok/releases/tag/v0.8.0).
The release publishes a Kubernetes 1.36.1 cluster image and its
[Go module](https://github.com/kubernetes-sigs/kwok/blob/v0.8.0/go.mod)
uses Kubernetes 1.36.1 clients. Use the controller in the existing pinned
RKE2 v1.36.6+rke2r1 cluster. The published minor version matches and the
patch-version compatibility needs a real run. KWOK's
[Apache-2.0 licence](https://github.com/kubernetes-sigs/kwok/blob/v0.8.0/LICENSE)
permits commercial use, redistribution with applicable notices, and
offline use. No new product dependency or data set is introduced.

Verify the upstream release asset digests for `kwok.yaml` and
`stage-fast.yaml`. Resolve the OCI index from the upstream Kubernetes
registry, verify its SHA-256 against the registry's digest, and select
its single Linux amd64 descriptor. Fetch and verify that manifest and
every referenced blob on the connected host. Transfer the local OCI
archive into the disconnected RKE2 fixture and import it into
containerd's `k8s.io` namespace. Register the fully qualified digest
reference as a local image alias with `ctr images tag`, and confirm that
`crictl inspecti` resolves it to the verified config digest before applying
the fixture deployment. Archive import alone leaves the exact reference
unavailable to the CRI, which reports `ErrImageNeverPull`. A fixture
copy of the deployment uses the exact manifest digest and
`imagePullPolicy: Never` while the upstream manifests stay intact.

Keep the two packet recorders and the site DNS log from ARC-104. Run a
bounded gateway fixture with the RKE2/KWOK fixture alive. After real
gateway sessions and scoped heartbeats are visible, correlate each
enrolled identity with a KWOK Node of the same UUID, restrict KWOK to
annotated nodes, and check real scheduler placement and simulated Pod
readiness. Require another complete gateway heartbeat observation after
that proof. Report the scratch coordination separately from a managed
cell provisioned through the signed installation and console path.

### Verified inventory

The release's asset digests matched the downloaded bytes. The registry
index digest matched its `Docker-Content-Digest`. The amd64 manifest,
config and each layer matched both the upstream descriptor digest and size.
The verified bytes remain in lane-specific test storage.

| Input | SHA-256 |
| --- | --- |
| `kwok.yaml` | `a4c16e6431e382dcb5c1903139344b7a68652f16a6460337fe17a678a426f405` |
| `stage-fast.yaml` | `2f28d95564ec43056c0873f7a25ac7d2a5bba4c8496c72f8b3ee73fd4f54ee24` |
| Official multi-platform image index | `6d25aa8fbdfe78845423160bf125b5513f9522e2770981f0945c2a250c2b26f0` |
| Official Linux amd64 manifest | `28ee38abba19bd0b89600b1b367c32480da45f1d954c80d14db5fc74feee83f2` |
| Official Linux amd64 image config | `8b980198bbeeb7351c22f0a8ea4d4acd615911d101793c10355b8aafeaa29a13` |
| Local amd64 OCI archive | `410b11561f663afaeb170010ff505c9dc8fed999bc6f9d6ca8620d6bab5c5bbf` |
| KWOK licence | `b40930bbcf80744c86c46a12bc9da056641d722716c378f5659b9e555ef833e1` |
| Local disconnected deployment | `42da4c523b8332e7594a57296b14c834792813307c5e1b0be07752f4798aa83d` |
| Local Felix test AddOn | `5ce998f624bcf15dd3e44905d554f5f68a8354a43c5468b8d3fb89f711be6bf6` |

The disconnected Linux amd64 RKE2 server reported Kubernetes
`v1.36.6+rke2r1`. The stages CRD established and the controller
deployment rolled out from the local archive. The CRI used a local
archive index alias for the running container's reference. Its sole
manifest matched the pinned amd64 digest, and the running image config
matched the official config digest. The original release assets stay
byte-for-byte intact. The pinned RKE2 binary and two image archives also
passed their release SHA-256 checks before server startup.

The verified archives and original manifests are kept on `ownsolid-cpu`
in `/root/lane-arc-kwok.0DKaGD`. Run `sha256sum -c SHA256SUMS` from each
of its `kwok/` and `rke2/` directories, and
`sha256sum -c fixture-SHA256SUMS` from `kwok/` for the derived
deployment and test AddOn. All fixture inputs stay outside Git and
are used only on the test host.

### Eight-node coupled run

The disconnected RKE2/KWOK server and the developer gateway installation
ran in separate isolated network namespaces. The latter used the
control binary from `451319f7` and the same pinned base image as the
24-agent smoke. A scratch harness shared the site, cell and eight
enrolled UUIDs between the fixtures. The two gateway processes handled
real TLS sessions. Independently wrong site and cell metadata were
refused with gRPC NotFound.

- All eight annotated KWOK Nodes reached Ready. The real Kubernetes
  scheduler bound one Pod per enrolled UUID and KWOK made all eight Pods
  Ready. The real RKE2 node stayed Ready and outside KWOK's selection.
  Applying the fixtures and observing placement and readiness took
  5 seconds.
- Steady gateway sessions split 3/5. Two fresh PostgreSQL heartbeat
  observations reached all eight agents in 12.3 and 12.4 seconds.
  A third observation after KWOK readiness reached all eight in
  11.1 seconds.
- After killing one gateway, the survivor held all eight sessions in
  8.436 seconds. After the restored gateway became ready, the split
  converged to 3/5 in 13.631 seconds. Four facts tasks completed and the
  control listener recorded zero direct Agent session opens.
- Both external packet recorders saw zero product packets, with the
  deliberate probe traffic counted separately. The site resolver
  recorded a CoreDNS HINFO question and two public questions (A and
  AAAA) for `usage.projectcalico.org`. Felix reported the failed lookup
  during its first usage report attempt. The combined egress gate
  failed. The site resolver never forwarded the questions.

The fixture demonstrates compatibility with the tested Kubernetes
patch version and the scratch identity correlation. Signed delivery and
the managed console cell flow are still required for the acceptance run.
The shared Linux host had 16 logical CPUs and 31 GiB RAM. While both
fixtures were live, free memory dropped below the briefing's 2 GiB
threshold twice (about 1.6 GiB and 1,947 MiB), with 18–19 GiB available
and no free swap. The 1,000-node run at twice the load did not start.
These observations do not establish dispatch p99, reconnect time or
database write throughput at the target scale.

### Delayed Felix reporting

The pinned RKE2 release installs the Canal chart
`v3.32.2-build2026102200`. Its installed chart had no values field
for setting an arbitrary Calico environment variable or disabling
usage reporting. [Felix v3.32.2](https://github.com/projectcalico/calico/blob/v3.32.2/felix/config/config_params.go)
enables reports by default and waits at least 300 seconds.
[The reporter](https://github.com/projectcalico/calico/blob/v3.32.2/felix/usagerep/usagerep.go)
starts that timer after receiving stats and configuration and adds
host-count jitter. A recorder check at the Kubernetes Ready boundary
can miss the first attempt.

For the follow-up fixture, place a
[`FelixConfiguration`](https://docs.tigera.io/calico/latest/reference/resources/felixconfig)
named `default` with `spec.usageReportingEnabled: false` in the RKE2
[server manifest directory](https://docs.rke2.io/install/packaged_components)
before starting a fresh server. Use the `crd.projectcalico.org/v1`
API installed by the pinned chart. The
[pinned AddOn deploy controller](https://github.com/k3s-io/k3s/blob/c2fdd7c56c2c/pkg/deploy/controller.go)
rescans every 15 seconds and records a file's processed timestamp only
after a successful apply. This lets the AddOn retry while the CRD is
being established. [Felix starts the reporter](https://github.com/projectcalico/calico/blob/v3.32.2/felix/daemon/daemon.go)
only when the setting is enabled. This preserves the official chart and
image bytes. Resolver blocking by itself still permits a public-name
lookup, and a patch to the generated DaemonSet could race startup or
be overwritten by chart reconciliation.

The fresh Linux amd64 server reached Ready in 127.09 seconds. The AddOn
recorded `ApplyManifestFailed` while the requested resource was absent,
then `AppliedManifest`. The Canal DaemonSet became ready and Felix logged
the disabled setting. The disconnected deployment then brought the same
eight KWOK Nodes and eight scheduled Pods to Ready. This follow-up ran
the RKE2/KWOK side alone to isolate the source of the earlier DNS
questions. The AddOn is a test fixture input and is absent from default
Agent provisioning.

The startup capture and 331 seconds after KWOK readiness recorded one
CoreDNS HINFO question and no public DNS questions or external IPv4
or IPv6 packets. Before startup, the site resolver recorded a deliberate
lookup for the same Calico hostname and the TUN recorder detected
deliberate packets on both IP routes. Felix logged no usage reporter startup
or report attempt. The site DNS and packet logs are retained under
`observations/` alongside the staged fixture.

## Signed managed cell scale fixture

The [ARC-104 signed API run](kubernetes-cells.md#signed-release-and-managed-api-acceptance)
provides the control-plane and real-node fixture. The load fixture reuses
its test-signed package, verified installer, and two real Kubernetes
members in the same isolated site. It adds a second Agent gateway and
preloads the pinned KWOK archive into the managed RKE2 server. Only the
simulator's 1,000 enrolled UUIDs are selected by KWOK. The actual
Kubernetes scheduler binds one simulated Pod per UUID while the Agent
simulator is active. Each Ready Pod, Ready synthetic Node and persisted
Agent identity must match the same scope and UUID.

Keep ten complete 60-second windows after the fleet and the Kubernetes
workload are both ready. Offer heartbeats and claims at the five-second
pacing and distribute one hundred facts tasks per minute across the
fleet. Judge connected and on-stream counts, failed requests, heartbeat
p99, and dispatch p99 against the ARC-015 steady thresholds. Measure
gateway loss to full stream recovery on the survivor, fresh persisted
heartbeats after the loss, and redistribution after the gateway
returns. Record the exact scope, count, and duration of every sample.
Take [PostgreSQL 18 database statistics](https://www.postgresql.org/docs/18/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW)
at steady and recovery boundaries. Report commits separately from the
sum of inserted, updated and deleted rows per second, and report WAL
bytes per second as a separate measurement.

Keep the three unmodified packet logs and the site DNS log from before
provisioning through the load. The dedicated test entry point checks
one exact IPv4 probe and one exact IPv6 probe before allowing a zero
product-packet result. A duplicate probe, any other external packet,
a public-name DNS query, or a nonzero test process status fails the
gate. Retain the simulator report and resource samples even on failure.

Before starting, check disk, host MemAvailable and free memory, and
the DEL recovery workload. Reserve 8 GiB for the nested Control,
PostgreSQL, two gateways and simulator, 6 GiB per real RKE2 node and
2 GiB for the host. Require at least 22 GiB MemAvailable, 2 GiB free
memory and 30 GiB free disk, with full I/O PSI avg10 at most 5%.
Before starting the generator, require 8 GiB host MemAvailable and
2 GiB of effective cgroup headroom for the control fixture and 1 GiB
for each RKE2 node. Read the live
[cgroup v2 memory limits and counters](https://docs.kernel.org/admin-guide/cgroup-v2.html#memory-interface-files)
including finite ancestor limits. Sample every five seconds and stop
the fixture if DEL starts or new memory-limit or OOM events appear.
These fixture budgets are separate from product capacity claims.

### Prepared launch and admission result

The launch and observation package is staged privately at
`/root/lane-arc105-launch.XuP5zk` on `ownsolid-cpu`. Run
`bash launch.sh --check` in that directory to record a fresh admission
sample. The explicit `--run` action uses the same gate before copying
the signed test archive or starting containers. The runner sources
and Linux amd64 simulator and test binaries passed SHA-256 checks.
The signed media and KWOK inputs stay in the previous ARC fixture
directories. The prepared runner carries no signing or enrollment key.

While DEL recovery was active, the host had about 20.6 GiB
MemAvailable and 357 MiB free memory. Full I/O PSI avg10 reached
45.77% in the sampled refusal. A second check after DEL finished
recorded 20.78 GiB MemAvailable, about 5.03 GiB free memory,
about 809 GiB free disk and 0.70% full I/O PSI avg10. The Docker
systemd parent slice had unlimited `memory.high` and `memory.max`.
The second admission check exited 75 on the 22 GiB host reservation.
A further check after staging returned 75 with 20.80 GiB MemAvailable
and 18.88% full I/O PSI avg10. A later check still exited 75 with
20.77 GiB MemAvailable, about 822 GiB free disk, 0.05% full I/O PSI
avg10, and no active DEL restore. No fixture container or live test
cgroup was created.

The launcher refusal path was tested before fixture creation. Synthetic
verification checked the full set of 1,000 identity, Node and Pod
mappings, wrong-scope and wrong-node refusals, finite cgroup ancestors,
and changed OOM counters. The dedicated Go test entry point accepted
the prior unmodified ARC-104 dual-stack probe capture and rejected an
extra IPv6 packet with exit 1. The original ARC-104 generic entry
point also exited 1 after rejecting the deliberate IPv6 probe. That
result and the original raw capture are unchanged. No dispatch,
reconnect or database write-rate measurement was collected at
1,000 nodes.

### Host resource attribution

A read-only follow-up on the same Linux host checked the Docker systemd
cgroup v2 paths, active mount namespaces, loop devices, and open fixture
files. Neither the Docker daemon nor its parent system slice had a
finite memory limit. The parent held 5.66 GiB, including 3.17 GiB of
shmem. The user slice held 11.87 GiB, including 5.20 GiB of shmem.
These are shared cgroup totals. No ARC fixture process, live container
cgroup, mount, or loop device was present.

The five ARC fixture directories were on ext4. The core, Canal and RKE2
archives visible under three fixture paths were already hard-linked to
one inode per archive. An inode-deduplicated [mincore snapshot](https://man7.org/linux/man-pages/man2/mincore.2.html)
found 106.38 MiB of resident file cache across all five directories,
including 21.41 MiB for the signed media file. The three hard-linked
archives had no resident pages in that snapshot. On the host's
ext4-backed Docker store, two untagged image IDs had the same complete layer
list as the node base image. Their ownership was unverified and no
container referenced either ID. The DinD base had no matching layer
list under a different image ID. These image records provided no
evidence of a resident duplicate. The signed media and the KWOK
OCI archive are separate offline inputs. The large tmpfs filesystems
visible in the host mount namespace belonged to lane B. Their combined
16.26 GiB of allocated blocks can include swapped pages.
Host Shmem was 8.37 GiB and about 172 KiB of swap remained free. See
the kernel accounting for [tmpfs](https://docs.kernel.org/filesystems/tmpfs.html)
and [MemAvailable](https://docs.kernel.org/filesystems/proc.html#meminfo).

No ARC resources warranted removal or migration. The cache snapshot
was smaller than the available-memory deficit, and Linux already
accounts for reclaimable file cache in its MemAvailable estimate.
The same gate returned 75 before and after the inspection. MemAvailable
was 20.80 then 20.77 GiB, free memory was 12.27 then 12.24 GiB, and
disk stayed about 822 GiB. Full I/O PSI avg10 was 0.02% then 0.01%.
No active DEL restore appeared in either sample. These are host
samples taken without a capacity intervention. A later check again
returned 75 with 20.75 GiB MemAvailable and 30.36% full I/O PSI avg10.
No load was started and no fleet KPI was measured.

The private runner retains the samples, inode and cgroup inventory,
image identity comparison, and checksums under `resource-attribution.V2Dg47`.
The prior packet and DNS captures remain in place and passed a checksum
recheck. Admit a formal run only when the original host gate passes
with at least 22 GiB MemAvailable, 2 GiB free memory, 30 GiB free disk,
full I/O PSI avg10 at most 5%, and no competing heavy work. The live
cgroup headroom gate still runs before the generator. The 1,000-agent
count and twice-load schedule are unchanged.
