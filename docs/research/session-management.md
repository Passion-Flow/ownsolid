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

The status endpoint and browser idle warning already exist. The remaining
device list and forced sign-out require new public contracts and browser
screens, beyond the lane's permitted edits.
