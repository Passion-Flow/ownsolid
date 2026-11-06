# 0032: Agents keep running work when control is away

Status: accepted (ARC-013). Research: [static stability](../research/static-stability.md).

## Context

A task lease lasted 30 seconds. When renewals failed for that long, the agent cancelled the task
and stopped its container, control marked the job `recovery_required`, and the token was refused
from then on. A control outage of half a minute ended every training run and model preparation in
the fleet. A restarted agent stopped the container it found, and with control unreachable the
process exited at startup.

## Decision

**Only an answer from control ends work.** `client.Interrupted` separates a failure that says
nothing about the work (no connection, a reply that is not control's own, a 5xx, 408, 429, a retry
hint, a busy or timed-out upload) from a refusal control wrote with a code. Renewals, events,
checkpoints, uploads and results wait out the first kind for as long as it lasts, with full-jitter
backoff in the default pacing bounds, and end the work only on the second. A body without a valid
code, and a 401 without one of the codes that revoke a credential, count as silence.

**The token is the authority until the deadline.** `lease_expires_at` now only records when control
last heard from the agent. Renewal, events, uploads, results and stop receipts check the token, the
task state and `deadline_at`. The task stays with its node meanwhile: a node runs one task at a
time, so the claim path, which still fails a task whose lease expired on its last attempt, runs only
when the agent holds nothing. The periodic sweep, which runs without such proof, ends tasks only at
their deadline.

**Lost contact is shown, not judged.** The training and model preparation reconcilers keep a job
whose lease expired `running`, keep its GPU reservation, and set `wait_code` to
`TRAINING_NODE_UNREACHABLE` or `MODEL_PREPARATION_NODE_UNREACHABLE` until the agent is heard again.
Cancel, a removed or disabled node, a lost actor and the deadline end it as before, through a stop
receipt or the fenced removal.

**The agent process does not own the work.** On shutdown the agent leaves a started container
running and keeps its journal. The next process adopts it (`Resumable`, `Resume`) before anything
else, with what remains of the wall limit measured from the journal's creation, and follows it to
its end. Startup no longer needs control. A shutdown before the container started closes the
attempt with a never-created receipt as before.

**Transfers repeat safely.** A checkpoint event is recorded locally only after its upload finished,
so an interrupted upload runs again. An upload control already accepted sends no chunk. Results of
component and inference tasks are resent until control answers or the task's own limit passes.

## Consequences

- A training run survives a control outage of any length below its wall limit and deadline, and an
  agent restart during that outage, and its events and output arrive afterwards.
- Work runs unsupervised for as long as control is away. It stays inside its wall limit, resource
  limits and output budget, which the agent enforces on its own.
- A task's last attempt is no longer failed by the sweep when its lease expires; it waits for its
  deadline or for a claim from its node.
- MySQL and Dameng get the same rules: the changed statements are shared.
- An agent older than this decision still stops work on disconnect; upgrade agents to get it.
