# Scheduling wait reasons

SCH-108 needs an explanation from the decision that actually deferred a job.
[Kueue troubleshooting](https://kueue.sigs.k8s.io/docs/tasks/troubleshooting/troubleshooting_jobs/)
separates jobs not yet considered from quota shortfalls and from execution
conditions. The [Kubernetes scheduling framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/)
separates queue sorting, resource filtering, reservation and waiting, and
reconsiders jobs when relevant capacity changes. [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html#section-5)
explains why diagnostic details crossing an interface must be screened for
private information. [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html#section-15.5.4)
allows inaccessible and absent resources to be indistinguishable.

Attach typed guidance to the existing decision, preserving established error
identities and the ranked queue. A queue reason describes only a confirmed
deferral from the current ranking for that exact entry. The dispatcher evaluates
resource pressure and physical placement in their own stages. Preflight and reservation
share the same hard ceiling and currently available comparison. Preflight
retains its existing estimator data for its authorized caller. The safe
projection exposes exact available units only for the subject workspace.
Ancestor and cell reasons report the resource, level and request without
exposing shared available quantities.

A wholly nominal request blocked by live loans can ask the owner to initiate
reclaim. A pending exact claim blocked by selected releases waits for positive
release evidence. A different attempt held by the workspace's existing claim
waits for the claim to settle or be withdrawn. Insufficient lendable capacity
reports a capacity action without lender identities or sibling usage. Keep
victim identities in the owner-only stop plan. An absent or inaccessible scope
returns one generic access reason without quota details. The existing caller
still authorizes the job and workspace before projecting a result. Successful
admission and unrelated errors have no waiting reason.

Derive reasons on demand from the current input, transaction decision or the
exact previously read release proof. A fresh attempt observes
committed quota or release changes through the existing locks and reads. A
persisted reason could outlive the state that justified it. Parsing error text
would lose stable distinctions and make translations dependent on the engine.
Re-running an independent scheduler would create another admission decision.

Use the existing [Go](https://go.dev/LICENSE) standard library, BSD-3-Clause,
and PostgreSQL and pgx already used by the repository tests. The
[PostgreSQL License](https://www.postgresql.org/about/licence/) and [pgx MIT
license](https://github.com/jackc/pgx/blob/master/LICENSE) permit commercial,
redistributed and offline use with their notices. Kueue and Kubernetes are
Apache-2.0 references. This slice adds no dependency, model or dataset.

Verify deterministic shortfalls and exact fits, no waiting reason for the head
or an absent queue entry, and a changed rank after a share changes. In the
isolated PostgreSQL schema, distinguish hard limits from held quota, observe
the transition from a needed reclaim to an exact pending release and then
admission, and verify that foreign and absent scopes give the same result.
Include an exact replay and a release rolled back by its owner. HTTP, browser
rendering, permissions at the product entrance and cell placement diagnostics
remain owner integrations for full SCH-108 acceptance.

## Verified Kueue blockers

Read the same exact v1beta2 Workload over the existing cell TLS connection.
The [Kueue v0.20.0 Workload contract](https://github.com/kubernetes-sigs/kueue/blob/v0.20.0/apis/kueue/v1beta2/workload_types.go)
defines granular quota reservation reasons. Its
[condition writer](https://github.com/kubernetes-sigs/kueue/blob/v0.20.0/pkg/workload/workload.go)
stamps the Workload generation. The
[versioned troubleshooting guide](https://kueue.sigs.k8s.io/v0.20/docs/tasks/troubleshooting/troubleshooting_jobs/)
also documents legacy `Pending` and `Inadmissible` conditions and shows
that an unattempted Workload can lack any condition.

Use current `QuotaReserved=False` with `WaitingForQuota` to wait for
reservation, `ExceedsMaxQuota` to adjust the request or quota, and
`TopologyPlacementFailed` to review the cell's placement constraints.
`Misconfigured` and the legacy `Inadmissible` ask the operator to review
the cell queue. A current admitted Workload with `PodsReady=False` waits
for its pods. Ignore stale and unrecognized reasons, including generic
`Pending` when it cannot identify a blocker. Finished and terminating
states take precedence. Preserve Volcano's separate observation semantics.

Project the fixed codes through the scheduling wait-reason boundary.
Never copy Kueue's message, queue name, capacity amounts or other jobs into
the result. This avoids leaking details while reusing the decision that
actually deferred the workload. The controller observation never releases
a reservation or grants placement authority.

Verify the allowlisted codes, a current false condition versus stale
conditions, readiness and terminal transitions, and an exact-UID TLS read.
The Linux lane has no pinned offline cell-controller assets installed.
The installation owner must supply the signed RKE2, Kueue and Volcano
artifacts, scoped service account and workload ownership binding before
live cell qualification. This slice uses the existing Go standard
library and authenticated transport. Kueue is an Apache-2.0 reference
with no new runtime dependency, model or dataset.

## Installed workload handoff

The existing [training attempt record](../../services/control/migrations/postgresql/000015_training.up.sql)
binds the job, node and agent task. The
[cell orchestration contract](../../api/agent/v1/kubernetes.go) provisions
the Kubernetes cell. These records do not establish which scheduler
object belongs to an authorized attempt. The scheduler adapter's current
production call sites do not provide a workload lookup.

The workload owner must persist the controller kind, namespace, name
and UID from the creation result with the exact authorized site, cell,
tenant, workspace and attempt. [Kubernetes object identity](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#uids)
distinguishes successive objects that reuse a name by UID. A new attempt
or a replaced UID requires a fresh owner binding.

The scheduling consumer takes the reference from rechecked owner
authority and the client for its bound cell. It must read the exact
object over that client's verified TLS connection and only project the
blocker after the adapter checks API kind, namespace, name and UID.
Remove the public projection of caller-assembled observations. A
name-only lookup or an exported projection of cached input could show
another object's blocker after name reuse. Keep errors from the adapter
and leave the decision free of guidance when verification fails.
This contract does not create the owner binding or authorize a caller.

Use a disconnected TLS fixture to exercise the consumer with a current
blocker, a stale generation, a replacement at the same name and a
forbidden read. Require fixed guidance from the exact current UID only
and no guidance on errors. An incomplete reference must fail before
network I/O. The test uses the existing Go transport and the pinned
Kueue contract above, with no added dependency or test data source.

Live qualification requires the signed offline RKE2, Kueue and Volcano
asset inventory and a ready disposable cell. Provision a service account
restricted to the scheduler objects in the workload namespace and
materialize its protected credential and CA. On that cell, prove an
authorized workload waiting for reservation and pods, refusal of an
inaccessible namespace and replacement UID, and clean cancellation
through the existing owner path. The current lane can qualify TLS and
condition projection independently while these inputs are pending.

## Authorized workspace quota status

[Kueue ClusterQueue status](https://kueue.sigs.k8s.io/docs/concepts/cluster_queue/)
distinguishes reserved quota from admitted and executing workloads. Expose the
ownsolid workspace's configured guarantee, hard ceiling and aggregate held
reservations as quota accounting. Hard ceiling minus held reservations is
`unreserved_quota`; it is not physical availability, lendable capacity or a
promise that a particular job can start. Borrowing, queue turn, topology and
execution remain separate decisions.

Use the selected repository and the existing Workspace owner's
`ReadScopeWithin` in the same READ COMMITTED transaction as the quota read.
The owner rechecks the live session and membership. Ignore cached actor
workspace and role fields. Ordinary current workspace members may read this
resource allocation; they gain no policy mutation or metering authority.
Acquire the shared policy fence and existing workspace reservation lock before
reading policy and held usage. PostgreSQL's
[row and transaction advisory locks](https://www.postgresql.org/docs/18/explicit-locking.html)
keep replacement, reserve and release ordered through the confirmed read
transaction outcome. A separate authority transaction followed by a pool read
would allow revoked authority or a mixed policy/usage result.

An absent policy returns `unconfigured` with fixed operator configuration
guidance and no quantities. An authorized workspace without an allocation
returns `unallocated` with workspace allocation guidance; inaccessible and
missing workspaces remain indistinguishable and reveal neither state nor
revision. A configured allocation returns only its own resource rows, sorted
by resource. Encode integral resource quantities as decimal strings so byte
counts and aggregate reservations retain int64 precision through browser JSON.
Fail closed on malformed, overflowing, foreign-cell or over-ceiling active
usage. Validate each contributing demand as an integral, nonnegative JSON number
before trusting SUM; a positive row must not conceal a negative, fractional or
string-valued row. Return no payload on any transaction failure, including an uncertain
commit. Never include reservation identities, other workspaces, ancestor
amounts, raw database/controller messages or stored job wait text.

Verify real authenticated PostgreSQL reads for Admin and Portal, current
membership and session revocation, forged cached scope, foreign installation
scope, absent policy and allocation, committed reserve/release and a rolled
back reserve. Exercise a blocked reservation lock to show that a reader sees
the committed usage after it waits, and verify data corruption produces no
payload. Check uncertain transaction outcomes independently of payload
projection. HTTP composition, the public OpenAPI contract, translations and
the UI remain integrations owned by IAM and Web; an uninstalled module handler
is not an end-to-end public entrance or completion of SCH-108.

The Go, pgx and PostgreSQL licences above cover this addition. The existing
google/uuid dependency uses the [BSD-3-Clause licence](https://github.com/google/uuid/blob/master/LICENSE),
permitting commercial use, redistribution with notices and offline use.
No new dependency, controller asset, model or dataset is introduced.

## Public quota-status failure contract

The current [identity recheck](../../services/control/internal/modules/identity/session_repository.go)
reports expired, revoked, disabled-user, removed-admin and invalid-source
sessions through `AUTH_SESSION_EXPIRED`. The
[Workspace authority](../../services/control/internal/modules/workspaces/scope.go)
hides absent and inaccessible scope behind `RESOURCE_NOT_FOUND`. Preserve
these real owner codes and the established `ACCESS_DENIED` boundary.
Do not invent additional account/source/reauthentication codes or publish
unregistered error names. Unknown owner or policy failures become
`STORAGE_UNAVAILABLE` at the HTTP boundary, with no payload.

The owned HTTP component produces `VALIDATION_FAILED` (400),
`AUTH_SESSION_EXPIRED` (401), `ACCESS_DENIED` (403),
`RESOURCE_NOT_FOUND` (404), `STORAGE_COMMIT_OUTCOME_UNKNOWN` (409),
and these retryable 503 codes: `STORAGE_UNAVAILABLE`,
`STORAGE_CANCELLED`, `STORAGE_TIMEOUT`, `STORAGE_LOCK_TIMEOUT`,
`STORAGE_DEADLOCK` and `STORAGE_COMMIT_ROLLED_BACK`. This is the complete
owned failure set; IAM retains entrance authentication, origin, capacity
and readiness failures. Configuration guidance remains a successful
status with its fixed reason, not an HTTP error.

Test the real owner codes, storage and uncertain-outcome projection, and
refuse an unregistered equal-text identity label. This contract uses only
the existing source interfaces and licensed stack described above.

## Exact revision metadata

[RFC 8259 section 6](https://www.rfc-editor.org/rfc/rfc8259.html#section-6)
limits exact interoperable JSON integer numbers to the binary64 safe range.
The stored quota revision can reach PostgreSQL bigint's positive maximum.
Represent `policy_revision` as a decimal string as well as resource quantities;
do not silently round it or reject a valid stored revision for the browser.
Kubernetes also uses strings for
[resource-version metadata](https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions).
The internal admission counter and its concurrency rules remain int64.

The unconfigured status reports `"0"`; configured and unallocated status
report the actual positive revision string. Extend the real authenticated
PostgreSQL read to a revision above binary64 precision and to MaxInt64, then
restore only that test fixture's original revision. Check the JSON field's
string representation independently of resource totals. This requires no
new dependency, model or dataset and retains the licences already recorded.
