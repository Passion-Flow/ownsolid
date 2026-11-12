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

An explicitly composed `ConnectorSignInTests` owner checks its required PostgreSQL
projection before use. This is not catalog or grant admission. Its candidate DDL remains beside its tests until the
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

## Integration handoff

The exact candidate schema is
[`connector_sign_in_tests.up.sql`](../../services/control/internal/modules/sso/testdata/connector_sign_in_tests.up.sql),
with its matching down file beside it. It adds one global identity table,
`sso_connector_sign_in_tests`, and a transition function/trigger. The records
belong to the server-selected installation tenant; they have no site or cell
columns. Partial tenant/expiry and tenant/finished-time indexes bound capacity
and retention scans. The runtime needs SELECT, INSERT, UPDATE and DELETE on
this table; the migration owner retains DDL authority. It must admit the
global table inventory, catalog fingerprints, runtime grants, fresh/upgrade
histories and backup/restore together. No numbered migration is supplied here.

Pending state, browser proof and verifier share one protected keyring bundle;
the associated data includes tenant, connector, test ID and the new
`connector_test_secrets` purpose. Formal startup/key inventory and restore
selection must include this state family. Restoring must invalidate pending
and claimed tests and erase their bundle without reviving any session. Retain
the configured authentication HMAC and protected SSO keyring with the existing
backup rules. Those production integrations are pending; the projection check
does not establish them.

After constructing the selected SSO owner and its identity SourceGuard,
construct `NewConnectorSignInTests` and pass the result to
`identityHandler.SSO.ConnectorTests`. Join its `Run` method to the existing
maintenance context and wait for it on shutdown. A default identity handler
does not mount the new routes. Keep ordinary login/link callbacks registered
and add this exact connector-specific diagnostic callback at the provider:

```text
<AdminOrigin>/api/v1/admin/sso/connectors/<connector_id>/sign-in-tests/callback
```

The proposed start body is `{"revision": <observed revision>, "reason": "..."}`
with the existing `Idempotency-Key`, Origin/CSRF and Admin cookie requirements.
The successful data envelope contains `id`, `authorization_url` and
`expires_at`. The browser proof is only a secure, HttpOnly Admin SSO-flow
cookie. It is not a JSON field. Navigate to the returned URL in the initiating
browser; the fixed first-party result is `/sso?test_id=<id>`. The result GET
under that same connector namespace returns `id`, `connector_id`, `state`,
nullable `subject`, `login`, `display_name`, `error_code`, `finished_at`, and
`expires_at`. Only `succeeded` contains identity fields. A result is tied to
the initiating live Admin session and the inspected connector revision, even
when another session belongs to the same user.

The screen must state that the identity is diagnostic and grants no ownsolid
access. Render provider display values as text. Do not offer account linking
or reuse an email claim as local identity. Fixed error redirects carry an
existing public code; claimed, expired, denied or otherwise terminal attempts
need a fresh start with a new idempotency key. Pending exact-key retries retain
the original URL and proof. One browser has one active SSO-flow cookie, shared
with the existing login/link path. Display actionable retry, session-expired
and connector-changed states in both product languages. A rendered offline
installation must verify these interactions before IAM-118 is ticked.

## Qualification boundary

The schema 58 predecessor passed nine diagnostic PostgreSQL HTTPS cases and
both actual Gitea flows: the new isolated diagnostic identity read and the
existing recovery/device-revocation regression. Fixtures used PostgreSQL 18.6,
pgvector 0.8.7 and pinned Gitea 28.1.0 in network-none Linux namespaces on the
workstation. The Gitea amd64 image used workstation emulation. These are not
native Linux amd64 installation or rendered IAM-118 UI acceptance.

The schema 59 repository run passed `make check` and `make build`. Chromium
passed 1,075 Console and 389 Portal cases, with 37 installation-dependent
cases skipped. `make test` then stopped in the unchanged delivery fixture
`TestRestorePutsBackAMissingModelBeforeTheDatabaseChanges`: its model helper
creates a file with mode `0444` through `os.WriteFile`, but inherited
`umask 077` makes the original file `0400`. Restore preserves that recorded
mode; the test expects `0444`. Delivery owns making the fixture's permissions
deterministic. This run is not a green repository test gate.

The retained input is `dd3efa99` plus its captured candidate patch. Durable
evidence is under `iam118-20261112.2o1O0L6A/gate-source59`, including the exact
input, commands, raw logs and exit codes. The owned scratch volume was detached.
Final schema 59 native qualification includes stronger known-foreign-record
postimage checks and remains pending; no full-gate or product-completion claim
is made from the predecessor's passing cases.
