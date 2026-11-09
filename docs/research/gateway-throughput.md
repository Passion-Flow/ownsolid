# GW-112 throughput and added latency

The [vLLM serving benchmark](https://docs.vllm.ai/en/stable/benchmarking/cli/)
records completed requests per second and end-to-end percentiles at declared
load and concurrency. The installed gateway adds TLS, control authorization,
tokenization and durable outcome publication before the client completes a
request. A throughput number without this path and its success criteria can
count refusals as completed inference or hide time in a caller-side queue.

Start with a repeatable component baseline on one Linux node. Exercise the
public chat route with its real TLS listener and private spool, and compare
it to direct HTTP on the same synthetic model endpoint. Both clients close
each connection, matching the gateway's one-request-per-connection listener.
Use a fixed-size warm-up and then measure closed-loop traffic at one and
eight concurrent callers. Report completed requests per second and p50, p95
and p99 end-to-end time for each path. The difference between like
percentiles includes the gateway's TLS and control calls. It is a comparison
of the observed distributions, with no claim about per-request causation.
Confirm every gateway response and drain its corresponding receipts so a
fast failure cannot improve the result.

A quota-only microbenchmark does not exercise the request path. A single
CPU vLLM comparison cannot isolate gateway costs from model startup and
engine queueing, so report it separately when run. This baseline uses
existing Rust test dependencies and synthetic text. It adds no dependency,
dataset, model or redistribution obligation.

For GW-112 acceptance, repeat against the shipped entrance and real
control-plane admission and accounting. Record TLS, software versions,
hardware, engine, prompt and output lengths, streaming status, concurrency,
offered load, refusal rate, p50/p95/p99 latency and completed throughput.
A run is incomplete if the client sees malformed completions, the result
ledger disagrees with the requests, or the upstream can leave the test
network. The component baseline measures one gateway process with
synthetic control and backend. It cannot establish installation capacity.

## Linux component result

On 2026-11-09, Rust 1.99.1 release mode on `ownsolid-cpu` ran with
only loopback and the spool on tmpfs. The shared host had 16 logical
CPUs. Each path used 32 warm-up requests and 256 measured requests at
each concurrency. All 576 gateway requests, including warm-up, received
successful durable outcomes and matching control acknowledgements.

| Concurrent callers | Direct HTTP req/s | Gateway TLS req/s | Gateway p50 / p95 / p99 (ms) | Added p50 / p95 / p99 (ms) |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 2,912 | 546 | 1.69 / 2.63 / 3.66 | 1.40 / 2.10 / 2.79 |
| 8 | 10,441 | 2,204 | 2.98 / 4.82 / 14.37 | 2.40 / 3.87 / 13.20 |

The first disk-backed attempt timed out under shared-host I/O pressure.
The tmpfs profile measures request processing and TLS with spool
publication, while durable-media latency and real control-plane costs
belong in the installation benchmark. Receipt delivery was checked after
each batch, outside the measured interval.

```sh
cd services/gateway
TMPDIR=/dev/shm cargo test --release --locked --offline chat_route_throughput_benchmark \
  -- --ignored --nocapture --test-threads=1
```

## Control-plane query cost

The shared machine authorization path uses the management alias projection.
That projection counts held requests for the alias so Admin and Portal can
show them. The count is ignored by both machine operations, and admission
separately counts held requests for the selected deployment before taking
a capacity slot. One normal generation runs model authorization before
queueing and fresh admission after queueing, so the unused aggregate runs
twice. The PostgreSQL partial capacity index is keyed by deployment.

The [PostgreSQL wire protocol](https://www.postgresql.org/docs/18/protocol-flow.html)
and the pinned [pgx query tracer](https://pkg.go.dev/github.com/jackc/pgx/v5@v5.11.0#QueryTracer)
make each awaited SQL call observable. Split the administrative count from
the shared readiness check. Keep the ready-revision checks and deployment
capacity count in their current transactions. Retain the per-alias count
on the management projection and the existing explicit cutover drain
checks. A cache would make ready and revoked state lag. Adding an index
for the extra query would still execute an aggregate machine callers never use.

Use a real PostgreSQL connection with a test-only tracer that counts
matching SQL statements without recording arguments. After issuance,
the model lookup and admission should each execute zero alias-capacity
counts, and admission should still execute one deployment-capacity
count. The human alias detail must report one held request after
admission and zero after its receipt. Re-run native gateway traffic,
cutover interleavings, and rejected and concurrent admissions against
the same isolated engine. The resulting query counts are separate from
installed-system requests per second and latency.

This change reuses the [pgx MIT license](https://github.com/jackc/pgx/blob/master/LICENSE)
and [PostgreSQL License](https://www.postgresql.org/about/licence/).
Both permit commercial use, offline use and redistribution with their
notice terms. No dependency, model or data set is added.

The disconnected PostgreSQL trace observed one administrative alias count
in model lookup and one in admission before the change. Afterward both
were absent, admission still checked deployment capacity once, and the
human alias detail reflected the hold and release. Native Rust foreground
Responses and Messages reached the synthetic engine through the real Go
owner and recorded their reports. Cutover and revocation interleavings
passed. No installation-wide throughput or latency claim follows from
this query trace.

## Release durable capacity as soon as the outcome is published

The real control plane keeps a deployment slot until it acknowledges the
terminal outcome. The native Rust reporter currently polls the spool at
the configured retry interval. The approved runtime defaults to four
held requests per deployment. A fast call can still occupy that slot for
most of a reporter interval, and a burst can see a capacity refusal
while the engine is idle. The isolated component baseline above flushed
receipts after each batch, so it did not measure this effect.

Wake the single reporter after an outcome becomes durable and visible in
the existing spool index. The pinned
[Tokio Notify implementation](https://github.com/tokio-rs/tokio/blob/tokio-1.53.3/tokio/src/sync/notify.rs)
retains one permit if the waiter has not started. Multiple publications
can coalesce into one wake because the reporter drains the durable
spool. Startup schedules a first drain to cover retained records.
If an attempt fails, wait the configured retry interval before retrying.
New completions during a failure must never bypass this delay. Shutdown
interrupts the regular worker and keeps the final bounded flush.

A shorter polling interval would wake the process repeatedly while idle
and still leave completed capacity held for a timer interval. Sending
reports in the foreground would make a client response depend on
control being reachable after an otherwise successful generation.
An in-memory work queue would duplicate the durable spool. Keep
publication before response, validated acknowledgements before deletion,
and the control plane as the sole owner of capacity release.

Test startup recovery and a second completed call with a retry interval
long enough to expose timer-only dispatch. Run the native gateway with
the real PostgreSQL owner and verify each returned request is reported
and its capacity hold released before the interval. Exercise control
outage, publication while retrying, replay and bounded shutdown. The
test backend is synthetic. Tokio is already pinned under
[MIT](https://github.com/tokio-rs/tokio/blob/tokio-1.53.3/LICENSE), which
allows commercial and offline use and redistribution with notices.
No dependency, model or dataset is added.

In the disconnected native Go/Rust/PostgreSQL test on 2026-11-09,
the prior reporter still held capacity at the four-second test cutoff
with a 30-second retry interval. The wake-based reporter released two
successive holds about 24 ms and 13 ms after the corresponding client
responses, as observed by polling PostgreSQL. The same run verified
canonical nine-token receipts. The Rust TLS fixture delivered an
already pending receipt at startup, woke for the next publication,
kept new publications from bypassing backoff after a 503, and drained
the retained receipts on shutdown after control recovered. Native
shared and dedicated TLS outage tests and the scoped CSV regression
also passed. This measures receipt-driven capacity release on two
synthetic calls. Installed-system throughput and latency remain open.

## Single-node native path comparison

On 2026-11-09, the Linux release gateway from commit 9628bb33 used
real Go HTTPS control and PostgreSQL 18.6 with pgvector 0.8.7 in a
loopback-only network namespace. PostgreSQL and the gateway spool used
tmpfs. The shared server had 16 logical CPUs and the PostgreSQL
container had one CPU. The same synthetic HTTP backend returned nine
tokens for nonstreaming requests in both cases. The gateway route also
called the synthetic tokenizer. Both clients opened one connection
per call. Each path ran 12 warm-up calls and 96 measured calls at each
concurrency. The fixture raised only the per-key request window limit
to 1,000 calls per 60 seconds. Control retained the default four
deployment slots.

| Concurrent callers | Direct HTTP req/s | Gateway TLS req/s | Gateway p50 / p95 / p99 (ms) | Added p50 / p95 / p99 (ms) |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 2,346.0 | 70.3 | 13.61 / 18.90 / 29.14 | 13.31 / 17.98 / 26.22 |
| 2 | 4,520.0 | 80.3 | 23.83 / 32.18 / 38.65 | 23.54 / 31.16 / 36.47 |

There were no refusals. PostgreSQL acknowledged all 216 gateway
calls, including warm-up, and released every hold. Completion and
token usage were checked for every response, and the receipt counts
were checked after each batch. Latency deltas compare corresponding
percentiles from the two distributions. This result uses one synthetic
deployment, local transport and volatile storage. It leaves the shipped
installation entrance, production storage, representative engine and
sustained load to installation qualification.

The isolated test overlay and full output are retained under
`/root/lane-gw-acceptance/gw112-reporter.0hfX9Eeu/profile` on the Linux
test host. Build the optimized gateway with
`cargo build --release --locked --offline --bin projectone-gateway`,
then run the profile's `run-profile.sh` with its own directory, a unique
container name stem, and `^TestRealGoRustPostgresCapacityProfile$`.

## Retry without additional traffic

The [pinned Tokio timer](https://github.com/tokio-rs/tokio/blob/tokio-1.53.3/tokio/src/time/sleep.rs)
allows the reporter to wake at its deadline independently of spool
notifications. Existing backoff coverage exercises a fresh publication
during a 503 and a final shutdown drain. Add a separate TLS regression
that leaves one durable record pending after a 503, restores the
control endpoint, and proves the same canonical digest is acknowledged
while the reporter stays running and no new inference takes place.
Calling the drain explicitly or ending the process would exercise a
different recovery path. Reuse the existing pinned MIT test stack and
synthetic fixture. No dependency, model or dataset is added.

The disconnected Rust TLS run retained a receipt after a control 503,
then acknowledged its original digest through the scheduled retry
with the reporter still running. Only the original request generated
output. The publication wake and outage backoff regression passed in
the same run, followed by Clippy with warnings denied.

## Retry after a committed acknowledgement is lost

The [HTTP retry boundary](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2.2)
requires the client to know that repeating a non-idempotent method is
safe. Outcome reporting is a scoped idempotent write keyed by the
request ID and canonical digest. The Rust reporter keeps the exact
record until the matching acknowledgement reaches it. A transport
failure cannot establish whether Control committed.

Reuse the native single-target Go/Rust fixture and its prepared
synthetic loopback upstream. Let the real Control dispatcher handle
the first outcome and commit in PostgreSQL. Drop only that successful
HTTPS reply. Hold the following report request long enough to check
the committed row and the gateway's pending spool record, then let it
reach the dispatcher and return the actual replay acknowledgement.
This exercises response loss after a commit, which a simulated 503
before the write or a direct service method replay would miss.

Verify the original workspace, key, actor, alias and target revisions,
nine reported tokens and released capacity before replay. After
replay, require the same canonical acknowledgement, one reported
audit event, one generation and removal of the pending file. Keep the
run within the existing disconnected PostgreSQL fixture. The test adds
no dependencies and uses the existing Go BSD-3-Clause and PostgreSQL
License fixture components. The synthetic upstream remains separate
from actual deployment or installation qualification.

The disconnected Linux run passed
`TestRealGoRustPostgresLostOutcomeReply` and the existing
`TestRealGoRustPostgresGatewayChainAndControlOutage` variants. The
first attempt committed nine tokens, released capacity and created
one audit event under the original scope before its TLS connection
closed. The second returned the same digest. The native gateway
removed its pending record, PostgreSQL preserved the original report
timestamp and outcome, and the upstream saw one generation.

## Recovery after the gateway process exits

The existing Rust process test uses synthetic Control replies.
Exercise the same spool recovery with the real Control dispatcher and
PostgreSQL, including the optional receipt projection from GW-107.
Keep the canonical receipt and retry semantics described in
[RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2.2).
The Rust reporter begins a flush at startup. Reuse the same private
configuration and spool directory across both child processes. Use
[Go process wait](https://pkg.go.dev/os/exec#Cmd.Wait) to join the killed
child and verify the SIGKILL exit before starting its replacement.

Drop the first successful TLS reply after the report and ledger commit.
Use an extended scheduled retry interval for this test so the original
process exits before it can send a second report. Confirm the spool
file's bytes are unchanged after exit. Start the replacement with the
same configuration and no incoming requests. Require an identical
acknowledgement, then verify deletion of the spool file, the original
report timestamp and authorization scope, one generation, one report
audit event and one ledger event at the original admission time.
A manually replayed POST would miss the startup reporter and lock
reacquisition. A graceful stop would allow a final flush to hide them.

The fixture enables metering only with its candidate ledger SQL.
Production composition continues to use the default-disabled service.
The prepared upstream remains synthetic. No model, dataset or
dependency is added. The existing Go, Rust test stack, and PostgreSQL
license and offline-use boundaries remain as documented above.

The disconnected Linux amd64 run passed
`TestRealGoRustPostgresRestartAfterLostOutcomeReply`. The original
process exited on SIGKILL after Control committed its outcome, audit
and nine-token ledger event. The replacement replayed the receipt
through real TLS before receiving any caller request. Both processes
used the same spool and credential. After the exact acknowledgement,
the pending file was removed and the report timestamp and body were
unchanged. The scoped ledger and report audit each contained one row,
and the upstream saw one generation. The same run passed the
same-process lost reply and shared and dedicated Control outage cases,
the optional metering rollback and retry, and the alias upgrade.

With the private loopback PostgreSQL 18.6 fixture and pgvector 0.8.7,
set `PROJECT_ONE_TEST_DSN_FILE` and the canonical absolute path to the
compiled Rust binary in `PROJECT_ONE_GATEWAY_BINARY`. Run from the root.

```sh
go -C services/control test -p 1 -tags gateway_integration ./internal/modules/distribution   -run '^Test(RealGoRustPostgres(RestartAfterLostOutcomeReply|LostOutcomeReply)|PostgresGatewayOutcomeMeteringAtomicRetry|AliasDescriptionPostgreSQLUpgradeAndHTTPS)$' -count=1
```
