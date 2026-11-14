# Lane M boundaries

The current offline import flow accepts the one embedded Qwen3-0.6B revision.
The roadmap boxes below remain open. Preserve the current contracts until the
responsible owners can extend their respective boundaries.

| Item | Boundary and continuation |
| --- | --- |
| MOD-101 | The models module resolves the legacy digest for upload, archive verification, publication and recovery. The two real archives exercised fail-closed admission and Qwen replay. The next slice needs the shared multi-entry API, immutable catalogue persistence and digest-bound Agent transfer in the [contract handoff](model-registry.md#contract-handoff). |
| MOD-102 | The Admin catalogue endpoint returns a single approved descriptor, and the existing UI facets cover that descriptor or already imported rows. Type and capability coverage for the catalogue needs a multi-entry read contract and its UI consumer. |
| MOD-103 | Retained training and holdout references use the existing publication graph. The [staged family/history domain](training-dataset-lineage.md#durable-family-domain-contract) now supplies unnumbered atomic membership DDL, signed bounded keysets and current-authority ready/retired reads with [PostgreSQL evidence](training-dataset-lineage.md#postgresql-family-domain-acceptance). ARC must admit migration/roles/catalog and retained-identity rollback; DEV and the route owner must admit public schemas and dispatcher, and Web owns S-202. Evaluation remains with SCI. |
| MOD-104 | Chat-template defaults need to reach the serving engine with an explicit command or request policy. The Agent inference adapter and its wire command are shared boundaries. |
| MOD-105 | The preparation worker accepts the existing pinned recipe. LoRA merge and format conversion need an admitted recipe and an Agent runner that can dispatch and verify the new output; the current runtime alone cannot make the job usable. |
| MOD-106 | Serving variants require recipe admission and a serving manifest accepted by the Agent, including matching quantization arguments and validation. |
| MOD-107 | Deployment and alias reference checks run in the shared retirement callback. [Both commit orders](model-retirement-dependencies.md#disconnected-postgresql-acceptance) passed with actual deployment and retirement services. Complete application and key references require their owning modules to participate, and the existing UI only receives a generic refusal. |
| MOD-108 | The [approved import review](model-import-file-safety.md#disconnected-postgresql-acceptance) refuses executable members and renamed pickle before extracting any payload. Generic catalogue admission and richer review require MOD-101 and a shared verifier contract. |
| MOD-109 | A complete-file request compares its digest with the pinned catalogue. A bounded range of a larger file carries its own response digest; the Agent verifies the assembled file against the catalogue before use. [Disconnected TLS and SQL acceptance](model-transfer-integrity.md#disconnected-tls-and-postgresql-acceptance) covers complete-file substitution, two-range assembly, descriptor refusals and retry. Site-registry pre-warming still requires the Agent owner. |
| MOD-110 | Deployment admission checks the reviewed Qwen catalogue and on-site use. The [consumer handoff](model-license-admission.md#consumer-handoff) defines the startup configuration, public reasons, training and SCI launch checks still needed. |
| MOD-113 | The signed index needs a trust root and delivery integration, plus a new multi-entry contract consumed by the UI. No unsigned index should replace the embedded trust decision. |
| MOD-114 | Offline search depends on MOD-113. The browser's current local filter covers imported inventory. |
| MOD-115 | The exact pinned fetch manifest depends on MOD-113. The connected-side command belongs in tools/cli, outside this lane. |
| MOD-116 | Multi-model publication and tokenizer staging depend on MOD-101 and MOD-113. The Agent must verify the same pinned files and serve exclusively from the staged path. |
| MOD-121 | The [expiry handoff](model-import-cleanup.md#mod-121-abandoned-upload-retention) identifies the typed Models policy and public expiry reason. The worker candidate reuses the existing import locks and bounded discard and passed [disconnected PostgreSQL acceptance](model-import-cleanup.md#postgresql-expiry-acceptance). Admin HTTPS detail, list, resolution and replay expose the terminal reason. See the [operator handoff](model-import-cleanup.md#operator-projection-and-owner-handoff) for the separate Problem, startup policy and localized Console changes. |
| MOD-101 / MOD-119 archive instructions | The console's BSD tar command on macOS emits USTAR headers that Go classifies as unknown, even when every pinned payload hash matches. The existing importer accepts canonical USTAR. Use GNU tar or Python's `tarfile.USTAR_FORMAT` for the verified fixture. Updating the published console instructions or widening the shared archive verifier belongs to their owners. |

## Serving P1

| Item | Boundary and continuation |
| --- | --- |
| SRV-101 | Deploy and observe are implemented for the approved Agent recipe. Generate, cancel, metrics and unload across engines need a shared runtime and gateway contract. |
| SRV-102 | The current vLLM recipe and transfer are pinned to the approved Qwen revision. General import (MOD-101) and verified model-specific Agent configuration are prerequisites. |
| SRV-103 | Embedding and rerank need imported model types, Agent tasks and gateway request/response support. |
| SRV-104 | Writable compiler and CUDA cache mounts are set on the inference container by the architecture lane. Qualification needs a real GPU with egress blocked. |
| SRV-105 | Multiple replicas and rolling replacement need shared deployment commands, per-replica readiness and gateway route draining. Reserve spare capacity before promotion. |
| SRV-106 | Queue depth and latency come from engine and gateway telemetry. Scale to zero also needs a wake-up path for new requests. |
| SRV-107 | Hot LoRA requires Agent runtime changes and matching generation semantics in the gateway. |
| SRV-108 | CPU serving requires an offline CPU image and Agent execution profile. The current Agent preflight requires a supported GPU. |
| SRV-109 | Gateway and engine owners need to propagate cancellation and deadlines across live response streams. |
| SRV-110 | The available test hosts have no qualified NVIDIA GPU for the required end-to-end run. |
| SRV-112 | Names and descriptions need versioned request and response fields and web integration. A database column alone leaves the feature inaccessible. |
| SRV-113 | Preserve recorded revision and admission state, then roll back through the health-gated route switch and drain in SRV-105. An in-place restart cannot preserve in-flight traffic. |
| SRV-114 | Deployment metrics and logs need observability and gateway producers. Scale and rollback dialogs and the rolling progress view need the web lane. |
| SRV-115 | A conservative fit breakdown needs qualified engine profiles, current hardware facts and a shared preview contract. See [serving admission research](serving-admission-and-rollout.md). |

## P2 boundaries

| Items | Prerequisites and ownership |
| --- | --- |
| MOD-117, MOD-118 | A pinned and verified source manifest (MOD-113 and MOD-116) and a configured acquisition policy. Relay and direct network access need explicit origin, redirect and DNS constraints. |
| MOD-201 | The in-site registry and Agent pre-warming (NET-102 and MOD-109) precede peer distribution and rack seeding. |
| MOD-202 | Publishing requires the signed model index/bundle flow and the separate application bundle owner. |
| MOD-203 | Checkpoint and evaluation report retention depend on training, SCI and backup policy. |
| MOD-204 | A per-version compatibility declaration depends on the model version/manifest contract and admission consumers. |
| SRV-201, SRV-202, SRV-203 | Cache-aware routing, disaggregation and cache tiers depend on replica routing (SRV-105) and engine/gateway instrumentation and isolation. |
| SRV-204, SRV-205, SRV-206 | Alternative engines, Ascend and parallel workers require the Agent engine contract (SRV-101) and hardware execution profiles. |
| SRV-207 | Traffic splitting and rollback depend on ready replicas and revision routing (SRV-105 and SRV-113). |

## Shared Help gate

Clean upstream `e8c853c0` reproduced two failing cases in
`web/packages/help/src/help.test.ts`.
`pnpm --dir web --filter @ownsolid/help test` returned 17 passed
and two failed in an archive containing only upstream files.

- `console API reference > lists exactly the operations the contracts give this entrance`
- `portal API reference > lists exactly the operations the contracts give this entrance`

The raw Nodes comparison expected these four operation paths under Nodes. The Help index assigns them to Components.

- `GET /api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes/{node_id}/components/node-exporter`
- `GET /api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes/{node_id}/components/node-exporter/logs`
- `POST /api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes/{node_id}/components/node-exporter/actions`
- `GET /api/v1/sites/{site_id}/cells/{cell_id}/workspaces/{workspace_id}/nodes/{node_id}/components/node-exporter`

The Help package, generator and Nodes contract had the same contents at
`0c78eb79`. Commit `9d25ee7c` uses the canonical path resolver in the
comparison. All 19 Help tests then passed on clean main and in the
full gate on `d4095c34`. The MOD-121 and MOD-107 slices also passed
disconnected PostgreSQL after rebasing onto the updated catalogue
admission and node code. They reached main at `e97a4fb4` and `33bbf870`.

## Shared full-test order

Follow the current resource handoff order. The carrier at
`/private/tmp/ownsolid-workstation-full-gate.lock` is a persistent regular
file owned by the current user. Reject symlinks and an occupied directory;
acquire a nonblocking exclusive `fcntl.flock` and retain that file descriptor
for the entire gate or publication window. Release it by unlocking and
closing the descriptor. Never unlink, replace or truncate the carrier.
Inspect the legacy `/private/tmp/ownsolid-macos-full-gate.lock` and actual
processes before admitting one heavy workstation gate. Product-target tests separately
acquire `/run/lock/ownsolid-heavy.lock` on the Linux test server after
resource checks. Release the workstation window once its own heavy
work is complete when Linux is occupied.

Use the preinstalled Python 3.12 from the
[development environment](../development/environment.md#workstation).
The system Python 3.9 cannot parse the SDK generator's `match` syntax.
On a workstation below the object store's 30 percent free-space
threshold, use an owned APFS test volume and a short canonical temporary
path. HFS+ timestamp precision and long Unix socket paths are unsuitable
for the existing full suite. Retain each failed environment attempt
separately from the completed gate.

The rebased model and deployment directories matched the preserved
candidates byte for byte. `a3b3747d` on baseline `67293e12` passed
`make check`, `make build` and `make test` with Python 3.12 and a short
APFS temporary path. Chromium passed 1,075 Console and 389 Portal cases,
with the installation-dependent skips retained. Fresh disconnected
Linux amd64 binaries then passed the scoped archive, transfer and
expiry roots described in their research notes. The previous temporary
logs and assets were unavailable at resume, so these are fresh runs.

The publication root explicitly excludes its snapshot-and-retirement
subtest. Go returned success after exercising publication rollback,
fresh-worker recovery and cleanup recovery, while also emitting a
no-subtests warning for that exclusion. The initial harness treated
that warning as a failure. Its exit and raw log remain preserved. The
corrected predicate admits this explicit exclusion only after checking
the passed root. Independent validation confirmed all six selected Go
commands, then confirmed consumer exit and disposed of the owned
PostgreSQL container, temporary archive, model store and private inputs.
