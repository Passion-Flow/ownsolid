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
The HTTP test uses synthetic deployment observations. The regression validates
the four previews against the published response schema and the foreign,
stale and undrained replies against the shared Problem schema.

## Public contract and client acceptance

Publish `adminPreviewAliasCutover` on the registered cell-scoped Admin path.
The [OpenAPI Operation Object](https://spec.openapis.org/oas/v3.1.0.html#operation-object)
lets this operation reuse the apply request schema while declaring separate
headers and responses. Keep the explicit nullable request fields for pause,
resume, retarget and rollback. The live validator checks which fields must
be null for each action and compares the rollback source with the expected
revision. Preview requires the Admin session, exact Origin and CSRF token.
It does not require an idempotency key. The response contains the current
and proposed states in the standard `data` envelope. It does not reserve
an apply or promise that the target will still be ready when apply runs.

Use the existing generated Go and Python clients with the installed origin
and local CA. Their dependencies and offline redistribution terms are listed
in the [SDK review](sdk.md). No dependency, model or dataset is added. A
second request model or a caller-computed preview could diverge from apply.

Validate the success schema and safe problems against the registered TLS
dispatcher on PostgreSQL with the network disconnected. Cover missing and
duplicate fields, unsupported media, oversized payloads, missing CSRF,
unauthenticated sessions, foreign workspace, stale revisions and undrained
requests. Compare the four projected states to committed results and confirm
preview leaves the stored state and audit counts intact. Exercise both
SDKs with explicit null fields, the scoped path, CSRF, the typed success
and a revision conflict. The Console preview and confirmation are owned by WEB.

The published contract passed the Linux loopback TLS/PostgreSQL fixture.
The dispatcher returned the typed preview for all four actions, rejected an
anonymous request without CSRF with 403, and rejected an anonymous caller
with a valid CSRF context with 401. It rejected malformed, oversized and
non-JSON bodies and returned safe 404 and 409 problems for foreign scope
and live conflicts. The generated Go and Python clients serialized all four
actions, preserved explicit nulls, and decoded the preview and revision
conflict in local wire probes.

### Console handoff

Use the generated `adminPreviewAliasCutover` operation with the selected
site, cell, workspace and alias. Fetch CSRF as for apply, send the same
request fields and display `data.before` and `data.after` with both
deployment revisions and pause state. On confirmation, apply using the
same expected revision and the apply operation's idempotency key. A
`VERSION_CONFLICT` requires a fresh preview after reading the current alias.
An `ALIAS_REQUESTS_UNDRAINED` response leaves the alias paused. The UI owns
the preview display, confirmation and retry flow.

Help currently indexes conflict codes with entries in both locale catalogs.
The downloadable contract's 409 description and shared Problem schema
retain ALIAS_PAUSE_REQUIRED, ALIAS_ALREADY_PAUSED, ALIAS_NOT_PAUSED,
ALIAS_REQUESTS_UNDRAINED and ALIAS_ROLLBACK_TARGET_INVALID. WEB can add
these codes to the localized error list when both message catalogs have
their titles and recovery details.

## Admin CLI (DEV-102)

The [decK diff workflow](https://developer.konghq.com/deck/gateway/diff/) shows
live changes before an explicit [sync](https://developer.konghq.com/deck/gateway/sync/).
Use the same separation in the existing `deployment alias` command group.
`cutover preview` checks the proposed change on the server and shows both states.
`cutover apply` requires explicit confirmation and the operator's request UUID.
Use the same alias revision and arguments for apply after reviewing preview.
Reuse the same UUID and arguments on uncertain apply. The server performs
revision and authorization checks under lock and owns idempotent replay.
The `cutover history` read shows immutable revisions and an opaque cursor
bound to the selected alias, operator and workspace. A rollback selects an
older alias revision and a currently ready deployment of that recorded model.

The CLI preserves null target fields and uses the installed Admin HTTPS
origin and existing session, CSRF, scoped paths, problem handling and page
rendering. A single interactive command would make automation and replay
harder. A local diff would miss server drain and target checks. No dependency,
model or dataset is added. The existing Go standard library and Cobra
licenses are recorded in the [SDK review](sdk.md#licenses) and permit offline
commercial use and redistribution under their notice terms.

Run the compiled CLI through the disconnected TLS/PostgreSQL fixture.
Confirm that preview changes no state, apply needs confirmation, lost apply
results can be replayed with the same UUID, stale state and undrained requests
are refused, and history retains the original target revisions. Test a
retarget and a rollback with verified replacement observations and verify
that unrelated workspaces and invalid action/target combinations fail closed.

The disconnected Linux CLI run covered all four preview/apply actions.
Preview and an unconfirmed apply preserved target history. A truncated
successful pause response led to an exact replay with the same UUID,
with one audit entry for each committed pause. Stale revisions, an
undrained request, foreign workspace and incompatible target arguments
were refused. The history cursor returned the rollback source and
replacement deployment. Deployment verification observations were
synthetic. The test used the production dispatcher and PostgreSQL.

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
