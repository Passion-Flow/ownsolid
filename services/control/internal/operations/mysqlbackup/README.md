# Native MySQL backup and isolated restore

This package owns native MySQL dump/import plus the four configured artifact stores. [The selected native command layer](../../../cmd/server/mysql_native_commands.go) supplies an actual operator context, typed configuration, protected paths, and a reason only for the first backup/restore operation. Its products are immutable backup/restore evidence, not release attestation or proof that external workloads stopped.

## Inputs and source ownership

[Backup](backup.go) implements `backup-mysql` and reconciliation of an existing backup. [Restore](restore.go) requires explicit isolated-target confirmation and a fresh bound target; it cannot silently select production. Native tool paths, operation budgets, version/digest pins, and protected connection inputs come through `Config.Database.MySQLBackup` and the selected database configuration. [Native execution](native.go) validates the selected executable rather than downloading a replacement or substituting the host architecture.

[Store owners](store_owners.go) bind models, datasets, training, and model preparation to their configured roots and installed tenant. Roots must be absolute, distinct, and non-overlapping with backup/restore paths. Operators must admit database and filesystem capacity, quiesce relevant writers, and retain the original configuration before starting; a manifest is not a live database-directory backup.

## Exact native CLI boundary

Every row below lists the complete allowed flag set; all listed values are required. The dispatcher selects MySQL from the protected `--config` file. Do not add `--env-file`, another command's flags, or an explicit false/empty value for a disallowed flag: the validator rejects supplied flags outside the allowlist.

| Command | Allowed and required flags | Effect |
| --- | --- | --- |
| `backup-mysql` | `--config`, `--backup-root`, `--reason` | Capture a new backup in a new owned root |
| `reconcile-mysql-backup` | `--config`, `--backup-root` | Inspect the original backup and completion audit; do not pass `--reason` |
| `restore-mysql` | `--config`, `--backup-root`, `--restore-context`, `--reason`, `--confirm-isolated-mysql-restore=true` | Import once into an explicitly selected isolated destination |
| `reconcile-mysql-restore` | `--config`, `--restore-context`, `--confirm-isolated-mysql-restore=true` | Verify/publish the original result; do not pass `--backup-root` or `--reason` |
| `prepare-restored-runtime-access`, `check-restored-runtime-access`, `reconcile-restored-runtime-access`, `resume-restored-runtime-access` | `--config`, `--runtime-config`, `--restore-context` | Separate MySQL access-owner operations; no `--backup-root`, `--reason`, or isolated-confirmation flag |

`--reason` must satisfy the operator-reason validator. Paths are local protected inputs/outputs, not URLs. Reconciliation derives the original backup root from the retained restore intent; it does not accept a replacement backup path. The final row is the selected MySQL interface; do not infer PostgreSQL command parity.

## Native tools and destination prerequisites

Before admitting the operation, provide both native `mysql` and `mysqldump` executables, the protected artifact manifest, and their exact configured SHA-256 pins. The manifest must match the running host's OS/architecture and expected server/tool version. It also binds the original license files and, when declared, the license-exception file. Executable/license paths must resolve without symlinks, refer to single-link regular files owned by root or the effective operator, and disallow group/other writes; executables need execute permission. `NewNative` checks both binaries with `--no-defaults --no-login-paths --version`; merely installing binaries does not satisfy this contract.

The configured scratch root must already exist as an operator-owned private directory. This native TLS path requires the selected MySQL `Host` to equal `ServerName`; keep its actual CA and protected connection inputs intact. There is no TLS bypass or runtime download fallback.

Restore requires an empty destination database whose `(server UUID, database)` pair differs from the backup source. The authenticated principal and exact server-version string must match the source. The destination store roots are separately bound by path, device, inode, UID, and GID; an arbitrary empty database or replacement directory is insufficient. Capacity admission must cover the SQL archive, all four store copies, manifests/diagnostics, and destination database capacity.

## Effects and completion boundaries

Backup exports verified store contents and invokes the pinned native dump process with protected credentials. Database row images and source trees are compared before and after capture. The result binds tool evidence, SQL bytes, row state, store trees, target identity, and reason. `manifest.json`, the SQL completion audit, and `complete.json` are distinct durable steps. Reconciliation inspects the original root and publishes only when those bindings permit it; it does not silently take a new dump.

Restore records its intent before native import, requires an empty isolated database, and binds the destination roots' actual identities. It verifies restored source rows before authority invalidation, imports the four owned stores, and records the post-restore state. [Restore-within logic](restore_within.go) uses the selected transaction boundary; [restore receipts](restore_receipt.go) and [context](restore_context.go) bind subsequent operations to this specific result.

## Private retained material

The backup root contains `database.sql`, `manifest.json`, `complete.json`, the owned store snapshots, and native diagnostics. Restore uses the supplied context path plus `.intent`, `.files`, and `.native.stderr`. The `.intent` binds the original absolute backup path and destination identities; `.files` binds the joined native import and actual store readback. A final context alone cannot replace them.

Treat archives, snapshots, and diagnostics as private data-bearing recovery material. The [file boundary](files.go) requires operator-owned `0700` private directories and `0600` single-link regular document files, with exclusive writes and synchronized readback. Retain original bytes and required evidence after uncertainty; do not publish these files with source or diagnostic summaries. Before moving/copying retained material, inventory it and verify the durable destination copy and permissions. A returned archive copy does not make relocated context paths or newly created store inodes valid for reconciliation; do not remove the original bound copy while it remains required by recovery.

## Recovery

1. Preserve the complete original private bundle above, its source/target bindings, destination roots, and exact diagnostic code. A failed return does not prove an import or audit did not occur; missing completion records are not permission to reconstruct success evidence.
2. Stop new writes, verify storage identity and headroom, and reacquire the actual installation/namespace authority on the same target before inspection.
3. Use the exact reconciliation flag set above. Backup reconciliation can publish completion only when the original manifest and SQL audit already validate. Restore reconciliation verifies/publishes only from retained intent, files proof, actual restored state, and the committed audit. Neither command reruns the native process or repairs a missing earlier phase.
4. Keep an incomplete imported target isolated. Do not erase intent files, repurpose roots, replay import over a nonempty database, or grant runtime access without the required bound restored-context/freshness checks.
5. Complete external workload cleanup through the relevant recovery owners. Database authority invalidation does not physically remove an inference, training, preparation, or cluster workload.

## Maintenance and verification

From `services/control`, the package entry is `go test ./internal/operations/mysqlbackup`. [Native tests](native_test.go) cover executable shape, protected options, stream limits, and cancellation; [restore tests](restore_test.go), [tree tests](tree_test.go), and [database-within tests](database_within_test.go) cover their specific bindings. These tests must not be relabeled a real native MySQL backup/restore run without actual tool, server, fixture, and execution evidence.

Changes to schema inventories, any store owner, native pins, restore invalidation, or receipt format need matched capture/import/readback and failure recovery verification.
