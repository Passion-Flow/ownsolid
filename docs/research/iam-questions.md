# IAM lane boundaries

| Items | Boundary | In-scope treatment |
| --- | --- | --- |
| IAM-102 | The SSO client, callback and binding code are outside the IAM checkout's assigned paths. Connector types also flow through a shared API and the console. | Preserve the Gitea path and record the [generic OIDC design and acceptance](oidc-sign-in.md). A provider change needs coordination with the SSO and console owners. |
| IAM-103, IAM-104, IAM-110 | Directory and SAML sign-in, synchronization, and group mapping need the SSO provider and binding owners, connector contracts and administrator screens. | Carry forward tenant-scoped provider identity and explicit local binding. Use existing local sign-in during provider rollout. |
| IAM-105 | The production migration, protected key configuration, published challenge and enrollment operations, Admin UI and federated Admin issuance remain separate integration points. | The [local candidate](local-multifactor-authentication.md#verification-and-integration) retains the keyless deployment's browser contract and verifies the full configured flow over disconnected HTTPS. |
| IAM-106 | Catalog app client registration, redirect authorization and key custody need coordination with the app catalog owner and new OAuth/OIDC operations. | Keep client registration and the issuer protocol together so an app can exercise discovery through a valid tenant-bound token. |
| IAM-107, IAM-108 | The workspace membership authority and protected resources live in other modules. Identity-only role definitions would leave those access checks unchanged. | Integrate the authorization evaluator and its explain output with one actual resource family first. |
| IAM-109 and AUD-101 | Distribution keys require a human user, current authentication epoch and Workspace membership authority. There is no service principal in the existing tenant identity model. | Keep the real key lifecycle as the integration point. The [service account design](service-account-keys.md) records the required Distribution, Workspace and audit connections. |
| IAM-111 | The self-service list and deletion contract exists. Acceptance still needs a browser device list, its targeted sign-out flow and upstream sign-in revocation coverage. | The [HTTPS race verification](session-management.md#credential-rotation-interleaving) proves device revocation and credential rotation serialize through existing authority. |
| IAM-113 | The installer must accept typed SSO provider configuration, including the provider CA and keyring file references, and apply it during installation. | Preserve the existing audience and provider credential checks. |
| IAM-114 | Control startup and installation must default to rejecting a shared hostname, with an explicit typed exception for distinct ports. Operator documentation and both sign-in pages must explain shared cookies when it is enabled. | The [same-host HTTPS regression](browser-entrance-sessions.md#shared-host-compatibility-boundary-iam-114) verifies the session, Origin and CSRF safeguards needed by the exception. |
| IAM-118, IAM-119 | Real connector testing and keyring loss recovery live in the SSO callback, connector and startup owners outside the lane. Both need connector UI states. | Preserve fail-closed credential checks. Design a separate test callback outcome and explicit unavailable credential state with the SSO owner. |
| IAM-201 to IAM-206 | P2 follows the P1 completion gate. Its provisioning, policy, approval, emergency access, delegation and WebAuthn flows need public interfaces and operator or user screens. | Preserve the P2 order and keep the items open until their dependencies and delivery paths are available. |

## Repository acceptance gate

The IAM-105 candidate and IAM-111 and IAM-114 regressions passed
`make check` and `make build` at `4f3593d8`. The full `make test` run
passed Go, gateway and both Chromium suites, with 1,074 Console and 387
Portal cases passed and 33 skipped. Thirteen IAM HTTPS cases passed
three runs on disconnected PostgreSQL 18.6. The localized audit coverage
test passed with both IAM-105 action names.

The full suite is blocked by the Console and Portal API-reference
assertions in `web/packages/help/src/help.test.ts`. The same two failures
reproduced on unmodified `origin/main` at `c6de726c`. Since `601e3dc7`,
the Web help generator resolves the Nodes path aliases to the canonical
Components operations using `tools/codegen/openapi-paths.mjs`. The test
still reads raw path items and misses three Console operations and one
Portal operation for node-exporter. To clear the gate, the Web/DEV owners
must make the reference test and generator enumerate the same resolved
operation set for both entrances and run the full repository suite.
The focused reproduction is
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
