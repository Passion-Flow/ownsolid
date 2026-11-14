# First-run bootstrap recovery

DEL-103 uses the existing Compose installer and control operator bootstrap. The
operator takes the installation lease before creating the administrator. The
identity transaction creates the user, tenant, default workspace, owner
membership, and completed installation row together. PostgreSQL's
[transaction contract](https://www.postgresql.org/docs/18/tutorial-transactions.html)
provides the all-or-nothing database boundary. The local intent retains the
original generated credential for recovery if the operator loses the result of
that transaction.

[Kubeadm's initialization phases](https://kubernetes.io/docs/reference/setup-tools/kubeadm/kubeadm-init/)
separate local certificate material, bootstrap configuration, and persisted
cluster resources. [K3s cluster initialization](https://docs.k3s.io/cli/server)
uses server startup and durable state to distinguish initialization from
subsequent starts. ownsolid already has these responsibilities split between
the Compose installer, local control operator, and database. Keep those entry
points and the completed database gate.

## Protected credential publication

The current writer opens the final credential path before it has written its
contents. A write failure or process interruption at that point leaves a
malformed final path. Apply subsequently treats its presence as a retained
intent and cannot resume. Stage the complete document as an exclusive 0600 file
in the existing 0700 directory, sync it, then publish with an exclusive rename
relative to the open directory. Sync that directory and verify the exact
published document before starting the SQL transaction. A resume that is about
to mutate the database must also establish durability for the retained file,
including after an interrupted directory sync.

[Linux renameat2](https://man7.org/linux/man-pages/man2/renameat2.2.html) provides
atomic no-replacement publication. The [Linux fsync contract](https://man7.org/linux/man-pages/man2/fsync.2.html)
requires a separate directory sync to persist the name. [Darwin's exclusive
rename](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/APFS_Guide/ToolsandAPIs/ToolsandAPIs.html)
keeps the existing developer path available. An ordinary rename can overwrite
an existing document. Linking a staged name leaves a crash window with two
names for the credential, which the reader rejects. An abandoned staging name
after process loss stays private and has no authority to bootstrap. Previously
published malformed files still fail closed and require operator review.

## Serving certificate recovery

A generated certificate may still chain to the current CA and match both
configured origins after a previous run has replaced its private key. A crash
between the two writes can then make Apply preserve a broken HTTPS pair. Use
Go's [TLS key-pair parser](https://pkg.go.dev/crypto/tls#X509KeyPair) to validate
the pair before reusing it. Reissue a mismatched generated pair under the same
installation CA and keep the existing hostname and expiry checks. Each file
remains individually published by the installer. The next Apply repairs an
interrupted leaf update before trying to start the control plane.

## Dependencies and acceptance

The implementation uses the existing Go standard library
([BSD 3-Clause](https://go.dev/LICENSE)) and pinned
[golang.org/x/sys](https://github.com/golang/sys/blob/v0.49.0/LICENSE)
(BSD 3-Clause). Both permit commercial use, offline use, and redistribution
with their notices. The existing PostgreSQL fixture uses the
[PostgreSQL License](https://www.postgresql.org/about/licence/) and the existing
[pgvector PostgreSQL License](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE).
No new model, dataset, runtime dependency, or network service is required.

A failed write before publication must leave the final credential path absent
and the installation open. A repeat after the intent is published but before
the SQL commit must use the original credential. A repeat after commit must
preserve the administrator, tenant, workspace, membership, and credential and
must refuse a fresh bootstrap request. Concurrent file publication must
preserve the first document. A generated certificate and a different valid
private key must reproduce an invalid serving pair, then converge on retry to
a matching pair for both origins under the original CA. A further retry must
keep the resulting pair stable.

The shared setup HTTP contract and first-run UI are separate dependencies. They
must use the same committed installation gate, reject setup after commit and
under concurrent submissions, and must be verified through the admitted
offline release before DEL-103 can close.

## Linux amd64 acceptance

On `ownsolid-cpu`, the 2026-11-09 targeted run used the actual installer Apply
function, native Control operator and server commands as UID 10001, and
PostgreSQL 18.6 with pgvector 0.8.7. The disposable adapter routed the
installer's Docker Compose commands to one cached PostgreSQL container with
`--pull never --network none`, one CPU, 768 MiB, and no published ports. It
acknowledged the two telemetry start operations without starting their stores.
The real Control served both configured HTTPS origins on the namespace's port
8443. This run covers the changed Apply and operator paths. The DEL-102 run
covers the full Compose topology.

1. The repository egress recorder owned the namespace's only IPv4 and IPv6
   default routes. IPv4 TCP, IPv6 TCP, and a DNS query each reached the
   recorder, with no path beyond it.
2. `prlimit --fsize=0:0` on the original bootstrap command left a zero-byte
   final credential path. SQL returned `f|0|0|0|0|0|0` for the completed flag,
   users, tenants, workspaces, memberships, password rows, and bootstrap
   audit records. The next Apply failed with `BOOTSTRAP_FILE_INVALID`.
3. The same write limit on the fixed command left the final path absent
   and SQL unchanged. Retrying without the limit committed a single
   administrator, tenant, workspace, membership, password row, and audit
   record. The adapter then dropped the successful operator response.
4. After replacing only the generated leaf private key in the disposable
   installation, two Apply retries succeeded. Both made the actual HTTPS
   readiness requests with CA and hostname checks, and SQL IDs, credential
   bytes and CA bytes stayed the same. A direct repeat of
   `bootstrap-admin-file` against a new output path returned
   `BOOTSTRAP_ALREADY_COMPLETE` and created no file.
5. The PostgreSQL integration tests separately killed a child with a durable
   intent while its SQL was waiting on a lock, verified resume from that
   intent, and verified reconciliation after a committed child exited
   without reporting success. The original credential authenticated.

The recorder contained only the three harness probes through database
shutdown. The temporary container, database, and namespace were removed.
For the targeted regressions, run
`go -C deploy/offline test ./internal/install -run 'TestMaterialize|TestAdministrator'`
and `go -C services/control test ./internal/operations/bootstrap`. The
`TestActualBootstrap*` tests require the disposable loopback PostgreSQL
fixture documented in the bootstrap package.
