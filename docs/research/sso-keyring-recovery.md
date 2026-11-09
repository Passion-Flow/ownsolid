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

The candidate entry is `sso.NewWithKeyringRecovery(startup, accounts)`.
It is explicit and PostgreSQL-only. It accepts a missing file under a
verified private parent or authenticated decryption failure on a scoped
stored record. Invalid configuration, insecure or malformed files, provider
trust failures, invalid scopes and failed storage scans still reject startup.
The keyring remains immutable for the service lifetime. A file change requires
a new service using the selected configuration.

Read credential availability from the same stored row as catalog and source
authority. At SSO start, check the credential inside the existing connector
fence before allocating a flow. At callback, a matching browser proof with an
unreadable verifier or connector credential ends the pending flow without
provider I/O. Keep encrypted connector records and their persisted enabled
intent intact. The authenticated Go catalog carries unavailable connector IDs
for the returned page. The existing HTTP contract continues to serialize its
published fields until the API owner supplies the new state.

An unreadable connector can be disabled without new credentials. Turning it
on or leaving it enabled during an edit requires complete new credentials.
Replacing those credentials requires a current local Admin session as well
as the existing revision, tenant and idempotency fences. A configured,
readable provider can retain its ordinary management path.

The configuration owner selects the candidate constructor at the existing
`serveSelected` injection. On success, assign the returned service to
`identity.Service.Sources` and use it for the existing maintenance and close
hooks. The dispatcher reuses that instance. An error still aborts startup.
With no active key, a local Admin can inspect connector metadata and disable
an affected connector. An operator must install a protected replacement
keyring and restart before the service can seal new credentials.

The public connector contract and Admin screen own the explicit state,
instructions and recovery action. Closing IAM-119 requires those owners and a
real provider sign-in after recovery.

### Candidate verification

`TestSSORecoverableServiceHTTPS` passed three times on Linux arm64 with
networking disabled, PostgreSQL 18.6 and pgvector 0.8.7. The test used two
connectors with distinct key versions, verified local and sourced session
authority, checked both forms of unreadable callback, and observed zero
requests at its loopback TLS provider. The stored credentials matched their
preimages until the authorized replacement. Unsafe keyring mode and missing
provider trust refused startup. Restored keys allowed an original connector
start. A no-key write, sourced Admin, missing CSRF and stale revision were
refused. A complete local replacement and idempotent replay committed one
audit event. The original strict-constructor boundary test also passed.
