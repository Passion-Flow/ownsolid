# Scheduling

This module validates the adapter-neutral job intent, quota policy, queue order
and whole-job preflight. The quota snapshot repository uses the caller's
PostgreSQL transaction and an expected policy revision. The cell adapter reads
the exact Kueue or Volcano object over verified TLS. Runtime controllers still
own execution, and a preflight result does not reserve resources.

For unit and TLS transport tests, run:

```sh
go -C services/control test -race ./internal/modules/scheduling/...
```

For the policy concurrency test, point `OWNSOLID_SCHEDULING_TEST_DSN_FILE` at a
protected file containing the DSN of a disposable local PostgreSQL database
whose name ends in `_test`. The test creates and removes its own schema. The
candidate schema under `testdata/` does not change the installation lineage.
See the [integration boundaries](../../../../../docs/research/lane-sch-questions.md).
