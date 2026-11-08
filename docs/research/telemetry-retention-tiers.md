# Fleet telemetry retention tiers (OBS-109)

[ADR 0009](../decisions/0009-telemetry-and-log-stores.md) keeps the
open-source VictoriaMetrics edition. Use its
[single-node retention period](https://docs.victoriametrics.com/victoriametrics/single-server-victoriametrics/#retention)
for each store and the
[vmalert recording rules remote write target](https://docs.victoriametrics.com/victoriametrics/vmalert/#recording-rules)
for retained aggregates. Read high-resolution data from the existing short
retention store. Write compact cell-level aggregates to a separate instance
with its own longer retention, configured independently by the operator.
Do not use Enterprise per-series retention filters or downsampling.

VictoriaMetrics and vmalert v1.155.0 are already selected
[Apache-2.0 components](https://github.com/VictoriaMetrics/VictoriaMetrics/blob/v1.155.0/LICENSE).
Their license permits commercial use, redistribution with notices and offline
operation. No new dependencies, models or datasets are used.

The first rules record observed and reachable node counts by site and cell
every minute. The source is the trusted per-node scrape health series used
by OBS-104. Missing or stale input yields no output for that scope. An
observed cell with all targets down has a positive observed count and a
measured zero reachable count. Avoid an unconditional zero fallback because
it would hide the absence of a scrape source. Use independent query endpoints
rather than falling back silently to raw data for long-range queries.

The [scale qualification](telemetry-scale.md) shows why these aggregates are
useful. The example adds at most two series per cell per site and samples them
once a minute. It does not aggregate every metric family. The test must ingest
raw node samples into one store, evaluate with real vmalert, find exact scoped
counts in the other store, and confirm raw samples did not enter the long tier.
Deployment wiring, retention selection and console query routing still need
to consume this policy before the whole roadmap item can close.

## Verified path

TestFleetRollupsWriteOnlyAggregatesToTheLongTier passed in a Linux amd64
namespace with only loopback. One VictoriaMetrics instance used two-day
retention and the other 90-day retention. With one healthy and one failed
node in the first cell and one failed node in the second, vmalert recorded
observed/reachable pairs of 2/1 and 1/0. The unscoped series produced no
aggregate. Raw samples stayed in the short tier, and aggregates were only
present in the long tier. The test exercises tier routing and retention
configuration without waiting for the expiry periods.
