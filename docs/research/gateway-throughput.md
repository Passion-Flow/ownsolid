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
