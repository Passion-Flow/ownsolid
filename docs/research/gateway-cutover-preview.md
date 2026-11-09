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
before the first write avoids both problems. The Console can show the before and after state and apply using the
previewed expected revision. The existing cutover HTTP contract retains
its current shape.

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

## Admin HTTP preview

Use `POST .../aliases/{alias_id}/cutovers/preview` under the existing
cell-scoped Admin workspace distribution path. It accepts the same strict
JSON union as apply. A POST body carries the full proposed action and
expected revision while using the existing browser origin, session and
CSRF checks. Preview requires no idempotency key because it records no
mutation. Success contains only `before` and `after` with `revision`,
`deployment_id`, `deployment_revision` and `paused`. Apply retains its
existing request and response. Its transaction checks the same live state
again even when preview succeeded. A separate GET would place the action
and its reason in the URL and require a second input decoder.

Run all four preview actions through the production dispatcher over TLS.
Compare both states with PostgreSQL before and after the actual apply,
including zero alias/history/intent/audit changes on preview. Check
CSRF refusal, missing or duplicate fields, Portal and foreign tenant
refusal, stale revision and outstanding requests. DEV owns the public
OpenAPI and generated clients. WEB owns the preview and confirmation UI.
The route uses the existing [Go standard library](https://go.dev/LICENSE)
under BSD-3-Clause, with commercial and offline use and redistribution
under its notice terms. No dependency, model or dataset is added.

On 2026-11-09 the production dispatcher passed the disconnected
PostgreSQL and TLS checks. Pause, retarget, resume and rollback each
returned the exact before and after state. Preview left the alias,
target versions, cutover intents and alias audit counts unchanged.
The Admin preview succeeded without an idempotency key. It rejected
missing CSRF, missing and duplicate JSON fields, foreign tenant and
Portal callers, an old revision and a held request. Existing apply
replay, audit rollback and pause/admission lock ordering passed.
The HTTP test uses synthetic deployment observations.

## Prepared target boundary

The native Portal key run at 23b99915 used the real Rust gateway,
Control and PostgreSQL with a [synthetic loopback upstream](browser-suite-replacement.md#native-gateway-for-the-portal-key-flow).
It is a useful protocol fixture for a single target. The Linux
development lane had no pair of prepared, authorized actual targets.
The existing staged model files do not establish that a second
deployment is running or verified. The other lane's live environment
was outside this test scope.

Keep the cutover qualification open until the same authorized
workspace and gateway node have two actual, ready target deployments
available for the serial transitions. Rollback requires a ready target
with the original retained model, catalogue and serving recipe. Then
issue real native calls before and after pause, retarget, resume and
rollback, and compare the bound revisions and reported usage with the
immutable history. Existing evidence still covers the HTTP dispatcher,
real SQL, synthetic deployment observations and preview semantics.
Console interaction remains separate.
