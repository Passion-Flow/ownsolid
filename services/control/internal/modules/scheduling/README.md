# Scheduling

This module validates the adapter-neutral job intent, quota policy, queue order
and whole-job preflight. The reservation repository borrows the workload owner's
PostgreSQL transaction to hold or release the exact attempt. The quota snapshot
repository also uses an expected policy revision. Enabled departments can
attribute loans to sibling workspaces and keep a reclaim pending until every
selected victim has a committed owner release. The cell adapter reads
the exact Kueue or Volcano object over verified TLS. Provision the cell's
bearer credential as a protected regular file. Rotate it by atomically
replacing the file at the same path. Each observation reads the current file
and refuses insecure replacements before contacting the cell. Runtime controllers still
own execution, and a preflight result does not reserve resources.

For unit and TLS transport tests, run:

```sh
go -C services/control test -race ./internal/modules/scheduling/...
```

For the PostgreSQL policy and reservation tests, point `OWNSOLID_SCHEDULING_TEST_DSN_FILE` at a
protected file containing the DSN of a disposable local PostgreSQL database
whose name ends in `_test`. The test creates and removes its own schema. The
candidate schema under `testdata/` does not change the installation lineage.
The [training and database handoff](../../../../../docs/research/scheduling-training-reservations.md)
has the owner transaction points, candidate catalog fingerprints and the
fresh/restore reproduction. The `repository_integration` test tag also tests
real lost commit acknowledgements with an isolated plaintext loopback endpoint.

[Borrowing and reclaim](../../../../../docs/research/scheduling-quota-borrowing.md)
describes the optional candidate schema and its isolated test.

[Wait reasons](../../../../../docs/research/scheduling-wait-reasons.md) describes
how to project an authorized decision without leaking other workloads' details.
Use `WaitReasons` on the current reservation or reclaim error,
`PreflightResult.WaitReasons` on the estimate and `QueueTurnReason` when rank
actually deferred the current entry. Use `ObserveCellWaitReasons` with the
client for the authorized attempt's bound cell and its persisted workload
reference. The adapter verifies the UID before projecting any blocker. Current
Kueue conditions distinguish waiting for quota, exceeding its ceiling, topology placement failure,
an inadmissible queue and admitted pods that are not ready. The projection
uses fixed codes and actions without controller messages or capacity figures.

The submission owner can call `EstimateSubmissionWithin` with a read-only
PostgreSQL REPEATABLE READ transaction, its authorized workspace and a trusted
`CellCapacitySample` for the database's site and cell. The sample must be
current and cover the total and reserved quantity of every requested resource.
An unavailable sample returns `SCHEDULING_CELL_CAPACITY_UNAVAILABLE`. The
estimate returns the policy revision, whole demand and fixed wait reasons.
The owner submits through the existing reservation transaction.

Set `PROJECT_ONE_TEST_DSN_FILE` to the protected loopback fixture DSN for a
disposable PostgreSQL 18.6 database with pgvector 0.8.7, then run:

```sh
go -C services/control test -race ./internal/modules/scheduling -run '^TestPostgresPreflightUsesAuthorizedWorkspaceAndCurrentCell$' -count=1
```

The test uses the installed Workspace authority and fixture cell samples.
See the [owner handoff](../../../../../docs/research/scheduling-admission.md#authorized-scope-and-cell-capacity-handoff)
for the real capacity source and transaction requirements.

For distributed work, `ReserveWithin` stores the entire replica-group demand
on one attempt. `ReleaseGangWithin` checks that the owner has supplied verified
stop or non-delivery evidence for every reserved slot. See [gang reservation
and release](../../../../../docs/research/scheduling-admission.md#atomic-gang-reservation-and-release).

`RankWorkspaceQueueWithin` uses current quota and reads aggregate reservations
for all authorized workspace queues in one tenant with one query.
If an active record in that set has foreign site/cell coordinates, it returns
`SCHEDULING_QUOTA_POLICY_UNAVAILABLE` so the owner can reconcile the records.
Pass a read-only REPEATABLE READ transaction
and the owner's durable queue entries. See [live workspace shares](../../../../../docs/research/scheduling-queue-policy.md#live-workspace-shares).
