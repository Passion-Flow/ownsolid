# Managed cell scale validation

Research for ARC-105 and ARC-106. The Agent-side RKE2 installer ran on
disconnected Linux with a site-local resolver and an external packet recorder
([ARC-104](kubernetes-cells.md)). Signed delivery and the console cell flow
remain separate acceptance gates. A developer installation using a local
image archive must be reported separately from either gate.

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
containerd's `k8s.io` namespace. A fixture copy of the deployment uses
the exact manifest digest and `imagePullPolicy: Never` while the verified
upstream manifests stay intact. This exercises the packaged controller
without an image pull or a registry lookup.

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
deployment. The assets and the local deployment stay outside Git and
are used only by the test fixture.

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
