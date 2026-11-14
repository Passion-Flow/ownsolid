# WEB migration source preservation

This directory is a historical archive on a standalone salvage branch. It is not
part of the accepted product implementation, and it makes no new roadmap claim.
Do not merge this directory into main as product code.

`sources/` preserves readable private implementations, test drivers, configurations,
retired gate scripts, and existing acceptance summaries byte for byte. Retired gates
remain retired: do not run them to resume work. Build a new verification command
against the chosen source only after the new machine meets the documented admission.
Absolute paths and former machine identities are historical inputs, not deployment
defaults. No archived script was executed for this migration.

`source-manifest.json` maps every archived file to its original absolute path,
SHA-256, size and role, and lists every excluded file and pruned dependency/cache
root. Original files, screenshots, bundles and worktrees remain on the old machine.
Generated dependencies, caches, builds, binaries, media, raw browser reports and
transient process output are excluded from this source archive. Existing screenshots
already in original Git commits remain in those preserved commits.

`original-ref-head-mapping.json` records original refs, all seven worktree HEADs,
newer serial-lint tips and all existing bundle heads. Every source is covered either
by the recorded GitHub main or a planned immutable salvage tip. The final migration
receipt outside this archive contains the completed GitHub readback proof. The raw
Git bundle files are not committed; their hashes and original heads are recorded in
`bundle-inventory.json`. Static review found no embedded binary bundle in private
source literals; all code files are retained without projections or truncation.

The current UX127 source is fc8d26d3ec64793c29ed7121594400344613dbfa.
The Portal joint source is bfeea90cd5adb082b77047b0c97fcea7c7654c51.
The serial-lint source is 98cf81c8f3c2f82ec2967c017e266f9c345560c0,
which covers newer source snapshots 56a19799, 4ca31d94 and f556daff.
UX127 and POR112 remain open. Full/joint gates and installed authority, SQL,
offline and SSO acceptance remain unfinished. See the existing owner handoffs and
research notes for the exact old scope; do not treat directed browser passes as
full release acceptance.

Fresh raw Free was below 6 GiB, so make check/build and rebase were NOTRUN and no
main push was attempted. On the new machine, restore the recorded refs, choose the
current source for each workstream, retain main changes during integration, and
run make check/build before proposing any main update. Historical checkbox state
is recorded separately; no checkbox was changed by this migration.
