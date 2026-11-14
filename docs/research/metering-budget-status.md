# Workspace budget threshold status

TEN-108 needs a reproducible comparison between an authorized workspace's
usage estimate and an operator's budget. [FinOps budgeting](https://www.finops.org/framework/capabilities/budgeting/)
distinguishes cost comparison, notification and enforcement decisions.
[OpenMeter threshold events](https://openmeter.io/docs/integrations/notifications/entitlements)
use exact usage or percentage thresholds and deduplicate notification
delivery at the entitlement boundary. [Google Cloud budgets](https://docs.cloud.google.com/billing/docs/how-to/budgets)
describes alerts-only budgets separately from usage control.

Evaluate one explicit half-open budget period with the existing scoped
showback report in one read-only PostgreSQL snapshot. The policy specifies
one currency, a positive budget in integer millionths of that currency
and a warning fraction in basis points. Use an inclusive limit and an
inclusive warning boundary. Compare the warning using cross-multiplied
integers to preserve fractional thresholds and avoid overflow. Use the
whole budget window as one report so rounding remains consistent.

A rated subtotal at or above the limit establishes that the currently
reported estimate reaches it, including when further usage is unrated.
Otherwise report an indeterminate state for unrated usage. A complete
report below the limit returns its warning or below-warning state. These
states describe the ledger snapshot. Late data or a missing producer can
change a later report. A currency mismatch is a configuration error.

Summing already rounded daily reports would change the threshold result.
Treating unrated usage as zero could hide an overage. Using report status
as an admission decision would race with concurrent consumption and
would ignore usage that has yet to be delivered. The runtime owner needs
authoritative meter coverage, a scoped admission and pending usage policy,
and an atomic budget decision before any enforcement. The notification
owner needs a durable period and threshold identity and retry-safe
delivery before reporting alerts as implemented.

The [Go](https://go.dev/LICENSE) BSD-3-Clause standard library, [pgx](https://github.com/jackc/pgx/blob/master/LICENSE)
MIT and [PostgreSQL](https://www.postgresql.org/about/licence/) permit
commercial redistribution with their notices and offline use. The other
projects are design references. This slice adds no dependency, model or
dataset.

Verify exact and fractional warning thresholds, exact limit, unrated
usage below and at the limit, retry stability, currency mismatch and
workspace isolation on the same PostgreSQL ledger fixture. Full acceptance
needs effective authorized policy, reliable producer coverage, delivery
deduplication and transactional enforcement in the workload path.

## Authenticated admission preview

Reuse the existing owner or Admin workspace report path for a preview of an
explicit operator policy. The live session and workspace fence precede one
rated ledger query. Return that same report and threshold status with the
preview action. Read the full original price window in one statement and
keep the original effective rates for late receipts. PostgreSQL documents
[statement snapshots](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED).
A second report query can observe a different commit even inside the
authorization transaction.

Require an explicit `alert_only` or `deny_at_known_limit` mode. Both
preview an alert for a fully rated warning. At the inclusive limit, the
alert-only policy previews an alert and the deny policy previews denial.
Nonnegative prices make the known subtotal a lower bound. Once that bound
reaches the limit, unmeasured or unpriced usage cannot reverse the result.
Below the limit, either kind of incomplete data yields an indeterminate
preview. Fully measured usage priced at zero is a clear result. Reject
missing or invalid modes instead of defaulting to enforcement. An alert
preview carries no notification identity and sends nothing.

Prove these transitions with the existing authorized PostgreSQL fixture.
Include exact warning and limit boundaries, old rate selection for late
usage, a replay that would otherwise change the decision, and scope and
permission failures. Run the verified gateway receipt regression as a
separate source check. It proves that receipt replay leaves the charge
unchanged and that null usage remains unmeasured.

A budget preview is an observation of recorded usage. A clear preview
grants no capacity or spend reservation. A production admission owner
must use the effective workspace policy and reliable current and pending
usage in the same atomic transaction as admission. The notification
owner needs durable alert identity and delivery state. The current
installation defines neither policy activation nor those write paths.
No dependency, model, data set or runtime endpoint is introduced.
