# Service accounts and scoped API keys (IAM-109)

[Kubernetes service accounts](https://kubernetes.io/docs/concepts/security/service-accounts/)
give workloads their own namespace-bound identity and support expiring tokens.
[GitLab self-managed service accounts](https://docs.gitlab.com/user/profile/service_accounts/)
separate machine accounts from human sign-in. Its
[project access tokens](https://docs.gitlab.com/user/project/settings/project_access_tokens/)
show scoped issuance, expiry, one-time secret disclosure, rotation and
revocation through an existing token owner.

## Current authority and integration boundary

Distribution already owns creation, secret hashing, one-time disclosure,
expiry, rotation, revocation and request admission for Workspace API keys.
It gets Workspace authority on the server and checks the gateway, alias and
key in the same tenant and Workspace. The `api_keys.created_by` foreign key
references `users`. Issuance and admission recheck the user's authentication
epoch and the active Workspace membership's authority ID. The current
audit writer attributes key management to the authenticated human user.

The identity package exposes browser-session authority for human accounts.
The tenant and Workspace membership model has no service-account principal
that Distribution can resolve. The Kubernetes credential used by the private
cluster adapter identifies that adapter at an external API and grants no
ownsolid Workspace authority. An identity-only table or DTO would leave
existing issuance, rotation, revocation and use bound to human users. Giving
a bot a browser user row or attributing its calls to the operator would lose
the principal distinction required by AUD-101.

Keep Distribution as the one key lifecycle owner. A joint integration
needs an explicit service principal and authoritative tenant/Workspace
binding, a credential-owner reference that admits both existing human
owners and new service owners, and an audit event that distinguishes the
authenticated issuer from the service that later uses the key. Existing
personal keys keep their owner, scope and revocation rules. Apply authority
changes and audit writes in the same transaction. A second credential store
would give two places to expire, rotate or revoke the same authority.

This design adds no dependency, model, dataset or network service. A local
implementation can use the existing Go cryptographic primitives
(BSD-3-Clause), pgx (MIT) and PostgreSQL (PostgreSQL License). Those licenses
permit commercial use and offline redistribution with their notices.

## Acceptance before enabling a service principal

- A scoped operator can create and disable a service account, issue and rotate
  its key, and revoke it in the existing management flow. The secret appears
  once and the service principal cannot acquire a browser session.
- The actual gateway accepts the key only for its authorized tenant, Workspace
  and alias. Foreign tenant or Workspace identifiers fail at each management
  and use path without exposing credentials or writing a business result.
- Expiry, explicit revocation and service disable deny new use. Rotation
  preserves the scope, handles idempotent retries and honors the bounded
  overlap. Concurrent disable and rotation cannot restore authority.
- The audit path identifies the human operator on management actions and the
  authenticated service on key use, with neither the secret nor its digest.
  Exercise the lifecycle in one disconnected deployment over the real gateway
  and PostgreSQL before enabling AUD-101's service-actor assertion.

The IAM lane can change the identity module. The required Distribution
ownership and admission changes, Workspace integration, public contract and
UI remain outside that assignment. The current schema and API provide no
existing adapter seam that can preserve the lifecycle with a real service
principal, so this item remains open.

## Existing lifecycle verification

With PostgreSQL 18.6 in a container without a network route, six existing
Distribution tests passed. They cover one-time disclosure and creator
isolation, configured expiry, rotation preserving the human owner and
Workspace, unauthorized rotation, concurrent successors, and serialization
between admission and revocation. These runs establish the existing human
key integration point. Service principal acceptance depends on the shared
connections described above.

## Identity repository slice

The two-phase gateway authorization in
[Distribution](../../services/control/internal/modules/distribution/authorization.go)
authenticates the gateway and key, observes licensing, and then rechecks
the workspace, owner, gateway, key and alias in the admitting transaction.
Its workspace comes from the authenticated gateway, and the key is looked up
within that scope. The existing key table and request receipts require a user
owner. Keep that complete path for personal keys. A service principal is a
separate identity with no password, browser session, membership epoch, secret
or key registry. Creation records the human operator as management provenance.

The first management path requires a current platform Admin session. It
checks the completed installation tenant and the exact workspace pair in the
caller transaction. Create and check require an active workspace, and Admin
can revoke in an archived workspace so restoring the workspace cannot restore
the principal. A principal is created with an explicit expiration and is
permanently revoked. An exact create retry is identified by tenant, workspace,
operator and idempotency key. A different input for that tuple is a conflict.
Revocation retries preserve the first revoker and its one audit event.
Management events record the human actor and the service principal as the
target, with the actual workspace and no fabricated user subject.

For a machine request, the authenticated Distribution owner passes its
resolved workspace pair and the principal reference from the existing key
record into `RecheckServicePrincipalWithin`. The identity method checks the
active installation and workspace, locks the exact principal in share mode,
and tests expiry with the database clock after the lock is acquired. The
revoke path takes a conflicting principal lock. Keep the admission and audit
write in the same borrowed transaction as this check. The authority order
is user/source/session where applicable, then workspace, then principal,
before downstream gateway/key locks. This prevents a later authorization
after revocation commits and preserves in-flight outcomes whose admission
committed first. See [PostgreSQL row locking](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS)
and [materialized CTEs](https://www.postgresql.org/docs/18/queries-with.html#QUERIES-WITH-CTE-MATERIALIZATION).

### Handoff by owner

- Schema. The [candidate PostgreSQL table](../../services/control/internal/modules/identity/testdata/service_principals.sql)
  is installed only by the isolated identity fixture. Review the scoped
  foreign key and unique creation intent, then coordinate the migration,
  runtime grants, restore fences and retention. Introduce mutually exclusive
  human and service owner references in the existing `api_keys` and
  `gateway_requests` records. Keep the existing user identity and epoch for
  personal keys and use a composite tenant, workspace and principal
  reference for service keys. Keep the creator as management provenance.
- Distribution and Gateway. Select the service principal reference from the
  authenticated, scoped key record during admission. Call
  `RecheckServicePrincipalWithin` with the Workspace owner's resolved scope
  in create and rotate, and the authenticated gateway's scope in admission.
  Preserve the existing credential and alias checks. Key revocation and expired-principal cleanup
  must remain possible after service authority ends. Existing admitted
  receipts keep their report and reconciliation paths.
- Audit. Commit service attribution from the successful transaction
  alongside the request receipt. Define and verify the service actor
  commitment before exposing it in the public audit list or offline export.
  Account management continues to name the actual human actor.
- DEV and Web. Define the public management contracts, generated clients and
  operator flow after the schema and Distribution owners agree on authority.
  Keep production dispatch disabled until that joint acceptance passes.

A user-backed surrogate, independent key registry and out-of-transaction
authority cache would each lose an existing authorization invariant.

Verify the PostgreSQL repository with a real Admin session, a non-default
workspace, a foreign tenant and a sibling workspace. Cover scoped creation,
retry conflict, true expiry, revoked authority, cross-scope refusal, permanent
revocation, archived-workspace behavior, concurrent check and revoke, and
rollback with the audit row. The existing dependencies and offline licensing
analysis above apply to this slice.

### Verification

With PostgreSQL 18.6 and pgvector 0.8.7 in a container with networking
disabled, `TestServicePrincipalRepository` passed three times. Its lock
test observed the waiting database backend and verified expiry with the
database clock before releasing the held row. It also tested rollback when
the audit append is refused, and preserved an active workload identity
after its creator signed out. The existing Distribution tests for personal
key issuance, expiry, scoped rotation, audit rollback and concurrent
admission/revocation passed in the same environment.

Set `PROJECT_ONE_TEST_DSN` to an isolated loopback PostgreSQL test database
with pgvector installed and run the following from the repository root.

```sh
go -C services/control test ./internal/modules/identity \
  -run '^TestServicePrincipalRepository$' -count=3
go -C services/control test ./internal/modules/distribution \
  -run '^(TestAPIKeySecretOnceCreatorIsolationAndPersistentRevocation|TestAPIKeyConfiguredTTLQuotaExpiryAndAliasDisable|TestAPIKeyAuditRollbackLeavesNoAuthorityOrOneTimeSecret|TestRotationKeepsScopeAndOwnerAndOverlapsTheOldKey|TestRotationRefusesWhatTheCallerMayNotRotate|TestActualAdmissionAndKeyRevocationSerializeInBothOrders)$' -count=1
```
