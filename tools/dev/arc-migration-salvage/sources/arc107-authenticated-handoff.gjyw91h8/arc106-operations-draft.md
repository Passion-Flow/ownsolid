# Measured fleet performance

Use measured results together with their topology, pacing and resources. A result
for simulated nodes measures the control-plane protocol and synthetic work; it
does not measure model inference, training throughput or GPU performance.

## Current baseline

Pending the complete unchanged 2,000-agent TestFleetLoad result. No candidate
numbers in this draft are an accepted capacity claim.

## What the indicators mean

- **Dispatch p99:** the 99th percentile of elapsed time from persisted task
  creation to persisted completion for the steady collect_facts.v1 task set.
  It includes synthetic task execution and result delivery. PostgreSQL computes
  percentile_cont over those task rows; this is separate from an HTTP latency.
- **Heartbeat p99:** the upper histogram bucket containing the 99th percentile
  of successful simulator calls in each one-minute steady window. Buckets grow
  by 1.25, and reported milliseconds are rounded to a tenth. Failed calls remain
  separately counted; a good whole-run average cannot hide a bad window.
- **Disconnect or partition recovery:** elapsed time until every affected agent
  has a successful heartbeat. Partition recovery begins when the partition heals.
- **Control-restart recovery:** elapsed time from initiating the restart until
  every idle node has a persisted heartbeat newer than that instant. Busy nodes
  are separately checked for original task attempts, acknowledgements and stream
  recovery. This idle-node value is not full-fleet reconnect time.
- **Database row modifications per second:** the sum of inserted, updated and
  deleted row-counter deltas, divided by the actual sample duration. The table
  also keeps those three rates separately. Committed transactions include reads
  and are reported separately. These values are not SQL statement counts,
  storage IOPS or committed business-object counts.
- **WAL KiB/s:** current WAL position movement divided by elapsed seconds and
  1,024. This covers the whole isolated PostgreSQL cluster, including maintenance,
  rather than only Agent writes. Historical raw logs label this value KB.

A counter reset or PostgreSQL restart invalidates a database-rate window. Retain
statistics-reset and server-start observations along with the raw test log.
Resource figures are samples with their stated cadence; Docker cache-adjusted
memory is not RSS and a sampled maximum is not an instantaneous peak.

## Fixed acceptance

The existing TestFleetLoad runs for 25 minutes with 2,000 agents at a five-second
heartbeat, twice the planned ten-second rate. It checks every minute in the
steady ten-minute stretch, then disconnects the entire fleet, partitions 30%,
and restarts control while 100 acknowledged 90-second tasks run.

Every steady window must retain all 2,000 connections, with no failed heartbeat
or claim and heartbeat p99 at most one second. Dispatch p99 must stay within five
seconds; every measured fault must recover within 300 seconds. Every busy task
must finish on its original acknowledged attempt, all nodes must return to
streams by the existing deadline and terminal report, and control must have no
unexpected restart, out-of-memory kill or liveness/outbox failure. The isolated
egress recorder must see its positive probe and no product outbound attempt.

The exact thresholds and diagnosis of historical misses are in the
[load research](../research/fleet-load-test.md). Results from the separate
[500-agent gateway lifecycle drill](../research/agent-gateway-rebalance.md#authenticated-handoff-installed-acceptance)
show natural retirement and failure recovery, not sustained capacity. The
10,000-node multi-cell claim requires ARC-203's separate measured run.

## Reproduce a load run

Use the pinned Linux amd64 release images and binaries from the same source.
Read the [test environment](../development/environment.md#end-to-end-install),
check host resources and reserve its heavy-test lock before starting. Keep
another lane's services and data untouched. With all images already loaded,
select the existing isolated install harness:

```sh
OWNSOLID_E2E_LOAD=1 \
OWNSOLID_E2E_RUN='^TestFleetLoad$' \
OWNSOLID_E2E_TIMEOUT=50m \
bash tests/e2e/run-single-node.sh "$E2E_BINARIES" "$CONTROL_IMAGE" \
  "$POSTGRES_IMAGE" "$METRICS_IMAGE" "$LOGS_IMAGE" "$DIND_IMAGE"
```

The variables name prepared local inputs; use immutable image references and
verify them before the run. The harness starts a network-none Docker daemon
with only recorder routes and removes its own disposable installation on exit.
Keep the complete log and simulator report before accepting a result. Preserve
failures with their exact source and resource configuration; do not change a
threshold or combine percentiles from different runs to make one pass.
