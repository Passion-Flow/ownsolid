# OPS private source archive

This directory preserves private workstation tooling on an independent migration
salvage branch. It is not product code, a supported gate, or roadmap acceptance.
The source files are byte-for-byte copies; original files remain in place.
`source-manifest.json` maps their hashes and every excluded temporary record.
`handoff.json` maps original refs, worktrees, historical inputs and existing proof.

## Retired admission code

Do not execute the historical `.recorded` files or the old lock protocol tests.
They include retired directory locks and a superseded file-creation protocol.
The active wrapper copies are sealed and refuse execution. This archive does not
activate the coordinator's current persistent admission epoch. Its lock must never
be recreated by these scripts. The copied original README describes historical
acceptance and includes obsolete reproduction directions; use it as evidence only.

## Rebuild on a new workstation

Fetch the original OPS salvage refs from GitHub and recreate `lane-ops` and
`lane-ops-cat105` from the exact source mapping. The latter historical tip is
already reachable from main. Compare old retained branches to current main before
integrating; they are alternative original DAGs, not a sequence to replay wholesale.
Recreate this source archive separately. Install the documented development
toolchains and use current coordinator admission rules. Do not rerun retired gates.

The migration did not run rebase, make check, make build or make test because raw
Free was below the required 6 GiB. No main update or new roadmap tick is claimed.
Existing passing full checks apply only to the exact historical commit recorded
in the handoff. Temporary logs, build caches, dependencies and binaries are not
included. The manifest preserves paths and hashes for existing local evidence;
those raw logs require a separate evidence transfer if needed on another machine.

Complete the durable installation authority and authorized user paths described
in the research notes before ticking catalog items. Application Helm acceptance
must use the application's approved signed package and independent publisher
trust; platform release fixtures do not substitute for that acceptance.
