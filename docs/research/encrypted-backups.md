# Encrypted logical backups (REL-110)

## Existing format and upstream practice

The Compose installer uses a version 1 manifest, a PostgreSQL custom-format
dump, and the installation files and published model objects the database
references. It loads the dump into a separate PostgreSQL database before
publishing a backup. Restore checks the manifest and checksums before preparing
the incoming database and uses the existing recorded writer exchange. Keep this
database-aware path and the unencrypted directory format. [PostgreSQL
pg_dump](https://www.postgresql.org/docs/18/app-pgdump.html) and [pg_restore](https://www.postgresql.org/docs/18/app-pgrestore.html)
document the logical archive and restore semantics.

[pgBackRest repository encryption](https://pgbackrest.org/user-guide.html#configure-repository-encryption)
encrypts backups with credentials configured outside its repository. Its
physical PostgreSQL backup format does not include ownsolid's installation
keys or model objects. A password or symmetric repository key would have to be
present on the backup host. [age v1](https://age-encryption.org/v1) uses a
recipient public key and an authenticated streaming payload. Its [reference Go
implementation](https://pkg.go.dev/filippo.io/age@v1.3.2) provides a native
X25519 recipient and identity with no service, passphrase or plugin. The
[age-keygen procedure](https://github.com/FiloSottile/age/blob/v1.3.2/README.md)
allows an operator to generate the pair offline and transfer only the public
key to the backup host.

Use age v1 with a tar stream holding the existing manifest, database dump and
files. Require exactly one native X25519 key per protected file and keep the
recovery identity outside both the archive directory and installation root.
Reject symlinks, hard-linked keys and writable-by-others inputs. Use
[Go archive/tar](https://pkg.go.dev/archive/tar) for framing. A format designed
only for PostgreSQL misses installation files. A custom cryptographic format or
an installer subprocess for key handling adds review or packaging overhead.
age proves that the stream is intact under its file key. Repository custody is
still required because anyone with the public recipient can encrypt a new
archive.

## Licensing

The pinned [age v1.3.2 module graph](https://github.com/FiloSottile/age/blob/v1.3.2/go.mod)
and license files permit commercial use and offline redistribution. BSD binary
redistribution must reproduce the applicable copyright, conditions and
disclaimer in accompanying materials. Source redistribution must retain them.

| Module or tool | License |
| --- | --- |
| filippo.io/age v1.3.2 | [BSD-3-Clause](https://github.com/FiloSottile/age/blob/v1.3.2/LICENSE) |
| filippo.io/hpke v0.4.0 | [BSD-3-Clause](https://github.com/FiloSottile/hpke/blob/v0.4.0/LICENSE) |
| golang.org/x/crypto v0.58.0 | [BSD-3-Clause](https://github.com/golang/crypto/blob/v0.58.0/LICENSE) |
| golang.org/x/sys v0.49.0 | [BSD-3-Clause](https://github.com/golang/sys/blob/v0.49.0/LICENSE) |
| Go standard library | [BSD-3-Clause](https://go.dev/LICENSE) |
| PostgreSQL tools, already provided by the PostgreSQL image | [PostgreSQL License](https://www.postgresql.org/about/licence/) |

The Linux amd64 workspace build resolves the versions above. The
standalone installer module resolves x/crypto v0.55.0, under the same
[BSD-3-Clause license](https://github.com/golang/crypto/blob/v0.55.0/LICENSE).
Include the compiled versions in the installer binary notices. The installer does not ship the upstream age commands or tests.
The test vectors retained in the Go module graph are published under
[0BSD, CC0-1.0 or Unlicense](https://github.com/C2SP/CCTV/blob/4448f2097b2d/age/README.md#license).
The accompanying upstream vector generator has a
[BSD-3-Clause license](https://github.com/C2SP/CCTV/blob/4448f2097b2d/age/internal/LICENSE).
The upstream command dependency x/term v0.45.0 is
[BSD-3-Clause](https://github.com/golang/term/blob/v0.45.0/LICENSE).
The installer introduces no model or dataset and needs no runtime packages or
network access.

## Acceptance and failures

- Run an explicit encrypted backup and restore on Linux amd64 Compose without
  an uplink, with host and workload egress probes proving capture. Verify the
  actual database and the original administrator after restore.
- The backup directory holds only ciphertext. Publish under a new name after
  closing and syncing the stream, with atomic no-replace publication. An
  existing archive stays byte-identical on retry. Interrupt a backup and retry
  the same unpublished destination.
- Reject wrong identities, ciphertext modification and truncation before
  connecting to the live database for restore. Consume the complete
  authenticated stream, including after tar EOF. Reject undeclared, duplicate
  or unsafe members, and validate manifest sizes and hashes before restore.
- Store staging data privately under the installation root. An interrupted
  process can leave staging files there. The next encrypted operation clears
  only its owned workspace before use. The existing durable restore record
  governs interruption during the database exchange.
- Verify the previous unencrypted backup and restore flow without key flags.
  Encrypted backup is explicit. Scheduled backups, upgrade checkpoints and
  physical WAL archives keep their existing formats.

## Release qualification

The explicit single-node path passed the disconnected Linux amd64
`TestEncryptedBackupIsolatedDocker` drill recorded in `fe49ce45`. The
[signed cold restore in `3b47c1de`](signed-release-recovery.md#linux-amd64-acceptance)
recovered the original account and all nine approved Qwen files from the
age archive onto an empty site. This covers the installed CLI path with
an external recovery identity.

The repository's `make check`, `make build` and `make test` passed on the
release input change that followed this work. The browser suite passed
1,074 Console cases and 387 Portal cases. The Linux amd64 focused archive
check accepted the complete stream and rejected a missing tar end and
extra authenticated bytes. The accompanying transaction reconciliation
check exercised both directions with controlled command replies. Its
network namespace recorded only the deliberate IPv4, IPv6 and DNS
probes. The every-release signed restore qualification remains REL-104.
