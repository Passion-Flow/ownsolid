# Interactive connector sign-in tests (IAM-118)

## Upstream behavior and constraints

[Auth0 connection testing](https://auth0.com/docs/authenticate/identity-providers/test-connections)
starts an actual provider authentication and displays the returned user profile.
[Cloudflare One](https://developers.cloudflare.com/cloudflare-one/integrations/identity-providers/#test-idps-in-cloudflare-one)
opens a provider test window and reports success or a failure explanation.
The current ownsolid connection check only tests confidential credentials and
registered callbacks with an invalid authorization code. Keep that operation;
it does not establish which upstream identity actually authenticated.

[RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html#section-2.1)
requires exact registered callbacks and protects redirect flows against code
injection, mix-up and CSRF. [PKCE](https://www.rfc-editor.org/rfc/rfc7636.html)
binds the authorization code to the initiating exchange. Use the existing
approved provider transport, TLS trust, S256 challenge, bounded exchange slots,
introspection and stable-subject validation. Provider access tokens, codes and
raw profile claims must not be stored or sent to the browser.

## Decision

Use a separate PostgreSQL test record, owned by the existing SSO module. The
ordinary `sso_flows` schema admits only login/link and its succeeded rows confer
session-issuance authority. A diagnostic test must have no such authority.
Do not add a mode to that table, simulate a login and revoke it afterward, or
create a binding for the test identity. No call to `IssueSessionWithin` belongs
in this path. The new record is bound to the installation tenant resolved by
the server, the connector's revision/auth version, and the initiating Admin
user, session and authentication epoch. A disabled connector can be tested.

An explicitly composed `ConnectorSignInTests` owner validates its PostgreSQL
storage before use. Its candidate DDL remains beside its tests until the
shared migration/catalog owner admits it with the HTTP contract and UI.
The existing default handler keeps the unpublished routes closed. An opt-in
handler permits real HTTPS qualification and supplies a concrete integration
surface to the API and Web owners. This is a functional server candidate;
IAM-118 remains open until the installed UI path and formal schema admission
are complete.

Start requires a current platform Admin, CSRF at the existing transport
boundary, an idempotency key, the observed connector revision and a reason.
The first accepted start generates random state, browser proof and PKCE
verifier. Seal their bounded bundle with the existing AES-GCM keyring under a
new tenant/connector/test/purpose associated-data domain. Store only the state
digest and encrypted bundle. A pending exact-key replay decrypts and returns
the same start; a changed request/session conflicts. Claim clears the bundle
before provider I/O. Terminal and claimed starts require a new key.

The callback is an exact server-generated Admin URL, separate from the login
callback, with a separate browser-proof MAC domain. It requires the same live
Admin session and consumes pending state once under the existing user, sorted
source/target connector, session and installation fences. Code exchange runs
outside SQL. Completion rechecks authority and the connector version before
persisting only the stable provider subject and bounded display identifiers.
Results are readable only in that same live Admin session. A safe first-party
redirect carries a test ID, never a code, token or profile.

Start, terminal success/failure and bounded cleanup use existing audit actions
inside their state transactions. Audit failure rolls back the corresponding
state; an uncertain claim or exchange is never repeated. Recovery starts a new
test with a fresh key/state. Missing key material, provider rejection, denial,
timeout, expired intent, stale connector, revoked session and tenant mismatch
all fail without creating or rotating any ownsolid session or binding.

Use existing typed SSO operation, flow lifetime, cleanup and retention policy.
Pending diagnostic intents have an installation cap using
`MaximumPendingFlows`, separate from ordinary login intents. Their provider
requests share the existing global exchange semaphore. Lifecycle composition
must join the diagnostic owner's cleanup; it starts no detached worker.

## Dependencies and licensing

No product dependency, model or dataset is added. Existing Go standard-library
code and Google UUID are BSD-3-Clause, pgx is MIT, and PostgreSQL uses the
PostgreSQL License. Commercial use, redistribution and offline operation are
permitted with the existing notices. The optional actual-provider fixture uses
pinned Gitea 28.1.0 under its [MIT license](https://github.com/go-gitea/gitea/blob/v28.1.0/LICENSE).
Research describes the upstream behavior; no vendor implementation is copied.

## Acceptance

- Real PostgreSQL and verified HTTPS exercise start, exact idempotent replay,
  provider authentication, persisted result and failure followed by a new test.
- Every operation refuses foreign tenant/connector/test IDs, another Admin
  session, Portal, non-admin, missing CSRF and caller-supplied scope fields.
- Wrong browser/state, expired intent, parallel callback replay, changed
  connector, actor revocation, cancellation and audit refusal have durable
  postimage assertions. At most one claim reaches the provider.
- Session and binding preimages remain unchanged on success and failure. A
  diagnostic test ID cannot be used as a session's SSO source flow.
- Codes, provider tokens, secret bundles, ciphertext and raw claims are absent
  from responses, redirect URLs, persisted result fields and audit events.
- A real Gitea exchange validates a synthetic upstream identity without a local
  binding. Run native Linux work only after the shared DEL installation window;
  use owned isolated fixtures and retain exact input SHA and raw exit codes.
- The complete item also needs the API owner's published contract, the Web
  owner's bilingual screen, admitted migration/restore/grants and an installed
  disconnected browser run. No candidate-only roadmap tick is allowed.
