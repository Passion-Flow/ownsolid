# Durable assistant loop guard (AGT-112)

Reviewed: 2026-11-12. This boundary extends the staged execution budget;
it does not activate a production schema, route or model.

## Sources and dependencies

[LangGraph's recursion limit](https://docs.langchain.com/oss/python/langgraph/errors/GRAPH_RECURSION_LIMIT)
bounds graph steps before a stop condition. Its
[persistent checkpoints](https://docs.langchain.com/oss/python/langgraph/persistence)
support interruption and recovery. A loop counter kept only in a worker's
memory cannot enforce the same bound after restart. The
[Temporal execution model](https://docs.temporal.io/workflow-execution)
separates execution identity, event history and cancellation. Cancellation
must be distinguished from proving that an already admitted effect did not
happen. PostgreSQL's
[transaction-held row locks](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS)
provide the shared serialization boundary for one active step per run.

Reuse the budget's Go standard library (BSD-style), pgx v5.11.0 (MIT),
google/uuid v1.6.0 (BSD-3-Clause), PostgreSQL and pgvector 0.8.7
(PostgreSQL License). Their primary licence links and commercial,
redistribution and offline-use terms are recorded in the
[budget research](product-assistant-execution-budgets.md#sources-and-licences).
No dependency, model or dataset is added. LangGraph and Temporal are
comparisons, not shipped components. Test records are synthetic.

## Chosen path

Add persistent runs and steps beside the candidate reservation schema.
Borrow the existing identity-owned transaction and policy source. The
policy supplies finite installation and user step limits; their minimum
is the current ceiling. Zero allows no run. Capture the initial ceiling
on creation and never increase it for that run. Admitted steps consume a
monotonic counter, including failed or withheld steps. A window change
does not reset that counter.

Derive tenant, user, workspace and entrance from the rechecked server
authority. Bind a server-issued run UUID to its intent digest and scope.
Bind each server-issued execution UUID to one registered command and its
canonical arguments. Lock the run, then the reservation and its counters.
Only a newly committed claim may dispatch. Permit one active step per run.
A duplicate or uncertain claim cannot dispatch again, including after a
new process opens the database. Store no prompt, cursor, command arguments
or result body in these checkpoints.

Start with deterministic navigation and typed reads through the existing
command service. Compute the step fingerprint on the server from command
ID and cursor. The existing risk admission rejects sensitive reads and
writes; no callback or shell interface is added. No model is needed.
Future writes still require their signed plan, human confirmation and
the action owner's transactional authorization and audit.

Read installation and user policy before claim, immediately before
dispatch and before returning data. Missing or failed policy is unavailable,
never enabled. A durable cancel prevents new claims. An in-flight read
may finish, but cancel or a failed final check suppresses its result.
An explicit stop is irreversible for that run. Recovery uses a new run
and a new authorized request; it never silently resumes a stopped run.
Request cancellation still reaches the existing bounded command context.
There is no claim that a cross-process stop instantly cancels a backend.

Finish a known command outcome in a new owner transaction. A cancelled or
unavailable completion transaction leaves the claim pending; no automatic
takeover or refund follows. Preserve unknown commit outcomes. Known failed
steps may be retried under a new execution UUID within the remaining limit.
Replaying an existing step returns a conflict with no result and no second
action. Scoped inspection exposes only run counters and state.

Deterministic steps reserve zero model tokens. Their local completion
digest identifies the exact run, execution and outcome, without result
data. Reuse the existing settlement field to close that zero-token hold;
it is not a gateway usage receipt. Positive model-token settlement continues
to require trusted authenticated gateway usage. Execution units are never
refunded. Cancelling a run does not release an uncertain reservation.

## Rejected alternatives

- A process-local counter or cancellation map loses authority on restart.
- A caller-supplied ordinal, limit or actor scope can bypass the bound.
- Holding a database transaction open across a model call or approval
  prevents prompt cancellation and consumes a connection for the pause.
- Automatically taking over a pending step can repeat an effect after a
  lost commit reply. Expiring its reservation can refund completed work.
- A generic operation callback would create an unreviewed action surface.
- Sharing the workflow engine's configuration or a new assistant parser
  would introduce a second policy authority.

## Acceptance

Use disconnected PostgreSQL with two independent pools and actual audited
commands. Cover installation and user step ceilings separately, initial
ceiling preservation, concurrent claims, duplicate and changed bindings,
restart-style reopening, and known failure retry. Verify cancellation and
policy loss before and after dispatch, no result after denial, and no new
action or success audit for refused claims. Preserve a real committed
claim when a simulated reply is lost and refuse automatic execution on
replay. Cover cancelled/failed finalization, revoked sessions and membership,
other users, workspaces and tenants, missing policy/schema and recovery.

Keep AGT-112 unchecked. Production startup policy and file references,
forward migration, grants and restore inventory, API contract, operator
controls, telemetry, model transport and browser flows need owner binding.
Provide an unnumbered DDL and exact factories; do not assume a migration
queue position or reuse the qualified two-case CPU transport as safety
qualification for the three retained assistant safety cases.

## Exact integration handoff

The startup and persistence owners must queue this candidate explicitly after
their current GW-120, DAT-101 and metering work. There is no assigned migration
number and no assumption that these tables exist in schema 58. Do not apply the
fixture DDL to a running installation or patch its catalogue fingerprint.

The additive factory signatures are:

```go
productagent.NewExecutionBudgets(commands *productagent.Service,
    policy productagent.ExecutionBudgetPolicySource) (*productagent.ExecutionBudgets, error)
productagent.NewExecutionRuns(budgets *productagent.ExecutionBudgets) (*productagent.ExecutionRuns, error)
productagenthttp.NewGuarded(apps *applications.Service,
    installation productagent.ExecutionPolicy,
    policy productagent.ExecutionBudgetPolicySource) (*productagenthttp.Handler, error)
```

`ExecutionPolicy` is `func(context.Context) (bool, error)`.
`ExecutionBudgetPolicySource` is
`func(context.Context, transaction.Executor, productagent.BudgetSubject) (productagent.ExecutionBudgetPolicy, error)`.
The supplied executor is the current owner's transaction; do not open another
connection to read policy. `BudgetSubject` contains rechecked tenant, user,
workspace and entrance. Neither scope nor policy may come from model or browser
fields. Source reads must honor cancellation and return an error for an absent
decision. Explicit user disable is `BudgetAccessDisabled`; absence is not disable
and must remain unavailable. No default allow, cached allow or fallback allowance.

The typed configuration owner must provide an explicit installation enable,
stable UTC window definition, finite installation/global-user execution and
token ceilings, explicit per-user access and both finite run-step ceilings.
Reject unknown fields, negative values and ambiguous booleans at startup; zero
means no capacity. Preserve the same window boundaries across replicas and
policy revisions. Installation kill must be reread for every admission and
result fence. Any policy artifact or future gateway credential is supplied by
a protected file reference, never inline secret text, command-line content or
browser configuration. This candidate adds no secret and requires no model.

The unnumbered forward input is
[`testdata/execution-budget.sql`](../../services/control/internal/modules/productagent/testdata/execution-budget.sql):
five tables, no policy table, sequence, trigger or function. Installation and
global-user counters are global authorities. Reservation/run/step placement
must be classified by ARC under ADR 0006 before registration: the isolated
fixture has tenant/user/workspace/entrance binding but no site/cell columns.
Production cell-owned classification requires trusted local defaults, scoped
foreign keys and reads, plus foreign-cell denial tests; do not silently treat
these fixture tables as registered global tables. Current startup restricts
the database to the default cell. This is an explicit integration boundary.

Minimum runtime privileges are `SELECT, INSERT, UPDATE` on those five tables;
the command owners retain their existing session, workspace and audit rights.
No assistant grant of `DELETE`, `TRUNCATE`, schema creation, general SQL or a
policy-edit permission is needed. Bind these grants to the deployment owner's
existing runtime role, without inventing a second credential or role name.
The migration owner must register table count, catalogue fingerprints, cell
classification, restore inventory and least-privilege tests together. Never
run the fixture's owner-privileged test connection as the production role.

Restore must preserve counters, reservations, run counts, stop state, step
bindings and pending claims in one consistent snapshot. A rollback first
disables and unmounts this handler. Dropping its tables is unsafe when claims
exist because it loses the replay and quota authorities. Keep the additive
schema and data across application rollback; any destructive downgrade needs
the persistence owner's explicit supported backup/restore procedure. No timer
deletes or refunds a pending claim. Retention and privileged reconciliation
need a separate authorized and audited contract.

Native test entry points are `TestDurableExecutionBudgets`,
`TestDurableExecutionRuns`, `TestAuthenticatedAssistantHTTP` and
`TestAuthenticatedGuardedAssistantHTTP`, using the README's protected fixture
inputs. The last two require the real dispatcher registration overlay until
the shared owner mounts the handler. The HTTP candidate preserves the existing
versioned command body. Public run inspection/cancel, confirmed actions and
browser controls require separate owner contracts; they are not exposed here.

## Isolated acceptance

The schema-58 candidate passed nineteen real PostgreSQL run scenarios on
Linux arm64, using two independently opened pools and network mode `none`.
Separate installation and user ceilings, concurrent claims, duplicate and
changed replays, immutable initial limits, known failure retry, pre/post
policy failure, installation kill, cross-instance stop, cancelled context,
lost claim and completion replies, actual finalization rollback, changed
intent, cross-workspace/tenant and revoked membership/session all passed.
Registered sensitive-read and write risk were refused before charging.

In one fresh disconnected PostgreSQL 18.6/pgvector 0.8.7 fixture, all thirteen
reservation scenarios, those nineteen run scenarios, the existing scoped
command root, both real HTTPS dispatcher roots, and the real assistant audit
export with the standalone verifier passed together. The guarded HTTP root
covered Admin and Portal, typed reads without a model, explicit disable,
missing/failed policy, zero run and execution capacity, CSRF, unknown fields
and recovery. Denied initial requests left neither an empty run nor a success
audit. Cancellation after dispatch suppressed data while preserving the actual
read's correlated audit.

The candidate passed the complete `make check`, `make build` and `make test`
gate on macOS. Browser suites ran sequentially with two workers and no
automatic retries: Console 1,075 passed/28 live-environment skips; Portal
389 passed/9 live-environment skips. Assistant and audit race tests and vet,
and the five qualifier unit tests, passed. Linux amd64 binaries have been
cross-compiled but native execution is not yet recorded; test-server transfer
is still pending. Neither that compilation nor the separate two-case model
transport qualifies the three retained assistant safety cases.
