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

The first release covers the eight assigned commands and their offline Admin
flow. Keep the broader roadmap item open. The Admin already exposes common
write operations including node enrollment and disablement, deployment
creation, and deployment stop. These are outside the first release's CLI
command set. A full tick needs a defined inventory of common operations and
evidence for the additional commands. Equating the first release with the
whole roadmap item would overstate what operators can do from SSH.

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
