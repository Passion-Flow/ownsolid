# 0033: A protocol-level agent simulator

Status: accepted (ARC-014). Research: [agent simulator](../research/agent-simulator.md).

## Context

ownsolid is published as supporting 10,000+ GPU servers, and the P0 exit needs 2,000 agents
steady at twice the planned load, a full reconnect within five minutes and a control restart
that leaves work running. None of that can be shown with real nodes. A load test needs
thousands of agents that behave like the real one on the wire, from one process, with faults
that hit a chosen share of the fleet at one moment.

## Decision

**Speak the contract, not the agent's code.** `tools/dev/simulator` is its own Go module. Its
messages, session codec, validators, call names and pacing come from `api/agent/v1`; it imports
nothing from the agent. The pacing arithmetic (full-jitter backoff, the jittered interval, the
spread above a retry hint) moved from the agent into `api/agent/v1/backoff.go`, so both pace the
same way by construction. The simulator's cycle follows the agent's run loop step by step:
heartbeat, a claim held for most of the interval, acknowledgement before execution, progress and
result, HTTPS fallback after a stream that never answered, idle stream replacement, retry hints,
and an end on a 401.

**One goroutine and one connection per agent.** An agent's session gets its own HTTP/2
connection by default, as on a real node, because connections are part of what a load test
measures. `agents_per_connection` multiplexes agents on one connection when a run needs more
agents than the host has sockets. Task traffic and enrollment share a bounded HTTPS pool.

**Identities persist between runs.** `enroll` spends one real join token per agent and writes an
identities file (mode 0600) with credentials and task cursors; `run` reads it and writes the
cursors back. A second run continues the same fleet, and stopping the simulator is itself a
fleet restart.

**Faults are scripted and measured, not judged.** A run lists faults by kind, time and share:
`disconnect`, `restart`, `partition`, `https_only`, `lose_replies` and `lose_cursor`. The JSON
report counts every call by outcome with latency percentiles, the tasks, resyncs and lost
replies, and for each fault the agents it hit and how long until each had a good heartbeat
again. Tests and load tests judge the report.

**Simulated nodes are read-only.** They run `collect_facts.v1`, the one task of the `read_only`
profile, which exercises enrollment, liveness, dispatch, acknowledgement, progress and results.
Pretend executors for training, inference and components are added when a load test needs their
traffic.

## Consequences

- A run from one host shares one client address. The run raises `agent_admission.address`,
  `http.max_connections`, `nodes.maximum_live_nodes` and `rates.agent_enroll_ip_count` instead of
  spoofing addresses; real fleets come from many.
- `http.max_connections` is at most 4,096 per control process, so one process cannot hold more
  agents than that; past it is the gateway tier of ARC-102.
- Building the simulator found that the agent ignored a revoked credential on the session stream:
  control answers it with `Unauthenticated`, which the agent counted as a refusal and retried for
  ever. It now ends the agent with `AGENT_CREDENTIAL_REVOKED`, as the same answer over HTTPS does.
- The simulator can drift from the agent where they do not share code. The shared pacing and the
  contract's validators bound that; a change to the agent's run loop updates the simulator in the
  same change.
