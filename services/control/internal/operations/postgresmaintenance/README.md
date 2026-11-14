# PostgreSQL maintenance observations

`quiescence.go` borrows the selected installation-lock session's read-only
transaction with the exact effective pg_catalog/public schema path. Its own
queries qualify public relations; schema-path substitution refuses before the
existing catalog reader is called. It checks the existing cumulative `releases` ledger/catalog through
`database.CheckAccountProfileWithin`, the installed authority graph and fourteen
fixed remote-work/capacity predicates. It creates no pool, grants, table, worker,
provider call or process executor. It never changes task state, clears leases or
manufactures removal receipts. The caller retains transaction/lock ownership.

The strict native `check-kubernetes-maintenance` dispatcher in `cmd/server`
loads the existing protected typed configuration/environment mapping, acquires
the original installation lock and opens that read-only transaction. Its output
is historical after the command releases the lock. A later matched export must
use this check while continuously holding its own lock; a stored DTO is never
mutation authority. Current code/test execution is recorded in the M18 execution
checkpoint; source presence alone is not a passed SQL or native command gate.

Unresolved node tasks, running/unknown components or inference, unreleased GPU
leases, held gateway capacity, active training/preparation jobs or attempts,
unaccepted uploads, pending/running workflow authority and non-quiescent model
imports refuse with fixed domain codes. Accepted terminal history and preparation
human review remain unchanged. Physical runtime qualification is separate.

The [2026-10-31 checkpoint](../../../../../docs/execution/mvp/m18-postgres-maintenance-20261031.md)
records current real PostgreSQL18.6/vector0.8.7 complete releases22/86 and native
CLI checks, separate minimal SQL predicate fixtures, builds and exact cleanup.
The first missing-extension migration/native failures remain failed. Approved
vector activation is an initialization prerequisite, never disabled by this gate.
See the [operator procedure](../../../../../docs/runbooks/kubernetes/postgres-maintenance.md).

## Cumulative MVP PostgreSQL CPU profile

`mvp-postgresql-cpu-v1` is an explicit second selection in `CheckWithin`.
It must pass the database owner's distinct forward ledger/catalog checker before
any observation can succeed; it never aliases the historical platform ledger.
The original `ApplicationProfile=releases`, `SchemaVersion=22` and fourteen
checks retain their meaning and output. The new selection additionally refuses
active Mongo imports, object mutations/ingestions, registry imports/unknown
mutations, task delivery custody, middleware work and private-cluster custody.
No timeout releases a physical reservation or clears uncertain external state.
The observed version is read from the already verified immutable lineage.

The profile predicates have focused refusal coverage. Actual new-lineage SQL,
joined-writer backup, fresh restore and reopened HTTP serving require their own
source-bound native evidence; predicate/unit results do not establish them.
