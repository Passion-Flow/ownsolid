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

Mount the candidate's new Admin browser operations only after its
dedicated keyring has been initialized in process. Keep the existing
password-change request strict on other entrances and on deployments
without the keyring. Session issuance still checks authoritative factor
rows on a keyless replica so an enabled factor cannot be bypassed.
Verify that unconfigured routes are absent, tenant overposting is
rejected, and existing account and Portal requests retain their contract.

### Startup configuration boundary

Follow the SSO owner's enabled-mode startup pattern in
[the SSO service](../../services/control/internal/modules/sso/service.go).
The identity owner takes explicit enabled intent and a dedicated file
reference. An omitted configuration leaves legacy startup and the
common session-issuance guard intact. Enabled configuration loads and
validates the reference, verifies the required PostgreSQL projections
and decrypts active or still-confirmable factors before publishing a
usable keyring. Refuse invalid configuration with fixed codes, including
when the configured key ID exists but its key cannot decrypt stored state.

Keep the candidate's `active` and `keys` JSON format and AES-GCM
associated-data layout so retained seed rows remain readable. Match the
protected file pattern used by SSO with a canonical absolute path, a
private process-owned parent and file, no symlinks or hard links, and
one bounded read from a pinned descriptor. Compare the pinned file's
identity against the configured mail, authentication HMAC and SSO key
references so a symbolic alias cannot cause two authorities to share the
same key file.
[Go's rooted file API](https://pkg.go.dev/os#OpenRoot) supports that
descriptor boundary. Use the standard
[strict JSON decoder](https://pkg.go.dev/encoding/json/v2) to reject
duplicate, unknown or case-aliased fields and canonical base64 for each
32-byte key. The mail loader checks file mode and parses keys but can
accept unknown or duplicate JSON fields and reads the file by path after
checking it. A second read for stricter validation would let the two
checks observe different content.

Use the existing Go, pgx and PostgreSQL licenses above. The startup
tests exercise missing and permissive files, malformed content and
private error diagnostics, shared key references through aliases, a legacy
database without the factor schema,
and valid and unusable retained encrypted factors. The shared config
owner maps its eventual `security.admin_totp` policy to this typed
initializer before constructing the public handler. Migration, restore
and public composition remain separate.

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
The six candidate cases and the session-rotation and shared-host
regressions passed three consecutive runs on the disconnected fixture.

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
  injects an audit write failure. The old session remains live and the same
  TOTP can retry. A successful retry retires the old password, session
  and pending challenge.
- `TestAdminTOTPCandidatePasswordResetKeepsFactorHTTPS` verifies that an
  actual password reset invalidates the old password and pending challenge
  while the enrolled factor still gates the new password.
- `TestAdminTOTPCandidateUnmountedHTTPS` verifies the existing login
  representation and strict password change input on a keyless deployment.
- `TestAdminTOTPCandidateStartupHTTPS` verifies that legacy startup works
  without the migration, enabled startup refuses missing relations or
  columns, and a retained key survives rotation and still authorizes an
  enrolled browser. A missing retained key or wrong bytes under the same
  ID refuses startup.

`TestAdminTOTPStartupRejectsUnsafeKeyReferences` and
`TestAdminTOTPKeyringReadsCandidateCiphertextWithoutExposingKeys` exercise
private file permissions, links, size bounds, strict content validation,
redacted diagnostics and compatibility with the earlier candidate's
ciphertext.

With `PROJECT_ONE_TEST_DSN` pointing at a disposable PostgreSQL fixture,
run `go -C services/control test ./internal/modules/identity/http -run '^TestAdminTOTPCandidate' -count=3`.
Use `go -C services/control test ./internal/modules/identity -run '^TestAdminTOTPRFC6238'`
for the RFC vectors.

The candidate management routes live under `/api/v1/me/totp/`.
`enrollment` returns the seed, `enrollment/confirmation` returns the
recovery values and `revocation` removes the active factor.
`POST /api/v1/auth/login/totp` completes the pending login and
`POST /api/v1/me/password-changes` accepts inline TOTP proof.

## PostgreSQL migration and serving handoff

The factor, challenge and recovery relations belong to global identity under
[ADR 0006](../decisions/0006-cell-and-site-scope.md), like `users`, `sessions`
and `sso_flows`. They carry no site or cell columns. The factor belongs to
the global user and the server-selected installation tenant. Composite
[foreign keys](https://www.postgresql.org/docs/18/ddl-constraints.html#DDL-CONSTRAINTS-FK)
bind both child relations to that exact tenant, user and factor. The cell
scope on audit events still comes from the existing audit writer.

Stage the forward and down SQL beside the identity HTTPS fixture, outside the
embedded migration lineage. Use the exact
[`up` file](../../services/control/internal/modules/identity/http/testdata/admin_totp_candidate.up.sql)
and [`down` file](../../services/control/internal/modules/identity/http/testdata/admin_totp_candidate.down.sql).
The forward change adds three tables without
rewriting existing identities, sessions, credentials or tenants. Retain the
candidate's factor IDs, AES-GCM associated data, secret format and recovery
HMAC purpose. Expiry and factor indexes give a later retention owner a
bounded removal path without changing the login contract. The down SQL
permits an empty rollback and refuses rollback while any factor exists.
Dropping enrolled factors would remove the authentication gate, and dropping
pending factors would interrupt an enrollment. The migration owner already
wraps DDL in a transaction. PostgreSQL's
[index definitions](https://www.postgresql.org/docs/18/sql-createindex.html)
and [backup utilities](https://www.postgresql.org/docs/18/app-pgdump.html)
add no runtime dependency. The
[PostgreSQL License](https://www.postgresql.org/about/licence/) permits
commercial use, redistribution with the license notices and offline use.

Verify the exact unnumbered SQL against a migrated, bootstrapped fixture with
an existing Portal session. Prove empty rollback and reapply, unchanged old
session authority, global relation shape, and refusal of cross-tenant child
links. After enrolling through HTTPS, prove a rollback cannot remove the
factor or its recovery values. Restart with a new active key plus the retained
key, verify the original factor and consume an original recovery value once.
Reject missing relations, missing columns, unsafe key files and absent or
wrong retained key material. Keep key selection at startup read-only. An
automatic rewrite during replica startup could make an older replica unable
to verify a factor. A separate rewrap and retirement operation requires
coordinated key distribution and recovery verification.

The startup fixture passed those checks three times on disconnected Linux
arm64 with PostgreSQL 18.6 and pgvector 0.8.7, using the schema 57 lineage at
`f83a9fde` as the predecessor. The other TOTP HTTPS cases and the session
rotation and shared-host regressions also passed three times with the
forward SQL.

Before enabling enrollment, integrate these owner boundaries:

- ARC owns numbering the reviewed migration and updating `SchemaVersion`,
  `TableCount` (+3), the global list in `cell_scope_test.go`, the schema
  inventory and runtime grants. Generate catalog fingerprints from the
  selected fresh and restore histories. Verify backup/restore preserves the
  three relations and keeps their protected keyring and the existing
  authentication HMAC available. Keep expiry cleanup with the lifecycle owner.
  Once the SQL joins the lineage, the identity upgrade test needs an explicit
  predecessor fixture and the ordinary HTTPS cases can use the migrated schema.
- The control configuration owner maps the dedicated typed policy to
  `identity.AdminTOTPConfiguration`. In `cmd/server/runtime_lifecycle.go`,
  `serveSelected` has the selected identity service after
  `verifyServingDatabase` and before workers, SSO and `identityhttp.New`.
  Call `Service.ConfigureAdminTOTP(startup, policy)` there and return its
  error before constructing any handler. The zero policy preserves legacy
  serving. When enabled, installation provisioning, restore and rotation
  must supply all live key versions to each replica. The shared identity
  issuer already refuses full Admin sessions for enrolled users without a
  verified second factor. An SSO challenge flow for those users remains
  with the SSO owner.
- The identity OpenAPI and generated clients must describe the pending
  login response, completion, enrollment, recovery display, revocation
  and the inline proof. Web needs an Admin challenge screen and account
  security flow through those operations. Additional privileged actions
  need explicit step-up admission in their respective owners.

The English and Simplified Chinese audit catalogs contain
`audit.action.auth.totp_enabled` and `audit.action.auth.totp_revoked`
since `1be6b5cc`. The focused check is
`cd web && pnpm --filter @ownsolid/i18n exec vitest run src/audit-coverage.test.ts`.
