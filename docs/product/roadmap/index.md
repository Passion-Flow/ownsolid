# ownsolid roadmap

The engineering ledger: every item, its phase, and the evidence that it is done.

## Before you start

This page is the engineering ledger: what is done, with evidence. It is no longer
enough on its own to start work on anything with a screen.

- [The interaction system](../design/README.md) — how a screen behaves: surfaces,
  the five page archetypes, the seven states, tokens and motion. Read it first.
- [The build plan](../plan/README.md) — what gets built in what order, with each
  screen's purpose, dialogs and click paths specified before it is written.

An item here that touches a screen waits for that screen's entry in the plan. The
two meet at the end: the item ticks when the code is merged and tested, and the
screen is done when it also behaves the way the design says.

## How to work

1. Find the current phase below, then the first module in **Work order** that still has
   open items in that phase.
2. Open that module file and pick the first unticked item of the current phase whose
   dependencies are ticked.
3. Research it first (AGENTS.md section 0), then build it to the definition of done,
   tick it and add the evidence link.
4. Update the **Progress** table on this page and commit with the item ID in the message,
   for example `SCH-004: Add hierarchical quota tree`.

## Parallel lanes

Items in different lanes touch different code and can run in separate sessions at the
same time; items inside a lane run in order. A session told to work on a lane takes the
next open item of that lane; a session without a lane works in lane A. Each extra lane
works in its own worktree (AGENTS.md section 10).

| Lane | Items, in order | Code it owns |
| --- | --- | --- |
| A. Fleet core | ARC-006 to ARC-016, then REL-001; in P1, the open items of the work order, starting with ARC-101 | `api/agent`, agent client and runtime, control platform and node modules, `tools/dev/simulator` |
| B. Web console | UX-001 to UX-005, then tick DEL-001; then the P1 console foundations UX-115, UX-116, UX-112, UX-118, UX-120, UX-119, UX-113, UX-110, UX-109 ([ADR 0015](../../decisions/0015-web-lane-after-p0.md)); then UX-121, UX-103, UX-114, UX-107, UX-105, UX-104 ([ADR 0026](../../decisions/0026-web-lane-second-foundations.md)); then FLT-114, FLT-113, IAM-115, DEL-116, UX-101 ([ADR 0031](../../decisions/0031-web-lane-first-module-pages.md)); then AUD-110, IAM-116, IAM-117, UX-122, UX-106 ([ADR 0034](../../decisions/0034-web-lane-governance-pages.md)); then UX-123, UX-124, UX-128, UX-126, UX-125, UX-127, UX-129 ([ADR 0039](../../decisions/0039-web-lane-navigation-and-lists.md)); then CMP-215, UX-102, UX-111, UX-108, UX-117 ([ADR 0041](../../decisions/0041-web-lane-truth-and-portal.md)) | `web/`, `deploy/images/control` web build, `docs/development/design-system.md` |
| C. Delivery and telemetry | DEL-002, OBS-001, OBS-002, REL-002, DEL-003 | `tests/e2e` egress harness, `deploy/`, monitoring modules, `services/control/internal/operations` |

DEL-003 changes agent container networking: coordinate with lane A by keeping the change
inside the runtime's network setup. P0 closed with REL-001: lane A continues in the work order,
lane B followed [ADR 0044](../../decisions/0044-web-lane-follows-the-build-plan.md) and now follows [ADR 0049](../../decisions/0049-web-lane-publishes-models-and-hands-out-keys.md), and lane C is finished.

## Definition of done (the tick rule)

Tick a box only when all of these hold. Never tick partly done work.

- Its research note in `docs/research/` is current and the build follows it.
- The implementation is merged on `main`, with no stubs, TODOs or fake success paths.
- Automated tests cover it and pass; `make check`, `make build` and `make test` pass.
- It works with the network disconnected.
- Configuration, API contracts, docs and `docs/product/status.md` are updated together.
- The user-facing part is reachable from the console or portal UI, when the item has one.
- The evidence link points to the commit or test run that proves it.
- A row in `pkg/capabilities/declaration.json` that names the item moves with the tick; `make test` fails until it does ([ADR 0042](../../decisions/0042-capability-declaration-follows-the-roadmap.md)).

Item format: `- [ ] ID Title (phase) - Catalog: Fnnn` and when done
`- [x] ID Title (phase) - Catalog: Fnnn - Evidence: <commit or run>`.

