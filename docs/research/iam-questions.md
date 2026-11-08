# IAM lane boundaries

| Items | Boundary | In-scope treatment |
| --- | --- | --- |
| IAM-102 | The SSO client, callback and binding code are outside the IAM checkout's assigned paths. Connector types also flow through a shared API and the console. | Preserve the Gitea path and record the [generic OIDC design and acceptance](oidc-sign-in.md). A provider change needs coordination with the SSO and console owners. |
| IAM-103, IAM-104, IAM-110 | Directory and SAML sign-in, synchronization, and group mapping need the SSO provider and binding owners, connector contracts and administrator screens. | Carry forward tenant-scoped provider identity and explicit local binding. Use existing local sign-in during provider rollout. |
| IAM-105 | TOTP enrollment, recovery and step-up need browser challenges, additive wire operations and integration with the SSO callback and sensitive actions in other owners. | Keep both entrances' existing password and CSRF checks. Design one challenge gate for local and federated session issuance before enrolling real factors. |
| IAM-106 | Catalog app client registration, redirect authorization and key custody need coordination with the app catalog owner and new OAuth/OIDC operations. | Keep client registration and the issuer protocol together so an app can exercise discovery through a valid tenant-bound token. |
| IAM-107, IAM-108 | The workspace membership authority and protected resources live in other modules. Identity-only role definitions would leave those access checks unchanged. | Integrate the authorization evaluator and its explain output with one actual resource family first. |
| IAM-109 and AUD-101 | Distribution keys require a human user, current authentication epoch and Workspace membership authority. There is no service principal in the existing tenant identity model. | Keep the real key lifecycle as the integration point. The [service account design](service-account-keys.md) records the required Distribution, Workspace and audit connections. |
| IAM-111 | Existing idle and absolute timeouts work through the session endpoint. Device management and forced logout need new management operations and a browser device list. | Keep server-side revocation authoritative. Add device operations together with the caller-visible management flow. |
| IAM-113, IAM-114 | Installation configuration lives in `deploy/`. Shared-hostname policy also needs control configuration validation and sign-in notices in `web/`. | Keep current configured audience checks and treat same-hostname validation as its own item. |
| IAM-118, IAM-119 | Real connector testing and keyring loss recovery live in the SSO callback, connector and startup owners outside the lane. Both need connector UI states. | Preserve fail-closed credential checks. Design a separate test callback outcome and explicit unavailable credential state with the SSO owner. |
| IAM-201 to IAM-206 | P2 follows the P1 completion gate. Its provisioning, policy, approval, emergency access, delegation and WebAuthn flows need public interfaces and operator or user screens. | Preserve the P2 order and keep the items open until their dependencies and delivery paths are available. |

## Repository acceptance gate

The previous training shutdown failure was reproduced from an unmodified
`origin/main` archive. ARC fixed cancellation propagation in `aa5e712f`.
After syncing the fix, the targeted training and model-preparation tests
passed ten repetitions on the workstation. The IAM account and entrance evidence is
recorded in [local accounts](local-accounts.md) and
[browser entrance sessions](browser-entrance-sessions.md).

With dedicated test ports, 372 Portal and 1,029 Console browser tests passed.
Two Console navigation focus assertions failed in the full run and passed
when rerun alone. That run did not satisfy the repository-wide gate.

On the next run, the Linux component recovery case failed twice in an
isolated arm64 container before `a8d66a32`. The fixture's
`state.Identity` contained the site and cell, but its HTTP client emitted
`/agent/v1/sites//cells//nodes/.../heartbeat` and the server refused it.
The same test passed twice in the isolated container after syncing ARC's
client fixture fix.

The rerun used browser ports 24156 and 24157 with two Chromium workers and
serial Console and Portal suites. The targeted cases passed in both
applications, including the distinct registration behavior, sign-out and
password recovery. The actual cross-tenant and cross-audience replays ran
against TLS and PostgreSQL 18.6 in a container with `--network none`.

The subsequent `make check && make build && make test` run passed at
`bfca2f3ad0de`, based on `a8d66a32`. Console passed
1,039 Chromium cases and Portal passed 387. Twenty-eight live cases that
require separately configured installations were skipped by the suites.
The actual IAM live results are recorded in the two feature notes above.
