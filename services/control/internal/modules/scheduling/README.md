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
