# Product assistant audit attribution

AGT-107 records the signed-in user as the subject of an assistant action and
the product assistant as the actor. The existing user permissions remain the
authority for execution. Actor attribution gives audit evidence of delegation
and does not grant access to an operation.

## References and dependencies

- [RFC 8693 actor claims](https://www.rfc-editor.org/rfc/rfc8693.html#section-4.1)
  distinguish an actor from the subject on whose behalf the actor acts. Use
  that distinction in audit without introducing token exchange or service
  account credentials.
- [Kubernetes audit events](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/)
  carry request correlation, authenticated identity, resource and outcome.
  [Kubernetes audit levels](https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/)
  distinguish request metadata from bodies.
- [OWASP application logging guidance](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
  describes interaction identifiers and data that must stay out of event logs.

The existing Go standard library uses a [BSD-style license](https://go.dev/LICENSE)
and PostgreSQL uses the [PostgreSQL License](https://www.postgresql.org/about/licence/).
Both permit commercial use, redistribution with notices and offline operation.
The proposed connection needs no new package, external service, model or dataset.

## Current boundary

The [audit writer](../../services/control/internal/modules/audit/repository.go)
accepts an actor user and a subject user. In the
[identity schema](../../services/control/migrations/postgresql/000001_identity.up.sql)
both fields reference real users. A null actor cannot distinguish the assistant
from other system activity. The writer also uses the installation's default
workspace instead of the authorized operation's actual workspace and has no
typed actor metadata parameter. The
[audit list projection](../../services/control/internal/modules/identity/lists.go)
and [OpenAPI audit event](../../api/openapi/v1/identity/openapi.json) expose
human actor fields. They cannot project or filter an assistant actor.

Using the signed-in user's ID as both actor and subject would erase delegation.
Creating an assistant user would confuse an executor with an authorized person.
Encoding actor identity in an action prefix or free-text reason would leave
identity inference to readers. Direct SQL from productagent could use existing
metadata but would bypass the audit owner's typed persistence and read contract.
These paths do not satisfy AGT-107.

## Smallest handoff

1. The audit owner provides a transaction-borrowing append operation with a
   server-assigned actor kind and stable actor identifier, the verified user
   subject, authorized tenant and workspace, audience, request ID, fixed action
   or tool ID and bounded outcome. Preserve the existing human write callers.
   The assistant actor comes from the server dispatch binding. No request,
   model output or tool argument supplies it.
2. The audit and identity owners expose that actor in the safe audit list and
   export. The existing export carries a metadata digest. An actor displayed
   beside that digest needs an independently verifiable binding. Prefer
   including the typed actor fields in the next hash projection so the export
   can keep other metadata redacted. Introduce a format boundary as in
   [audit chain v2](../../services/control/migrations/postgresql/000054_audit_chain_scope.up.sql)
   and update the [offline verifier](../../tools/audit-verify/verify.go).
   Preserve historical records and checkpoints.
3. The authenticated HTTP owner threads its server-generated request ID and
   current identity into
   [productagent Run](../../services/control/internal/modules/productagent/service.go).
   Navigation appends the event in the existing session recheck transaction
   before returning the route. Application and conversation reads append it
   in the [application owner transaction](../../services/control/internal/modules/applications/repository.go)
   after authorization and projection, using the resolved workspace. Expose
   a narrow internal read-completion seam when the real writer is ready.
   The ordinary application routes keep their current read behavior.
4. Each later confirmed write appends the event inside the owning mutation
   transaction with the exact action and idempotency record. A failed append
   cannot return a successful action or disclose a read result. After rollback,
   an authenticated failure can be recorded with a bounded error code and
   the caller's verified scope. Refused or unknown commands must not leak
   another tenant's identifiers or record the submitted command text.

Record only stable identifiers and outcomes. Keep prompts, aliases as typed,
cookies, tokens, confirmation data, cursors and result bodies out of audit.
An audited navigation result records the server's response to the command.
Whether the browser opened the page is a separate event.

## Acceptance

Extend the existing disconnected PostgreSQL command fixture to invoke actual
navigation and scoped application or conversation reads using real sessions.
Include a non-default workspace. Read the resulting events through the shared
audit projection and verify the assistant actor, distinct user subject, actual
workspace, request correlation and absence of command text and result data.
Exercise a revoked session, a foreign workspace, audit insertion failure and
uncertain commit. Add a confirmed mutation case when a production write
adapter exists and verify the effect and audit event commit together. Verify
new and historical events with the offline verifier.

The existing writer and projection cannot establish these claims. AGT-107
stays open and no event is emitted under a substitute human actor.
