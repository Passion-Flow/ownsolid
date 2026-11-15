---
name: product-direction
description: "User decisions of 2026-11-04 on ownsolid positioning, model scope, app deployment, DeepParser binding, license issuer and redesign"
metadata:
  node_type: memory
  type: project
  originSessionId: 5b180534-1ea7-45f4-8f52-3433974e0450
  modified: 2026-11-04T10:51:26.119Z
---

Decisions stated 2026-11-04:
- Positioning accepted: "provable control layer for AI you run yourself".
- The WHOLE product must run fully offline (air-gapped intranets in China and abroad), not only training and fine-tuning. That means no CDN, remote fonts, telemetry or online downloads at runtime.
- Two separate products, each in its own directory: ownsolid (customer product) and the license issuance platform (vendor-internal).
- Directory layout must be strict: every file has a defined home. Fine-grained is fine, messy is not; the user is very particular about this.
- UI: shadcn/ui and Tailwind, chosen by Claude. After prototype review the user picked warm neutrals with a teal accent, the Figtree font and Lucide icons; no playful or childish icons. Ease of use is a core value. Recorded in ownsolid `docs/decisions/0002-frontend-stack.md` and `docs/development/design-system.md`.
- Model scope: eventually every model type (chat, embeddings, rerank, vision, speech and so on). Embeddings is only the first step.
- Knowledge: only DeepParser, no other knowledge product.
- Add 1Panel-like application deployment (catalog, one-click install, upgrade, status, logs) so ownsolid is the infrastructure layer. DeepParser is deployed through ownsolid and deeply bound: it uses ownsolid models, and ownsolid shows its status and logs.
- A separate manufacturer-side license issuance platform will be built to manage licenses.
- Company, sales and partner matters are out of scope; the user handles them. Focus on the product.
- The user wants a high-quality, standard redesign of the frontend UI and the repository directory layout, for example no `.env` files at the repository root.

Scope additions (2026-11-04):
- Scale ambition: manage large data centers with thousands to tens of thousands of GPU servers; the user wants this designed in from the start.
- Every operation happens inside the product (logs, web terminal and so on), like Rancher. Decided: build 1Panel's capabilities natively in ownsolid. 1Panel itself is not shipped, not embedded and not offered as a catalog app. It is GPL-3.0 with trademark terms, so use it only as a feature reference and copy no code, app files or branding.
- One product-wide AI agent: customers direct it to open features, deploy models and more, instead of clicking.
- The user expects proactive thinking about the whole product, not only the examples they name.
- Planned working method: a split, checkbox roadmap under `docs/product/roadmap/` that AGENTS.md tells each new chat to read, so work can continue across chats with little context. Research notes are in `01-Work/Inbox/2026-11-04-ownsolid-fleet-scale-completeness/`. The roadmap is not written yet: research first, then the user decides.

Quality bar (2026-11-04, stressed as non-negotiable): both ownsolid and ownsolid-entitlement must meet big-tech, leading-commercial-product quality. Written into ownsolid AGENTS.md section 3 "Quality bar" (commit e0a120a) and entitlement AGENTS.md (bceae5b). The user asked whether all 438 roadmap items can be done by 2026-11-30 only out of curiosity; there is no deadline, work follows its own pace. Never cut roadmap features; scope may grow. Missing hardware, customers or external parties never block an item: done in available environments means supported (roadmap index rule, commit 4cd8d24). Inherited project-one code is improved while migrated, never moved as-is (AGENTS.md Quality bar, 74fa771). The user insists every such rule live in repository files, not only in memory, because other chats cannot see memory.

Pace (2026-11-04): about 2026-11-15 is a goal for a demo-ready v0.1 so sales can start in parallel, but the user is not in a hurry. Quality and high standards come first, one step at a time. Workflows/agents and offline fine-tuning may land after 11/15. The DeepParser integration comes last, after the rest of the product is done.

**Why:** The user wants a strong product that becomes the base other AI products run on, starting with DeepParser.

**How to apply:** Understand the whole project first, then make the MVP usable, and expand later. 1Panel is GPL-3.0 with trademark restrictions, so design an own catalog format and copy no code or app files. A 2026-11-04 search found no Dify-derived code. See [[mvp-definition]], [[knowledge-not-a-selling-point]] and [[positioning-research]].

Research before building (2026-11-06, the owner stressed it): every feature gets detailed research and a proven technical basis before code, then is built stable and high quality; no guesswork. Written into ownsolid AGENTS.md section 0 and the roadmap tick rule; notes live in `docs/research/` (commit b3fab35). The same commit added scientific computing (task-type models, life-science packs) and GxP, KMS, ELN/LIMS, cloud GPU pool items from a real customer request (a vaccine maker's research AI platform bid); the owner wants real customer needs added to the roadmap.
