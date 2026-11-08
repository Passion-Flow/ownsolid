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
| 2 | Bring a new approved model into the installation | Complete the resumable bundle upload and verification path with the exact approved artifact. The TLS fixture seeds metadata and cannot verify import or file integrity. This lane has no prepared approved bundle for that run. |
| 2 | Publish a deployment to callers | Exercise gateway provisioning, alias publication and protected credentials with the gateway delivery path. The current installation fixture stops at control-plane admission and the installer does not start the agent-managed gateway. |
| 2 | Manage node monitoring components | The component flows need the approved node exporter assets and the corresponding agent execution profile. The current inference fixture provides neither. |

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
