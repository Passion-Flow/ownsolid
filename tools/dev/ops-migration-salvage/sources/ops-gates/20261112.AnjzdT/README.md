# OPS workstation acceptance

## Admission closed

The old epoch is closed. All active OPS gate and Git wrapper entry points
refuse before opening a carrier or starting a command. There is no carrier
creation, fallback path, queue or automatic retry. Original wrapper bodies are
retained as .pre-epoch-close.recorded evidence.

A new persistent epoch must be designated by the coordinator before these
entry points are updated. Tests and builds remain NOTRUN until activation.


The final accepted and pushed commit is `67293e12`, based on `4f6a1c8c`.
The complete passing replay is in `replay.hkPG6H/`. Its `ok` file identifies
the checked commit. Its push log and remote confirmation record the main update.
The earlier `replay.7rfk2X/` also passed, then its push was refused after main
advanced. Both runs preserve their full inputs and outputs.

The final replay additionally passed selected catalog metadata, initial
selection, dependency binding and wrong-pin refusal tests under a process
policy denying all network access. See `replay.hkPG6H/catalog-offline.log`.
The detached scratch images and test binary were removed after acceptance.
Logs, source references, filesystem setup and replay scripts remain.

The files `head`, `base` and `tree` identify the frozen input. The
original local commits are retained on `codex/ops-preserved-20261112`.

## Evidence

- `check.log` and `build.log` contain the passing repository checks and build.
- `test.log` contains the initial HFS+ snapshot detection failures.
- `apfs/factory-regression.log` contains the passing snapshot regression on APFS.
- `apfs/test.log` contains the subsequent Unix socket path length failures.
- `short/install-regression.log` contains the passing Catalog Compose regressions at the short APFS mount.
- `short/test.log` contains the complete test retry at that short mount.
- `short/catalog-build.log`, `short/catalog-race.log` and `short/catalog-vet.log` contain the supplemental catalog checks.
- `short/ok` is written only after every supplemental check and a clean Git state.
- The preflight files record process, lock and resource checks. Cleanup records volume detach.

The earlier native Linux disconnected results are recorded in the repository's
topic research notes. Their original external logs were stored in temporary
directories and were lost after the workstation restart. This run uses the Mac
only and does not reproduce that native acceptance.

## Recorded reproduction before epoch closure

The current wrappers use the permanent regular file
`/private/tmp/ownsolid-workstation-full-gate.lock`. They refuse symlinks,
non-regular carriers, a foreign UID and an occupied nonblocking `flock`.
`workstation-lock.py` holds one file descriptor for the complete child run
and passes that descriptor to its child. Release unlocks and closes the
descriptor while retaining the carrier's bytes and inode.

Use `with-lock.zsh COMMAND ARG...` for a client build or main publication.
The bounded Git helper also enters this lock for a standalone push.
The preflight script checks resources. Atomic lock acquisition follows the
two samples. Historical directory-lock runners are retained as
`.recorded` files, and their old executable entry points refuse execution.
Five disposable lock-fixture checks and wrapper syntax checks passed.

Run the following with absolute paths to the OPS worktree and its previous
worktree holding the Rust target cache.

```sh
zsh replay.zsh /path/to/lane-ops-cat105 /path/to/lane-ops
```

The wrapper takes two resource samples, acquires the common workstation lock,
fetches and rebases on main, records the input and creates a private APFS image.
Its short mount avoids the Unix socket pathname limit. Both complete Chromium
suites run serially with four workers each and no retries. The normal Go, Rust
and Web test recipes run unchanged. Catalog build, race tests and vet follow
because the root Makefile omits that module. Go and Cargo use cached packages.
The exit handler detaches the owned image and releases the owned lock.

The wrapper ran with `OWNSOLID_OPS_BASE_REF` set to the exact previously fetched
main commit when the default Git transport stalled. Git operations now have
a timeout through `git-command.py`. The final push used the workstation's
existing proxy for that command. Actual acceptance is shown by the logs above,
including both failed environment attempts.
