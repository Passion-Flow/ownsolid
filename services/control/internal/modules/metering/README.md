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

`DecodeRateCard` validates installation-owned JSON with one `currency` and
effective `versions`. Each version has an `id`, microsecond-aligned RFC 3339
`effective_at` and explicit `rates` array. Each rate has a ledger `meter`,
positive base `units` and explicit nonnegative `amount_micros`. One million
amount micros equal one currency unit. Retain earlier versions when adding a
new one.

`ShowbackWithin` reads an authorized workspace in a read-only PostgreSQL
REPEATABLE READ transaction. It uses the original occurrence time, returns
rounded amounts as decimal integer strings and flags unpriced usage. The
report is for internal showback. See [showback research](../../../../../docs/research/metering-showback.md).

`BudgetWithin` compares one authorized workspace period against an
operator's currency, limit in amount micros and warning in basis points.
Use the whole budget period in one snapshot. A known subtotal that reaches
the limit takes precedence over unrated usage. Below the limit, missing
rates leave the assessment indeterminate. See [budget status research](../../../../../docs/research/metering-budget-status.md).
