---
name: roadmap-workflow
description: ownsolid work now runs from the checkbox roadmap; new chats start in Development/ownsolid and follow AGENTS.md section 0
metadata:
  node_type: memory
  type: project
  originSessionId: 5b180534-1ea7-45f4-8f52-3433974e0450
  modified: 2026-11-03T22:22:08.344Z
---

Since 2026-11-04 (ownsolid commit e3d01bb), all ownsolid work follows `docs/product/roadmap/index.md`: 435 items in 24 modules and phases P0 to P4. The current phase is P0. The user opens a fresh chat per batch of work in `Development/ownsolid` and pastes a standard prompt.

Owner decisions recorded in the index: both customer types (enterprise first, operator commerce in P2); billing follows the planned pace; Kubernetes per cell; publish "supports 10,000+ GPU servers" backed by simulated load tests; demo decided later; Claude's technical recommendations were accepted for scheduler, 1Panel scope, accelerators, agent tiers and telemetry.

The user rejected permanent "pending verification" labels: record "load tested with N nodes" from real simulated load tests, and record hardware or customer results separately and only when they were actually run.

**Why:** Keeps each chat's context small and makes progress auditable. **How to apply:** In a new chat, read AGENTS.md section 0, the roadmap index and the one module being worked on. See [[product-direction]] and [[dev-environment]].
