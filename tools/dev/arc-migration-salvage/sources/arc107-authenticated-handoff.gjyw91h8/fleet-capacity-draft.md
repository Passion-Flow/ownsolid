# Fleet load measurements

Use the measured workload and topology when sizing an installation. This baseline
covers 2,000 simulated read-only agents against one control process and one
PostgreSQL instance. The 10,000-agent, multi-cell validation is ARC-203.

## Accepted baseline

RESULT_TABLE

The measured source is `3ae6bfebb67b47d684de4651cc597c26f104d725` on native
Linux amd64, with PostgreSQL 18.6 and pgvector 0.8.7, schema 60. The isolated
Docker host has four CPUs, a 6 GiB memory ceiling, a 4,096 PID ceiling, no host
ports and no external network. The fixture uses fresh native binaries and
same-source qualified web assets. It runs the actual installer; it does not
qualify the standard release-image factory.

The generator runs for 25 minutes at a five-second heartbeat, twice the planned
ten-second liveness rate. It completes 1,000 facts tasks during ten steady
minutes, disconnects all agents, partitions 600 agents for one minute and
restarts control while 100 acknowledged 90-second tasks run. These are synthetic
facts tasks and protocol faults, not GPU workloads, Kubernetes scheduling or
physical fabric faults. Gateway age and failback have a separate
[500-agent qualification](../research/agent-gateway-rebalance.md#bounded-fallback-integration-qualification).

## Reading the numbers

Task creation-to-completion p99 comes from persisted completion and creation
timestamps for the 1,000 steady tasks. It includes claim delivery, execution and
result delivery. It is not an isolated scheduler latency. Heartbeat p99 is the
simulator histogram's bucket upper bound; the table reports the worst of all
ten steady one-minute windows rather than averaging percentiles.

Database row modifications are the differences in PostgreSQL's
[inserted, updated and deleted row counters](https://www.postgresql.org/docs/18/monitoring-stats.html#MONITORING-PG-STAT-DATABASE-VIEW),
over the 600-second steady interval, divided
by elapsed time. Each displayed rate is rounded by the existing test logger;
the total sums those rounded row rates. These are logical row changes, not
storage IOPS or distinct business objects. Committed transactions are reported
separately and include read-only commits. WAL is the isolated cluster's
[write-LSN movement](https://www.postgresql.org/docs/18/functions-admin.html#FUNCTIONS-ADMIN-BACKUP)
in KiB/s, including its maintenance. The independent observer verifies
an unchanged database start time, no statistics reset and track_counts=on.

Disconnect and partition recovery wait for the first successful heartbeat from
every affected agent; partition time starts when the fault heals. Restart
recovery waits for persisted new heartbeats from all 1,900 idle agents, measured
from before the control restart. The 100 busy tasks have separate original
attempt/acknowledgement and completion checks. Full stream recovery must also
meet the unchanged three-minute post-restart deadline and terminal 2,000/2,000
stream count. A successful terminal count does not excuse a missed window.

Older agents can coexist with the current v1 protocol. Agents without the
bounded-fallback change retain the older five-minute HTTPS fallback policy.
This baseline uses the measured current simulator version; it is not a
mixed-version recovery result or a requirement to upgrade all agents at once.

## Repeating the measurement

Use a disposable Linux amd64 test server with Docker and the exact release's
locally available control, PostgreSQL, telemetry and DinD images. Prepare the
matching binaries as described in the
[development environment](../development/environment.md#end-to-end-install).
Keep existing fleet limits and test thresholds. Acquire the shared heavy-test
lease and check memory, disk and I/O pressure before starting. Never run this
fixture against a customer's database or a running installation.

With E2E_BIN_DIR and CONTROL_IMAGE naming the already qualified local inputs:

```sh
OWNSOLID_E2E_LOAD=1 \
OWNSOLID_E2E_RUN='^TestFleetLoad$' \
OWNSOLID_E2E_TIMEOUT=50m \
bash tests/e2e/run-single-node.sh "$E2E_BIN_DIR" "$CONTROL_IMAGE" \
  pgvector/pgvector:0.8.7-pg18-bookworm \
  victoriametrics/victoria-metrics:v1.155.0 \
  victoriametrics/victoria-logs:v1.53.0 \
  docker:29.1.3-dind
```

This starts the stock selected fixture. Record its actual outer CPU, memory and
PID limits; the published measurement additionally bounded these to four CPUs,
6 GiB and 4,096 PIDs before installation. For an observed recorder probe, use
the exact owned container name printed by the harness. From a second terminal,
after its recorder routes are ready and the selected test has started, send:

```sh
docker exec "$E2E_CONTAINER" nc -z -w 2 192.0.2.1 9
```

The command must fail and the final recorder report must count its probe.
The address is TEST-NET and the fixture routes it to its own recorder.
The published measurement also retained external route, resource and database
statistics-state observations throughout execution. Keep these inputs and
observer overhead in the new result's scope when comparing runs.

Preserve the complete selected-test log, simulator JSON, exact source/binary/image
identities and resource observations. Observe PostgreSQL start/reset state
throughout the run and retain recorder routes and the positive fixture probe.
Require zero product egress, every fixed test threshold and cleanup of the owned
outer container and disposable data. A failed test remains a failed candidate;
diagnose it before another selected run. The earlier recovery failure and the
successful input identities belong to the
[measurement research](../research/fleet-load-test.md).
