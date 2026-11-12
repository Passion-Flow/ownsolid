# Durable product assistant execution budgets (AGT-112)

Reviewed: 2026-11-12. This is an implementation contract for the owned
reservation boundary. Production configuration, route binding, loop limits
and browser acceptance remain open. No policy is inferred from model output.

## Sources and licences

[Kubernetes resource quotas](https://kubernetes.io/docs/concepts/policy/resource-quotas/)
apply aggregate limits when admitting work. Retrospective usage reports do
not substitute for admission. The existing ownsolid metering budget preview
reserves no capacity and has workspace, rather than user, scope.

[PostgreSQL row locks](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS)
last until transaction completion. A waiting writer reads the updated row
after its predecessor commits under
[Read Committed](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED).
[INSERT ON CONFLICT](https://www.postgresql.org/docs/18/sql-insert.html)
establishes a unique reservation before charging either counter. Lock every
reservation first, then installation and user counters in that fixed order.

[LangGraph persistence](https://docs.langchain.com/oss/python/langgraph/persistence)
uses persistent checkpoints for interruption and recovery. Its in-memory
checkpointer loses state on restart. Budget reservations must likewise
outlive the process admitting an action. A checkpoint alone cannot establish
whether a remote effect happened after a lost reply.

No dependency, model or dataset is added. The code reuses Go's
[BSD-style standard library](https://go.dev/LICENSE), existing
[pgx v5.11.0](https://github.com/jackc/pgx/blob/v5.11.0/LICENSE) under MIT,
and [google/uuid v1.6.0](https://github.com/google/uuid/blob/v1.6.0/LICENSE)
under BSD-3-Clause. The fixture reuses
[PostgreSQL](https://www.postgresql.org/about/licence/) and
[pgvector v0.8.7](https://github.com/pgvector/pgvector/blob/v0.8.7/LICENSE)
under the PostgreSQL License. These terms permit commercial use, offline
use and redistribution with applicable notices. LangGraph is a comparison,
not a runtime dependency. Fixture identities and requests are synthetic.

## Contract

The command service's current installation policy remains the first
execution gate. A separate trusted policy source must supply an explicit
user enable decision, a stable UTC budget window, and finite installation
and user ceilings for admitted executions and model tokens. An absent,
unreadable or invalid policy is unavailable. Explicit user denial is forbidden.
A zero ceiling means zero capacity, never unlimited capacity. No defaults,
environment parser or second configuration store are introduced here.

Recheck the real session and workspace in the owning transaction before
reading policy or touching a reservation. Derive tenant, user and workspace
from those server authorities. The browser and model cannot choose them.
User counters span both entrances, tenants and every workspace for the same
global user UUID; installation counters span the database. The trusted source
must supply the same user ceiling for that UUID and window. Policy revisions do not reset
usage. The policy owner must keep window boundaries stable across replicas
and configuration changes. Use the database clock after acquiring counter
locks to reject an expired or future window.

A trusted adapter supplies a server-issued reservation UUID, a digest of
the exact bounded execution, and its maximum total model-token charge.
Deterministic commands may reserve zero tokens. One newly admitted
execution permanently consumes one execution unit and holds the maximum
tokens in both counters atomically. Check by subtraction to avoid signed
integer overflow. Any refusal rolls back the new reservation and both
counters. There is no tool execution inside this storage transaction.

A repeated UUID with the same subject, entrance, workspace, digest and
maximum charge returns the durable state without charging again. A
different binding conflicts. Only a newly committed reservation permits
the adapter to start one execution. A held replay is pending, not another
permission to execute. A settled replay is already complete. A missing or
failed commit response permits no execution; reconcile the same UUID.

Settlement accepts only trusted, bounded actual usage and the digest of
the authenticated gateway receipt. It changes held to settled once,
returns unused tokens to both original windows, and keeps the execution
unit charged. Repeating the same usage and receipt is idempotent; changed
usage or receipt conflicts. Require the original authenticated scope for
settlement. Revocation can leave a conservative hold; it cannot grant a
refund. There is no automatic release on cancellation, timeout, restart,
window expiry or missing usage. A new policy window cannot move an old hold.

The reservation is internal admission state, not an executed product action
and not a success audit. Existing action transactions still own permission,
confirmation, effects and assistant audit attribution. Reservation admission
does not authorize a write or replace its signed plan and human confirmation.
Reserve an effect only after its required human confirmation has been verified;
planning inference is a separate bounded read execution. An approval pause must
not become an automatic effect merely because a reservation exists.

## Alternatives rejected

- Process-local counters cannot coordinate replicas or survive restart.
- Counting audit events or historical cost reports races concurrent work
  and cannot hold the worst-case charge before inference.
- Blindly retrying after an unknown commit can start an effect twice.
- Expiring a hold by time can refund work that completed without a reply.
- Resetting usage on policy revision lets configuration changes bypass a
  still-active window. Changing ceilings must reuse the same counters.
- A new assistant-owned policy table or default allowance would compete
  with the configuration owner's missing authoritative source.

## Verification requirements

Use real disconnected PostgreSQL transactions through independent service
instances. Concurrent requests must never exceed either ceiling, including
two different users sharing the installation ceiling. A denied request
must leave no reservation, counter change, tool effect or success audit.
Verify duplicate races, changed bindings, zero capacity, exact capacity,
integer bounds, expired windows, cancellation while locked, transaction
rollback, and a lost commit reply. Reopen state through a new instance and
verify a held replay cannot execute again. Verify settlement races and
changed receipts, and preserve the original window across policy rollover.
Exercise revoked sessions and workspace, user and tenant isolation. Missing
candidate schema must fail closed. The candidate DDL is fixture-owned until
the persistence owner registers a forward migration and restore catalogue.

The public endpoint, production migration, trusted policy source, gateway
usage binding, production run controls and both browser entrances
are separate integration requirements. AGT-112 remains unchecked until
those complete the main flow and offline acceptance.

The staged [persistent run guard](product-assistant-loop-guard.md) now binds
deterministic commands to monotonic step counts and policy/cancellation
fences. Its guarded HTTPS factory uses this reservation component and does
not register a production route or schema.

## Integration surface

`productagent.NewExecutionBudgets(commands, policySource)` borrows the
existing command service and accepts no pool configuration. `Reserve`
takes the server-authenticated actor and a `BudgetExecution`; `Settle`
takes the same scope and a `BudgetSettlement`. The trusted adapter alone
constructs execution and canonical receipt digests and token bounds. Do not
expose these methods as an endpoint taking those values from a caller.

The boundary uses existing public error codes: `RATE_LIMITED` (429),
`ACCESS_DENIED` (403), `SERVICE_NOT_READY` and sanitized storage errors
(503), `IDEMPOTENCY_CONFLICT` (409), `RESOURCE_NOT_FOUND` (404), and
`VALIDATION_FAILED` (400). Scope mismatch does not disclose an existing
reservation. Commit uncertainty preserves the existing transaction outcome
and returns no newly admitted result. The generic HTTP rate fallback is not
a promise that capacity will be available after that time.

The [decision](../decisions/0069-retain-assistant-reservations-until-usage-is-known.md)
records conservative recovery. Production registration must pair a forward
migration and restore catalogue with the trusted policy and bounded adapter;
creating the schema alone does not enable the gate or complete the feature.

## Isolated acceptance

On 2026-11-12, the Linux arm64 repository binary passed all thirteen
`TestDurableExecutionBudgets` cases on PostgreSQL 18.6 with pgvector 0.8.7
in Docker network mode `none`. Two independently opened pools exercised
shared installation and global-user counters. Execution and token ceilings
were exhausted independently. Duplicate admission and settlement races,
window changes, integer bounds, policy and schema recovery, cancellation
while actually blocked on a PostgreSQL lock, revoked authority and foreign
scope all passed. A callback fault forced a real rollback. An injected lost
reply after a real commit returned no admission result, and a fresh service
on the second pool recovered the held or settled state without another
charge or refund. This is not a native network-proxy commit-drop test.

The assistant and audit package race tests and vet passed on the same
rebased source. The paired audit export and standalone verifier, current
session commands and both-entrance HTTPS policy cases also passed against
disconnected PostgreSQL. These are component and staged-dispatcher results;
they do not establish a production migration, policy source or browser flow.

The same component passed a complete native repository gate with both
browser suites at one worker. After integrating the monitoring contract,
it passed the full gate again at `3b49f6ae`, with two workers per entrance,
no automatic retries, and focused race tests and vet. The
[shared browser gate boundary](app-lane-questions.md#shared-browser-gate-boundary)
retains the earlier failure separately.

Later upstream changes added standalone CLI code and Markdown documentation.
Comparison with the measured source verified that every non-CLI,
non-Markdown repository file remained byte-identical. Only the CLI command
imports its module. Repository checks and the complete build, CLI tests,
CLI race tests and vet passed after that rebase before fast-forward
publication at `279382f2`. This preserves the unchanged completed stages
and records the affected module's separate acceptance.
