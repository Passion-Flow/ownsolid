# Gateway cutover preview (GW-111)

Reviewed 2026-11-09.

[Kong decK gateway diff](https://developer.konghq.com/deck/gateway/diff/)
resolves changes against the live gateway before sync. The
[Kubernetes dry-run API](https://kubernetes.io/docs/reference/using-api/api-concepts/#dry-run)
uses the ordinary authorization and validation chain without persistence.
A later apply rechecks its resource version.

Ownsolid already stores an immutable history of alias target revisions and
supports pause, drain, resume, retarget and rollback. Use the existing
cutover request, workspace authority and expected alias revision for the
control service preview. It returns the current and proposed alias
revision, paused state and exact deployment ID and revision. The service
resolves both from the locked state through the same validation as apply.
A preview holds only the ordinary transaction locks, executes no mutation
statements and creates no intent, target version or audit event. A later
apply revalidates under its own transaction and retains its usual
idempotency, target history and audit requirements.

Computing the diff in a caller from existing GET responses would skip the
live drain check and immutable rollback target validation. Running the
normal mutation and rolling back would execute triggers and assume all
its present and future side effects are transactional. Sharing a planner
before the first write avoids both problems. It gives the DEV contract
owner one service entry point for a future versioned Admin preview route.
The Console then needs to show the before and after state and apply using
the previewed expected revision. The existing cutover HTTP contract stays
on its current shape.

No dependency, model or dataset is added. The PostgreSQL fixture uses
[PostgreSQL](https://www.postgresql.org/about/licence/) and existing
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE) under the PostgreSQL
License and MIT respectively, permitting commercial use, offline use
and redistribution under their notice terms.

- Preview and apply pause, retarget, resume and rollback with the same
  inputs on a real PostgreSQL installation and compare projected state
  with the committed state. Confirm the same workspace key after cutover.
- Read the alias, target history, intent and audit counts before and after
  preview. They must remain identical. A held request blocks resume in
  both paths. Portal and foreign workspace sessions are refused.
- Commit another cutover between preview and apply. The older expected
  revision must fail. Retain existing HTTP validation, exact replay,
  concurrency, rollback checks and immutable history tests.

In the disconnected Linux run, the preview matched the committed state
for all four actions. SQL readback confirmed no alias, history, intent or
audit changes during preview. Portal and a separately authenticated
tenant were denied, a stale revision returned a conflict, and the held
request blocked resume. The existing cutover TLS contract, replay, failed
audit rollback and both pause/admission lock orders passed.
