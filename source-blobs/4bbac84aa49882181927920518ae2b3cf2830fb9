# Bound fallback after a usable Agent stream

Status: accepted for ARC-106 implementation after the external verified-TLS prototype; native installed and load acceptance remain open.

## Context

The unchanged 2,000-agent test failed its three-minute stream recovery deadline:
76 agents stayed on HTTPS for the fixed five-minute fallback after control
restarted. The session had already demonstrated a valid gRPC path, but the
fallback detector treated later paired failures as an unknown unsupported path.

## Decision

Retain the existing two probes and 300-second fallback for initial unknown or
unsupported paths. After a valid active/disabled stream heartbeat, remember that
fact only in memory for the current Agent session. Bound subsequent fallback by
min(300 seconds, the currently adopted valid maximum reconnect backoff advertised by control).
Use the same pure rule in the Agent and simulator; existing pacing and the cycle
remain the only retry owner. Do not replay an application exchange or create a
background worker. A new process forgets this fact; old agents retain their old
policy and v1 messages, credentials, persisted cursors and leases are unchanged.

## Required proof and tradeoff

A changed proxy can still use HTTPS; a previously usable path gets a bounded
retest instead of remaining there for five minutes. Such retests can be more
frequent, but each remains one existing paced attempt within server admission.
Do not increase failure counts, change deadlines, disable fallback permanently
or alter stream accounting. Qualify the verified TLS prototype before adoption,
then native race/vet, full repository gates and unchanged 500/2,000-agent runs.

[Research, licenses, rejected alternatives and retained failure](../research/fleet-load-test.md#previously-usable-streams-and-bounded-fallback)
contain the exact input and acceptance criteria.
