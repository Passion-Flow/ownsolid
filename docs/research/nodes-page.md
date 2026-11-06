# Nodes page

Research behind FLT-114 (list, filters, details and adding a node) and FLT-115 (node events).

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

## Licences

No new dependency. Everything is built from the workspace's existing packages.

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
