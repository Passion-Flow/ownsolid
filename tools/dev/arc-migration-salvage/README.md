# Retired ARC source salvage

This directory preserves private ARC prototype, transport, calibration and wrapper source for machine migration. It is isolated on a salvage branch. It is not a product implementation, installation input, supported workflow or new acceptance result. Do not execute the retired wrappers.

`source-manifest.json` maps original paths, SHA-256 values and modes to byte-identical readable source. Repeated contents share one archived file. Archived files are stored as non-executable text. Original files and worktrees remain untouched.

`external-payload-reconstruction.json` records original binary archive hashes and Git bundle heads. Binary bundles, database dumps, compiler output, dependencies, credentials and runtime output are excluded. Original source snapshots already reachable from the preserved Git tips are represented by Git blob identities in the external migration receipt. No binary payload was embedded in the archived readable source.

The three-document NOTRUN update is preserved separately. Fresh raw Free was below the full 6 GiB admission requirement; no make check/build, compiler, PostgreSQL, Linux runtime qualification or main push ran for this migration. ARC-111 and the measured-schema61 candidate remain unqualified.
