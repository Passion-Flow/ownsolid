# Gateway, aliases and API admission

Distribution owns gateway identity, Workspace API keys, model aliases and target history, request admission/outcomes and gateway recovery. Rust forwards model traffic but calls this owner for current business authority.

[authorization.go](authorization.go), [keys.go](keys.go) and [aliases.go](aliases.go) are the current access surface. [rotation.go](rotation.go) replaces a key's secret: the successor keeps the key's alias, label, owner and authority, the old key stops after an overlap of at most seven days, and a key has one successor (migration 000051, PostgreSQL only). [outcomes.go](outcomes.go), [reconciliation.go](reconciliation.go) and [recovery.go](recovery.go) bind reported outcomes. [cutovers.go](cutovers.go) and target_history.go retain model target changes. [monitoring.go](monitoring.go) projects existing observations without becoming a new traffic owner.

Migration 000007 holds gateways, aliases/history, API keys, requests and recovery records; lifecycle migrations extend target versions/intents. Keep request/gateway identity, canonical outcome digest and current alias/model target consistent. A response stream is not proof that accounting was acknowledged; revoked keys, stale alias targets, duplicate outcomes and fenced recovery stay explicit.

## Request usage history

Use the existing workspace request list and its returned cursor to extract
authorized JSON pages in descending admission order. Keep the
`api_key_id` filter and authorization scope fixed across pages. Admin can
read workspace history and Portal reads calls owned by the signed-in actor.
Counts come from the admitted rows. Sum only known token counts. A null
outcome or null usage leaves the token count unknown. Each reported outcome
carries elapsed milliseconds and the terminal status.

History detail and pagination check each stored outcome against its canonical
digest, the same check used for report replay. If a stored receipt fails
verification, the request returns `STORAGE_UNAVAILABLE` without a partial
page. See the [usage history verification](../../../../../docs/research/gateway-usage-history.md).

The Report transaction can project an accepted receipt into the PostgreSQL
usage ledger through `Service.ProjectGatewayReceipts`. The service defaults
to disabled. Enable it in control composition only after the ledger migration
is active. A projection failure rolls back the receipt and its report audit
and leaves the gateway able to retry the same canonical outcome.

Add `/export` to the cell-scoped Admin or Portal request list path to
download one authorized page as `gateway-usage.csv`. The request takes the
same `api_key_id`, `limit` and `cursor` query parameters. To continue,
use the cursor from the corresponding JSON history page with the same
authorization and filter. Both reads observe live data.

Each CSV row counts one admitted request. Blank elapsed time means no
reported outcome. Blank token cells mean unknown usage. A reported zero
is written as 0. The file excludes key identifiers and secrets, request
content and actor or deployment metadata.

## Trust a published gateway

Add `/ca` to a cell-scoped Admin or Portal alias detail path to download
`model-gateway-ca.pem` for its active agent-managed gateway. Use the file
with `curl --cacert model-gateway-ca.pem` and the alias's published HTTPS
origin. The download contains the public gateway CA certificates loaded by
control. Retain previous roots during a CA replacement until the serving
certificates they issued have been replaced. An administrator-placed
gateway uses its administrator's trust setup.

## Trace an admitted call

Keep the gateway's X-Request-ID response header. In the matching site and
cell, use the Admin request detail at
/api/v1/sites/{site}/cells/{cell}/admin/distribution/workspaces/{workspace}/requests/{request_id}.
Read the node, deployment ID and admitted revision. Use the Admin
deployment detail at
/api/v1/sites/{site}/cells/{cell}/admin/deployments/{deployment_id}
to read the GPU assignment. Verify that the node and revision match the
admitted request before attributing placement. The gateway uses the
same UUID in the generation request to its local engine.

## Cutover preview

In Admin, append `/cutovers/preview` to the cell-scoped alias detail
path. Send a POST with the same JSON body used for apply and an Admin
session with CSRF protection. The response shows the before and after
alias revision, deployment and pause state. Preview leaves target
history and audit unchanged. Apply rechecks the expected revision,
target and outstanding requests in its own transaction.

## Model alias descriptions

On PostgreSQL with the description migration, publish a plain-text
`description` with the alias. Admin changes it through a POST to
`/aliases/{alias_id}/description` under the workspace distribution path.
Send the current `expected_description_revision`, a reason and a UUID
`Idempotency-Key`. A blank description clears the text. A revision
conflict requires reading the alias again before editing. Portal sees
the description on its authorized alias list and detail. Description
edits preserve the published target, route revision and existing keys.

