# 0014: Platform backup and restore through the installer

- Status: accepted
- Date: 2026-11-04

## Context

REL-002 asks for database-aware backup and restore of the control plane. ADR 0005 removed the
offline operator, which held the previous backup path, and left
`services/control/internal/operations/postgresbackup` (about 12,900 lines) to decide on. That
package backs up a Kubernetes or native installation through a request file, a policy overlay,
pinned client tool hashes, four domain store owners and the 86-table schema of an old prefix.
Nothing that `ownsolid-install` produces can satisfy its inputs: it has no request, no domain
stores and a 109-table schema.

## Decision

**The installer owns backup and restore of what it installs.** `ownsolid-install backup` and
`restore` act on the Compose installation through the same typed `install.yaml`, the same
closed command environment and the same step and error model as `apply`. They use the
PostgreSQL 18 tools inside the pinned database image, so no client tool is installed or
pinned separately.

**A backup counts only if it restores.** The dump is restored into a scratch database, and the
manifest records each table's row count and an order-independent checksum read from that copy.
Restore loads into a new database and compares the same checksums before the swap.

**The database is swapped, not overwritten.** Restore renames the current database aside in the
same transaction that renames the loaded one into place, keeps the previous one, and swaps back
if migration, the schema check, revocation or the health check fails. A database too damaged to
migrate can still be restored, because nothing runs against it.

**The keys travel with the database.** The session HMAC key and the mail keyring encrypt or sign
what the database holds. A backup carries them with the database identities, TLS material and the
administrator credential. Restoring over a running installation requires identical keys, and an
empty `install_root` takes them from the backup.

**Restored authority is revoked.** Restore runs the existing `invalidate-restored-auth` owner, so
sessions, invitations, password resets and node credentials from the backup's point in time stop
working.

**One operation at a time.** `apply`, `backup` and `restore` take an exclusive `flock` on the
installation, which the kernel releases if the process dies.

## Consequences

- `postgresbackup`, `kubernetes_backup.go`, `kubernetes_restore.go` and their commands remain,
  unused by any install path. `damengbackup` imports `postgresbackup`, so removing it is a
  separate change; the Helm chart item (DEL-107) decides whether a Kubernetes backup builds on
  it or on this command. Until then they are not a supported way to back up ownsolid.
- Middleware keeps refusing platform-owned backup and restore (ADR 0005): the platform backup is
  this command, not a middleware operation.
- Telemetry is not in a backup (OBS-113), and neither are model, dataset or training stores, which
  the installer does not create yet. REL-103 adds a coordinated backup with a consistency point
  across them.
- The checksum is `hashtextextended` over each row's text form. It detects any changed row in
  practice, but it is not cryptographic: the manifest's SHA-256 of the dump is what protects the
  backup against tampering.
