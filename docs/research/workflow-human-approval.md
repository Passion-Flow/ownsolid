# Durable human approval for workflow runs

APP-103 needs a run to stop at a real approval boundary for as long as the policy
allows, then continue the same published version when an authorized person decides.
The browser, worker and control-plane process can all be absent while it waits.

## Primary references

- [n8n Wait](https://docs.n8n.io/integrations/builtin/core-nodes/n8n-nodes-base.wait/)
  persists execution data to the database when a run pauses and reloads it when
  the wait condition is satisfied.
- [Dify Human Input](https://docs.dify.ai/en/use-dify/nodes/human-input)
  routes approved, rejected and expired decisions through workflow edges.
- [Temporal design patterns](https://docs.temporal.io/design-patterns) model a
  human decision as a durable event and distinguish an acknowledged update from
  a query or a notification.
- [PostgreSQL 18 row locking](https://www.postgresql.org/docs/18/sql-select.html#SQL-FOR-UPDATE-SHARE)
  supports serializing decisions on the affected run and claiming ready work.

## Design and boundary

Extend the existing workflow owner. Commit the waiting state, its pending approval,
its immutable version binding and the continuation cursor in one transaction.
Release the worker lease on that transition. Keep waiting runs out of stale-worker
recovery. A decision transaction checks the approver's current membership and
permission, consumes the pending approval exactly once, appends audit evidence
and publishes runnable work as one atomic change. Use a fresh execution lease
when claiming the continuation. Record a retryable decision using the existing
mutation-intent semantics so an uncertain commit can be reconciled.

An in-memory channel or a blocked worker cannot survive restart. A URL bearer token
alone would lose the ownsolid identity and audit boundary. Installing a separate
workflow engine would duplicate the existing run, permission and publication owner.
The existing executor, recovery and SQL constraints must be changed together and
remain outside the APP lane's allowed paths. There is no implementation claim.

The design adds no model, dataset or external engine. Go's standard library uses a
[BSD-style license](https://go.dev/LICENSE) and the already used PostgreSQL engine
uses the [PostgreSQL License](https://www.postgresql.org/about/licence/). Both permit
commercial use, offline operation and redistribution subject to retaining notices.
The product references above provide design examples only.

## Acceptance

In a real installation with external egress blocked, start a published flow and
observe the pending approval in its browser run view. Verify its downstream node
has not run. Stop and restart control, allow any former worker lease to expire,
then decide as a different authorized actor and observe exactly one continuation
from the checkpoint. Test denial, expiry, cancellation while waiting, cross-tenant
and revoked-approver denial, concurrent opposing decisions, duplicate decision
requests, uncertain commit recovery and unchanged behavior for existing run versions.
