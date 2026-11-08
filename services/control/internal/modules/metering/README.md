# Usage metering

A trusted producer supplies an immutable event and separately supplies the
tenant and workspace authorized in its PostgreSQL transaction. Append the
event with the source state or acknowledge the durable outbox message only
after commit. Use the same producer and event IDs on retry. Totals use the
event's original occurrence time and exact integer units.

Set `OWNSOLID_METERING_TEST_DSN_FILE` to a protected DSN file for a disposable
local PostgreSQL database whose name ends in `_test`. The tests create and
remove a private schema using the candidate ledger DDL.

```sh
go -C services/control test -race ./internal/modules/metering -count=1
go -C services/control test -race -tags repository_integration ./internal/modules/metering -run TestPostgresLedgerCommitAcknowledgementLoss -count=1
```

The second command uses the PostgreSQL wire fault harness with an isolated
plaintext loopback endpoint. The candidate schema is exercised in tests.
The installation migration and real producers are tracked in the
[ledger research](../../../../../docs/research/usage-ledger.md).
