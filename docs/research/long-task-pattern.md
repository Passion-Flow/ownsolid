# Long-task pattern

Research behind UX-104: a step timeline with durations, a live log, and tasks that keep running
when the page is left. Every page that starts long work builds on it, so it is settled once here.

## How others solve it

- **GitLab** ([job logs](https://docs.gitlab.com/administration/cicd/job_logs/),
  [jobs API](https://docs.gitlab.com/api/jobs/)): the runner sends a running job's log to the
  server, which stores it and archives it when the job ends. "If a job's log is open, updates are
  sent every 3 seconds", and every 60 seconds when it is closed. A log is limited to 100 megabytes
  by default. The public API reads a whole log; the page documents no offset for it.
- **GitHub Actions**
  ([workflow run logs](https://docs.github.com/en/actions/how-tos/monitor-workflows/use-workflow-run-logs)):
  a run lists its steps with the time each took, failed steps open by themselves, a step's log can
  be searched, and the whole log can be downloaded.
- **Proxmox VE** ([API](https://pve.proxmox.com/wiki/Proxmox_VE_API)): a call that starts slow work
  answers at once with a task ID, and the caller follows that ID. The fetched pages did not reach
  its task viewer, so nothing here relies on how it reads a task's log.
- **Server-sent events**
  ([MDN](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events/Using_server-sent_events)):
  over HTTP/1 a browser holds six connections per browser and domain, shared by all tabs, and
  browsers will not raise that; over HTTP/2 the limit is negotiated and defaults to 100 streams.
- **`role="log"`**
  ([MDN](https://developer.mozilla.org/en-US/docs/Web/Accessibility/ARIA/Reference/Roles/log_role),
  [WAI-ARIA 1.2](https://www.w3.org/TR/wai-aria-1.2/#log)): a live region where new information
  arrives in a meaningful order and old information may disappear. It needs an accessible name, and
  its implicit `aria-live` is `polite`.
- **TanStack Query**
  ([useQuery](https://tanstack.com/query/latest/docs/framework/react/reference/useQuery)):
  `refetchInterval` can be a function of the latest data, so a query stops once a task has finished,
  and by default it does not refetch while the tab is in the background.
- **Carbon**
  ([progress indicator](https://carbondesignsystem.com/components/progress-indicator/usage/)):
  steps are completed, current, not started or in error, and labels avoid vague terms such as
  "Processing". The page says nothing about long-running work.
- **Kubernetes** ([logging](https://kubernetes.io/docs/concepts/cluster-administration/logging/)):
  a node bounds container logs by size and file count, so a chatty process cannot fill the disk.

Cloudscape's steps page could not be fetched, so this note does not rely on it.

## What ownsolid has

Every operation on a node is a row in `node_tasks`: the agent claims it, holds a lease, and reports
a result. Training and model preparation also report events, through the `events` agent call, which
is telemetry priority and so is shed before heartbeats (ADR 0018). Other operations report nothing
until they finish, and the result carries no steps. Training and preparation cannot run on the test
server, which has no GPU and too little disk for their images.

## Choices

- **The pattern is built on node tasks.** They are the one thing every node operation shares, a real
  agent runs them on the test server, and training and preparation pages can use the same pieces
  with the events they already have.
- **The agent reports progress on the existing `events` call.** A new path, `task-progress`, carries
  a batch of step changes and log lines under the task's lease. Lane A owns these paths, so the
  change only adds to them: new types, one route, a reporter the runtime passes to each operation.
- **Progress never decides a task.** The result does. A report that fails or is shed is retried from
  a bounded buffer and then dropped, and the task carries on.
- **The agent reports at most every five seconds, and at once on finishing.** This is GitLab's rule
  with a longer interval, since a fleet is larger than a runner pool: nodes do not know whether
  anyone is watching, and shedding protects control when many tasks run at once.
- **Progress is an append table, partitioned by month** by the task's creation time (ADR 0029), so
  it leaves with its partition and rows are never deleted one at a time. A retried attempt starts its
  own sequence, and the timeline shows the latest attempt.
- **A task's log is capped at 4,000 lines.** Trimming the oldest lines would need row deletes, so the
  agent stops sending lines at the cap, keeps reporting steps, and says how many it left out. The
  result's error code still states why a task failed.
- **Log lines are written by agent code, not copied from command output.** That keeps secrets and
  terminal control sequences out by construction. Lines are English, like every log; step names are
  stable keys the console translates.
- **Durations use the agent's clock at both ends,** so a skewed clock or a delayed report cannot
  distort them.
- **The console polls with a cursor, every three seconds, only while the task runs and the tab is
  visible.** A viewer holds no connection on control, a poll passes every proxy and uses the session
  and limits of any other read, and six HTTP/1 connections per domain would otherwise bound how many
  tabs one person can keep open. Server-sent events were rejected for these reasons, and WebSockets
  for the same ones with more machinery. The agent's five seconds, not the poll, sets how fresh a
  line can be, so streaming would add nothing visible.
- **Leaving the page changes nothing,** because the task runs on the node. The header shows how many
  of the person's own tasks are running, with links to them, and a toast says when one finishes, on
  whatever page they are on. The server holds this state, so it survives a reload or another browser.
- **The log can be paused.** New lines stop appearing until it resumes, as WCAG 2.2.2 asks of content
  that updates by itself. It follows the end only while it is scrolled to the end, so reading earlier
  lines is not interrupted. Copy and download work on the lines loaded, with no server call.

## Licences

No new dependency. The server change uses PostgreSQL only; MySQL and Dameng do not store progress,
and their tasks show the states the task itself records.

## Failure modes and tests

- An agent resends a batch after a lost answer: sequences make it idempotent. Server test.
- A stale lease reports progress for a task it lost: refused. Server test.
- A report arrives malformed, too large or past the cap: refused with a stable code. Server and
  wire tests.
- Control sheds reports under load, or is unreachable: the buffer stays bounded and the task's result
  still arrives. Agent test.
- A person reads a task of another tenant: not found. A person without the role: denied. Server test.
- A task finishes and moves to the history: it is still listed and its progress still reads. Server
  test.
- A poll fails: the page says so, keeps what it has and tries again. Browser flow.
- An older agent reports nothing: the page shows the task's state without steps. Browser flow.
- A retried attempt: the timeline shows that attempt, and the log marks where it began. Browser flow.
- A log of thousands of lines: the page stays responsive and memory stays bounded by the cap. Browser
  flow.
- Leaving and coming back while a task runs: the page shows the current state, and a finish that
  happened meanwhile is announced once. Browser flow.

## Done when

An administrator starts a task on a node from the console, watches its steps fill in with durations
and its log grow, leaves the page, sees the task running in the header from any page, is told when
it finishes, opens it again from the task list, reads the same timeline and log after a reload, and
runs a failed task again. Every state passes the accessibility and keyboard flows in both themes and
both languages, the pieces are documented in the design system for the next pages, and a real agent
on `ownsolid-cpu` has done all of this.