**Scale and hardware results are their own items.** "Load tested with N simulated
nodes" and "Verified on <named hardware or customer environment>" are separate boxes.
Status records only what was actually run; no item claims more than its evidence shows.
A capability counts as supported once its implementation items are ticked (AGENTS.md
section 4). Verification boxes for hardware, customers or scale that no available
environment can run never block other items, a phase or a support claim: skip them,
keep them open, and tick them when the run happens.

## Product decisions

| Topic | Decision |
| --- | --- |
| Customers | Both enterprise private clusters and compute-center operators. Enterprise features come first; operator commerce follows in P2. |
| Billing | Usage ledger, rate cards, chargeback, budgets and prepaid or offline top-up in P1; full operator commerce in P2. |
| Fleet substrate | One Kubernetes cluster per cell (2,000 to 5,000 nodes). The ownsolid agent handles bare metal, health, host operations, drivers and firmware. A fully custom scheduler is considered only after the business is profitable. |
| Published scale | ownsolid is published as supporting 10,000+ GPU servers. Load tests back the claim: 2,000 simulated nodes in P0, 10,000 in P2 and 20,000 in P3. |
| Scheduler | Adopt Kueue and Volcano per cell; ownsolid owns admission, the quota tree and topology inventory behind an adapter boundary. |
| In-product operations | Build 1Panel's operations capabilities natively. 1Panel is a feature reference only: never embedded, forked, shipped or offered as an app, and no code is copied. Web hosting features (websites, PHP, FTP, antivirus, WAF) are deferred. |
| Accelerators | NVIDIA on Linux amd64 first; Ascend in P2. The device model supports both from the start. |
| Product agent | Navigate, read and plan-then-confirm writes in P1; second-approver tiers in P2; autonomous runbooks later. A deterministic command layer works without any model. |
| Knowledge | DeepParser only, installed through the app catalog. ownsolid builds no knowledge base. |
| Models | Every model type over time; embeddings and rerank count as model serving. |
| Telemetry and logs | A dedicated time-series store and log store, never the control-plane database. Chosen by license, offline packaging and a load test at about 17 million series: VictoriaMetrics and VictoriaLogs, open-source editions (ADR 0009). |
| Scientific computing | Models that run as jobs on files (structure prediction, design, sequence optimization) use one task-type contract on the job model; domain packs, life sciences first, build on it. Models with restrictive terms run only under the customer's licence ([research](../../research/scientific-computing.md)). |
| What is in each entrance | Who the person is decides the entrance, not which subsystem a feature belongs to. A capability usually appears in both, with different scope and verbs: the console allocates and limits across every tenant, the portal submits and calls within one workspace ([ADR 0035](../../decisions/0035-what-belongs-in-each-entrance.md)). |
| Entrances | Four: admin console, portal, model gateway and agent endpoint. Each has its own hostname; four hostnames on one TLS port is the default, a listener per entrance is configurable, and two browser entrances never share an origin ([ADR 0027](../../decisions/0027-entrance-addresses.md)). |
| Demo | Decided later. Build and polish first. |

## Phases

| Phase | Goal | Exit criteria |
| --- | --- | --- |
| **P0 Foundation** | One topology model replaces the old profiles; agent protocol v1; scale invariants; simulator; new web console foundation | Existing behavior still passes; 2,000 simulated agents steady at twice the load; full reconnect within 5 minutes; a control-plane restart does not stop running work; one install path produces every former profile |
| **P1 Enterprise release** | Every P1 item: one site, one cell, NVIDIA amd64, complete in-product operations and product agent | Supports 1,000 nodes and is load tested at 2,000; offline install, upgrade and rollback pass; console and portal cover every P1 capability |
| **P2 Fleet operations** | Automated remediation, fabric and storage operations, multiple cells, Ascend, operator commerce, DeepParser integration | Load tested with 10,000 simulated nodes across at least two cells; failure drills pass |
| **P3 Multi-site** | Global layer, cross-site quota and routing, site autonomy | Load tested with 20,000 simulated nodes across simulated sites; a WAN cut leaves every site running |
| **P4 Breadth** | All model types, advanced serving, compatibility-matrix entries | Per item |

**Current phase: P1.**

## Work order and progress

Work modules in this order inside a phase. Update the counts when you tick items.

