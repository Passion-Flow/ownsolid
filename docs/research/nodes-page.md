# Nodes page

Research behind FLT-114 (list, filters, details and adding a node), FLT-113 (agent pacing) and
FLT-115 (node events).

## How others solve it

- **Kubernetes** ([node status](https://kubernetes.io/docs/reference/node/node-status/),
  [nodes](https://kubernetes.io/docs/concepts/architecture/nodes/)): `Ready` is True, False, or
  Unknown when the node controller has not heard from the node within a grace period. Unknown is
  distinct from False, where the node itself reports being unhealthy. Being cordoned is not a
  condition but a mark on the node's spec, so a node can be ready and cordoned at once.
- **Nomad** ([`node status`](https://developer.hashicorp.com/nomad/docs/commands/node/status)): the
  list shows pool, datacenter, name, class, drain, eligibility and status as separate columns, with
  OS, address and version as options, a server-side filter and paging. A node's details add its
  events, allocated and used resources and its allocations.
- **GitHub self-hosted runners**
  ([add runners](https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/add-runners)):
  adding one shows ordered shell steps (download, configure with the URL and a token, run). The token
  is generated for the person and expires after one hour, and the runner then appears in the list with
  its status.
- **Rancher's** registration page was not checked: the official page we tried answered 404.

## Choices

- **Reachability and administrative state stay apart.** The control plane already derives
  `connectivity` (online, unknown, offline) from the last heartbeat, and `status` (active, disabled,
  removed) is what an administrator chose. A disabled node that still reports shows both.
- **Filter and count on the server,** as the list pattern requires. `connectivity`, `status` and a
  case-insensitive `name_prefix` narrow the list; a cursor is bound to its filters. `nodes/summary`
  counts a fleet with one query for the filter's labels and the overview. Removed nodes leave the list
  unless asked for, because their names can be used again.
- **Adding a node asks only for a name.** The token is minted for the workspace in use and is shown
  once. It stays out of the commands, so copying them cannot leak it, and it goes into a file by paste,
  so it reaches neither shell history nor a process list (AGENTS.md section 8). The dialog watches the
  token and says when the node has joined.
- **Joining is read-only for now.** A node with any other execution profile needs further sections in
  its agent configuration (roots, service user, Docker socket). Offering them before the page can write
  that configuration would produce commands that fail, so the rest comes with FLT-102.
- **Removal is typed, disabling is undone.** Removing a node clears its credential and cannot be
  reversed, so the person types its name. Disabling and renaming take effect at once and offer undo
  ([research](undo-and-confirmation.md)).

Rejected: a separate page for each node, because the list pattern's drawer shows everything the API has
today and keeps a person in the list; counting by paging through the list, which does not scale to a
fleet.

## Agent pacing (FLT-113)

The protocol and its bounds were settled in ARC-007 ([ADR 0013](../decisions/0013-agent-pacing.md)).
Kubernetes sets the kubelet's lease renewal and the controller's grace period as separate flags and
documents that the grace period must exceed the update interval; Nomad likewise derives a node's
heartbeat deadline from a server-side grace. Neither lets an operator pick an interval the grace
cannot hold.

- **Say the limit before control refuses it.** The read returns `online_window_seconds`, and the page
  shows the slowest interval the chosen jitter allows, computed with control's own inequality
  (`FitsOnlineWindow`), and a unit test holds the two equal at every edge. An interval past it is
  explained with the next step, raising `nodes.online_seconds`, and cannot be saved.
- **A change takes a reason and offers undo,** like every reversible setting; a conflicting save
  reloads what the other administrator saved.
- **The license never blocks it.** Pacing protects control from its own fleet, so it is a recovery
  action, like disabling a node.
- **It lives under Nodes,** at `/nodes/pacing`, linked from the Nodes page: it is about the fleet, and
  the navigation stays as the prototype has it.

## Licences

No new dependency. Everything is built from the workspace's existing packages.

## Kubernetes execution profile (DEV-101, ARC-104)

The node inventory and pending ownsolid enrollment tokens carry
`execution_profile`. The [Nodes API handoff](lane-d-questions.md#arc-104-generated-web-consumer)
adds `kubernetes_node` to those generated responses. The backend already admits
that profile for the managed cell path. The Console can display the value before
the generated contract moves. Keep one set of translated profile labels, checked
with TypeScript's [`satisfies` operator](https://www.typescriptlang.org/docs/handbook/release-notes/typescript-4-9.html#the-satisfies-operator)
against the Node profile union plus the known incoming literal. The typed token
consumer indexes the same labels. This keeps both contract revisions exhaustive
without weakening their types.

Show the label in the node list's Agent column, the pending enrollment record,
and the existing node preview and detail. The profile describes what the ownsolid
agent is authorized to do. Agent connectivity and the
[Kubernetes Ready condition](https://kubernetes.io/docs/reference/node/node-status/#condition)
have separate evidence. The create-token form retains its existing supported
choices and the cell screens still require their plan entries.

Compile the Web change with the current generated types and with the incoming
profile in the generated node and token shapes. In Chromium, return both the
older and the Kubernetes profile through the existing Admin reads. Verify the
labels in English and Simplified Chinese and keep registration status and node
connectivity independent of the profile. There is no new dependency, model or
dataset.

Land the label map before the Nodes contract change. Regenerate and check the
Web workspace on the combined branch before merging the contract.

## Failure modes and tests

- A filter applied to a cursor from another filter: refused with `INVALID_CURSOR`, integration test.
- A name prefix containing `%` or `_`: escaped, integration test.
- Two administrators changing one node: `VERSION_CONFLICT`, shown with the next step; browser flow.
- A token shown and then lost: it cannot be read again, so the person revokes it and mints another.
  Browser flow.
- A portal session or a workspace member: refused by the server, integration test.
- A link to a node the first page does not hold: the drawer reads the node by ID, browser flow.
- Numbers disagreeing with the rows: summary and list share one definition of connectivity,
  integration test across all three dialect statements on PostgreSQL.

## What done means

From the navigation an administrator can see every node, filter by connectivity, status and name from
the server, share the link, read a node's system facts and recent tasks, collect its facts again,
rename, disable and remove it, add a node and see it join, and revoke a token that was not used. Every
state has loading, empty, error and permission views in both languages.

## Open questions

- The offline bundle does not carry the agent binary yet, and no service unit is installed; a person
  copies the binary from the release. FLT-102 closes this.
- Live resource use and accelerators are not in the API (FLT-103, OBS items).
