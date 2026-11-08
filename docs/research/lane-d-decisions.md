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

Land the verified CLI separately from the roadmap tick. The lane briefing
reserves the roadmap index to the coordinator. A checkbox change alone makes
the progress-table check fail. The coordinator needs to record the CLI
evidence, update the status table and refresh the index in the same change as
the tick. Bypassing the check would leave repository CI failing. The CLI and its
verification remain available while roadmap reconciliation is pending.
