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
