# Lane D decisions

## CLI integration

Add the CLI as a Go workspace module. Register its directory in the repository
layout and its module in the existing build and test list. An isolated module
with `GOWORK=off` would compile in its own directory but would fail the requested
root-level build and bypass repository CI. These additions affect shared
build enumeration and leave existing module entries in place.

Use the existing Admin identity and resource contracts. A deployment name means
the exact model name in the selected workspace and succeeds only when it
identifies one model and one retained deployment. A deployment UUID addresses
the existing detail endpoint directly. Adding an independent deployment name
would require a schema, UI and API change. If model names prove too ambiguous,
the consequence is an extra lookup for the operator, who can select by UUID.

## TLS trust on macOS

Require a local CA file for HTTPS on macOS and verify against that trust pool.
Go's macOS system verifier can fetch missing certificates. Relying on it would
allow an outbound connection outside the selected Admin origin. The tradeoff is
one local configuration field for Mac operators. Linux can use its local
system roots or the same explicit CA file.

## DEV-102 roadmap completion boundary

The original eight commands remain the read and session baseline. Prioritize
the Admin Nodes, Models and Model Services workflows that an operator needs
when SSH is the only interface.

| Priority | Admin workflow | CLI scope or boundary |
| --- | --- | --- |
| 1 | Prepare, pause and resume a worker | Issue, list and revoke enrollment invitations. Read and update node status and name. Remove a node, collect facts and inspect its tasks and logs. |
| 1 | Run and maintain an approved model | Inspect GPU facts and placement, refresh GPU observations, create a deployment, and start, observe, verify, stop or remove it. Inspect the model manifest and retire an unreferenced version. |
| 2 | Bring a new approved model into the installation | The CLI uploads, resumes, verifies and cancels imports through the existing Admin API. The real verifier published the approved Qwen3-0.6B archive in the disconnected fixture. |
| 2 | Publish a deployment to callers | The CLI lists provisioned gateways and publishes or disables aliases with persisted Admin TLS evidence. Starting the agent-managed gateway and delivering its certificate and credential depend on GW-114. Caller credential handoff and serving traffic need the installed gateway. |
| 2 | Manage node monitoring components | The CLI manages node exporter tasks and reads their journal snapshots. The disconnected fixture used both pinned archives, PostgreSQL, VictoriaLogs and synthetic agent reports. The installed agent's systemd lifecycle needs the exporter delivery path in FLT-109 and its own target-host run. |

Keep the broader roadmap item open. Splitting these workflows preserves the
existing Admin contracts and puts the maintenance path in operators' hands
while the remaining paths receive their own end-to-end verification.

The lane briefing reserves the roadmap index to the coordinator. A checkbox
change alone makes the progress-table check fail. Record the evidence, update
the status table and refresh the index in the same change as any tick.

## SDK delivery and roadmap completion

Generate local Go and Python source packages from the current public OpenAPI
contracts. Build against pinned generators and install offline from a prepared
module cache or wheelhouse. Keep the projection in the generator and leave
the source contracts in their owning lanes. Direct generation fails on
boolean subschemas and otherwise drops some Python media types. The
generator-only compatibility settings preserve the public operations and
wire media types. A future contract that cannot be projected safely fails
generation rather than silently losing an operation.

The DEV-103 implementation has its own [SDK evidence](sdk.md). Its scope is
generation from the currently published API. DEV-101 tracks whether the
published API covers the whole product. The DEV-103 tick needs the
coordinator's index and status updates in the same revision.

## Remaining P1 dependency boundaries

| Item | Boundary before implementation can complete |
| --- | --- |
| DEV-101 | A complete public API needs the remaining P1 product owners to deliver their operations and permissions. The SDK generator covers the public contracts present now. |
| DEV-104 | The monitoring contract exposes reads and does not yet expose an alert event source. A delivery path needs authorized event producers and persisted subscriptions before it can cover the four required event families. |
| DEV-105 | The workspace contract has no notebook runtime or GPU admission flow. The user flow needs a workspace runtime owner and Portal integration. |
| DEV-106 | SSH and IDE access require an existing development container lifecycle and workspace-scoped credential authority. Neither is exposed by the current workspace contract. |
| DEV-107 | The current registry owns approved images. This item also needs an isolated build worker, a trusted build result admission path and an operator UI. |
| DEV-108 | Delegated tenant authority, quota admission and a durable job lifecycle must be available together before a callback integration can offer the full compute workflow. |

