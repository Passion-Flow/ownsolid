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
Verify step-up against the specific live session at the privileged write.
Record audit events without challenge secrets or recovery values.

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

## Candidate local Admin path

Use the [RFC 6238 verification window](https://www.rfc-editor.org/rfc/rfc6238.html#section-5.2)
with 30-second SHA-1 steps and six digits for common offline authenticators.
Allow the adjacent steps and store the highest accepted step for each
factor. Use the PostgreSQL clock after the account lock. The counter,
challenge consumption, session issuance and audit share the same commit.
The existing database rate limits also bound repeated verification against
one account across challenge renewal and replicas.

The candidate uses a separate versioned AES-GCM key file in the existing
protected keyring format. The associated data binds installation tenant,
user and factor. The first authenticated enrollment request confirms the
current password and stores a seed bound to the live session for ten minutes.
Proof of possession activates it. Activation and revocation retire every
Admin session for the account and expire the current entrance's cookies.
A Portal session remains independently scoped. Revoke requires the current
password and a fresh TOTP code. The candidate recovery path is described
below.

The existing Admin password login returns a 202 pending challenge for an
enrolled account. The three-minute challenge is an opaque random value
stored only as a digest, bound to the Admin CSRF-context cookie, preceding session, password
credential version, account epoch, tenant, factor and expiry. The completion
route uses existing Origin and CSRF checks and issues the original login
representation only after the second factor succeeds. Legacy unenrolled
accounts keep their original login flow. An enrolled Admin is fenced at the
common session issuer too, including any source or credential-change path
that has no verified factor proof. A missing decryption key cannot fall back
to a password-only session.

The isolated PostgreSQL schema and explicit key-file construction are
candidate-only. Production requires coordinated migration and retention,
mandatory startup schema/keyring validation, public contracts and an
offline enrollment UI. Do not enable production until those exist. The
candidate uses existing account problem codes and success envelopes.
Reuse the existing Go, pgx and keyring implementations and their licenses.

### Separate candidate checks

Enrollment confirmation returns ten recovery values once, in the same
commit as activating the factor. The values are independently random with
factor-bound HMAC digests, a candidate validity period of 90 days and
deletion on consumption. A recovery value is accepted only as the
second factor of a real pending Admin challenge, with the same browser
binding and per-user throttle. Revocation cascades to unspent recovery rows.

For the first sensitive action, the existing password change accepts a
fresh TOTP code inline. Verify it under the same live-session and user
fences as credential replacement. Consuming the time step, replacing the
password, revoking earlier sessions and issuing the new one are atomic.
An inline proof has no reusable bearer form. Recovery values cannot satisfy
the password-change check. Other protected operations still need their own
owner integration and end-to-end tests.

## Verification and integration

The candidate tests run the real identity HTTP handler on TLS against
PostgreSQL 18.6 with pgvector 0.8.7. The Linux arm64 container runs with
`--network none`. The embedded fixture creates only the factor, challenge
and recovery tables after applying the repository migrations.
The three candidate cases and the existing HTTPS session-management,
entrance isolation, identity response schema and invitation tenant isolation
regressions passed three consecutive runs on that fixture.

- `TestAdminTOTPCandidateHTTPS` covers enrollment, encrypted storage,
  refusal of permissive or absent key files, session revocation,
  cross-tenant scope, existing accounts, wrong browser and audience,
  CSRF and Origin checks, expired challenges, the TOTP time window and
  concurrent replay across two handlers with independent database pools.
- `TestAdminTOTPCandidateRecoveryHTTPS` exercises recovery sign-in,
  expiry and one-use enforcement. A forced audit write failure rolls back
  both session issuance and code consumption and the same request can retry.
- `TestAdminTOTPCandidatePasswordStepUpHTTPS` exercises the real
  password change, rejects absent, replayed and recovery proofs, then
  verifies retirement of the old password, session and pending challenge.

With `PROJECT_ONE_TEST_DSN` pointing at a disposable PostgreSQL fixture,
run `go -C services/control test ./internal/modules/identity/http -run '^TestAdminTOTPCandidate' -count=3`.
Use `go -C services/control test ./internal/modules/identity -run '^TestAdminTOTPRFC6238'`
for the RFC vectors.

The candidate management routes live under `/api/v1/me/totp/`.
`enrollment` returns the seed, `enrollment/confirmation` returns the
recovery values and `revocation` removes the active factor.
`POST /api/v1/auth/login/totp` completes the pending login and
`POST /api/v1/me/password-changes` accepts inline TOTP proof.
Before enabling enrollment, add a production migration and startup checks
for schema and key custody, define backup, rotation and record retention,
publish the API contract and ship localized UI. Federated sign-in needs
to enter the same factor gate. The candidate blocks alternate Admin session
issuance for enrolled accounts until the caller can provide the verified proof.
