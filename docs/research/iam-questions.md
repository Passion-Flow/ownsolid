# IAM lane boundaries

| Items | Boundary | In-scope treatment |
| --- | --- | --- |
| IAM-102 | IAM owns the SSO client, callback and binding code. Generic provider selection, issuer/subject/nonce storage, typed destination policy, the published connector API and browser forms still need their shared owners' admission. | The [patched verifier and real private-provider prototype passed](oidc-sign-in.md#protocol-prototype-qualification); follow the [storage and real-flow handoff](oidc-sign-in.md#storage-and-real-flow-admission). Preserve legacy Gitea authority and perform provider I/O outside authorization transactions. |
| IAM-103 | Directory login and group synchronization need approved configuration, stable external identity, formal schema and public/UI admission. | The [nested-group experiment](directory-login.md#nested-group-observation-qualification) passed twelve native race/eight disconnected OpenLDAP roots. Its real graph also passed the [storage candidate](directory-login.md#storage-qualification): six PostgreSQL roots/22 leaves in native race and disconnected runs, including failure retention, expiry, scope/current actor, audit rollback and actual COMMIT loss. Fresh serving-worker composition, real AD compatibility and formal product admission remain open. |
| IAM-104 | SAML needs formal metadata/key admission, the ACS contract and durable browser/session composition across an IdP POST. | The [strict protocol](saml-sign-in.md#actual-private-provider-protocol-qualification) passed the actual disconnected Keycloak signed-response flow. The [identity SQL experiment](saml-sign-in.md#persistent-identity-sql-qualification) adds five PostgreSQL roots/27 leaves in native race and disconnected runs: exact maximum Unicode identities, bound verifier authority, foreign tenant/trust refusal, immutable scope/revision, actual uniqueness waits, cancellation and rollback. Its combined graph is 26 modules/36 legal files. The [rendered transport probe](saml-sign-in.md#rendered-cross-site-cookie-qualification) then passed six actual cross-site POST/GET cases, including null Origin, missing flow cookies, changed ordinary cookies and untrusted TLS. It grants no authentication and is not disconnected. Actual current actors, durable one-use claims, local bindings/sessions, formal admission and public/UI remain open. |
| IAM-110 | Group-to-role mapping needs the upstream identity/group reader, role authority and preview API/screens. | The [mapping and preview design](group-role-mapping.md) requires complete trusted membership, managed-grant provenance, bounded freshness and the real Workspace evaluator. A mirrored group or provider claim alone grants no role. |
| IAM-105 | ARC owns numbering and admitting the migration and its restore histories. Control startup, the public contract, the Admin UI and the federated challenge remain with their respective owners. | The [migration and startup handoff](local-multifactor-authentication.md#postgresql-migration-and-serving-handoff) verifies the unnumbered DDL, legacy upgrade, fail-closed startup, retained-factor sign-in and one-use recovery over disconnected HTTPS. |
| IAM-106 | Catalog app client registration, redirect authorization and key custody need coordination with the app catalog owner and new OAuth/OIDC operations. | The [atomic protocol experiment](catalog-oidc-provider.md#atomic-fault-and-boundary-qualification) passed 28 roots/132 leaves in native race and disconnected PostgreSQL/HTTPS runs, including actual COMMIT loss, cancellation, concurrency/disable ordering, audit/signing failure, revocation and signing rotation/expiry. All 64 OP child processes exited normally and 83 retained logs passed sensitive-value checks. Its graph remains 45 modules/57 legal files. Actual Portal/MFA authority, key-loss startup/repair, Catalog, formal contracts/schema and rendered UI remain open. |
| IAM-107, IAM-108 | Current Workspace role enums, entrance policy and each protected resource need coordinated admission. Identity-only role definitions would leave their checks unchanged. | The [actual note-write experiment](scoped-role-authorization.md#selected-family-qualification) passed four PostgreSQL roots/thirteen leaf cases in native race and disconnected runs, including membership lifetime, foreign scope, delegation, lock ordering and actual commit loss. [ADR 0075](../decisions/0075-bind-scoped-role-grants-to-membership-lifetimes.md) records the target design. The product evaluator, built-in backfill, full resource matrix, formal contracts and UI remain open. |
| IAM-109 and AUD-101 | Distribution keys require a human user, current authentication epoch and Workspace membership authority. There is no service principal in the existing tenant identity model. | Keep the real key lifecycle as the integration point. The [service account design](service-account-keys.md) records the required Distribution, Workspace and audit connections. |
| AUD-108 | SCI supplied the exact-workspace writer; IAM pairs it with the actual current-actor reader. Web qualified the action/family names in both languages against its exact complete browser source. DEV owns the exact-action query enumeration and explicit commit-unknown endpoint metadata; Delivery owns collector admission and reconciliation. | The [identity consumer](identity-audit-read-access.md) connects the published installation-wide Admin audit view to its existing current-actor transaction and server request ID. Preserve the safe response projection and record no raw query, cursor, original reason or source metadata. The actual workspace consumer is qualified with the scoped writer; keep the complete item open for its remaining public/UI and collector paths. |
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
