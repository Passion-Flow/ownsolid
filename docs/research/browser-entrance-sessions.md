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

## Verification

`TestBrowserEntranceSessionIsolation` runs through a real TLS listener and
PostgreSQL 18.6. A user with memberships in two tenants receives the
installation's tenant on both entrances. Renaming each session cookie to the
other entrance's name fails on the current-account and session-status reads.
Logging out with either renamed value leaves both original sessions valid.
This regression passed in a Docker fixture with no network interfaces beyond
loopback. The earlier live browser checks are recorded in
[capability status](../product/status.md).

The lane's Linux amd64 install also served both built applications to
Chromium with separate hostnames, a pinned TLS public key and no default
route from control. The browser held both sessions in one context. Each
host received only its own session cookie. Admin sign-out ended its session
while the Portal session remained usable.

## Shared-host compatibility boundary (IAM-114)

[RFC 6265](https://www.rfc-editor.org/rfc/rfc6265.html#section-8.5)
defines the cookie exposure across ports on one host.
[RFC 6454](https://www.rfc-editor.org/rfc/rfc6454.html#section-4) includes
the port in the web origin, so the existing exact Origin and audience-bound
CSRF checks still distinguish the two addresses. Exercise this case with
one real cookie jar, two TLS listeners on different loopback ports and
the existing PostgreSQL authentication handlers. Observe both session
cookies in the jar at both ports. Verify that renaming either token
cannot cross audiences, Origin and CSRF proofs from the other port fail,
and signing out at one entrance leaves the other usable.

Exercise the published device session operations in that same shared jar.
Each port must list only its own audience. Send each port the other port's
live session ID with the local Origin and CSRF proof and require a scoped
404. For a second Admin browser, require a wrong-port Origin and a Portal
CSRF proof to fail before revocation. Then revoke that device from the
original Admin browser and verify the target is expired, the managing
Admin and Portal remain live, and exactly one target-bound revocation audit
event was committed. These checks cover the identity handler after
selection of the shared-host topology.

Use the existing Go cookie jar and PostgreSQL fixture with the licenses
above. A fixture using separate hostnames cannot establish shared-host
behavior. Changing cookie names alone cannot create port isolation.
The configuration owner must still add a typed, default-off exception
for the shared hostname, apply it at startup and installation, and
preserve distinct configured origins. The operator and sign-in UI must
explain the cookie sharing when the exception is selected. The
compatibility fixture qualifies the identity path after an exception
is selected.

`TestSharedHostPortsKeepBrowserAuthoritySeparateHTTPS` and the existing
distinct-host regression each passed three times over verified TLS against
PostgreSQL 18.6 with pgvector 0.8.7 in an isolated Linux arm64 container.
The extended device-session check, distinct-host check, device-management
check and revocation/password-change race each passed three repetitions with
the container network disabled.
With the isolated `PROJECT_ONE_TEST_DSN` configured, run
`go -C services/control test ./internal/modules/identity/http -run '^TestSharedHostPortsKeepBrowserAuthoritySeparateHTTPS$' -count=3`.
