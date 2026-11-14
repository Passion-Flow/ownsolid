# Session management (IAM-111)

[NIST SP 800-63B-4 session guidance](https://pages.nist.gov/800-63-4/sp800-63b/session/)
separates the idle limit, overall limit and reauthentication.
[Keycloak's session management](https://www.keycloak.org/docs/latest/server_admin/index.html#_user_sessions)
lets a user or administrator see and end active sessions.
[OWASP session guidance](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
requires server-side expiry and invalidation.

Keep the existing server-enforced idle and absolute deadlines. A browser poll
can observe the deadline without extending it. Use the live session rows for
a bounded device list and an idempotent revocation. Treat displayed device
labels as advisory and avoid returning tokens, token hashes or raw unbounded
User-Agent strings. Resolve the account and tenant scope from server authority.
Fence the target user and session in the same lock order as existing
account and connector mutations. Record a single revocation audit event in the
same transaction as the authority change. A second control replica must see
the revocation on its next session check. A stale in-flight mutation must fail
its transactional actor recheck.

Client-only logout leaves captured credentials usable. A cache or a new
distributed revocation ledger adds stale authority when the serving path
already reads durable session state. Upstream provider logout is a distinct
protocol responsibility, as shown by
[OIDC Back-Channel Logout](https://openid.net/specs/openid-connect-backchannel-1_0.html).

Use the existing Go standard library (BSD-3-Clause), pgx (MIT) and PostgreSQL
(PostgreSQL License). The product can redistribute them offline with their
notices. No new library, dataset or model is needed.

Acceptance requires a real device list with independent Admin and Portal
sessions, explicit targeted revocation, and a second control replica
rejecting the revoked session. Exercise pagination, expiry, duplicate
revocation, reset and account disable races, missing Origin or CSRF,
cross-tenant IDs and cross-audience cookies. A test must distinguish revoking
one device from ending every session after a credential change.

The status endpoint and browser idle warning already exist. Publishing the
device list and targeted sign-out also requires the public contracts and browser
screens owned by DEV and Web.

## Self-service browser session slice

[GitLab Active Sessions](https://docs.gitlab.com/user/profile/active_sessions/)
shows signed-in devices and supports ending another session. It reserves the
current session for the ordinary sign-out flow. Use the same sign-out semantics
if a client explicitly requests deletion of its current session, with the
existing audience-specific cookie cleanup after the database commits. The
[HTTP semantics for DELETE](https://www.rfc-editor.org/rfc/rfc9110.html#name-delete)
allow a bodyless 204 after removing the target's live authority and define
DELETE as idempotent.

Propose `GET /api/v1/me/sessions` and
`DELETE /api/v1/me/sessions/{session_id}` for DEV and Web. The list has
`items` and `next_cursor`, with existing configured page bounds and opaque
`limit`/`cursor` pagination. Items include `id`, `current`, `created_at`,
`last_seen_at`, `absolute_expires_at` and `authentication_method` (either
`password` or `sso`). The stored session is the browser sign-in record. This
slice has no physical device label because existing sessions have no durable
device metadata. Do not derive a device from the current request or collect
fingerprints, tokens, client addresses or raw user-agent strings.

Account and audience come from the current session. The list filters out
revoked, idle-expired, absolute-expired and old-epoch rows. Its immutable seek
keys and cursor scope are the creation time and session UUID, bound to the
account and entrance. A retained SSO session still has to pass the existing
source authority check when used. Other-entrance sessions of the same account,
and foreign accounts, are outside both operations. The no-touch read header
continues to work when the list is polled.

A bodyless DELETE uses existing Origin and CSRF checks and a live actor
recheck. The same transaction commits the session's revocation and one
`auth.logout` audit event whose target is that session. The user lock comes
before either session lock. Retries on an already ended owned session return
204 without a second audit. A foreign or unknown session UUID returns the
same 404. Once the current session is ended, its own credential cannot retry
the operation. This is account security and remains available without an
active license. The existing status and logout contracts stay available.

Use the existing session rows and account authority, with no new dependencies,
migration or distributed cache. A per-device flag in the browser would leave
copied credentials usable. A list spanning both entrances would make a Portal
session an authority over Admin sessions. Hard-deleting rows would lose the
revocation outcome needed for a safe retry and the existing authentication
history. User-Agent parsing would add unverified labels and leave old devices
indistinguishable.

Validate both audiences through real TLS and PostgreSQL, including a second
independent control handler and pool, same-account cookie replay across hosts,
foreign account/session and cursor rejection, expired and rotated row
exclusion, concurrent revoke and idempotent retry, CSRF/Origin refusal,
cookie cleanup for the current session and a pre-captured actor losing its
transactional authority. The [identity contract](../../api/openapi/v1/identity/openapi.json)
now describes the session list and deletion. The browser device-management
flow remains a separate acceptance condition.

### Verified behavior and integration handoff

The new HTTPS fixture and existing status, entrance, schema and account-scope
fixtures passed three consecutive runs against disconnected PostgreSQL 18.6
with pgvector 0.8.7. Two independent HTTPS handlers used separate PostgreSQL
pools. Both refused the revoked cookie and the second owner rejected the
previously authenticated actor at its transaction fence. Concurrent deletes
and a subsequent retry produced one durable logout event. A separate fixture
confirmed that revocation still succeeds when new business is blocked by
the license.

For the public contract, use the existing Admin or Portal session alternatives
for both operations. Require `Origin` and `X-CSRF-Token` on DELETE, leave its
request body empty, and describe 204, 400, 401, 403, 404 and existing storage
errors. The current-session DELETE also expires that entrance's session and
CSRF cookies. Keep the security page available without new-business
admission, and show the current session independently from other entries.
The browser flow and upstream sign-in or credential-change race acceptance
remain outside this verified slice.

The bundled Admin and Portal API references use the same localized problem
messages as the browser. The [identity contract](../../api/openapi/v1/identity/openapi.json)
maps a confirmed storage rollback to retryable 503. Explain that the operation
was rolled back and offer another try. An unknown commit returns 409 and needs
a fresh read before another write. The help tests require an explanation for
each exposed code in both languages. This adds no dependency or stored state.

Configure `PROJECT_ONE_TEST_DSN` for an isolated loopback PostgreSQL test
database with pgvector installed. Run from the repository root.

```sh
go -C services/control test -tags repository_integration ./internal/modules/identity/http \
  -run '^(TestBrowserSessionManagementHTTPS|TestBrowserSessionRevocationWithoutLicense|TestBrowserEntranceSessionIsolation|TestSessionStatusReportsWhatIsLeft|TestM01EveryHTTPResponseSchema|TestM01CurrentHTTPKnownResourceScope)$' -count=3
```

### Credential rotation interleaving

Exercise targeted revocation and password change over HTTPS through two
handlers with separate database pools. Both use the identity repository's
user-first lock order. An independent fixture transaction holds the
target session row and observes the real blocking chain using
[PostgreSQL's `pg_blocking_pids`](https://www.postgresql.org/docs/18/functions-info.html#FUNCTIONS-INFO-SESSION).
The session row lock allows the first caller to acquire the user fence
while delaying its write. This gives both orderings without changing
production code or guessing when a request reached the fence.

When revocation wins, the target's in-flight password change must fail
with expired session authority. The credential and manager session stay
valid. When password change wins, the in-flight manager must lose its
authority, both previous sessions end, and only the changing browser
gets a fresh session. Check credential version, auth epoch, affected
session rows and the exact committed audit counts in each ordering.
Reuse Go, pgx and PostgreSQL with the licenses above.

Both orderings and three existing browser session and password change
regressions passed three consecutive runs over verified HTTPS against
PostgreSQL 18.6 with pgvector 0.8.7 in an isolated Linux arm64 container
with no network. With the same disposable PostgreSQL configuration as
above, run the new test with
`go -C services/control test ./internal/modules/identity/http -run '^TestBrowserSessionRevocationPasswordChangeRaceHTTPS$' -count=3`.

The existing SSO repository fixtures
`TestM11ActualIssuedSessionIsRevokedByWaitingConnectorDisable` and
`TestM11ActualFlowCannotReissueAfterOrdinarySessionRetention` also passed
three times on the disconnected PostgreSQL fixture. They cover connector
invalidation and prevention of session reissuance from a spent flow.
They use a database-backed source fixture. Live provider sign-in followed
by targeted device revocation through the browser remains a separate check.

### Live provider prerequisite

The retained fixture on `ownsolid-cpu` served the Gitea 28.1.0 version
endpoint with the fixture CA and advertised TLS hostname. Its paired
Control image predates the device session API in `42ac50ca`. The test
tenant has five disabled Gitea connectors and no live sourced session.
Qualification needs a current isolated Control install, an enabled connector
with a verified synthetic account binding, both configured callback URLs
registered as needed, and protected credential files for the test identity.

Use the existing [Gitea OAuth provider flow](https://docs.gitea.com/development/oauth2-provider/)
and the existing verified TLS browser fixture. The
[OAuth authorization-code contract](https://www.rfc-editor.org/rfc/rfc6749.html#section-4.1.2)
and [PKCE](https://www.rfc-editor.org/rfc/rfc7636.html) bind each callback to
its initial browser exchange. Obtain two distinct sourced sessions through
the same test binding. In one session, revoke the other's ID using the
device API, verify the target is expired and the managing session remains
current, then replay the captured target callback in its original flow
context. Require that no replacement session appears and that the exact
session target has one durable revocation event. A fabricated flow or a
connector-wide disable cannot establish this behavior. Reuse the existing
test and product dependencies. Gitea is a fixture dependency under its
[MIT license](https://github.com/go-gitea/gitea/blob/v28.1.0/LICENSE),
which permits commercial use, offline use and redistribution with notices.
