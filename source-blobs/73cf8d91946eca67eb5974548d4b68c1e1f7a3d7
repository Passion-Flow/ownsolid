# Isolate connector sign-in tests from session authority

Status: accepted for the staged PostgreSQL IAM-118 server component.

## Context

An administrator must authenticate with an upstream provider to inspect the
identity it returns without signing that identity into ownsolid. Ordinary SSO
flows have durable binding and session-issuance authority. Reusing a successful
login and revoking it afterward would temporarily grant access.

## Decision

Keep interactive connector tests in their own scoped state machine. Bind each
test to the current platform Admin session and a connector revision. Reuse the
existing provider transport, S256 PKCE and authenticated identity validation.
Store the start's random secrets only as an AES-GCM bundle with a distinct
associated-data purpose. Claim once before exchange and recheck authority at
completion. A terminal test stores only a minimized identity result and audit.
It never creates a binding, issues a session or rotates a browser login cookie.

Compose the server owner and its HTTP adapter explicitly until the migration,
contract and UI owners admit the complete installed path. Existing connector
checks and login/link flow constraints retain their current contracts. The
candidate's acceptance and delivery boundaries are described in
[the research note](../research/sso-interactive-connector-tests.md).

## Consequences

Start retries recover the same pending intent; consumed or uncertain exchanges
require a fresh test. Revoked actors, stale connectors and wrong browser proofs
cannot disclose results. The new diagnostic table requires explicit backup,
restore, grant and cleanup admission before production selection. This is an
additional state family inside the existing SSO owner, not a new service.
