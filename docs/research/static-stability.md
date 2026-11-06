# Static stability: agents and disconnection

Research behind ARC-013. What running work does when an agent cannot reach control, and
when control may conclude anything from that silence.

## How others solve it

**AWS static stability.** "In a statically stable design, the overall system keeps working
even when a dependency becomes impaired"; "the data plane maintains its existing state and
continues working even in the face of a control plane impairment."
([Static stability using Availability Zones](https://builder.aws.com/content/3K9SiqiJ369TGkkGzqPM95BuYWI/static-stability-using-availability-zones))

**Nomad** states the choice most clearly in its
[`disconnect` block](https://developer.hashicorp.com/nomad/docs/job-specification/disconnect).
With `lost_after` set, a client that misses heartbeats is marked disconnected and its
allocations become `unknown`: they "may continue to run on the disconnected client". A
client stops work itself only when the job sets `stop_on_client_after`; without it, work
"will not be stopped on the client, regardless of how long the client is out of touch".
Nomad warns that this option stops work "if the Nomad server cluster fails", which is why
stopping on silence cannot be the default for us. Without `lost_after`, reconnecting
allocations "will restart because they have been marked 'lost'": the damage comes from
control's conclusion, not from the network.

**Kubernetes.** An unreachable node's `Ready` condition becomes `Unknown`
([Nodes](https://kubernetes.io/docs/concepts/architecture/nodes/)). Pods get automatic
tolerations with `tolerationSeconds=300`, so they "remain bound to Nodes for 5 minutes"
([Taints and tolerations](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/)).
Eviction exists because a Deployment wants a replacement elsewhere.

**Slurm.** `SlurmdTimeout`, default 300 seconds, is how long the controller waits before
marking a node `DOWN` ([slurm.conf](https://slurm.schedmd.com/slurm.conf.html)). The page
does not say what happens to the running jobs, so it is not evidence either way.

## What ownsolid did before this item

Measured in the code at commit 3486b17:

| Event | What happened |
| --- | --- |
| Renewals fail for 30 s (`TaskLeaseSeconds`) | The agent's watchdog cancelled the task and the training or preparation container was stopped and removed |
| One event, checkpoint or output transfer fails | Same: the attempt ended |
| The agent process restarts | `Recover` stopped the container at startup, and an unreachable control made the process exit |
| The agent receives SIGTERM | The context ended and the container was stopped |
| Lease expired at control | The job became `recovery_required`, and every later machine call with that token was refused |
| Inference containers | Not touched: no renewal, and the gateway does not read connectivity. An in-flight deploy was cancelled the same way as training |

A control outage of 30 seconds therefore killed every training run in the fleet.

## Chosen approach

Separate three things that one lease expiry had merged.

1. **Silence is not a verdict.** A failed request says nothing about a task. The agent ends
   work only on an answer control itself wrote: a refusal naming a code, not a retry hint,
   not a 5xx, not a proxy's page.
2. **Authority is the token, not the clock.** `lease_expires_at` records when control last
   heard from the agent. The token holds while the task is `running` and its `deadline_at`
   has not passed; an expired lease is renewed on reconnection. Nothing else can claim the
   task meanwhile: a task belongs to one node and that agent runs one task at a time
   (ADR 0011), so a re-claim happens only when the agent no longer holds it.
3. **The agent's process is not the work's owner.** The container belongs to Docker. On a
   restart or SIGTERM the journal keeps the attempt, and the next process adopts it.

Control has nothing to gain from concluding a training job is lost. Unlike Kubernetes and
Nomad, no replacement is placed, because a delivered attempt keeps its GPU until a stop
receipt proves it ended. So the job stays `running`, control marks `wait_code` with
`TRAINING_NODE_UNREACHABLE`, and the bounds are the ones that already exist: the task
deadline, the wall limit the agent enforces by itself, an operator's cancel, and the fenced
removal for a node that never returns.

## Alternatives rejected

- **A grace period, then `recovery_required`.** It needs a setting, a state flip and a
  reversal path. A control outage longer than the grace would flip the whole fleet at once,
  which is the failure this item removes.
- **`stop_on_client_after` style self-termination.** It kills work when control fails, which
  Nomad itself warns about.
- **A longer lease.** Moves the cliff, and turns a real refusal into a long wait.

## Bounds while disconnected

- The wall limit is enforced locally, also across a restart, from the journal's creation
  time, so unsupervised work cannot run forever.
- Output and event files stay within the attempt's existing budgets.
- Retries use full jitter with the default pacing bounds (ADR 0013), so a fleet returning
  from an outage does not arrive together. Renewals stay on their own 8-second tick.

## Failure modes and their tests

| Failure | Expected | Test |
| --- | --- | --- |
| Connection refused, 5xx, shed, proxy page | Work continues | `client` classification, `runtime` renewal tests |
| Control refuses the renewal, on reconnect too | Work stops, a receipt is sent | `runtime` renewal and guard tests |
| Checkpoint upload interrupted after its event | Upload retried, not skipped | `training` and `modelpreparations` manager tests |
| Finish reply lost, upload already accepted | No chunk is resent | Manager tests |
| Agent restarts with control down | Starts, adopts, keeps the container | Manager and `runtime` tests, real Docker test |
| SIGTERM | Container left running, journal kept | Manager tests, real Docker test |
| Wall limit passes during an outage or a restart | Attempt ends with a timeout | Manager tests |
| Lease expired past any length, deadline ahead | Renewal accepted, job still running | PostgreSQL integration test |
| Cancel requested during an outage | Refused on reconnect, container stopped | PostgreSQL integration test |
| Deadline passed | Refused | PostgreSQL integration test |
| Real container through an outage and a restart | Keeps running, adopted, removed at its end | `static_stability_integration` test on `ownsolid-cpu` |

## What finished means

- A training or preparation container keeps running through a control outage of any length
  below its wall limit, and through an agent restart during that outage.
- Its events, checkpoints and output arrive after reconnection, with none lost.
- The agent starts with control unreachable.
- Control keeps the job `running`, shows the lost contact, and accepts the token back.
- A refusal from control still stops the work and produces a stop receipt.

## Open questions

- GPU training through a real outage needs a rented GPU server; it has not run. The real-container
  test runs a counting process in place of the training image.
- Inference deployments keep serving when the agent stops, but a deploy task interrupted by a
  restart is not adopted; control delivers it again.
- Operators may want an optional per-job limit on unsupervised time, as Nomad offers. Off
  by default if it is ever added.
