# Managed cell clusters in the console

Draft screen entries and contract handoff for ARC-104. The
[build plan](../product/plan/README.md) specifies node inventory, enrollment and detail
in S-103 to S-105. It has no managed-cluster create, progress or recovery entry.
The identifiers and routes below are proposed for the plan owner. Contract and
handler behavior were reviewed at `4a3f0aba`. Signed installation acceptance
is tracked separately.

## Evidence and approach

[Rancher Manager's custom-node flow](https://ranchermanager.docs.rancher.com/reference-guides/cluster-configuration/rancher-server-configuration/use-existing-nodes/)
separates preparing Linux hosts, assigning roles and waiting for provisioning.
[Cluster API](https://cluster-api.sigs.k8s.io/developer/core/controllers/cluster)
keeps provisioned infrastructure, control-plane conditions and terminal failures
separate. [RKE2's air-gap guide](https://docs.rke2.io/install/airgap) specifies local
binaries and images. Here, nodes are already managed by ownsolid. The console sends
intent to control, then shows the states control records as its agents install and
join. Direct browser access to Kubernetes, a cluster registration script or a
version picker would duplicate authority the current flow assigns to control.

Reuse the existing console stack, session, routes and components. This draft adds
no runtime dependency, model or dataset. The existing Apache-2.0 RKE2 release and
its image notices, commercial use, redistribution and disconnected preparation
are covered by [signed delivery research](rke2-signed-delivery.md) and
[ADR 0051](../decisions/0051-kubernetes-cells-with-rke2.md). The browser receives
the public CA fingerprint when available. RKE2 join tokens and kubeconfigs stay
within the agent and control authority.

## Plan insertion and adjacent entries

Place these entries after S-109 in Stage 1, where the operator already finds
infrastructure. This is the managed Kubernetes path. The existing Docker-based
S-203 flow still has its own prerequisites.

| Proposed entry | Route | Archetype | Primary |
| --- | --- | --- | --- |
| S-110 Kubernetes cluster | `/cell-clusters` and `/cell-clusters/$clusterId` | Singleton detail, with an entry empty state | Create cluster before it exists, Check readiness on an existing cluster, Retry server join if the server failed |
| S-111 Create Kubernetes cluster | `/cell-clusters/new` | Flow | Create cluster |
| S-112 Add workers | `/cell-clusters/$clusterId/workers/new` | Flow | Add workers |

Add an Infrastructure destination and a link from the existing Nodes list
(`/nodes`, S-103). Keep Add nodes as that list's primary action. Expand S-104
to prepare the Kubernetes execution profile, and S-105 to distinguish node
agent connectivity from Kubernetes readiness. Add a version-local S-107 article
for signed preparation, the supported host profile and the operator's recovery steps.
Assign the final identifiers and navigation labels in the formal plan before building.

## S-110 Kubernetes cluster

**For** a signed-in Admin platform administrator overseeing this installation's local cell.

**Does** opens the one managed cluster, shows which nodes joined, and lets the operator
recover a failed join or request a fresh readiness check.

**Content** the entry uses the singleton read. An empty response offers Create cluster.
An existing cluster opens its detail address. Detail has a breadcrumb to Infrastructure
and a Nodes link, the cluster name, site and cell, and a textual cluster status. Show
the returned RKE2 version, server node and server state, and the five member counts.
The CA fingerprint is copyable on Overview when returned. Label timestamps as their
recorded values. Cluster `updated_at` can change for cluster-level work and is not
the time of the most recent member transition.

Overview presents the server first and then the workers' aggregate state. The detail
tabs are Overview, Members and Activity. On Members, a table shows node, role,
enrolled address, state, join attempts and the reported observation timestamp when
available. Show a failure in words beside the affected node. The node name opens
S-105. Clicking the row opens a preview drawer with the cause, the available action
and a link to that node's detail. Filter only by the supported member state, in the
address. Fetch additional pages with the returned cursor. Show count tiles as filters
only when a tile sets that state. A filter that goes empty during reconciliation keeps
its filter name and offers Clear filter.

Use recorded states for the timeline. Pending workers can be described as waiting
for the server while the server is not ready. After that, pending only establishes
that the worker is waiting to join. The server starts first, followed by bounded
worker batches. The API does not expose artifact download, token, service startup
or image import as independent progress steps. Show the member state and counts
without a percentage, simulated log or estimated completion time.

**Click** Check readiness opens a short form for an audit reason. On acknowledgement
show “Readiness check requested” and keep the current recorded state. When the
counts show no ready or not-ready member, keep Check readiness visible and explain
that a member must first finish joining. Add workers opens S-112. On a failed
server, Retry server join is the primary and opens the
server member's drawer. On worker failure the summary links to the failed member
filter. Retry join is offered only for a failed member. A `not_ready` member uses
Check readiness after the operator resolves the cause. Link to the existing node
Tasks tab for recorded operations once the node task contract describes the
Kubernetes task kinds. A nullable task ID can be copied in technical details.

**Dialogs** a member preview is a drawer. Its retry reason and action live inside
that drawer. The header's readiness request uses a one-field reason dialog.
Each action submits from its own surface and there is no second confirmation.
Success or an already resolved condition updates the in-page record. A task
admission is a request acknowledgement. Keep durable progress and failures on the page.

**Activity** reuse the cluster-target audit read for creation, workers added and
observation requests. Member join, failure and retry entries target the node.
Link to that node's Activity tab. Do not issue an audit query for every member or
present the cluster-target results as a combined event stream. Display actor or
system, audience, action, outcome and time from the read. The public projection
does not expose an event origin address, and the enrolled node IP is not the
actor's address.

**Done when** creating a cluster leads to this persisted detail, reload recovers
progress, a failed server or worker can be retried from its own record, and a
readiness request only changes the reported result when control returns a change.
A server-only cluster can report Ready. The worker and workload acceptance
requires an actual worker.

## S-111 Create Kubernetes cluster

**For** a platform administrator in Admin with an empty local cell and eligible nodes.

**Does** asks control to install the cell's approved RKE2 release on one server
and any workers selected in this request.

**Content** one page with three sections and one submit. Name the cluster and
choose a server. Select zero or more workers, up to 1,000 in a request. Review the
named server, worker count and selected nodes, the impact on those hosts and the
required audit reason. Name and reason use the contract limits of 100 and 500.
The selection is disjoint and each ID appears once. Show that the agents will
install RKE2 from this installation's approved offline artifacts and use the
selected hosts for the cell. The create contract has no version, network, URL,
credential or address input. Before creation the singleton read supplies no
release or per-node byte estimate. Show known host usage and prerequisites. Only
display a release version or byte estimate before submit if a typed server read
provides it. Show the recorded distribution version on the detail after creation.

Use the paginated Admin node inventory for a searchable picker. Show node name,
workspace, administrative state, agent connectivity and the supported execution
profile when represented by the read. Keep visible choices disabled with a reason
when known to be unsuitable, such as a different profile, a disabled node or the
already selected server. Offer Add nodes when none have joined with the required
profile. Search only within the supported node inventory filters and keep selected
nodes across pages. An empty search result does not prove that the whole cell has
no candidates. The server verifies the actual profile, protocol, capabilities,
enrolled address, membership and workspace state. The picker cannot verify the
address or its uniqueness before creation from today's node response. Explain a
server refusal beside the selection and allow correcting the draft.

**Click** submit once and open the returned cluster detail. A 201 may contain
`installing`, or `failed` when control could not start the server's join.
Retain the same normalized submitted body and intent key if an answer is lost.
Refresh the singleton read and offer the existing cluster if another
administrator created it. A changed intent gets a new key. Keep non-secret draft
fields locally for recovery, scoped to the account and cell. Keep audit reason
and request state out of browser persistence. On reload after an ambiguous write,
read the current cluster before submitting again.

**Dialogs** none. The review and the submit live on this page. Host preparation
is a link to S-104 and the offline operator instructions, not a cluster join command.

**Done when** an authorized operator can select an enrolled server and workers,
review the consequences before submission, then recover the same cluster after
a lost response. A concurrent create yields the existing cluster and no duplicate.

## S-112 Add workers

**For** a platform administrator who can act on the chosen nodes' workspaces.

**Does** adds prepared nodes to the existing cluster as workers.

**Content** keep the cluster name and local scope visible. Reuse the candidate
picker, show known existing members and explain why they cannot be selected,
review the selected hosts, and collect a reason. Require one to 1,000 distinct IDs.
The selected worker list is preserved across a refused write. A single page keeps
the list, consequences and submit together.

**Click** submit returns to the cluster's Members tab and re-reads the counts and
member pages. Repeating the same selection is membership-idempotent on the server,
which leaves already-added nodes unchanged. After a lost answer, refresh the
relevant memberships before offering a repeat. Existing failed members recover
from Retry join in their member drawer.

**Dialogs** none. The page is the review. It leaves the operator in control of
the selection and does not offer a destructive inverse.

**Done when** adding the same workers concurrently results in one membership per
node, a new worker's recorded progress appears without a manual reload, and any
failure leads to the same member recovery path as initial creation.

## Seven states

| State | S-110 entry and detail | S-111 create | S-112 add workers |
| --- | --- | --- | --- |
| Loading | Delay a skeleton for the header and tabs. Use independent skeletons for member and audit reads. | Delay form and picker skeletons. Submit waits for the cell and required selection. | Delay cluster and candidate skeletons. Keep draft selection when refreshing. |
| Empty, first time | A successful empty cluster read says “No Kubernetes cluster in this cell” and offers Create cluster. A created cluster always has a server. An empty Activity tab explains what will be recorded. | No enrolled Kubernetes nodes explains that a node must join with the Kubernetes profile and links to Add nodes. An untouched draft says to choose a server. | No available prepared workers explains that workers must be enrolled or already belong to the cluster and offers Add nodes. |
| Empty, filtered | “No members in {state}” with Clear filter. If the cluster does not exist, use the entry state or the missing-record view for a direct detail link. | “No nodes match {filter}” with Clear filters. | Same filter-specific copy, distinct from the no-available-worker state. |
| Partial | Keep the cluster summary when members or Activity fail. Keep known rows on a later-page failure, and mark retained status as last read during a failed refresh. | Keep known node rows and selected IDs if a later page fails. Explain which list could not refresh. A missing required server read blocks the write. | Keep the cluster, selected nodes and loaded pages if a later candidate or member page fails. Unknown eligibility stays for server validation. |
| Error | Show localized problem and request ID. A failed join explains the cause and gives the appropriate retry or host fix. A missing or out-of-scope ID uses the same unavailable experience. | Show field, profile, address, membership, node availability and concurrent-create problems with the applicable next action. Keep the draft. | Explain the refused node selection, retain it and allow a correction. Reload the cluster if it is unavailable. |
| Denied | Admin session and platform administrator role are required. Show who can grant access. Recheck on a later denial and stop showing previously cached protected rows. | Same, with server enforcement over each selected node's workspace. License and CSRF failures have their own recovery. | Same, including a node moved or made inaccessible since it was selected. |
| Not ready | Show missing Kubernetes setup or unsupported database with the operator's next step. An applicable action stays visible with `aria-disabled` and a reason when blocked. Release capability notices belong in S-106. | An unavailable installation, license write refusal, or unavailable Kubernetes enrollment path keeps the action visible with `aria-disabled` and the actual prerequisite. | Same. Disabled candidate rows say why. An existing failed member links to its retry path. |

## Reusable contract and boundaries

The [cell-clusters contract](../../api/openapi/v1/cell-clusters/openapi.json) already
generates `cellClustersContract`. Use the existing typed
[`createClient`](../../web/packages/api/src/client.ts) and
[`localCell`](../../web/packages/api/src/cell.ts) for current local-cell routing.
The workspace switcher does not change the cluster scope. It is a cell resource
in the installation tenant. The UI uses the Admin session's
`user.is_platform_admin` for access affordances. The server rechecks the session,
platform-admin role and applicable workspace access. Do not derive authorization
from a node's display name, a workspace filter or a pasted cell ID.

`{root}` is `/api/v1/sites/{site_id}/cells/{cell_id}/admin/cell-clusters`.
All writes use the same-origin Admin session and its CSRF transport.

| Method and path | Input | Meaning of the response |
| --- | --- | --- |
| GET `{root}` | Site and cell path | 200, `items` with zero or one `Cluster`. |
| POST `{root}` | `name`, `server_node_id`, `worker_node_ids`, `reason`, UUID `Idempotency-Key` | 201, the persisted cluster and current counts. A replay with the same intent can return that cluster. |
| GET `{root}/{cluster_id}` | Cluster ID | 200, state, server state, counts, recorded version, nullable CA fingerprint and timestamps. |
| GET `{root}/{cluster_id}/members` | Optional `state`, `limit` 1–100 and opaque `cursor` | 200, creation-ordered member page and `next_cursor`. Changing state discards the old cursor. |
| POST `{root}/{cluster_id}/members` | `worker_node_ids`, `reason` | 200, current cluster. Repeating an already present selection leaves membership unchanged. |
| POST `{root}/{cluster_id}/members/{node_id}/retry` | `reason` | 200, the member's resulting record. It can still be pending, joining or failed. The server accepts retry only from `failed`. |
| POST `{root}/{cluster_id}/observations` | `reason` | 202, cluster summary. The server attempts to queue observation for idle `ready` or `not_ready` members. Busy or unavailable hosts may be skipped, including every host in a request. |

Use the existing runtime-configured poll interval for detail and visible member
pages. Scope query keys by local cell, cluster and filter, and abort reads on
route change. Poll with GET. Keep prior readable data on transient failure and
stop reading protected data after session loss. Invalidate relevant queries after
an acknowledged write. Retry and observations have no request idempotency header.
After an ambiguous answer, read the member and cluster first, then let the user
start a new attempt deliberately. A stale retry returns
`KUBERNETES_MEMBER_STATE_CONFLICT` and refreshes the row.

Label cluster states `installing`, `ready`, `degraded` and `failed` as
Installing, Ready, Degraded and Failed. Label member states `pending`,
`joining`, `ready`, `not_ready` and `failed` as Waiting to join, Joining,
Ready, Not ready and Failed. Use words with color. A failed server takes
precedence even when workers are pending. Installation
continues across browser reloads. `join_attempts` counts join tasks issued.
When present, `observed_at` is the server's recorded observation timestamp.
The current handler can keep the old readiness and advance that timestamp
after a failed observation. A timestamp change or disappearance of `task_id`
does not establish a successful fresh observation. Show the returned member
state and label the request acknowledgement independently. Per-member acceptance,
skip reasons and fresh observation success would need a richer server projection.
Review the deadline path with ARC. The [cluster reconciler](../../services/control/internal/modules/cellclusters/orchestration.go)
scans pending and joining members, while observations hold a task ID on ready or
not-ready members until a result is recorded. A task that ends without a result
may leave an observation reference that prevents later observations from queuing.
Exercise that case before promising automatic recovery. The UI keeps the last
recorded state and exposes the task reference where available.

| Failure | Operator recovery to specify |
| --- | --- |
| `KUBERNETES_CELL_CLUSTER_EXISTS` or `IDEMPOTENCY_CONFLICT` | Read the singleton before another create. Offer the existing cluster. Use a new intent key only for a deliberate different request. |
| `KUBERNETES_NODE_IN_CLUSTER`, `KUBERNETES_NODE_ADDRESS_INVALID`, `KUBERNETES_NODE_ADDRESS_CONFLICT` or `AGENT_PROFILE_REJECTED` | Preserve the selection. Name the server refusal and guide the operator to change the affected selection or prepare the host. |
| `NODE_UNAVAILABLE`, `TASK_DEADLINE_EXCEEDED` or `KUBERNETES_TASK_LOST` | Check agent connectivity and the task reference. The recorded failed member can be retried when its cause is addressed. |
| `KUBERNETES_ASSET_UNAVAILABLE`, `KUBERNETES_ASSET_INVALID` or `KUBERNETES_ARCHIVE_INVALID` | Verify the installation's approved signed RKE2 assets with the operator procedure. Retry the failed join after the fix. |
| `KUBERNETES_CA_CHANGED`, `KUBERNETES_CONFLICT` or `KUBERNETES_CONFIG_CONFLICT` | Inspect the recorded cluster and host with the operator. The recovery form does not replace the CA or overwrite an existing installation. |

### Owner handoffs before implementation

- **Plan owner.** Adopt the entries and the S-103, S-104, S-105 and S-107
  cross-links. Resolve whether a separately typed observation outcome or audit
  event origin is required for the first acceptance. Neither is present today.
- **Nodes API and Web.** The running backend accepts the `kubernetes_node`
  profile, protocol 1.0, and enrollment from a usable unicast address. The
  [Nodes contract](../../api/openapi/v1/nodes/openapi.json) currently omits that
  profile in node and token enums and in the token request. It does not expose
  the enrolled address or dedicated candidate eligibility. Correct the typed
  contract before presenting the Kubernetes enrollment or filtering choices by
  that profile. The current Add node dialog only offers `read_only` and
  `docker_inference`. Expose the Kubernetes choice through the supported offline
  Linux amd64 setup, keeping the ownsolid enrollment token shown once. This is
  separate from RKE2's server-to-worker bootstrap token, which the browser never
  handles. Task kinds also need contract coverage before linking a purported
  Kubernetes task view. Existing enrolled nodes can support a read-only cluster
  view from the current cell-clusters contract after plan adoption.
- **Web on plan adoption.** Reuse `ResourceHeader`, `Tabs`, `ResourceActivity`,
  `FilterTiles`, `Drawer`, `Dialog`, `StatusPill` and `ProblemNotice`. Cover
  the OpenAPI error codes and the separate member result codes in both locales.
  Address, profile and scope failures explain how to choose or prepare a node.
  For agent asset or digest errors, link to approved offline preparation. For
  host privilege, disk, resolver or conflicting existing installation errors,
  name the check and the affected host. For timeout or task loss, link to the
  node and offer a new join only when the record is failed. An unknown code
  gets a safe fallback with the request or task identifier when one exists.

## Acceptance evidence to collect after plan adoption

| Risk | Verification |
| --- | --- |
| A request is mistaken for readiness | Use a real signed disconnected installation with PostgreSQL and an enrolled Linux amd64 server and worker. Create in Chromium, navigate away and reload while the server and then the worker join. Compare the displayed states with the existing API and the actual Kubernetes Ready condition. Observe a busy or unreachable node and verify that 202 alone never displays new success. Expire an observation without a result and verify how a later request recovers. |
| An answer is lost or operators race | Replay create with the same key and body. Race two separate creates and two adds. Simulate an ambiguous retry and verify read-before-resubmit. A join that fails again requires a fresh deliberate attempt. |
| A node cannot join | Cover wrong profile, inactive or offline node, duplicate or invalid address, and denied workspace. Inject an actual failed member, check the error and next action, then recover the same record. Confirm that a `not_ready` member offers observation and a failed server is surfaced before waiting workers. |
| Scope or data is overstated | Verify Admin without platform authority, Portal, wrong cell, stale session, unavailable configuration, unsupported storage, missing record, filtered empty, and failed member or audit read. Confirm browser traffic stays on the installation and exposes no kubeconfig, join secret or machine credential. |
| The screens drift from the plan | On the workstation, run Playwright on dedicated ports with English and Simplified Chinese, both themes, keyboard and narrow viewports. View screenshots of entry empty, create review, joining, failed server, failed worker, filtered empty and denied. Record each screen's archetype, seven states and one main action. |

The signed bundle reaching Ready is separate ARC and delivery evidence. The
console acceptance uses that real path and keeps ARC-104 open until the joint
evidence is recorded by its owners.
