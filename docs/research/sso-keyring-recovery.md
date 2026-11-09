# SSO keyring recovery (IAM-119)

External identity and local password credentials have distinct authorities.
[Keycloak's broker configuration](https://www.keycloak.org/docs/latest/server_admin/index.html#_identity_broker)
keeps external providers separate from local login. For our encrypted
connector credentials, [NIST's key management guidance](https://csrc.nist.gov/pubs/sp/800/57/pt1/r5/final)
and [Go's authenticated encryption contract](https://pkg.go.dev/crypto/cipher#AEAD)
support retaining protected old keys until their ciphertext has been
replaced or expired. A matching key ID alone cannot authenticate ciphertext.

The existing SSO owner uses a separate protected file and AES-GCM with tenant,
connector, record and purpose bound as associated data. Keep that boundary.
The identity HTTP fixture uses the existing Go standard library (BSD-3-Clause),
pgx (MIT), Google UUID (BSD-3-Clause) and PostgreSQL (PostgreSQL License).
These permit commercial and offline use and redistribution with their notices.
The fixture does not require a provider process, model, dataset or external
dependency.

## Current behavior and acceptance probe

The control configuration validates policy and path shape.
`cmd/server/runtime_lifecycle.go` calls `sso.New` in `serveSelected` and
returns its error before it builds the HTTP dispatcher or listeners.
`identity/http.New` also calls `sso.New` when a service has not been injected
and records its error in `InitializationError`. `sso.New` validates provider
origins and trust, then scans stored connector secrets and live pending PKCE
verifiers. One missing or malformed keyring fails as `SSO_KEYRING_INVALID`.
A lost retained key or wrong material under a matching ID fails as
`SSO_ENCRYPTED_STATE_INVALID`. Disabled connectors are included in the scan.

Exercise these failures through the normal identity composition against
PostgreSQL. Explicitly disabled SSO is an existing operator recovery path.
Verify local password sign-in, no public provider listing or start, and refusal
of credential replacement while disabled. The stored connector still carries
its administrator-set enabled flag. The current connector contract has no
per-connector unreadable state. Restoring a valid ring with retained material
permits the existing Admin replacement path. Verify origin, audience and input
scope refusal, one audit event for replacement, and no secret bytes or key file
paths in the tested error, HTTP or audit fields. After replacement, verify that
the same connector can start a new flow under an active-only keyring.

`TestSSOKeyringRecoveryBoundaryHTTPS` passed three times on Linux arm64 with
container networking disabled, PostgreSQL 18.6 and pgvector 0.8.7. The
fixture verified the SSO response contract and a durable Admin replacement
audit. It started an authorization flow after key retirement without
contacting a provider. With an isolated `PROJECT_ONE_TEST_DSN` configured,
run `go -C services/control test ./internal/modules/identity/http -run '^TestSSOKeyringRecoveryBoundaryHTTPS$' -count=3`.

## Production handoff

The SSO and configuration owners need to compose one recovery-capable service
at the existing `serveSelected` injection before publishing listeners. Reuse
it through `identity.Service.Sources` and the HTTP dispatcher. Give the
startup caller explicit key-custody outcomes. Continue to fail startup on
configuration, provider trust or database integrity failures. Preserve the
local password, license, CSRF, tenant and actor checks in degraded serving.

When a protected replacement key is absent, local Admin may inspect stored
metadata but cannot seal a new credential. With a valid new ring, classify
unreadable connector ciphertext separately from transient store failures.
Expose a stable, redacted effective credential state alongside the persisted
enabled flag. Exclude unreadable connectors from public discovery and fail
before creating a new flow or exchanging a code. In the source-guard path,
existing sessions derived from an unreadable connector must also fail
authority rechecks. Unreadable pending PKCE verifiers must fail terminally
without a replacement session. Avoid a cached status that could grant
authority after a credential mutation.

Allow a current local Admin to replace a connector's credentials under the
existing tenant, revision and idempotency fences. Seal under the active key and
retain the existing source invalidation and audit transaction. Never infer a
new trust policy from the missing key, regenerate a keyring on startup, mark
all rows administratively disabled, or silently accept an unreadable
credential. The public connector contract and Admin screen own the explicit
state, instructions and recovery action. Closing IAM-119 requires those owners
and a real provider sign-in after recovery.
