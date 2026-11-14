# Tasks

A task is work the control plane sends to one node's agent, such as collecting its facts. The
[Tasks](/tasks) page lists them, newest first.

## Following a task

- Filter by state to find what is running or what failed.
- Open a task to see its steps, its log and how it ended.
- A task that is waiting for its node says so. It starts when the node's agent reports again.

## When a task fails

The task's page shows the error and the log lines that led to it. Fix the cause on the node, then
run the action again. Failed tasks from the last day also appear on the overview.