The unnumbered down candidate selects READ COMMITTED before locking or
reading retained state. An operator transaction with an earlier stronger
isolation snapshot refuses before DDL and must be rolled back. Fresh
transactions still refuse to discard description text, independent
revisions or replay intents. The ARC-qualified formal source carries the
same bytes; production activation and Linux amd64 acceptance remain separate.

The current PostgreSQL lineage still returns predecessor alias responses.
The candidate DDL, offline fixture and shared integration order are
recorded in the [description qualification](../../../../../docs/research/gateway-model-description.md).

## Development checks

Synchronize [Rust gateway](../../../../gateway/README.md), contracts/gateway and HTTP/public gateway generators, nodes/deployments/modelpreparations, [distribution frontend](../../../../../web/README.md), [M07](../../../../../docs/milestones/m07-gateway) and gateway deployment/config.

Focused native check: go -C services/control test ./internal/modules/distribution. Actual admission, SQL concurrency/cutover and Rust/backend streaming faults are separate profiles; neither a synthetic gateway nor an alias row establishes real model serving.

New gateway identities select `Config.MachineOrigin()` only after the existing
idempotency replay branch. The explicit machine HTTPS origin, or the retained
shared secure Admin origin, supplies `control_origin_sha256` for both SQL
dialects. Browser-only HTTP has no usable machine origin and refuses a new
identity with `CONFIG_MACHINE_TRANSPORT_REQUIRED`, without a credential, row or
audit publication. This is the only configuration code passed through the
repository error boundary; unrelated storage diagnostics remain redacted.

An existing intent keeps its original hash and never returns its one-time secret
again. Recovery continues comparing a fence with the row's stored hash, even
after browser/machine configuration changes. No migration, backfill or receipt
rewrite is part of this selection change. Follow the
[machine-ingress contract](../../../../../docs/engineering/deployment-transport/machine-ingress.md):
drain and reconcile the old scope, retain/fence/revoke it, and provision a new
identity for a changed machine origin. Agent identity and Rust spool ownership
must not be edited in place.

`TestGatewayMachineOriginSelectionPreservesAdmissionOrder` checks refusal before
configuration access without a database or test double.
`TestDistributionMachineOriginBindingSurvivesTransportChange` requires both
`repository_integration` and `machine_ingress_integration`, with the existing
explicit isolated repository fixture for the selected PostgreSQL/MySQL engine.
It creates one fixture and exercises real SQL, sessions, signed synthetic License,
atomic provisioning, exact replay, historical fence and canonical outcome
readback. Its node/GPU/model facts and operator fence are explicitly synthetic;
it does not execute a gateway, inference engine or machine TLS listener. Source
presence is not an execution result; retain the selected engine's actual receipt
and leave unavailable engine/target qualification pending.

The native DM first chain adds explicit gateway/alias/key/admission/outcome/recovery SQL and exact native history cursors in [native_sql.go](native_sql.go). The [owner contract](../models/native-chain.md) records shared schema/session dependencies and acceptance. Native cutovers, rollback, target-history listing and monitoring remain outside this slice. No target database or real inference result is claimed.

The native schema source for this first path is the single `models_v2` contribution in `platform/database/dameng/migrate/contribution_models.go`. Central registration, shared prerequisites and fresh/upgrade DM execution remain separate gates; approved-base/publication-disable restrictions are deliberate.

The explicit cumulative `gateway` profile composes this owner with M01-M06 and entry12 rather than replaying platform000007. Its route allowlists exclude later cutovers/targets and its catalog constrains snapshots to approved-base publish/disable. Restored authority invalidation participates in the minimal coordinated transaction; unknown capacity remains held.

Human alias list and detail use AliasReadView, keeping optional capability
metadata out of mutation responses and machine bindings. The composition-time
PublishAliasCapabilities switch defaults to false while the public read schema
and generated clients are pending. Enable it only for the verified full-platform
PostgreSQL composition; earlier cumulative profiles keep it off. An enabled
read selects the alias and exact
verified target in one authorized transaction; unavailable evidence is omitted,
and query failures return the existing storage problem. See the
[capability contract and activation boundary](../../../../../docs/research/gateway-alias-capabilities.md#registered-read-composition).
