# AUD-108 diagnostic read access

## Sources and chosen boundary

[OWASP logging guidance](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
places event capture with the application that knows the actor and operation,
requires protection and monitoring of log reads, and excludes tokens, passwords
and keys from log payloads. [Replicated Troubleshoot redactors](https://troubleshoot.sh/docs/redact/redactors/)
separate collection and disclosure rules. ownsolid already uses typed support
projections rather than copying arbitrary configuration or subprocess output.
Keep that existing approach; access accounting and redaction are separate
requirements. This is a source inspection and integration specification, not a
new runtime acceptance result.

## Existing main paths

| Path and owner | Present controls | Remaining AUD-108 work |
| --- | --- | --- |
| [License Diagnostics](../../services/control/internal/modules/licensing/diagnostics.go), `licensing.Service.Diagnostics` | Current Admin authorization, bounded typed state JSON, `license.diagnostics_exported` through `audit.WriteWithin` in the same transaction, and an empty download on any transaction error | Preserve this implemented path. Its read consumer is outside the SCI-owned audit directory. |
| [Admin audit HTTP projection](../../services/control/internal/modules/identity/http/audit_list.go), `Handler.listAuditEvents`, and [listForActor](../../services/control/internal/modules/identity/lists.go) | Session/Admin recheck, bounded list plan and optional verified assistant projection. Workspace reads use `ListWorkspaceAuditForActor` and its installation-tenant workspace check | Add access accounting at the identity read transaction before releasing the page. The inspected transaction currently rechecks and reads without appending an access event. Preserve the current public representation and filters. |
| [Local support bundle](../../deploy/offline/internal/install/support.go), `SupportBundle` and `publishSupportBundle` | Typed configuration/state projection, a private archive, fixed collection issues and collection while Control is down | Admit and durably record the diagnostic access through the installer's existing operator mechanism before protected collection. The current path collects and publishes without such an access record. |
| [Managed support bundle](../../deploy/offline/internal/install/support_cluster.go), `ClusterSupportBundle` and `collectManagementSupport` | Protected independent credentials, bounded named HTTPS reads, response scope checks, fixed issues and typed projection | The installer must account for this collection; Control and Kubernetes owners retain authorization and source access accounting. A collector credential file does not identify a browser actor. |
| [Audit package](../../services/control/internal/modules/audit/repository.go), `WriteWithin` and `WriteAnonymousWithin` | Same-transaction append and exactly-one-row confirmation for the owning SQL dialect | This package owns no diagnostic reader, HTTP handler or collector lifecycle. An additional unused writer or local access ledger would not account for any of the reads above. |

The existing `TestM20LicenseUnknownHTTPAndExplicitRecovery` in
[the license HTTP repository tests](../../services/control/internal/modules/identity/http/license_repository_integration_test.go)
includes diagnostic COMMIT-not-forwarded and suppressed-ACK cases. It requires
no successful diagnostic payload on uncertainty and an additional access event
on an explicit later download. The existing license integration tests also
cover the ordinary user's refusal and a revoked Admin session. These are
existing test sources; this review does not claim a new execution of them.

[Support operation history](../../deploy/offline/internal/install/support_operations.go)
is a bounded projection of existing backup/restore completion records. It is
not an access receipt for creating a support bundle. The current CLI dispatch
calls the support functions directly. Existing redaction tests and the
[retained disconnected support evidence](support-diagnostics.md) remain valid
for their recorded scope and do not prove diagnostic access accounting.

## Required owner integration

Identity must place its read-access event in `listForActor`'s current
transaction after authorization and before the result can leave the service.
Use a fixed action and only the necessary server-derived scope, actor,
request ID and bounded read-selection metadata. Do not copy result rows,
reason text, credentials, raw query strings or driver errors into that event.
Keep unauthenticated/denied attempts distinct from completed diagnostic reads.
A failed append or unknown commit must release no sensitive response.

`audit.WriteWithin` currently resolves the installation tenant and default
workspace itself. It does not accept a requested workspace. An owner must not
use it to claim arbitrary workspace-read coverage. Exact workspace accounting
needs the real consumer's server-resolved transaction and scoped append
contract. `WriteProductAssistantWithin` is specifically an assistant actor
contract, not a diagnostic-user replacement. A new scoped writer should land
with that actual consumer rather than exist without a call site.

The installer must retain the Control-down collection path. Extend its existing
protected operator operation mechanism at `SupportBundle` and
`ClusterSupportBundle`, with a durable admitted-read record before collection
and a terminal outcome bound to the produced archive. The current
[`operation.record`](../../deploy/offline/internal/install/backup.go) is a
post-operation warning-only append, with no returned error or durability sync.
Calling it unchanged cannot enforce the required pre-read receipt. The delivery
owner must provide a failure-propagating durable admission operation. Use the actual local
operator identity; do not fabricate a SQL event or browser session. The owner
must define explicit reconciliation into the existing audit trail when Control
returns, including duplicate/retry behavior. A receipt write failure must stop
protected collection. No SCI-owned side ledger is selected.

For each consumer, first verify current authorization and resource scope, then
project the permitted fields before publication. Preserve private file handling,
size/time limits, cancellation and partial-source collection issues. An explicit
retry is a new read attempt with its own correlation, not evidence that an
uncertain prior access never occurred.

## Acceptance and unblock conditions

Exercise the real diagnostic download or collector, not a direct append probe.
Verify redaction with sentinel credentials and private source values; audit
failure and unknown commit must emit no protected response. Cover revoked
sessions, denied callers, foreign tenant/workspace selections, bounded selection
metadata, cancellation, concurrent reads, explicit retry and restart readback.
For the installer, include Control and Docker down, an unwritable access receipt,
a failed archive write and reconciliation after recovery. Check both the private
archive and existing operation/audit records independently.

No dependency, model or dataset is introduced. Existing Go standard-library
BSD-3-Clause terms, offline use and redistribution notices remain unchanged;
OWASP and Troubleshoot are references, not embedded code. The audit module has
no missing owned read consumer to implement within the current SCI allocation.
Identity owns the Admin audit read integration; delivery owns both support
collection entry points and the offline receipt/reconciliation. Those owner
changes and their actual acceptance unblock AUD-108. Keep it unticked.
