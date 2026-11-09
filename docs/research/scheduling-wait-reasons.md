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
