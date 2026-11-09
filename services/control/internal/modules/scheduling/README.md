# Scheduling

This module validates the adapter-neutral job intent, quota policy, queue order
and whole-job preflight. The reservation repository borrows the workload owner's
PostgreSQL transaction to hold or release the exact attempt. The quota snapshot
repository also uses an expected policy revision. Enabled departments can
attribute loans to sibling workspaces and keep a reclaim pending until every
selected victim has a committed owner release. The cell adapter reads
the exact Kueue or Volcano object over verified TLS. Runtime controllers still
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
actually deferred the current entry.

The submission owner can call `EstimateSubmissionWithin` with a read-only
PostgreSQL REPEATABLE READ transaction, its authorized workspace and verified
cell capacity. The result includes the policy revision, whole demand and
wait reasons. The owner submits through the existing reservation transaction.

For distributed work, `ReserveWithin` stores the entire replica-group demand
on one attempt. `ReleaseGangWithin` checks that the owner has supplied verified
stop or non-delivery evidence for every reserved slot. See [gang reservation
and release](../../../../../docs/research/scheduling-admission.md#atomic-gang-reservation-and-release).

`RankWorkspaceQueueWithin` uses current quota and reservations for authorized
workspace queues in one tenant. Pass a read-only REPEATABLE READ transaction
and the owner's durable queue entries. See [live workspace shares](../../../../../docs/research/scheduling-queue-policy.md#live-workspace-shares).