These boundaries rule out marking a partial CLI command, raw transport or
schema as the finished feature. Each capability needs its owning product
path and verification before the roadmap tick.

## Full repository test on a shared workstation

The lane snapshot based on `e1dba7e` (`6a16b43` before rebase) passed
`make check`, `make build`, and the full `make test` target. The browser
recipe used the repository's Playwright configuration on private loopback
ports, with both applications' complete suites. Console passed 1,029 cases
and Portal passed 372, with 27 browser cases skipped by default. The Make
target continued through every Go module, the Rust gateway and web unit
tests.

On the first run, a file-backed APFS scratch volume gave the control
object store adequate free space, but three Agent training tests missed
their 150 or 300 millisecond cancellation timing and reported
`TRAINING_OUTPUT_CHANGED`. Native-disk repetition was intermittent. A
10 GiB memory-backed volume passed five repetitions of each test. The full
retry assigned that volume to the Agent training test process through Go's
`-exec` option and left the other test processes on the APFS scratch
volume. This preserved the test inventory, timing, and disk space checks.
Running on the shared default ports or lowering the production disk space
threshold would risk interfering with another lane or bypassing a guard.

The DEV-102 write slice through `edfd7c8` also passed the full Make test
target on its own loopback ports and APFS volume. Agent model preparation
and training both used the separate memory-backed volume. The run included
1,401 passing browser cases, 27 default browser skips, all Go modules,
62 gateway unit tests, and the Web unit suites. The disconnected Admin
workflow ran separately through the committed CLI fixture.

The priority 2 implementation at `7fe9507` passed the full Make test
target with the same isolation. It included 1,401 browser passes, 27
default browser skips, all Go modules, the gateway and Web unit suites.
The separate Admin TLS run passed the original CLI workflow, real model
import and verifier retry, exporter maintenance with VictoriaLogs, and
alias publication and disable. The remaining DEV-102 gates are the
installed gateway delivery in GW-114 and an installed systemd component
run with the archive delivery in FLT-109.

## NVIDIA operations ownership and qualification

| Item | Available lane D slice or required boundary |
| --- | --- |
| GPU-101 | The device collector reads DCGM locally. The managed exporter, authenticated reporting, retention and operator view cross delivery, architecture and UI ownership. The loopback-only Linux fixture passed. NVIDIA hardware qualification remains open. |
| GPU-102 | The journal reader and advisory catalogue have offline tests. The architecture lane owns authenticated cursor delivery and identity reconciliation. The control and UI owners must surface the events. |
| GPU-103 | Quarantine must be enforced at every allocation path and persisted with an atomic fleet-wide breaker. A local collector cannot enforce this. It exposes no quarantine action. |
| GPU-104 | Chronic-node classification needs persistent, deduplicated incident and replacement history after GPU-102 and GPU-103. It belongs with the fleet control owner. |
| GPU-105 | Device plugin and DRA admission require cell runtime, offline packaging and scheduler ownership. The published NVIDIA and Kubernetes driver contracts keep allocation with the device driver. |
| GPU-106 | MIG and time-slicing change device allocations and isolation guarantees. The scheduler, runtime, delivery and operator UI own the live configuration. |
| GPU-107 | Exclusive leases need a durable authoritative allocator and fencing at workload admission and execution. Those paths belong to the scheduler and architecture owners. |
| GPU-108 | Effective-use reports need stored GPU-101 observations, workload attribution and the reporting UI. Device samples alone cannot provide utilization over time. |
| GPU-109 | The Linux amd64 server has no NVIDIA hardware. Keep the real-hardware verification open. |

## Developer and compatibility boundaries after the lane review

