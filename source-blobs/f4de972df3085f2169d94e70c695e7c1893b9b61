# 0030: Task progress on the events call, read by polling

Status: accepted (UX-104). Research: [long-task pattern](../research/long-task-pattern.md).

## Context

The console needs a step timeline with durations and a live log for work that runs on nodes, and
that work must carry on when the page is left. Until now a task reported nothing until its result,
except training and model preparation, which have events of their own. The agent call table has a
cost declared for every call, and control sheds telemetry first when it is short of capacity.

## Decision

**An agent reports on the existing `events` call.** A new leaf, `task-progress`, carries a batch of
step changes and log lines under the task's lease, so it is shed with other telemetry (ADR 0018) and
adds no new call to the cost table (ADR 0020).

**Progress never decides a task.** The result does. A report that is shed or cannot reach control
is kept in a bounded buffer and tried again; any other refusal ends reporting for that task, and the
task carries on. An older control answers such a refusal, so a new agent works with it.

**Reports are idempotent.** An entry is identified by task, attempt and sequence, and a repeat
changes nothing.

**Progress is an append table, partitioned by the task's creation time** (ADR 0029). It keeps no
foreign key to `node_tasks`, because a finished task leaves that table while its progress stays
readable beside `node_task_history`, and it leaves with its month after `task_history_days`.

**A log is capped per attempt rather than trimmed.** Trimming would delete rows from an append table.
The agent stops sending lines at the cap, keeps reporting steps, and says how many it left out.

**Lines are written by agent code and never copy command output.** That keeps secrets and terminal
sequences out by construction, and `Validate` refuses anything but one line of printable text. Steps
are stable keys that the console translates.

**The console polls with a cursor.** It reads on the installation's poll interval, only while the
task runs and the tab is visible, and asks only for lines after its cursor. The agent reports every
five seconds, which already sets how fresh a line can be, so a stream would add nothing a person can
see and would hold a connection per viewer. Polls are marked as background, so watching a task does
not keep a session alive.

**Leaving a page changes nothing, and the server holds the state.** A task runs on its node. The top
bar lists the person's unfinished tasks from any page, and a task that has left that list is
announced once, including one that finished while no page was open.

**PostgreSQL only, like the history it reads.** MySQL and Dameng answer `TASK_HISTORY_UNAVAILABLE`
and `TASK_PROGRESS_UNAVAILABLE` until CMP-201 and CMP-202.

## Consequences

- Fact collection and node exporter operations report their steps. Inference, training and model
  preparation keep their results and events; their pages adopt the timeline with their own items, and
  a task that reports no steps shows its state alone.
- The pattern lives in `@ownsolid/ui` (`StepTimeline`, `LiveLog`) and `@ownsolid/shell` (`TaskTray`).
- A new step key changes the agent contract, the nodes contract and both locales together.
