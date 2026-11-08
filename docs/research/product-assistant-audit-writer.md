# AUD-101 transactional product assistant writer

[RFC 8693 section 4.1](https://www.rfc-editor.org/rfc/rfc8693.html#section-4.1)
distinguishes a delegated actor from the subject. Kubernetes separates
authenticated identity, request correlation and operation outcome in its
[audit event](https://kubernetes.io/docs/reference/config-api/apiserver-audit.v1/).
The [OWASP logging guidance](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html)
calls for correlation without recording session tokens, secrets or input bodies.
Use these patterns without introducing a token exchange or a second credential.

Provide an additive PostgreSQL append method in the audit package. The method
binds the first-party product assistant as actor and accepts the authenticated
user as subject. The operation owner passes the server request ID, resolved
tenant and workspace, authenticated audience, registered tool ID and one of the
existing audit outcomes. The method accepts no actor value, arbitrary metadata,
prompt, free-text reason or tool arguments. Navigation and list commands need
only the workspace and tool identifier for this first path.

Use one guarded [INSERT SELECT](https://www.postgresql.org/docs/18/sql-insert.html)
on the borrowed transaction. The selected workspace must belong to the stated
active installation tenant. The subject must be an active user. Portal events
require membership in the active workspace, and Admin events require a current
platform administrator. This check catches misattribution at the persistence
boundary. The caller continues to authorize the actual operation and hold the
session and workspace fences from its existing flow. The append adds no row
locks ahead of the normal audit append. Under
[Read Committed](https://www.postgresql.org/docs/18/transaction-iso.html)
the guard checks committed state at the start of the statement. Return a scope
denial for zero affected rows, and propagate SQL errors so the transaction
owner rolls back. The caller owns retries and idempotency of the underlying
operation. The writer must not independently retry or commit an uncertain write.

Set the actor kind and stable ID in the existing audit metadata. V1 and V2
already hash that metadata's canonical commitment. This keeps old chain bytes,
exports, checkpoints and user writers compatible. Raw storage identifies the
assistant separately from the user subject. The existing offline JSONL proves
the metadata commitment and redacts its contents. A readable, independently
verifiable actor projection still needs the audit read and export owners.
Do not put the assistant in the human actor column or infer its identity from
the action prefix. A schema and hash-format revision would be larger than
the initial write boundary and would need coordinated projection and migration
work. PostgreSQL is the only chain-backed path for this method. Other dialects
must fail explicitly.

No new dependencies are used. Go uses its
[BSD-style license](https://go.dev/LICENSE), the existing UUID package uses
[BSD-3-Clause](https://github.com/google/uuid/blob/master/LICENSE), pgx in
the transaction adapter uses [MIT](https://github.com/jackc/pgx/blob/master/LICENSE),
and PostgreSQL uses the [PostgreSQL License](https://www.postgresql.org/about/licence/).
These permit commercial use, offline use and redistribution with the required
notices. No model, dataset or runtime download is needed.

Verify on disconnected PostgreSQL with an actual borrowed transaction. Commit
an assistant event in a non-default workspace, verify the distinct subject
and request correlation, and retain legacy human attribution. Reject a workspace
with a mismatched tenant, a foreign tenant even with membership, a nonmember
and an ineligible subject or entrance. Check that a returned append error rolls
back a business write and that caller rollback removes the event and tail
advance. Export a chain spanning V1 and V2 through a new assistant event. The
offline verifier must accept it with the expected checkpoint and reject actor
metadata tampering. Preserve the existing redaction. AUD-101 stays open
pending the service-account identity and the product assistant's use of the
writer and read projection.
