# Nodes

A node is a server that runs the ownsolid agent. The [Nodes](/nodes) page lists every node with its
state and lets you add, change and remove them.

## Adding a node

1. Choose **Add node**. The page creates a one-time join token and shows the commands to run on the
   new server.
2. Run the commands as shown. They keep the token out of the shell's history.
3. The node appears in the list once its agent joins. A token joins one node; an unused token can be
   revoked.

A node joins only while the installation has a valid [license](help:license).

## States

| State          | Meaning                                                                                   |
| -------------- | ----------------------------------------------------------------------------------------- |
| Online         | The agent reported recently.                                                              |
| Not responding | The agent has not reported for a while, but not long enough to call the node offline.     |
| Offline        | The agent stopped reporting. Check that the server is up and can reach the control plane. |
| Disabled       | An administrator took the node out of service. No new work goes to it.                    |

The tiles above the list count each state and filter the list when chosen.

## Changing a node

Open a node from the list to see its system facts and recent tasks.

- **Collect facts** asks the agent to report the server's hardware and software again.
- **Rename** and **Disable** can be undone from the notice that follows.
- **Remove** asks you to type the node's name, because it cannot be undone.

Select several nodes in the list to act on them together.

## Agent pacing

[Agent pacing](/nodes/pacing) sets how often agents report and how they back off after losing the
connection. Agents adopt a change from their next report.

## Refusing agent traffic

When the fleet itself is overloading the installation, the same page can refuse agents' calls, lowest
priority first: transfers, then telemetry, then everything except quarantine reports. A change acts at
once, needs a reason and is kept in the audit trail, and agents retry later by themselves.

With everything refused, nodes turn offline here and receive no new work, while tasks and model
services already running on them continue. The page asks you to type `heartbeats` first, and the
Nodes page says so for as long as it lasts.

If this page cannot be reached, run `ownsolid-install agents shed` on the server.
