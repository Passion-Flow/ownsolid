# Control-plane crash recovery

Research behind REL-001: control restarts or crashes without losing tasks. A clean
restart under load was proven by ARC-015. This note is about the case nothing else
covers: control dies at an arbitrary instruction, with no shutdown code running.

## How others solve it

| Source | What it does | What we take from it |
| --- | --- | --- |
| [Crash-Only Software](https://www.usenix.org/conference/hotos-ix/crash-only-software) (Candea and Fox, HotOS IX, 2003) | The only way to stop such software is to crash it, and the only way to bring it up is to run recovery | Startup is the recovery path. No shutdown hook may be needed for correctness |
| [River maintenance services](https://riverqueue.com/docs/maintenance-services) | A rescuer finds jobs stuck in `running`, for example after a hardware crash, and either enqueues them again or discards them at their maximum attempts. The default window is one hour, and a rescued job can run alongside an attempt that is still alive | A row left running is requeued within an attempt limit. A lock that ends with the process is better than a time window, because it cannot overlap a live attempt |
| [Temporal: detecting Activity failures](https://docs.temporal.io/encyclopedia/detecting-activity-failures) | The server does not detect a Worker crash; the Start-To-Close timeout does, and a retry increments the attempt count | A crash spends an attempt. Detection does not depend on the dead process saying anything |
| [Temporal: Activity definition](https://docs.temporal.io/activity-definition) | Recommends idempotent Activities, because a Worker can finish and crash before reporting, and the Activity then runs again | Every step must be safe to repeat, including after it succeeded unreported |
| [Kubernetes controllers](https://kubernetes.io/docs/concepts/architecture/controller/) | Controllers move current state toward desired state, and the design allows for a controller failing | Workers read durable state on each pass; nothing is held only in memory |
| [PostgreSQL advisory locks](https://www.postgresql.org/docs/current/explicit-locking.html#ADVISORY-LOCKS) | A session-level lock is held until released or until the session ends, and the server cleans it up | Holding an import's lock proves the last owner is gone |
| [Docker restart policies](https://docs.docker.com/engine/containers/start-containers-automatically/) | `unless-stopped` restarts a container that exits, but not one stopped by hand. The page does not cover `docker kill` | The drill kills the process from outside rather than using `docker kill` |

## What control keeps running, and what a crash leaves

Every worker reads durable state, so none holds work only in memory. The table is the
result of reading each one.

| Work | Recovery after a crash | Test, and whether it ran for this item |
| --- | --- | --- |
| Agent tasks, training, model preparation | Agents keep running and report when control returns (ADR 0032) | `TestControlCrash`, ran with simulated tasks. Training and preparation were not in the drill: ARC-013 covers them against a control outage |
| Model upload | Chunks are written to a temporary file, synced and linked, then recorded in one transaction. A chunk with no row is uploaded again and matched by digest | `TestControlCrash`, ran |
| Model import | The session lock ends with the process and the row stays `verifying`, so the worker selects it again. See below | `TestActualModelImportWorker` and `TestControlCrash`, ran |
| Mail | A `sending` row past `locked_until` is sent again up to the attempt limit, then marked `smtp_unknown_outcome`, because whether the server accepted it cannot be known | `TestM01CurrentHTTPMailAcceptedWorkerCrash`, ran: a child process is sent SIGKILL after the server accepted the message |
| Workflows | A lease with an expiry: a row whose lease lapsed is recovered, and a live owner is left alone | `TestM13ActualExpiredRunRecoveryIsBoundedAtomicAndPreservesLiveOwner`, ran. It sets timestamps rather than killing a process |
| Datasets | A lease with an expiry | `TestActualDatasetRuntimeWorkerRecoveryAndControls` exists and did not run: it needs the pinned data Python |
| Knowledge | A lease with an expiry | `crash_integration_test.go` exists and did not run: it needs the encoder model. Knowledge retires in favour of DeepParser |
| Task deliveries | A lease with an expiry | `TestTaskEventsWorkerRecoveryLifetime` exists and did not run: it needs the task-events build tags and their fixture |
| Task wakes | The transactional outbox (ADR 0028), with reconciliation behind it | `TestControlCrash` through the fleet's tasks, ran |
| Liveness | Rebuilt from heartbeats; last-seen is batched (ADR 0022) | `TestControlCrash`, ran: every agent is heard again |

The tests that need `PROJECT_ONE_CONTROL_BINARY`, `TestM01ActualProcessRestartAndLogicalRestore`
among them, are skipped by `make test`. That one stops at `COMPONENT_ASSET_UNAVAILABLE`
without the component assets, so it did not run either.

## The defect found

An import killed while extracting could not finish. The row stayed `verifying` and the
worker took it up with the same attempt number, so it met the files of the attempt that
died in the same stage directory. `VerifyDirectory` refused them and the import ended
`failed` with `MODEL_STAGE_INVALID`, which the integration cases confirm when the fix is
removed. The administrator had to start it again by hand.
The same happened after a clean stop or a timeout during extraction, because those
leave the row and the files in the same condition.

## Chosen approach

On taking up a `verifying` import, the worker classifies the attempt's stage without
reading its content:

| Stage | Action |
| --- | --- |
| Empty or missing | Same attempt: nothing was written |
| Every approved file, full size, read-only | Same attempt: `VerifyDirectory` checks the digests before publishing |
| Only approved files, some unfinished | Remove them, spend the attempt, start the next in its own stage |
| Anything extraction never writes | Same attempt, which refuses with `MODEL_STAGE_INVALID` and leaves it for an operator |

Read-only is the marker of a finished file because `ExtractArchive` makes a file
read-only only after writing and syncing all of it. Holding the import's session lock
and file lock proves no live process is writing, so the removal cannot race an attempt.

An import that keeps stopping control ends `failed` with `MODEL_RECOVERY_REQUIRED` at
the attempt limit, so a poison bundle cannot crash control forever.

## Alternatives rejected

- **Resume extraction in the same stage.** A tar stream cannot start from the middle
  of a file without its own checkpoint format, and a file cut short by a crash cannot be
  trusted. Extraction takes seconds to a few minutes, so restarting costs little.
- **Clear the stage at startup.** Startup does not hold each import's lock, and a second
  process could be working during a rolling restart. The claim already holds both locks.
- **Keep the same attempt number after removing the stage.** A bundle that crashes control
  during extraction would then retry forever.
- **Mark the import failed and ask the administrator to retry.** This was the old behaviour.
  It turns a restart into operator work.

## Dependencies and licences

No new dependency. The sources above informed the design; no code was taken from them.

## Failure modes and tests

| Failure | Test |
| --- | --- |
| Control killed during an upload, with a chunk in flight | `TestControlCrash`: the server's offset is within one chunk of the client's and the upload resumes |
| Control killed with a part-written stage | `TestControlCrash` kills on seeing an unfinished file; the import reaches `ready` on attempt 2 |
| Part-written stage on the last attempt | Integration case `partial_stage_on_the_last_attempt`: `MODEL_RECOVERY_REQUIRED` |
| A stage with foreign content | Integration case `partial_stage_retained`: refused and untouched |
| An attempt that wrote nothing | Integration case `empty_stage_keeps_the_attempt`: no attempt spent |
| Links, executables and hard links in a stage | `TestClassifyStage`: treated as foreign |
| Tasks running when control dies | `TestControlCrash`: every one finishes on its first attempt |
| A write repeated after a crash | `TestControlCrash`: the same `Idempotency-Key` finds the same task |
| Control does not come back | `TestControlCrash` reads Docker's restart count and readiness, with no operator step |

## What the finished item must do

- A SIGKILL of control at any moment loses no accepted task, upload or import.
- Control returns by its restart policy alone, and agents reconnect.
- Running agent tasks finish on their first attempt.
- A killed import finishes without an operator, spending one attempt.
- Repeated writes make no duplicates, and nothing leaves the host.

## Measured

`TestControlCrash` on `ownsolid-cpu`, two passing runs in a row with 2,000 simulated agents, offline,
with the model store in memory:

| Measure | Run 1 | Run 2 |
| --- | --- | --- |
| Control ready after the kill during upload | 1.8 s | 1.7 s |
| Control ready after the kill during unpacking | 2.4 s | 3.4 s |
| Tasks through the crash that finished on their first attempt | 101 of 101 | 101 of 101 |
| Import after the crash | `ready`, attempt 2 | `ready`, attempt 2 |
| Agents heard again, checked after the kill | every one, at 88 s | every one, at 90 s |
| Egress | None | None |

The agent figure is when the check ran, not how long reconnection took; ARC-015 measures that.
With the real bundle, the import's `Archive`, `Worker`, `WorkerWire` and `ProcessRecovery` suites
passed on PostgreSQL 18.6, including a worker sent SIGKILL after its claim.

## Limits

- Uploaded chunks stay in the store after an import is ready: MOD-120.
- A long-running task resumed after any component restart, not only control, is REL-106.
- MySQL and Dameng share the claim code, but the drill ran on PostgreSQL only.
