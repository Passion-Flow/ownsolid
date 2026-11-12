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

Scope recovery checks to the completed installation tenant, including when
another tenant has a connector with ciphertext unavailable to the selected
keyring. The authenticated catalog and public discovery must exclude that
foreign connector. Attempts to start it, replace its credentials or select its
tenant must fail before a write or provider call.

Force an audit append refusal during authorized replacement and compare the
encrypted row and unreadable-state projection with their preimages. The same
idempotency key must remain available for a successful retry. This exercises
the existing shared commit owner through the new credential recovery path.

With the entire keyring absent, exercise the existing disable mutation through
local Admin HTTPS. Its idempotent replay must preserve the ciphertext, append
one audit event and revoke sessions from that connector. Local sign-in remains
usable without loading or creating key material.

The configuration owner selects the candidate constructor at the existing
`serveSelected` injection. On success, assign the returned service to
`identity.Service.Sources` and use it for the existing maintenance and close
hooks. The dispatcher reuses that instance. An error still aborts startup.
With no active key, a local Admin can inspect connector metadata and disable
an affected connector. An operator must install a protected replacement
keyring and restart before the service can seal new credentials.

The public connector contract and Admin screen own the explicit state,
instructions and recovery action. Closing IAM-119 requires those owners and a
real provider sign-in after recovery through the installed startup path.

### Real provider acceptance boundary

Use Gitea's documented [OAuth provider](https://docs.gitea.com/development/oauth2-provider/)
and [CLI](https://docs.gitea.com/administration/command-line/), with its pinned
28.1.0 image, for an isolated synthetic identity. The existing provider unit
double cannot establish that repaired credentials complete a real exchange.
The upstream [sign-in form](https://github.com/go-gitea/gitea/blob/v28.1.0/templates/user/auth/signin_inner.tmpl)
and [consent form](https://github.com/go-gitea/gitea/blob/v28.1.0/templates/user/auth/grant.tmpl)
define the protocol fixture's interactive steps. Keep consent, PKCE, exact
callbacks and certificate verification enabled. Refuse an unexpected provider
version or form rather than manufacture a code or binding. Gitea's
[MIT license](https://github.com/go-gitea/gitea/blob/v28.1.0/LICENSE) allows
commercial, offline and redistribution use with notices. The probe uses the
existing Go standard library; no model or dataset is involved.

An opt-in real-provider probe must begin with a password-authenticated link,
obtain an actual sourced session, restart under a keyring that cannot open the
connector, and repair it through local Admin HTTPS. Require another real
provider sign-in afterward. Retain secrets only in protected fixture files or
process memory. A protocol client proves the HTTP/session path, not rendered
browser or installed production startup acceptance. Prototype the provider
fixture outside product source before adding the probe.

The isolated prototype completed verified TLS login, explicit consent,
confidential S256 code exchange and authenticated introspection with exactly
`read:user` and the configured issuer. The provider used its real SQLite store,
a synthetic user and a disposable confidential application, with container
networking disabled. Native form submissions retain the provider's Origin and
Referer checks. No provider security option was disabled.

`TestSSOActualGiteaRecoveryAndDeviceRevocationHTTPS` passed against that real
provider and PostgreSQL 18.6 with pgvector 0.8.7. PostgreSQL joined the provider's
network namespace; the provider had `--network none`, so both had only loopback.
The probe created its account binding through the actual password-confirmed
HTTPS link and verified that each issued session referenced a succeeded flow
and its persisted binding. With an active-only keyring, the old sourced session
was refused. A local Admin replaced the credential pair with a new actual
confidential application and two subsequent provider sign-ins succeeded.
Targeted device revocation expired only its target; the managing session stayed
usable, and replay of the target's original callback created no session. The
target had exactly one durable revocation audit. This was Linux arm64 protocol
and explicit HTTP composition acceptance, without rendered UI or installed
production startup acceptance.

The optional build tag is `sso_provider_integration`. Supply
`OWNSOLID_IAM_GITEA_FIXTURE_FILE` as an absolute path to a mode-0600 JSON file
with `origin`, `ca_file`, `username` and `password_file`. Use an isolated
loopback HTTPS origin, the pinned Gitea release and a disposable synthetic user;
the test creates confidential applications. The CA and password files must
also be mode 0600, and the CA's canonical parent must be mode 0700. With an
isolated `PROJECT_ONE_TEST_DSN`, run
`go -C services/control test -tags sso_provider_integration ./internal/modules/identity/http -run '^TestSSOActualGiteaRecoveryAndDeviceRevocationHTTPS$' -count=1`.
Missing prerequisites fail this selected test. Keep provider sign-in rate limits
in mind when reusing a fixture; independent acceptance runs need fresh fixtures.

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

The current probe also refuses foreign tenant IDs and connector IDs without
changing their ciphertext or adding a mutation event. An induced audit append
failure leaves the original credential unreadable and permits the same
idempotency key to succeed after storage recovery. With no keyring, a local
Admin disables a connector, replays the exact result, retains its ciphertext,
revokes its sourced session and remains signed in. These assertions passed
three disconnected repetitions together with the strict boundary probe.