| Item | Available lane D slice or required boundary |
| --- | --- |
| DEV-101 | The six cloud inventory operations have explicit IDs that preserve SDK names and help bookmarks. The four node exporter routes have a canonical Components declaration and unchanged Nodes client and help projections. API coverage for features still in development remains with the module owners. |
| DEV-102 | GW-114 now allows the CLI to register an agent-managed gateway through the existing Admin endpoint. The full CLI item still depends on common operations across the owning modules and an installed exporter run with FLT-109 delivery. |
| DEV-103 | The SDK tick and its existing evidence are already on main. |
| DEV-104 | Signed delivery requires authenticated subscriptions, committed events in all four families, per-subscriber retries and private endpoint policy. There is no public webhook contract in the current tree. The domain producers, persistence and the operator flow require their owners. |
| DEV-105 | Notebook GPU admission and runtime lifecycle are outside CLI and contract ownership. A notebook schema alone would advertise an unsupported operation. |
| DEV-106 | Workspace development containers and their scoped SSH authority must exist before SSH or IDE integration can be exercised. |
| DEV-107 | An approved base registry is a prerequisite. The isolated build worker, sandbox and trusted result admission remain outside the lane. |
| DEV-108 | Job callbacks need durable job state and delegated tenant authorization with quota admission. The current API slices do not provide that complete flow. |
| CMP-216 | The option visibility and accessible-announcement behavior is enforced in the console and portal. The lane cannot change or qualify every option from the shared declaration alone. |
| DEV-201 | A provider needs stable scoped machine identity and CRUD or lifecycle contracts for its managed resources. The existing interactive CLI session is unsuitable as its provider credential. |
| DEV-202 | A new adapter SDK needs a public extension boundary in engine, device and storage owners. The current SDK generates operator API clients. |
| DEV-203 | Pipeline persistence, execution, recovery and authorization belong to the workflow owner. |
| DEV-205 | ELN and LIMS integrations need supported import and export boundaries and offline target systems selected by the laboratory data owner. |
| DEV-206 | Delivery history and replay depend on the subscription and delivery records from DEV-104. |
| GPU-201 to GPU-203 | Remediation and active tests need authoritative fencing, scheduling and control tasks. Straggler detection needs job-attributed telemetry. Active burn testing also needs a qualified real GPU. |
| GPU-204 | Ascend health needs an admitted non-NVIDIA device observation before a collector can report through the agent. The current wire type and runtime probe are NVIDIA-specific. The architecture owner must provide that contract; an approved local driver remains required. See the accelerator report boundary below. |
| GPU-205 and GPU-208 | Ascend allocation, partitioning and heterogeneous pools need the cell scheduler and device admission owner. |
| GPU-206 | Capacity degradation and spare planning need retained fleet observations and capacity history. |
| GPU-207 | Real Ascend qualification requires the named driver, hardware model and count. Keep it separate from collector parser tests. |
| GPU-209 | Intra-node topology and failure attribution require GPU-101 telemetry and real PCIe and NVLink/NVSwitch configurations to establish attribution. |
| DEV-204 | The versioning guide and local comparison cover published operation identities. A release baseline, schema and behavioral compatibility checks, and runtime deprecation signaling need their owning release and API paths. |

## Training intent CLI (DEV-102, TRN-101)

