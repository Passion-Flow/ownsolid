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

Add `/export` to the cell-scoped Admin or Portal request list path to
download one authorized page as `gateway-usage.csv`. The request takes the
same `api_key_id`, `limit` and `cursor` query parameters. To continue,
use the cursor from the corresponding JSON history page with the same
authorization and filter. Both reads observe live data.

Each CSV row counts one admitted request. Blank elapsed time means no
reported outcome. Blank token cells mean unknown usage. A reported zero
is written as 0. The file excludes key identifiers and secrets, request
content and actor or deployment metadata.

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

The control service's `PreviewCutover` uses the same authorization, expected
revision, drain and ready-target checks as `Cutover`. It returns the current
and proposed deployment revision and pause state without writing. Apply
rechecks the expected revision and current readiness in its own
transaction. The public preview route and Console flow are pending.

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
