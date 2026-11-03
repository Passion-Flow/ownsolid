# Business module owners

Modules own business state and authorization. HTTP subpackages adapt bounded transport requests; repositories execute explicit selected-dialect SQL. A module directory is not a network service.

## Current responsibility map

| Module | Responsibility and key entry |
| --- | --- |
| [identity](identity) | Account/password/session authority, audience, current actor recheck; service.go and session_repository.go |
| [workspaces](workspaces) | Installed-tenant Workspace membership, roles, notes and current scope; scope.go |
| [audit](audit) | Transaction-bound audit event persistence; repository.go |
| [licensing](licensing) | Signed whole-platform license history and admission; service.go |
| [sso](sso) | Connector/binding/flow and session-source state, protected keyring and maintenance |
| [nodes](nodes) | Enrollment, node identity/facts, task leases, revocation and reconciliation |
| [components](components) | Managed component lifecycle and observations on admitted nodes |
| [models](models) | Model import/version bytes and integrity |
| [deployments](deployments) | Model/node deployment state, hardware/GPU reservations and runtime observations |
| [distribution](distribution) | Gateway identity, API keys, aliases, admissions and request outcomes |
| [datasets](datasets) | Upload/draft/row/version, processing jobs, immutable publication and training inputs |
| [training](training) | Training admission/attempts, dispatch, events, checkpoints/artifacts and recovery |
| [modelpreparations](modelpreparations) | Held-out inputs, preparation/evaluation, review, derived models and alias target workflow |
| [knowledge](knowledge) | Scoped TXT ingestion, generations/chunks, encoding, retrieval and answer orchestration |
| [workflows](workflows) | Validated versioned graphs, runs/conversations/traces and bounded executor |
| [plugins](plugins) | Signed built-in tool installation and current Workspace grants; not arbitrary downloaded code execution |
| [applications](applications) | Portal application projection over workflow owners |
| [preferences](preferences) | User preference persistence |
| [notifications](notifications) | User-visible terminal notifications and maintenance |
| [mongodatasets](mongodatasets) | Read-only granted MongoDB collection adapter, snapshot-session read |
| [cloudinventory](cloudinventory) | Configured AWS inventory discovery/import; not cloud provisioning |
| [artifactstores](artifactstores) | Object grants, upload/download/ingestion, SQL state and source snapshots |
| [imageregistry](imageregistry) | Pinned/signed Registry imports, grants, deployment references and reconciliation |
| [taskdeliveries](taskdeliveries) | SQL outbox/receipt/dead-letter state for existing node-task events |
| [middleware](middleware) | Admitted maintenance instance/operation state, backup/isolated-restore/reconciliation |
| [monitoring](monitoring) | Scoped Admin observations over existing owners and the HTTP recorder; no new telemetry authority |
| [privateclusters](privateclusters/README.md) | Newly imported M28 scoped Kubernetes proof-Job source; connection/run identity, exact remote reconciliation and cleanup |

The table covers the 26 owners inspected at the initial architecture checkpoint plus the subsequently imported M28 privateclusters source. All 27 source module roots now have a guide. M28 composition and acceptance must be reconciled against the [handoff](../../../../docs/milestones/m28-private-cluster/mainline-handoff.md). M29 Xinchuang combination and secure licensed connector obligations are separate. A retained source package or a directory guide does not by itself establish a composed and accepted capability.

## Dependency and transaction rules

Identity authenticates current actor/session; workspaces resolves current installed tenant and membership. Most business modules borrow those owners. Cross-domain calls follow actual dependencies: workflows uses knowledge/plugins/distribution; training uses datasets/models/nodes/deployments; modelpreparations additionally uses training; taskdeliveries uses nodes/workspaces. Do not use another owner's tables as an alternate authorization interface.

Use the same selected transaction for authority, state, audit and outbox. Tenant and Workspace predicates remain explicit. After external or long-running work, recheck current authority/state before committing outputs. Commit-unknown and external uncertainty retain explicit readback/reconciliation; retries require the owning idempotency/attempt contract.

The current identity/http package also constructs/routes unrelated modules. This is a recorded composition issue. Keep shared-owner identity and lifecycle checks intact until a separate verified extraction is performed.

## Extending a capability

Update its contract before changing wire/state semantics: HTTP/schema/generated types, SQL migrations and both supported dialect repositories, canonical messages, typed settings and env/deployment examples, worker/agent interfaces, source/archive manifests and meaningful failure/recovery tests. Contract generation does not establish runtime acceptance.

Run focused native packages after resource admission, then the actual selected SQL/browser/process harness that exercises the changed path. See [control guide](../../README.md), [backend audit](../../../../docs/architecture/current-product/backend-audit.md) and the owning milestone under [docs/milestones](../../../../docs/milestones). E001–E090 planning is not an instruction to populate future modules now.

[releases](releases/README.md) owns configured installation-bound Admin release projections,
current-authority read audit and bounded history. The offline scope journal remains authority.
