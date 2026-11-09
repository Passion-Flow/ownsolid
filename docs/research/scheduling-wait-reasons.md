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