| # | Module | File | P0 | P1 | P2 | P3 | P4 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | Architecture and scale | [architecture-and-scale.md](architecture-and-scale.md) | 16/16 | 3/16 | 0/7 | 0/4 | - |
| 2 | Delivery and upgrades | [delivery-and-upgrades.md](delivery-and-upgrades.md) | 3/3 | 8/17 | 0/4 | 0/1 | 0/1 |
| 3 | Identity and access | [identity-and-access.md](identity-and-access.md) | - | 3/19 | 0/6 | 0/1 | 0/1 |
| 4 | Usability and localization | [usability-and-localization.md](usability-and-localization.md) | 5/5 | 26/30 | 0/4 | - | 0/2 |
| 5 | Fleet and infrastructure | [fleet-and-infrastructure.md](fleet-and-infrastructure.md) | - | 2/15 | 0/11 | 0/2 | - |
| 6 | GPU operations | [gpu-operations.md](gpu-operations.md) | - | 0/9 | 0/8 | - | 0/2 |
| 7 | Scheduling and quota | [scheduling-and-quota.md](scheduling-and-quota.md) | - | 0/11 | 0/7 | 0/2 | 0/1 |
| 8 | In-product operations | [in-product-operations.md](in-product-operations.md) | - | 0/14 | 0/6 | - | 0/2 |
| 9 | Models and registry | [models-and-registry.md](models-and-registry.md) | - | 1/17 | 0/6 | - | 0/3 |
| 10 | Serving | [serving.md](serving.md) | - | 1/15 | 0/7 | 0/1 | 0/4 |
| 11 | Gateway | [gateway.md](gateway.md) | - | 3/22 | 0/7 | 0/2 | 0/2 |
| 12 | Training and fine-tuning | [training-and-finetuning.md](training-and-finetuning.md) | - | 0/11 | 0/5 | - | 0/4 |
| 13 | Data | [data.md](data.md) | - | 0/7 | 0/4 | - | 0/2 |
| 14 | Evaluation | [evaluation.md](evaluation.md) | - | 0/6 | 0/3 | - | 0/2 |
| 15 | Applications and agents | [applications-and-agents.md](applications-and-agents.md) | - | 0/11 | 0/7 | - | 0/3 |
| 16 | Product agent | [product-agent.md](product-agent.md) | - | 0/12 | 0/3 | - | 0/2 |
| 17 | App catalog | [app-catalog.md](app-catalog.md) | - | 0/8 | 0/7 | - | - |
| 18 | Tenancy, metering and commerce | [tenancy-metering-commerce.md](tenancy-metering-commerce.md) | - | 0/9 | 0/8 | 0/2 | - |
| 19 | Audit and compliance | [audit-and-compliance.md](audit-and-compliance.md) | - | 3/11 | 0/7 | - | - |
| 20 | Observability and alerting | [observability-and-alerting.md](observability-and-alerting.md) | 2/2 | 0/13 | 0/5 | 0/1 | - |
| 21 | Platform reliability | [platform-reliability.md](platform-reliability.md) | 2/2 | 0/13 | 0/4 | 0/2 | - |
| 22 | Network and storage | [network-and-storage.md](network-and-storage.md) | - | 0/6 | 0/7 | - | 0/1 |
| 23 | Developer experience | [developer-experience.md](developer-experience.md) | - | 1/8 | 0/5 | - | 0/1 |
| 24 | Compatibility matrix | [compatibility-matrix.md](compatibility-matrix.md) | - | 1/2 | 0/11 | - | 0/13 |
| 25 | Scientific computing | [scientific-computing.md](scientific-computing.md) | - | 0/9 | 0/10 | - | - |
| 26 | Portal | [portal.md](portal.md) | - | 1/14 | 0/3 | - | - |

## Sources

- Research behind this plan: the owner's Inbox reports `2026-11-04-ownsolid-must-have-capabilities` and
  `2026-11-04-ownsolid-fleet-scale-completeness`. They are working notes outside this repository.
- The original 655-item scope catalog, in Chinese, lives in the archived `project-one`
  repository at `docs/research/full-scope.json` (F001 to F565) and
  `docs/research/platform-evolution/catalog.json` (F566 to F655). Catalog IDs on items
  point there. Vendor-named adapter rows are collected in the compatibility matrix;
  knowledge-base rows (F009 to F033, F576 to F579) are retired in favor of DeepParser.
- Real-GPU results from 2026-11-04 (RTX 3090): the inherited vLLM path and offline LoRA
  training worked; the gaps found are items in `serving.md`, `training-and-finetuning.md`
  and `delivery-and-upgrades.md`.
