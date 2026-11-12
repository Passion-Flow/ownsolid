# Read-only access to published training inputs

Research for TRN-101, DAT-101 and the ARC-111 composition boundary. Reviewed
2026-11-13 before implementation. The separate Agent gateway currently constructs
its dataset service without file access. This note does not qualify that gateway,
its restricted database role or any new filesystem deployment.

## Current ownership and primary sources

The dataset and training stores retain one exclusive writer ownership flock.
Opening another full Store, including OpenStoreForVerification, correctly refuses
while control owns it. A verifier is an exclusive maintenance operation, not an
online reader. Do not remove or downgrade the writer/verification locks.

[Linux flock](https://man7.org/linux/man-pages/man2/flock.2.html) associates locks
with open file descriptions. Advisory writer exclusion does not prevent a
separate read-only descriptor from reading immutable bytes. A reader must never
unlock, replace or truncate the writer's carrier. Closing its independent
read-only descriptor cannot release the writer's lock.

[Go os.Root](https://go.dev/blog/osroot) anchors traversal-resistant operations to
an opened directory. The [Root contract](https://pkg.go.dev/os#Root) permits
concurrent access and retains its original directory if that directory is moved.
It is not an OS sandbox and does not prevent mount traversal. Retain current
private path/UID/mode checks, exact namespace identity, descriptor identity,
full size/hash verification and post-read checks. A changed pathname must not
silently switch an existing reader to a different namespace.

[Docker read-only bind mounts](https://docs.docker.com/engine/storage/bind-mounts/)
are an existing same-host mechanism for separately constraining a reader's writes.
This note does not assume a cross-host mount or qualify NFS/CSI. A deployment must
explicitly provide the same retained namespace at its configured artifact root;
no missing mount is created or adopted. Read-only submount behavior requires the
installation owner's real mount qualification.

[MLflow artifact stores](https://mlflow.org/docs/latest/self-hosting/architecture/artifact-store/)
separate large retained artifact bytes from SQL metadata and support explicitly
configured local/shared stores. This is a design comparison; no MLflow service,
package or protocol is imported.

## Chosen slice and rejected alternatives

First prototype independent read-only descriptors while the writer remains
exclusively locked, root replacement, changed bytes, symlinks, closure and repair
outside product code. Implement only after that mechanism holds. The dataset
owner then exposes a concrete read-only published-file reader with no mutation,
initialization or arbitrary callback. It independently owns and closes only its
root/carrier descriptors. Existing Store APIs retain their exclusive locks.

The service may use the concrete reader for published-source metadata admission,
training snapshot verification and authorized train/validation asset opens.
Neither the reader nor a retained snapshot authenticates a user/node or grants a
lease. Existing transactions retain current actor/Workspace/node/attempt locks,
retirement checks, frozen inventory comparison and the new authority pass after
file verification. Missing, closed, foreign or changed storage fails closed.

Reject giving a gateway the full writable Store, weakening ownership flock,
opening a new namespace, assuming a remote/shared mount, inventing an artifact
RPC, success callbacks, privileged retries and using SQL grants to hide missing
file access. Do not add processing/training/GPU workers to the gateway.

ClaimTraining currently needs dataset file verification and Models.ReadyWithin.
Gateway file transfers remain on control under ADR 0048. Preparation of an
accepted training adapter separately requires the training published store;
checkpoint resume/transfer and accepted adapter verification must retain their
own seal/receipt and current-attempt checks. Dataset access alone cannot qualify
all gateway claim profiles. Inventory these separate readers before claiming the
whole owner graph is closed. Model ownership remains outside this lane.

## Protected SQL locks remain a separate contract

PublishedSourceWithin locks dataset and immutable version rows FOR UPDATE;
training locks the current scoped job before its attempt/GPU checks. PostgreSQL
requires UPDATE privileges for those row locks. Keep their order and lifetime.
ARC/database owners must admit narrow trusted-schema security-definer functions
through the formal migration/catalog/role workflow, not arbitrary id UPDATE
grants. This slice introduces no function, privilege, query rewrite, protocol or
runtime switch. Its reader qualification does not qualify the restricted role.

## Licences and offline behavior

No new dependency, model or dataset is added. Existing Go/os and x/sys licensing
is BSD-3-Clause; pgx is MIT; PostgreSQL uses the PostgreSQL License. Commercial,
redistribution and offline use are allowed with their original terms/notices.
The external prototype uses only Python's standard library under the PSF licence
and generated fixture bytes; it adds no product Python dependency. Docker/MLflow
are comparisons, not new shipped inputs. No runtime network operation is added.

## Failure and completion checks

- Concurrent reader succeeds against actual published bytes while the existing
  writer and a second-reader descriptor remain open; another writer remains
  refused before and after reader closure. No source or namespace byte changes.
- Missing mount, wrong tenant/namespace, unknown root files, changed carrier,
  replaced root, symlink, changed/truncated/overlong file and hash mismatch refuse.
  Exact repair is retryable; no source or successful lease is exposed on failure.
- Cancelled/deadline reads close their descriptors. Explicit Close is idempotent,
  rejects later opens and cannot invalidate an already returned pinned file.
  Concurrent read/Close must be race-free and have bounded cancellation.
- The dataset service with Store=nil and the real reader verifies the same frozen
  train/validation snapshot and returns actual bytes. Zero/closed readers and a
  forged/mismatched snapshot refuse; arbitrary asset names cannot be opened.
- Actual PostgreSQL authorization/retirement/rollback/interleaving cases and the
  real ARC gateway graph must run separately when resources and owner integration
  exist. Preserve current-role revocation and final transaction after I/O. No
  standalone gateway, restricted-role or P1 completion is claimed by unit checks.

## External native mechanism result

The generated-only Linux amd64 descriptor prototype passed all ten checks in
0.0343 seconds, with 17,408 KiB maximum parent RSS and a 128 MiB address-space
ceiling. It ran no PostgreSQL, processor, Docker or GPU process. Evidence is
retained at /root/lane-trn-reader-prototype.af7ts6yw; prototype SHA-256 is
f8f74d6b50b66a61c6d28faae61a201b3d2e848b3da3c04ecf7d108513ae80ae.
Independent process reads and descriptor closure preserved writer exclusion.
Same-length file changes, missing/symlink files, namespace changes and root
replacement were refused; exact repair restored reads. The fixture was removed.
This proves the kernel descriptor/ownership mechanism only, before Go product
implementation. It does not qualify a mount, database role or gateway graph.
