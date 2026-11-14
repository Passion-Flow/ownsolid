# IAM Mac source preservation

This independent archive preserves private IAM prototypes, test drivers, retired
gate source, required manifests, and handoff mappings. It is not a product
acceptance commit and must not be merged into main. No source script was executed.
Original files, Git refs, stash entries, worktree and retired gate banners remain
unchanged. Archived historical script copies are evidence only; never run them
against their retired lock paths or reuse embedded environment assumptions.

## Contents and reconstruction

- `source/`: byte-identical selected files at their original root-relative paths.
- `source-archives/`: unique source members from retained historical tar archives.
- `archive-files.json`: exact original paths, tar members, SHA-256, and file mode.
- `exclusions.json`: every scanned nonselected file/member and its reason or
  already-preserved Git source mapping. Dependencies, keys, credentials, runtime
  fixtures, databases, binaries, bundles, logs and temporary outputs are excluded.
- `preservation-plan.json`: original branch/tag/stash mappings and exact review
  snapshot tree hashes. Review snapshots have no invented parent lineage.
- `bundle-coverage.json`: both standalone bundles and their three tar members;
  all five copies point to two original commits. No bundle is committed as a file.

Fetch an indicated salvage ref from the repository on GitHub, then create a new
worktree at its exact SHA. To reconstruct a private prototype, copy selected
files from this archive according to `archive-files.json`; recover Git-backed
exclusions from their exact commit and repository path. Recreate dependencies
from pinned manifests and provision fresh fixture credentials locally. Do not
copy old fixture databases, passwords, test binaries or lock carriers.

## Actual unfinished boundaries

The published-reader candidate b3c2a700fe55e50be61c35155f9d43de665ff5ae passed
three no-database roots and three cleanup subtests on its original qualified
source. It has no native SQL, startup-role, read-only mount or gateway drain
acceptance. The SAML completion HTTP candidate
c2b955ed7aca6cdaae98ef22d2a0759d92bac5ba was not compiled or tested: its
qualification was refused with exit 78 for insufficient raw free memory. Older
v4 or stage-only results do not qualify these newer source bytes. Schema, startup,
public API, generated code and UI admission remain incomplete.

The migration changed no product source, schema, roadmap tick or server state.
`make check`, `make build`, and `make test` were NOTRUN: current Mac raw free
memory was below the required 6 GiB. All candidates stay outside main. The live
roadmap remains 5/19 P1 and 0/6 P2 completed, with IAM-102 next by module order.

Linux/server sources are outside this Mac-only preservation scope. Shared and
reflog-only histories are covered by the coordinator separately. No original
resource, branch, worktree, stash or source file was removed.
