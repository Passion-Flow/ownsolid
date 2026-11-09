# Occurrence-rated workspace showback

TEN-107 gives an operator an internal estimate from usage that the ledger
already recorded for the authorized workspace. [FinOps terminology](https://www.finops.org/assets/terminology/#showback)
distinguishes consumption visibility from a financial transfer.
[FOCUS pricing quantity](https://focus.finops.org/docs/specification/v1-0/columns/cost-and-usage/pricing-quantity/)
separates measured consumption from the quantity used to price it.
[OpenMeter rate cards](https://openmeter.io/docs/product-catalog/plan/rate-card)
associate prices with metered features. Its [versioned plans](https://openmeter.io/docs/product-catalog/plan/overview#plan-versioning)
retain published versions and can give a new version a future effective date.

Accept a validated, installation-owned rate card in one currency. Each
version is a complete set of zero or more per-unit rates with a unique ID
and effective instant. Use the latest version at or before the ledger's
stored occurrence time, including an event at the exact cutover. Each
version ends at the next version's start. A rate specifies a positive number
of ledger base units and a nonnegative price in millionths of a currency
unit. Base units are GPU microseconds, tokens, storage byte-seconds and
requests. A zero price is explicit. Usage before the first version or
without a rate in the selected version stays visible as unrated.

Partition the requested half-open period into disjoint rate intervals.
Read each interval with the existing exact workspace totals query in one
read-only [PostgreSQL REPEATABLE READ snapshot](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-REPEATABLE-READ).
Use the existing authorization boundary to supply both scope IDs. Retain
the rate ID, pricing inputs, quantity and amount for each rated line.
Multiply and divide using arbitrary-width integers. Round half up to
one millionth of the currency unit once per workspace, meter, version and
report intersection. Sum those rounded amounts as the known subtotal.
Expose completeness separately. Rounding at the report's boundaries means
independently rounded subperiod reports can differ from one larger report
by a rounding unit. Operators preserve previous rate versions for historical
recomputation. The production policy owner still needs to govern changes to
retained versions and version identity.

Rating during append would couple reliable ingestion to whether pricing
is configured. Using the current rate would reprice old or late events
at the wrong version. Per-event rounding would make the report dependent
on producer batching. Floating point would lose units for large totals.
Copying events into a billing table would introduce another write and
deduplication boundary. Use the ledger as the report input on every run.

The existing [Go](https://go.dev/LICENSE) BSD-3-Clause standard library,
[pgx](https://github.com/jackc/pgx/blob/master/LICENSE) MIT and
[PostgreSQL](https://www.postgresql.org/about/licence/) permit commercial
redistribution with their notices and offline use. The upstream material is
a design reference. This slice imports no upstream code, dataset, model or
external service. The Go standard library
[JSON decoder](https://pkg.go.dev/encoding/json/v2) rejects duplicate object
fields and undeclared configuration fields in this path.

Verify exact half-up rounding and values beyond 64 bits, explicit zero
rates, unmatched usage, a rate boundary, late events at their old rate,
and replay of an already committed event. Against isolated PostgreSQL,
verify the same report on retry, correct scope filtering and a stable
snapshot despite concurrent ingestion. The authorized API, operator UI,
production policy source and real usage-producer reconciliation remain
separate acceptance work. This report is showback information.
