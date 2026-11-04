# AGENTS.md

Instructions for AI coding agents and human contributors working in this repository.
Read this file before changing anything. `CLAUDE.md` imports it.

## 0. Start here

1. Read [`docs/product/roadmap/index.md`](docs/product/roadmap/index.md): the phase,
   the product decisions, the work order and the tick rule.
2. Open only the module file you will work on and take the next open item of the
   current phase whose dependencies are done.
   - **Before the first item of a module**, review that module: check its item list
     against current upstream documentation and comparable products, confirm the
     technical approach is still current, and add any missing capability as new items
     (with the next free IDs) before writing code. Commit the roadmap change on its own,
     and record the date of the review at the top of the module file.
3. Read [`docs/development/environment.md`](docs/development/environment.md) before
   using toolchains, servers or the tunnel.
4. When an item meets the definition of done, tick it with its evidence link, run
   `python3 tools/dev/roadmap_progress.py` to refresh the progress table, and commit
   with the item ID in the message.
5. Work through several items per session when context allows; stop at a clean commit.
6. **Decide on your own.** The owner wants sessions to finish work, not to ask.
   - Settle technical, scope, ordering and roadmap-reading questions yourself: choose
     what best fits this file and the roadmap, act, and record significant choices as an
     ADR in `docs/decisions/`. When unsure whether an item is done, apply the stricter
     reading and leave it unticked.
   - Stop and ask only before spending money, publishing or sending anything outside
     the repository and its test servers, deleting data you did not create, or when a
     credential is genuinely unavailable. Test-server access is in the environment doc.
   - Diagnose and fix failures yourself. If something cannot be fixed in the session,
     record it in `docs/product/status.md` and move to the next item you can do.
   - End a session with a short report: items finished, how they were verified, and the
     next item. Do not end with questions or suggestions the owner must answer.

## 1. Product

ownsolid is a self-hosted AI infrastructure platform: the provable control layer for
AI that customers run themselves. It manages nodes and accelerators, models (import,
deployment, fine-tuning, evaluation), a model gateway, workflow and agent
applications, an application catalog, identity, permissions and audit, with Admin
(console) and Portal entrances.

- **Fully offline.** Every capability works in an air-gapped intranet: install,
  login, models, training, catalog apps, documentation, upgrades and licensing.
  No runtime CDN, remote font, telemetry, update check or online download.
- **No built-in knowledge base.** Knowledge capabilities come from DeepParser,
  integrated as a catalog application that uses ownsolid models, identity, status
  and logs. Do not build document parsing, chunking, vector storage or knowledge
  management features.
- **Whole-platform license.** No paid feature modules or tiers. License issuance
  lives in the separate `ownsolid-entitlement` repository; this repository only
  verifies licenses and never contains signing keys or signing code.

## 2. MVP rule

Make every capability genuinely usable through one complete path before adding
variants. Example: node management ships Linux amd64 first; arm64, other operating
systems and other accelerators come later. Prefer finishing a usable feature over
adding new verification layers, receipts or ledgers.

## 3. Definition of done

A feature is done only when all of the following hold:

- A user can complete the main flow from the UI.
- State persists, and permissions are enforced on the server.
- Failures show a clear message and can be retried.
- Automated tests cover it, and it has run once end to end in an available environment.
- It works with the network disconnected.

Compiling is not done. Interface shells, TODOs and fake success responses are not
implementations.

### Quality bar

The owner requires the quality of a leading commercial infrastructure product for
both ownsolid and ownsolid-entitlement. Speed never justifies a lower bar; when time
is short, finish fewer items at this standard rather than more items below it.

- **Code:** idiomatic, small cohesive packages; no dead code, copy-paste duplication
  or unexplained magic values; linters and formatters clean; every exported API
  documented. Review your own diff as a strict senior reviewer before committing.
- **Tests:** unit tests for logic, integration tests against real PostgreSQL and real
  protocols, end-to-end tests for each user flow; failure, retry, concurrency,
  permission-denied and cross-tenant cases, not only the happy path. No flaky tests.
- **APIs:** consistent resource naming, pagination, filtering, idempotency keys for
  writes, stable error codes with actionable messages, versioned contracts.
- **Security:** least privilege, input validation at every boundary, secrets never
  logged, OWASP Top 10 and supply-chain checks considered for every change.
- **Operations:** structured logs, metrics and traces on every new path; timeouts,
  bounded retries and graceful shutdown; safe upgrades and migrations.
- **UI:** follows `docs/development/design-system.md` exactly; loading, empty, error
  and permission states on every page; keyboard and screen-reader usable; English and
  Simplified Chinese complete; no layout breaks at common widths.
- **Docs:** operator and user documentation updated with the change, written for the
  reader, accurate to what the code does.

## 4. Support claims

- Write "supported" when the code is complete, tests pass and no failure is known,
  even if the real target (a cloud, a GPU model, a domestic chip) was not available.
  Test as far as available environments allow.
- A known failure means "not supported" until it is fixed. Fix it; never relabel it.
- ownsolid is published as supporting 10,000+ GPU servers. Simulated-node load tests
  back that claim and are recorded as "load tested with N nodes". Results on real
  hardware or customer environments are recorded separately with their exact scope.
- Status records only what was actually run. Never fabricate a run result. Track
  per-capability status in `docs/product/status.md` and the roadmap boxes.

## 5. Architecture and stack

| Part | Technology | Location |
| --- | --- | --- |
| Control plane | Go | `services/control` |
| Node agent | Go | `services/agent` |
| Model gateway | Rust | `services/gateway` |
| AI runtimes | Python | `runtimes/` |
| Console and Portal | TypeScript, React, Vite, TanStack, shadcn/ui, Tailwind CSS | `web/` |

