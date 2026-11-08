# AUD-101 verified assistant actor disclosure

The operator needs to see the assistant actor and user subject in an offline
export without revealing command text, results or arbitrary audit metadata.
[RFC 8693 section 4.1](https://www.rfc-editor.org/rfc/rfc8693.html#section-4.1)
distinguishes the actor from the subject. [RFC 9901 section 7](https://www.rfc-editor.org/rfc/rfc9901.html#section-7)
illustrates verifying disclosed values against an existing digest commitment.
Use the existing audit commitment and canonical encoding.

## Evidence from the existing writer

A disposable, disconnected PostgreSQL 18.6 fixture dispatched navigation and an
application list through the actual product assistant with an authenticated
non-default workspace. The export also contained a pre-upgrade V1 human event
and a V2 session audit event. Both assistant records committed to the exact
two-field object written by `WriteProductAssistantWithin`. An independent
Python implementation of the existing binary metadata encoding reproduced
their SHA-256 values. The existing verifier accepted the complete export with
the saved tail hash. The subject was already in the per-record hash projection.

## Disclosure and acceptance

Keep the database schema, V1 and V2 hash domains, field ordering and
checkpoints. The standard operator query continues to return the existing
JSONL. An explicit session setting enables a disclosure of `actor_kind` and
`actor_id` only for a V2 event with a user subject, no human actor and metadata
exactly equal to the built-in assistant object. Reuse the single export query
to preserve projection and ordering. The two disclosed fields open that
whole-object commitment. An object with additional keys is outside this proof,
so the exporter keeps its metadata redacted.

The verifier accepts the existing format and the optional two-field disclosure.
For a disclosure it checks the fixed actor kind and ID, the subject and human
actor shape, and recomputes the canonical metadata digest. It still checks the
original record hash, chain order and independently held checkpoint. Provide a
verification option that requires the pair for each eligible record, so removal
of both fields is detectable when an auditor expects the disclosed export.
Ordinary verification still accepts older redacted files.

Do not derive an actor from the action prefix or place an unverifiable
display field beside the digest. The existing digest covers the full metadata
object, so revealing just two keys of a larger object would not provide an
independent proof. A general actor projection or a different metadata shape
would need a coordinated versioned projection. For the public audit list,
the identity owner can extend the
[repository projection](../../services/control/internal/modules/identity/lists.go)
and the [AuditEvent contract](../../api/openapi/v1/identity/openapi.json).
Keep the existing human actor and user subject fields. Project the typed
assistant kind and ID only on the exact stored metadata and subject shape
used by this export. Preserve the existing audit query authorization, tenant
and workspace filters, and redaction. Changes to public search or filters
belong to that contract and its consumers. AUD-101 stays open.

No new dependency is needed. Go's [BSD-style license](https://go.dev/LICENSE)
and the [PostgreSQL License](https://www.postgresql.org/about/licence/) permit
commercial use, offline use and redistribution with their required notices.
The prototype uses Python's standard library under the
[PSF license](https://docs.python.org/3/license.html). It is absent from
the shipped tool. No model or dataset is needed.

In a disconnected fixture, verify the original and disclosed mixed chains
against the same checkpoint. Verify actor kind, actor ID and user subject on
actual command events. Reject edits to the actor kind, actor ID, user subject
or commitment, and reject a one-field disclosure or one attached to an unrelated
event. Check that strict disclosure verification detects removed fields and
that ordinary verification accepts historical exports. Verify that unrelated
metadata remains redacted even when it contains the two assistant keys.
