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
- **Adding a node asks for its name and purpose.** The active workspace is the default. The token is
  minted for the chosen workspace and shown once. It stays out of copied commands and reaches the
  host through a file, keeping it out of shell history and process arguments (AGENTS.md section 8).
  The dialog watches the token and says when the node has joined.
- **The registration dialog offers read-only and model serving.** Other profiles need their own
  validated agent configuration and preparation instructions before the dialog can offer them.
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
have separate evidence. The label-map change covers inventory and pending token
reads. The cell screens still require their plan entries.

Compile the Web change with the current generated types and with the incoming
profile in the generated node and token shapes. In Chromium, return both the
older and the Kubernetes profile through the existing Admin reads. Verify the
labels in English and Simplified Chinese and keep registration status and node
connectivity independent of the profile. There is no new dependency, model or
dataset.

Land the label map before the Nodes contract change. Regenerate and check the
Web workspace on the combined branch before merging the contract.

### Registering a Kubernetes node (S-104)

The existing S-104 flow in the [build plan](../product/plan/README.md)
can issue an ownsolid Agent token for this profile. The
[token writer](../../services/control/internal/modules/nodes/join_writes.go)
accepts `kubernetes_node` on PostgreSQL. The
[Agent runtime](../../services/agent/internal/runtime/runtime.go) accepts the
profile with the default Kubernetes settings. Its existing
[`enroll --config` and `run --config` commands](../../services/agent/cmd/agent/main.go)
use the same protected token file and configuration paths as the other roles.
The [signed release acceptance](kubernetes-cells.md#signed-release-and-managed-api-acceptance)
exercised that profile with two real Agents on an isolated network.

[RKE2 requirements](https://docs.rke2.io/install/requirements) and the
[air-gap install guide](https://docs.rke2.io/install/airgap) describe a separate
cluster installation with operating-system, network and artifact prerequisites.
Registering the Agent prepares the node for the managed cell workflow and leaves
RKE2 installation and Kubernetes readiness to that workflow. Keep the existing
three copyable steps. Do not generate a Docker command, an RKE2 token or a host
IP override. [Control](../../services/control/internal/modules/nodes/http/handler.go)
records the enrollment connection's source IP, and the
[Agent's later join operation](../../services/agent/internal/kubernetes/manager.go)
checks that the IP belongs to the host. The host needs to reach Control over its
private network from an address it owns. Loopback and link-local addresses fail
admission, and an address rewritten by NAT can fail the later join check.

Require the HTTPS origin that the operator configured for Agents and the CA
that verifies it. [Machine ingress](../../services/control/internal/platform/httpserver/README.md)
may use a different listener from the Console. Prefilling the Console URL
would send some users to the wrong ingress. Validate and normalize the origin
before minting. [The Agent client](../../services/agent/internal/client/client.go)
verifies the CA and host and refuses paths, credentials, queries and fragments.

Use the existing authenticated [cell-clusters list](../../api/openapi/v1/cell-clusters/openapi.json)
as a read-only check. A successful empty list permits registration for the first
cluster. A missing route, missing configuration, unsupported storage, or denied
request leaves the Kubernetes role visible with its reason. A failed check can
be retried. Guessing support from the Console version or issuing a disposable
token would either miss deployment prerequisites or write server state. The
check cannot verify the host route, CA file or later artifact delivery. The token
writer remains authoritative if support or permission changes after the check.

Verify both languages, explicit disabled reasons, an invalid Agent origin,
a transient read failure, server token refusal and the three copy steps.
Exercise the actual commands against a disposable Linux amd64 Agent on a
private unicast address, and verify that the existing Console flow sees it
register. The live Chromium test uses `OWNSOLID_LIVE_KUBERNETES_NODE_SHELL`
and `OWNSOLID_LIVE_KUBERNETES_AGENT_ORIGIN` in addition to the live Console
settings. The host needs the matching Agent at `/opt/agent` and the machine
ingress CA at `/opt/ca.pem`, as in the existing live node test.
No new dependency, model or dataset. The pinned RKE2 distribution and its
offline redistribution are covered by [signed delivery](rke2-signed-delivery.md).

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

## CSV enrollment (S-104, FLT-102)

[AWS IoT bulk registration](https://docs.aws.amazon.com/iot/latest/developerguide/bulk-provisioning.html)
separates a cancelable batch from individual successes and failures. Its hosted task and S3
input are unsuitable for an offline site. ownsolid's Nodes API has a one-token POST, a cursor
list and a revocation POST. It has no batch job, per-token GET or idempotency key. Use a
browser queue with three workers. Stopping the queue leaves the requests already sent to
settle. Retry a row only when the server confirms no token was issued. A transport failure or
uncertain commit leaves that row unresolved and directs the operator to the pending-token
list for reconciliation before issuing again. Honor the server's retry interval on throttling.
The default tenant capacity of 100 live tokens accommodates an otherwise empty 48-node batch.
Existing tokens consume the same capacity.

Accept UTF-8 CSV with a required `node_name` header and an optional `rack` column using
[RFC 4180](https://www.rfc-editor.org/rfc/rfc4180.html) quoting. Also accept LF and a UTF-8 BOM.
Preview input locally, reject ambiguous duplicate names after normalization and fix rejected
rows before issuance. Bound file size and row count. The operator chooses one authorized
workspace for the entire batch. The CSV never supplies a workspace or credentials. Initial
bulk support covers Linux amd64 with the read-only Agent profile and reuses the single-node
offline instructions. Pool assignment, labels and the other Agent profiles remain visible with
their availability explained.

Poll the authenticated token history and match each result to the original token ID and
workspace. The enrollment transaction sets `consumed` and the linked `node_id` together.
This is proof of enrollment. Agent connectivity remains the node detail's responsibility.
A missing history record stays unverified. Keep the last confirmed results on a failed poll.
Show issued, joined, revoked and expired separately and aggregate only by the rack label from
the CSV. The Nodes API does not expose authoritative physical rack data. FLT-101 owns that
gap. Drafts retain names and status for the signed-in operator in the current tab. Secrets
exist only in the mounted page's memory and expire according to Control. The bulk flow revokes individual pending tokens with typed confirmation.

Validate syntax and duplicate names before writes. Exercise a 48-row batch, stopping and
resuming, refused writes, uncertain delivery, wrong-workspace results and actual Agent
enrollment. Verify the English and Simplified Chinese flow at desktop and narrow widths.
No new library, model, dataset or runtime service is required. The RFC and AWS documentation
are design references and are not bundled.

The enrollment record has no rejected-handshake history. If an Agent fails before consumption
(including a collision with an existing node), its token still appears as pending. The Agent's
output identifies the refusal. The Console can report token issuance refusals and actual
enrollment, and does not infer a rejected join from a quiet Agent.

The [live browser test](../../web/apps/console/e2e/live-nodes.spec.ts) used the local Mac's
Chromium, an isolated Docker network, real Control and PostgreSQL 18.6. Control issued 48
tokens and recorded 48 issuance audit events. One Linux amd64 Agent used the commands and
its token, producing a consumed receipt linked to a node in the same workspace. The
corresponding rack count changed on the open page. The other 47 tokens remained pending.
The Agent and Control had no external route. The browser asserted that all requests stayed
on the fixture origin. The [browser scenarios](../../web/apps/console/e2e/bulk-node-enrollment.spec.ts)
also cover scope mismatch, duplicate names, permission refusals, stopping, throttled retry,
uncertain delivery, resumed drafts and typed revocation.