- Go module paths start with `github.com/Passion-Flow/ownsolid/`. `pkg/license`
  defines the license format and verification and is also imported by
  `ownsolid-entitlement`; change it only with both repositories in mind.
- Keep orchestration separate from training and inference backends.
- Before changing a component, read its code, its contract in `api/` and its README.
  When code and docs disagree, the code wins and the doc gets fixed in the same change.
- Do not split services early or create empty modules.

## 6. Repository layout

Every file has exactly one correct home. If none fits, propose a new home before
creating the file. `make check-layout` enforces the rules below in CI; its
allowlists live in `tools/lint/layout/rules.json` and change together with this section.

```text
ownsolid/
├── .github/workflows/     CI and release pipelines
├── api/                   Versioned contracts, the source of truth for generated code
│   ├── openapi/           HTTP APIs, one directory per domain
│   ├── agent/             Control plane to agent protocol
│   └── <domain>/          Other contracts: datasets, training, release, configuration
├── catalog/apps/<app>/    Application catalog definitions, one directory per version
├── deploy/
│   ├── components/        Recipes for components installed on nodes
│   ├── compose/           compose.yaml and the repository's only .env.example
│   ├── helm/ownsolid/     Helm chart
│   ├── images/<image>/    One Dockerfile per image
│   ├── offline/           Offline bundle manifest and installer
│   └── systemd/           Units for host-installed components
├── docs/
│   ├── product/           Scope, capabilities, status
│   ├── architecture/      Current architecture
│   ├── operations/        Install, upgrade, backup, offline delivery
│   ├── development/       Setup and conventions
│   └── decisions/         Architecture decision records: NNNN-title.md
├── pkg/<name>/            Go libraries shared by more than one service
├── runtimes/<runtime>/    src/, tests/, pyproject.toml, locks/
├── services/
│   ├── control/           cmd/, internal/{app,modules,platform}/, migrations/, config/
│   ├── agent/             cmd/, internal/, config/
│   └── gateway/           src/, tests/, config/
├── tests/
│   ├── e2e/               Cross-service end-to-end tests only
│   └── fixtures/          Test data shared by more than one component
├── tools/                 codegen/, dev/, lint/, release/
└── web/                   apps/{console,portal}/, packages/{ui,api,i18n,config}/
```

- **Root:** only `README.md`, `AGENTS.md`, `CLAUDE.md`, `LICENSE`, `Makefile`,
  `go.work`, `.gitignore`, `.gitattributes`, `.editorconfig` and the directories above.
- **Configuration:** each service keeps one commented example in its `config/`.
  Deployment variables live only in `deploy/`.
- **Tests:** unit tests sit beside the code (`_test.go`, `*.test.ts`, Python `tests/`).
  Test data used by one component goes in a `testdata/` directory next to its test.
- **Generated code:** only in directories named `gen/`, marked "do not edit".
- **Names:** lowercase paths. Directories and docs use kebab-case; Go and Python
  files use snake_case; TypeScript files use kebab-case. Conventional names such as
  `README.md`, `Makefile`, `Dockerfile` and `Cargo.toml` keep their usual spelling.
- **One README per responsibility root**, not per helper directory.
- **Vendored code** lives in `third_party/` directories and keeps its original names,
  language and license text; `locales/` directories keep locale-specific names.

## 7. Language

Everything is written in English: code, identifiers, comments, docs, configuration,
error codes, log messages, commit messages and file names. The only exception is
internationalization: translated product text lives only in directories named
`locales/`. Unicode test input in code is written with escape sequences.
Unmodified third-party files are not ours to translate. Replies to the project owner
are in Chinese.

## 8. Configuration and secrets

- All deployment-, customer- and policy-dependent values enter through the owning
  component's typed configuration, validated at startup. Unknown or invalid values
  fail clearly. Parse booleans explicitly.
- Changing deployment configuration never requires editing source or rebuilding.
- Secrets are file references only. They never appear in source, examples, command
  lines, logs or browser bundles. There are no default passwords or keys.
- Listen addresses and advertised origins are configured separately. HTTP or HTTPS,
  hostname or IP are explicit choices. Never bypass certificate verification.
- Pin dependency versions and image digests. Never use `latest`.

## 9. Security invariants

- Identity, tenant and workspace scope come from the authenticated server context,
  never from client input.
- Authentication, authorization, CSRF protection and tenant isolation are never
  configuration switches.
- Every privileged action is audited.

## 10. Git

- `main` is the only long-lived branch. Temporary branches are deleted after merge.
  Parallel work uses separate worktrees with explicit path ownership.
- Commit each coherent change. Review the diff first and stage explicit paths;
  never `git add .` or `git add -A`. Check for secrets and large files.
- Commit messages are English, imperative, and state what was verified.
- Push at meaningful stages with a fast-forward update. Never force-push or rewrite history.
- Models, datasets, databases, dependencies and build output never enter Git.

## 11. Environment and operational safety

- Never hardcode machine paths or resource limits; read them from configuration.
- Do not buy resources, upload real data, expose services publicly or send external
  messages.
- Start only the services a task needs, and check resources before heavy jobs.
- Clean up only resources you created. Never run `docker system prune`, `git clean`
  or wildcard deletes. Delete a database only when it is a disposable fixture you own.

## 12. Documentation

- `README.md` is the visitor-facing product page. It contains no development status
  or verification records.
- `docs/product/status.md` is the single status table: implemented, run end to end,
  verified in a customer environment, and known failures.
- Record significant decisions as ADRs in `docs/decisions/`. Do not keep dated logs
  or progress diaries in the repository.

## 13. Finishing a task

Report in Chinese: what changed and where, what was verified and how, how to run it,
and what remains.
