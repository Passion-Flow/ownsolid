# Local accounts (IAM-101)

Local accounts must support invitation and registration, sign-in and sign-out,
bounded login retries, and recovery through the installation's own mail server.
The database owns session, token and invitation authority. Each browser entrance
has its own audience and CSRF context.

## Sources and approach

[Keycloak's brute force guidance](https://www.keycloak.org/docs/latest/server_admin/index.html#_brute_force)
documents temporary lockout, uniform credential errors and the denial-of-service
risk of persistent account lockout.
[GitLab's self-managed password recovery](https://docs.gitlab.com/security/reset_user_password/)
uses a link mailed to the account's registered address.
[NIST SP 800-63B-4](https://pages.nist.gov/800-63-4/sp800-63b.html) calls for
per-account throttling and notification on recovery. The
[OWASP Forgot Password Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Forgot_Password_Cheat_Sheet.html)
describes uniform request responses, expiring single-use reset tokens and
session invalidation. [RFC 9106](https://www.rfc-editor.org/rfc/rfc9106.html)
defines Argon2id.

Keep the existing database-backed admission limits and password and token
formats. Retain bounded temporary lockout so a single burst cannot permanently
disable an account. Retain the configured-origin mail links, hashed
tokens and atomic credential, session, outbox and audit updates. A recovery
link must not create a new session. Server-side token and session records supply
identity and scope. Accepting an invitation and resending or revoking it already
require its recorded tenant to be the installation tenant. Apply the same
boundary to the invitation list and invitation mail list and reject foreign
invitation mail before inspecting delivery state or resending it. The global
user and audit administration endpoints retain platform administrator authority.

Use the existing account endpoints and browser forms. A parallel invitation or
reset implementation would duplicate credential and session revocation paths.
Using request-supplied tenant identifiers or applying a UI filter would expose
foreign-tenant records through the API.

## Dependencies and distribution

The affected paths reuse Go's standard library, `golang.org/x/crypto` and
`golang.org/x/text` (BSD-3-Clause), `github.com/google/uuid` (BSD-3-Clause),
`github.com/jackc/pgx/v5` (MIT), and PostgreSQL with pgvector (PostgreSQL License).
These permit commercial use and offline redistribution subject to the existing
license notices. No new library, model, dataset or Internet service is needed.
The operator's private SMTP relay handles invitation and recovery messages.

## Acceptance

- An administrator can invite a person through the console. The recipient can
  register, sign in and sign out through the Portal. A replayed, expired or
  revoked link fails without issuing a credential or session.
- A second tenant's invitation and mail are excluded from list results, and
  using its real invitation ID, mail ID or token fails on the server. Exercise
  both local and foreign records in one PostgreSQL fixture through HTTPS.
- A user can request and complete a reset in the UI. Unknown accounts get the
  same request response. A reset token for the other entrance or a replay fails.
  The previous password and both entrances' sessions cease to work.
- Wrong credentials, absent or mismatched CSRF, throttling and concurrent reset
  attempts fail without creating sessions or changing credential state. A
  correct password works again after the retry window.
- Verify the existing account forms and mail flow against an installation with
  no Internet route, in addition to the full repository gates.