[Kubectl create](https://kubernetes.io/docs/reference/kubectl/generated/kubectl_create/)
accepts a local declarative file. The
[Kubeflow TrainJob lifecycle](https://trainer.kubeflow.org/en/latest/user-guides/trainjob-lifecycle.html)
keeps training intent distinct from executor state. The
[ownsolid training contract](../../api/openapi/v1/training/openapi.json)
already supports Admin creation, detail and cancellation. Send the existing
session, configured cell and selected workspace through the existing CLI
transport. Use the operator's immutable dataset version and explicit request
UUID for creation. Return the current revision and require that revision
for cancellation. The control plane owns validation and state transitions.

Pass the training configuration as a bounded local JSON object. Keep workspace
selection in the existing CLI flags and settings. A file containing the entire
request would create a second workspace selector. A flag for every tuning field
would duplicate the versioned server schema. Use the current JSON or YAML
output and display the durable state and revision in the default table.
The existing Cobra (Apache-2.0), Go and x/sys (BSD-3-Clause), and
[CLI test dependency licenses](sdk.md#licenses) cover this slice. These allow
commercial and offline use and redistribution under their notice terms.
No new dependency, model or dataset ships with the CLI change.

Verify the actual Admin HTTPS workflow in disconnected PostgreSQL. Use a
real processor-backed dataset publication and synthetic node, GPU and model
metadata. Check a missing or malformed configuration, idempotent creation
and refusal of a changed payload with the same key, scoped detail, stale-revision cancellation,
a durable successful cancellation, and a replay with no additional audit
event. The test exercises training intent and does not dispatch a worker.

The Linux amd64 loopback HTTPS and PostgreSQL fixture ran the compiled CLI
against the real Admin dispatcher. It published input rows with the installed
Python 3.12 processor, queued one job on replay, refused changed intent and
a foreign tenant, and preserved exactly one cancellation audit after a
stale-revision refusal and a successful retry at the new revision.

## Shared Linux installer test identity

The [factory test instructions](../development/environment.md#factory-tests-on-linux)
and [factory runner](../../tools/dev/test-offline.sh) start the compiled
installer test binary as a real non-root user and group using
[setpriv](https://man7.org/linux/man-pages/man1/setpriv.1.html) on a
writable tmpfs. The temporary installation and the files in its backup
then have a representable owner and group. Keep Make and its build
recipes outside a UID-only user namespace.

On Linux amd64 with loopback only, a one-rule GNU Make 4.3 recipe
reproduced the prior exit 2. The UID-only namespace exposed the
[unmapped group as the overflow GID](https://man7.org/linux/man-pages/man7/user_namespaces.7.html).
The child failed at setresgid(-1, 65534, -1) with EINVAL before
executing /bin/sh. Mapping both IDs, or running the recipe as
nobody:nogroup through setpriv, passed.

The same compiled installer binary ran these exact cases against a
read-only checkout mount and disposable tmpfs.

- TestRestorePutsBackAMissingModelBeforeTheDatabaseChanges
- TestRestoreLeavesAMatchingModelAndReplacesADamagedOne
- TestRestoreOntoAnEmptyInstallationBringsBackItsFilesAndRoles

Host root failed configuration validation with INSTALL_CONFIG_INVALID.
With only the UID mapped, all three reached restore and failed at
fchownat with UID 1000, GID 65534 and EINVAL. The model cases reported
INSTALL_RESTORE_MODELS_FAILED. The empty-installation case reported
INSTALL_RESTORE_FILES_FAILED. Both the complete UID/GID mapping and
the documented nobody:nogroup run passed all three cases.

For a narrow replay, build the Linux installer test binary, then run
from its package directory with an accessible private tmpfs.

~~~sh
setpriv --reuid=nobody --regid=nogroup --clear-groups \
  env TMPDIR="$fixture_tmpfs" HOME=/nonexistent \
  "$installer_test_binary" -test.count=1 \
  -test.run '^TestRestore(PutsBackAMissingModelBeforeTheDatabaseChanges|LeavesAMatchingModelAndReplacesADamagedOne|OntoAnEmptyInstallationBringsBackItsFilesAndRoles)$'
~~~

The installer handoff is the ownership publication in
[restoreFile](../../deploy/offline/internal/install/restore.go).
Preserve the backup UID/GID and its restore assertions. The verified
correction is the test invocation's identity and scratch setup.
The evidence covers the named restore cases and Make spawn probe.

## Accelerator report boundary (GPU-204 and GPU-209)

The published [host facts](../../api/agent/v1/protocol.go) contain OS,
CPU, memory and disk observations. The
[inference accelerator observation](../../api/agent/v1/inference.go)
contains NVIDIAGPU, CUDA compute capability and MIG mode. The
[current runtime probe](../../services/agent/internal/inference/gpu.go)
returns that type. Neither surface admits Ascend health samples.
Keep the missing report contract with the architecture owner instead
of encoding an NPU as a CUDA GPU or adding an unconsumed health parser.
Allocation, device-plugin delivery and the operator flow remain their
own prerequisites. The CPU-only test server does not qualify hardware.

The [DCGM collector](../../services/agent/internal/discovery/dcgm.go)
has physical GPU identity, an optional PCI address and per-device
metrics. A PCI address is not a peer topology, and a device-level NVLink
counter does not identify a failed peer or switch. GPU-209 needs a
reported peer/link inventory and qualified fabric observations before
fault attribution can target a device. Missing link samples remain
missing; they must not be converted into a fault or an isolation action.
No agent protocol, runtime admission or quarantine behavior changes
with this boundary.
