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
