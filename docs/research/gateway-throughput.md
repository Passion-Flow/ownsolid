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
