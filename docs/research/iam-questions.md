# IAM lane boundaries

| Items | Boundary | In-scope treatment |
| --- | --- | --- |
| IAM-102 | IAM now owns the SSO client, callback and binding code. Generic provider selection, issuer/subject/nonce storage, typed destination policy, the published connector API and browser forms still need their shared owners' admission. | Preserve Gitea authority while qualifying the [current OIDC verifier, dependency licenses and adapter boundary](oidc-sign-in.md). Prototype with an actual private provider before changing the serving path. |
| IAM-103, IAM-104, IAM-110 | Directory and SAML sign-in, synchronization, and group mapping need the SSO provider and binding owners, connector contracts and administrator screens. | Carry forward tenant-scoped provider identity and explicit local binding. Use existing local sign-in during provider rollout. |
| IAM-105 | ARC owns numbering and admitting the migration and its restore histories. Control startup, the public contract, the Admin UI and the federated challenge remain with their respective owners. | The [migration and startup handoff](local-multifactor-authentication.md#postgresql-migration-and-serving-handoff) verifies the unnumbered DDL, legacy upgrade, fail-closed startup, retained-factor sign-in and one-use recovery over disconnected HTTPS. |
| IAM-106 | Catalog app client registration, redirect authorization and key custody need coordination with the app catalog owner and new OAuth/OIDC operations. | Keep client registration and the issuer protocol together so an app can exercise discovery through a valid tenant-bound token. |
| IAM-107, IAM-108 | The workspace membership authority and protected resources live in other modules. Identity-only role definitions would leave those access checks unchanged. | Integrate the authorization evaluator and its explain output with one actual resource family first. |
| IAM-109 and AUD-101 | Distribution keys require a human user, current authentication epoch and Workspace membership authority. There is no service principal in the existing tenant identity model. | Keep the real key lifecycle as the integration point. The [service account design](service-account-keys.md) records the required Distribution, Workspace and audit connections. |
| IAM-111 | The self-service list and deletion contract exists. Acceptance still needs a rendered browser device list and its targeted sign-out flow on a current installation. | The [HTTPS race verification](session-management.md#credential-rotation-interleaving) proves device revocation and credential rotation serialize through existing authority. The [actual Gitea probe](sso-keyring-recovery.md#real-provider-acceptance-boundary) verifies sourced device revocation and callback replay refusal through explicit identity HTTP composition. |
| IAM-113 | The installer must accept typed SSO provider configuration, including the provider CA and keyring file references, and apply it during installation. | Preserve the existing audience and provider credential checks. |
| IAM-114 | Control startup and installation must default to rejecting a shared hostname, with an explicit typed exception for distinct ports. Operator documentation and both sign-in pages must explain shared cookies when it is enabled. | The [same-host HTTPS regression](browser-entrance-sessions.md#shared-host-compatibility-boundary-iam-114) verifies the session, Origin and CSRF safeguards needed by the exception. |
| IAM-118 | Formal diagnostic schema admission, the published start/result/callback contract and the Admin result UI remain with their owners. | The [interactive server candidate](sso-interactive-connector-tests.md) has a dedicated test record, exact Admin session and connector fences, single-use claim, minimized result and atomic audit. Its default routes stay closed until explicit composition; no binding or login session is issued. |
| IAM-119 | Control configuration owns selecting the recoverable service in production. API and Web own the explicit unreadable state and repair action. | The [keyring recovery candidate](sso-keyring-recovery.md) verifies selective PostgreSQL recovery, local and sourced session authority, and audited replacement without the old key. |
| IAM-201 to IAM-206 | P2 follows the P1 completion gate. Its provisioning, policy, approval, emergency access, delegation and WebAuthn flows need public interfaces and operator or user screens. | Preserve the P2 order and keep the items open until their dependencies and delivery paths are available. |
| AGT-107 | API owns the public operation and route registration. APP and AUD own the durable audit writer and its authorized workspace and export prerequisites. | The [authenticated HTTP handoff](product-assistant-audit.md#authenticated-http-handoff) keeps the adapter in a build-only test. `TestProductAssistantUnregisteredHTTPS` passed three times on disconnected PostgreSQL 18.6. The default dispatcher returned 404 for licensed Admin and Portal sessions and produced no assistant audit event. |

## Repository acceptance gate

The IAM-105 candidate and IAM-111 and IAM-114 regressions passed
`make check`, `make build` and `make test` at `36313f07`, based on
`23b99915`. Chromium passed 1,074 Console and 387 Portal cases, with 33
skipped. Eight focused IAM HTTPS cases passed three repetitions on
disconnected Linux arm64 with PostgreSQL 18.6 and pgvector 0.8.7. The
localized audit coverage test passed with both IAM-105 action names.

After the private-file alias regression was added, all eight disconnected
HTTPS cases passed three repetitions at `ab02c3b1`. The same IAM test
inputs were used at `544c77a9`, where `make check`, `make build` and both
Chromium suites passed with Python 3.12. Console passed 1,074 cases and
Portal passed 387, with 35 skipped. The dataset import peak-RSS assertion
failed in the first Go run. It passed four targeted repetitions on the
unmodified base `3641bd42` and four on the candidate. The full non-browser
test phase passed on the same candidate with Go package scheduling limited
to two concurrent packages.

At `c6de726c`, the Help test read raw OpenAPI path items while the generator
followed the Nodes aliases to the canonical Components operations. The
same two assertions failed on unmodified main. The shared resolution fix
in `9d25ee7c` passed all 19 Help tests in the full gate. To exercise the
contract independently, run
`cd web && pnpm --filter @ownsolid/help exec vitest run src/help.test.ts`.

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

Closing IAM-101 and IAM-112 also requires the synchronized capability
status update in `docs/product/status.md`, outside this lane's shared-file
allowlist. The evidence above is ready for that status update and the
corresponding roadmap ticks.

## Product status handoff

The product status owner can use these rows in `docs/product/status.md`.

```markdown
| Local account lifecycle (IAM-101) | Yes. Invitation and invitation mail operations enforce the installation tenant on the server. | Chromium on an isolated Linux amd64 install with TLS SMTP exercised invitation, resend, registration, account disable and enable, password reset, lockout, and retry. Disconnected PostgreSQL 18.6 tests refused foreign tenant invitation IDs, mail IDs, and tokens. [Evidence](../research/local-accounts.md) | No | None known |
| Separate Admin and Portal sessions (IAM-112) | Yes. The entrances use host-only cookies and server-enforced session audiences. | Chromium on an isolated Linux amd64 install verified independent sign-out. TLS tests on disconnected PostgreSQL 18.6 refused cookie replay in both directions and verified that cross-audience logout leaves both valid sessions usable. [Evidence](../research/browser-entrance-sessions.md) | No | None known |
```

The status owner coordinates `docs/product/status.md` with the IAM owner,
who owns `docs/product/roadmap/identity-and-access.md` and regenerates
`docs/product/roadmap/index.md`. Both boxes stay open until the status rows
and ticks land together.
