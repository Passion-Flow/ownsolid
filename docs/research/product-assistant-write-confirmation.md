# Product assistant write confirmation

AGT-105 needs a server-rendered plan and a human confirmation before any mutation.
A signed plan provides integrity. Authorization and one-time execution also need
live checks in the resource owner's transaction.

- [OWASP transaction authorization](https://cheatsheetseries.owasp.org/cheatsheets/Transaction_Authorization_Cheat_Sheet.html)
  calls for a distinct authorization step, meaningful transaction details,
  ordered state transitions and a final control gate tied to execution.
- [Terraform saved plans](https://developer.hashicorp.com/terraform/cli/commands/apply)
  give preview and execution an exact operation set. ownsolid additionally
  requires the same authenticated browser session to submit an explicit confirmation.
- [Go HMAC](https://pkg.go.dev/crypto/hmac) provides a keyed message authenticator
  and constant-time comparison. [PostgreSQL conflict handling](https://www.postgresql.org/docs/18/sql-insert.html)
  supports reconciliation against a unique durable intent under concurrency.

## Proposed execution boundary

The server authorizes and canonicalizes a proposed typed action before signing.
Use a versioned envelope that binds the installation, subject, session, audience,
tenant and workspace where applicable, tool ID, exact canonical arguments, resource
revision, nonce and expiry. The preview is rendered from those same typed arguments
and never includes credentials. Use a file-backed, purpose-specific signing key.
The plan and confirmation credentials stay outside the model's context.

A dedicated authenticated browser confirmation accepts the signed plan with no
replacement arguments. It rechecks current authority, target revision and action
admission. The resource owner consumes or reconciles the nonce, records the user
subject and assistant actor, and applies the exact mutation atomically. An
uncertain commit returns an actionable retry of the same plan. Ordinary tools
cannot invoke confirmation, and disabled or revoked authority fails closed.

A stateless signature check would permit replay. A process-local nonce cache would
lose the same guarantee on restart or across replicas. A generic write proxy or
separate audit transaction would bypass the owner's final permission or commit
boundary. The shared [scoped assistant writer](product-assistant-audit-writer.md)
can record delegated attribution in PostgreSQL. A real confirmed mutation
adapter must borrow it in the resource transaction before writes can be enabled.

This design uses the [BSD-licensed Go standard library](https://go.dev/LICENSE)
and the existing [PostgreSQL License](https://www.postgresql.org/about/licence/)
engine. Both permit commercial and offline use and redistribution with required
notices. It needs no additional dependency, model or dataset.

Acceptance requires zero effects before confirmation, one effect and one audit
record after confirmation, and safe behavior for tampering, expiry, changed
revision, wrong session or audience, membership revocation, parallel confirmations,
process restart and lost commit acknowledgements. Test from a browser with no
model running, including refusal of a confirmation presented as command text.
