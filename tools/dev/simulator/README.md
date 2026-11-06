# Agent simulator

Runs thousands of simulated node agents in one process against a real ownsolid control
plane, over the real agent protocol: the session stream with HTTPS fallback, heartbeats,
held claims, acknowledgements, task progress and results. Faults are scripted, and every run
writes a JSON report. Load tests use it ([ADR 0033](../../../docs/decisions/0033-agent-simulator.md),
[research](../../../docs/research/agent-simulator.md)).

Simulated nodes enroll with the `read_only` profile and run `collect_facts.v1` tasks. They
report the facts of a large GPU server and differ only in host name (`sim-000000` onwards).

## Build

```sh
cd tools/dev/simulator && go build -o ../../../bin/simulator .
# for a Linux test host
cd tools/dev/simulator && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o ../../../bin/simulator .
```

`make e2e-binaries` builds it for the end-to-end run.

## Use

1. **Raise the limits a fleet meets** in the install configuration and run `apply`. All
   simulated agents share the simulator's address and each holds a connection:

   ```yaml
   control_settings:
     runtime:
       http: {max_connections: 4096}
       nodes: {maximum_live_nodes: 10000, maximum_live_join_tokens: 10000}
       rates: {agent_enroll_ip_count: 10000}
       agent_admission:
         address: {per_minute: 60000, burst: 10000}
   ```

2. **Mint one read-only join token per agent** through the administrator API
   (`POST /api/v1/sites/default/cells/default/admin/node-join-tokens`) and write them to a
   file, one per line, mode 0600.

3. **Enroll**, which writes the identities file and refuses to overwrite one:

   ```sh
   simulator enroll --config simulator.json
   ```

4. **Run**. SIGINT or SIGTERM ends the run early; the report and cursors are still written.

   ```sh
   simulator run --config simulator.json
   ```

   A summary is logged every `log_seconds`:
   `simulator_fleet elapsed_seconds=30 agents=2000 connected=2000 on_stream=2000 ...`.

A second `run` continues the same fleet from the saved cursors.

## Configuration

A JSON file; unknown fields are refused and paths are absolute.

```json
{
  "control_origin": "https://admin.example.internal:8443",
  "ca_file": "/srv/sim/ca.pem",
  "dial_address": "127.0.0.1:8443",
  "identities_file": "/srv/sim/identities.json",
  "join_tokens_file": "/srv/sim/tokens",
  "agents": 2000,
  "duration_seconds": 600,
  "start_seconds": 10,
  "report_file": "/srv/sim/report.json",
  "faults": [
    {"kind": "disconnect", "at_seconds": 120, "percent": 100},
    {"kind": "partition", "at_seconds": 240, "percent": 30, "duration_seconds": 60},
    {"kind": "restart", "at_seconds": 420, "percent": 100}
  ]
}
```

| Field | Default | Meaning |
| --- | --- | --- |
| `control_origin` | required | The origin agents use, as on a real node (`https`, no path) |
| `ca_file` | system roots only | Extra CA that verifies control |
| `dial_address` | the origin's host and port | Where connections go; TLS still verifies the origin's name |
| `identities_file` | required | Written by `enroll`, read and updated by `run` |
| `join_tokens_file` | required by `enroll` | One join token per line |
| `agents` | required | Agents to enroll or run, 1 to 100,000 |
| `agents_per_connection` | 1 | Agents sharing one session connection |
| `https_connections` | 64 | Pooled HTTPS connections for enrollment and task traffic |
| `enroll_concurrency` | 16 | Enrollments in flight |
| `request_seconds` | 10 | Budget of one request, plus the claim wait for a claim |
| `duration_seconds` | required by `run` | Length of the run |
| `start_seconds` | 10 | First cycles are spread evenly over this window; 0 starts every agent at once |
| `log_seconds` | 10 | Interval of the summary log line |
| `report_file` | required by `run` | Where the report goes |
| `faults` | none | Scripted faults, below |

## Faults

Each fault hits `percent` of the agents, chosen at random, `at_seconds` into the run. Lasting
faults take `duration_seconds` and must end before the run does.

| Kind | Lasts | What the chosen agents do |
| --- | --- | --- |
| `disconnect` | no | Their connections close; they reconnect on their own backoff |
| `restart` | no | Their connections close and a new cycle starts at once, without backoff or old pacing |
| `partition` | yes | Every call fails without reaching control, then all heal together |
| `https_only` | yes | The stream cannot open, so they fall back to HTTPS long-polling |
| `lose_replies` | yes | Control's reply to each acknowledgement on the stream and each result is lost; the agent sends it again as the real agent does |
| `lose_cursor` | no | They forget their task cursor; control answers with a resync |

With `agents_per_connection` above one, a connection fault reaches every agent on that
connection.

## Report

The shape of the report, shortened, with example values:

```json
{
  "agents": 2000, "connected_at_end": 2000, "on_stream_at_end": 2000, "revoked": 0,
  "calls": {"heartbeat": {"succeeded": 120345, "failed": {"CONTROL_CONNECTION_FAILED": 612},
            "latency_milliseconds": {"p50": 2.1, "p90": 4.7, "p99": 11.6, "max": 140.3}}},
  "tasks": {"leases": 150, "acknowledged": 150, "succeeded": 150, "resyncs": 50, "lost_replies": 24},
  "faults": [{"kind": "disconnect", "agents": 2000, "measured": true, "recovered_agents": 2000,
              "full_recovery_seconds": 1.9, "p50_recovery_seconds": 0.7, "p99_recovery_seconds": 1.6}]
}
```

- `calls` covers `enroll`, `heartbeat`, `claim`, `acknowledge`, `events` (progress), `result`
  and `stream_open` (opening a stream with its first heartbeat). Latency covers successful calls;
  a claim's includes the time control held it.
- Recovery is measured for `disconnect`, `restart` and `partition`: from the fault (from the
  heal for a partition) to each hit agent's next good heartbeat. `full_recovery_seconds` is set
  only when every hit agent recovered.
- The simulator measures; it does not pass or fail a run. Exit status 1 means it could not run
  or write its files.

## Tests

`go test ./...` runs the simulator against a small fake control plane in the test files,
including every fault. `tests/e2e` runs it against a real offline install
(`OWNSOLID_E2E_SIMULATED_AGENTS`, default 1,000).
