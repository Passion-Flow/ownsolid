# Process HTTP measurements

This package records bounded in-process HTTP facts for the [monitoring domain](../../modules/monitoring/README.md). It does not start a metrics daemon, authorize monitoring APIs or measure host/GPU capacity. [Server composition](../../../cmd/server/monitoring.go) constructs the recorder from the selected monitoring policy and gives the same recorder to the domain service.

## Recorder and policy

[New](recorder.go) accepts a validated [Policy](policy.go) and logger. Policy owns increasing latency bucket bounds in milliseconds, an explicit structured-logging switch and maximum log events per second. Enabling logging without a logger fails. The recorder copies the bucket list and `MatchesPolicy` prevents composition from quietly ignoring the owning policy. The deployment selector and examples live with the [monitoring configuration](../../../../../config/examples/monitoring/README.md).

`Middleware` wraps an existing handler using the reviewed `httpsnoop` hooks, preserving its actual optional response interfaces. It observes header commitment, Write/WriteString/ReadFrom, flush, hijack, cancellation, write failure and panic outcomes. It counts bytes actually reported written; it does not buffer bodies or turn partial streams into successful responses.

Methods and route classes are closed labels, never raw URLs, query strings, bodies, identities or arbitrary user labels. The current route classification is defined by `route` in [recorder.go](recorder.go); add route categories intentionally rather than assuming every health or new module path has a special category. The agent family includes the current /agent/v1/ paths and exact agent gRPC SessionPath, and gateway includes /gateway/v1/ after cell-relative normalization. Legacy API-prefixed classes are retained. The health family includes the exact /health/live and /health/ready paths and the legacy /healthz and /readyz classifications; unknown suffixes remain other. Logs are rate-bounded and count skips. Only a canonical server response request ID is eligible for inclusion.

## Snapshots and restart semantics

`Snapshot` returns detached counters and bucket values with a process epoch, start time, observation time, inflight count and overflow indicator. Counters saturate at the largest exact supported integer; overflow is visible. Each new recorder has a new epoch, so clients must not treat counters from two process lifetimes as one monotonic series.

[Runtime](runtime.go) reads a fixed set of standard Go runtime metrics. Unavailable or unrepresentable values remain unavailable. Go memory accounting is not host RSS, free physical memory, GPU capacity or target-hardware qualification. The domain owner combines and authorizes these observations; this package has no persistence or replay lifecycle.

## Verification and change boundaries

With the pinned Go toolchain and admitted cache/temp roots:

```sh
go -C services/control test ./internal/platform/monitoringmetrics
```

[Recorder tests](recorder_test.go) cover interface transparency, SSE, actual local HTTP/2, panic/cancellation, log redaction and overflow. [Process tests](process_test.go) exercise partial-stream failure and a fresh restart epoch. These checks do not establish host capacity or workload performance. When adding an observation, synchronize policy validation, the monitoring API/client and bounded-label tests; do not add hidden background sampling or unbounded per-user metrics here.

## VictoriaMetrics fleet scale fixture

The standalone scale test uses a fresh, loopback-only VictoriaMetrics
instance. It verifies stored sample and series counts, measures ingest
intervals and measures selected queries with and without competing writes.
The Linux runner requires a local Go toolchain and permission to create a
network namespace. Verify the upstream v1.155.0 Linux amd64 archive checksum
before extracting the binary. Then run from this directory:

    bash testdata/run-scale.sh /absolute/path/to/victoria-metrics-prod 10000 9

Use 5 nodes and 2 rounds for a short smoke test. The runner creates its own
storage directory and stops the process after the test. Its port is 58428
inside the isolated namespace only. See the [scale qualification](../../../../../docs/research/telemetry-scale.md)
for measured size, throughput and query times.

## Scoped series cardinality fixture

The fixture checks the native TSDB status rankings and totals for one
workspace with 10,000 synthetic agents. It also reproduces cross-scope
metric-name usage metadata in the raw response. A customer-facing
projection must expose the qualified counts and omit usage metadata.
See the [scope and admission boundary](../../../../../docs/research/series-cardinality-scope.md).

From this directory on a Linux test host with permission to create a network
namespace, use the verified VictoriaMetrics 1.155.0 binary.

    unshare --net --pid --fork sh -ec 'ip link set lo up; python3 "$1" "$2"' sh \
      testdata/check_cardinality_scope.py /absolute/path/to/victoria-metrics-prod
