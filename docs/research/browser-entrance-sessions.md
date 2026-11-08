# Browser entrance sessions (IAM-112)

The Admin console and Portal serve different audiences from separately
configured origins. People can use both in one browser. A token issued on one
entrance must fail at the other even when a caller renames the cookie.

[HTTP cookie scope, RFC 6265](https://www.rfc-editor.org/rfc/rfc6265.html#section-8.5)
describes the lack of port isolation.
[OWASP session guidance](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
recommends host-specific cookies, HTTPS and server-side enforcement.
[ADR 0027](../decisions/0027-entrance-addresses.md) separates the advertised
origins from the listeners and records the shared-hostname policy (IAM-114).

Keep distinct host-only cookies and resolve the entrance from trusted server
configuration and the validated request authority. Bind the random session
token to the entrance in PostgreSQL and require that audience on each read or
revocation. Bind CSRF tokens to the same audience, browser context and session.
Cookie names and a front-end route guard alone are insufficient because an HTTP
client can submit a captured value under another cookie name. The identity
service derives a default workspace from the installation tenant and active
membership rather than accepting client authority.

The existing Go standard library (BSD-3-Clause), pgx (MIT) and PostgreSQL
(PostgreSQL License) support local builds, commercial use and offline
redistribution subject to their notices. No new libraries, datasets or
models are needed.

Acceptance requires actual Admin and Portal login and logout, independent
cookies, and bidirectional cookie-value replay refusal through a real TLS
listener and PostgreSQL. Check both current identity and session status. A
wrong-audience logout must leave the source session usable. An unprivileged
Portal account must be refused Admin login. Test bad Origin and cross-audience
CSRF with valid credentials, and validate that a foreign tenant membership
cannot replace the installation tenant in the login or profile response.
