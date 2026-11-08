# Local multifactor authentication (IAM-105)

[Keycloak's authentication flows](https://www.keycloak.org/docs/latest/server_admin/index.html#_authentication-flows)
support an OTP challenge and one-use recovery codes after another factor.
[GitLab's self-managed 2FA flow](https://docs.gitlab.com/user/profile/account/two_factor_authentication/)
requires proof of possession before enrollment succeeds and displays recovery
codes once. [RFC 6238](https://www.rfc-editor.org/rfc/rfc6238.html) defines TOTP
time steps and replay prevention. [NIST SP 800-63B-4](https://pages.nist.gov/800-63-4/sp800-63b/authenticators/)
requires bounded verification and replay resistance.

Use a pending server-side authentication transaction for an enrolled user.
Bind it to the browser, tenant, user, entrance, expiry and verified first
factor. Password and SSO paths should converge on the same gate before issuing
any session. Encrypt the TOTP seed under a versioned installation key, using
tenant and factor identity as authenticated data. Confirm possession before
making enrollment authoritative. Consume a TOTP time step or recovery code in
the same transaction that issues the session, with persistent rate limits.
Record step-up proof against the specific live session with a bounded age
and recheck it at the privileged write. Record audit events without challenge
secrets or recovery values.

Reject a reusable second-factor claim from the client. Issuing an ordinary
session before the challenge would let other routes bypass it. A mail reset
may replace the password but must preserve the factor and step-up requirements.
A separate WebAuthn path supplies phishing resistance in IAM-206.

The approach can reuse Go's BSD-3-Clause cryptographic primitives, PostgreSQL
under the PostgreSQL License and existing MIT-licensed pgx. Use RFC 6238 test
vectors and synthetic secrets. No additional package, dataset or online
service has been selected.

Acceptance requires enrollment and sign-in in a real browser with an offline
authenticator, one-use recovery and an audited sensitive action requiring
step-up. Test out-of-window codes, simultaneous replay from two replicas,
wrong browser and audience, missing or invalid CSRF, replaced sessions, wrong
tenant and account, and expired or previously used recovery codes. A
password reset, SSO callback and existing local session must not bypass MFA.

The lane can modify the local identity owner. Enabling the flow also needs the
SSO session issuer, additive public contracts, translated challenge and
enrollment screens, and step-up enforcement in the protected resource owners.
