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

The checked host had 16 logical CPUs and 31 GiB RAM, with about 20 GiB
available at inventory time. It was shared, and no KWOK image or pinned
stage manifests were present in its Docker cache or test staging
directories. The repository had no staged KWOK assets either. The 1,000
node coupled run was not started. ARC-105 and ARC-106 remain open.
