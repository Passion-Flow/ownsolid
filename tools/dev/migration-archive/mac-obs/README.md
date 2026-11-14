# Mac OBS source preservation

This salvage branch preserves the original private Python sources, their required
configuration and recovery mappings. The source files are copied byte for byte.
The frozen gate belongs to the retired inode 50313339 epoch and must remain
inactive. It is historical source, not a migration or test entry point.

The Git bundle remains at its original private path. Its dda4e8c317bd head and
66a3cafd/49f03d45 ancestry are preserved through the original delivery tip. No
binary bundle or payload is committed here; these Python files have no embedded
Git bundle. Runtime logs, result files and duplicate Git objects remain at their
original locations and are listed as exclusions in handoff/manifest.json.

The detached typed-producer source remains 66a3cafd. Its 26 Go test roots and
Linux integration-tag compilation are unrun. The old schema-59 native result
qualifies only its original 5f83cfbf source. Actual installed-source ownership,
startup integration, public API/Console and hard ingest limits remain open.

The migration full-build admission reported insufficient raw Free memory. No
rebase, make check or make build ran, and this archive does not advance main or
claim acceptance. Original branches, missing-worktree registrations and the
24-entry staged index are retained without alteration.
