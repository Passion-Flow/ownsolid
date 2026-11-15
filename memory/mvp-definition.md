---
name: mvp-definition
description: "User's definition of the ownsolid MVP - every feature usable through one target (e.g. Linux for nodes), then demo, then expand"
metadata:
  node_type: memory
  type: project
  originSessionId: 5b180534-1ea7-45f4-8f52-3433974e0450
  modified: 2026-11-03T16:11:31.611Z
---

MVP (stated 2026-11-04): every capability must be genuinely usable through one complete path. Example: node management's full form covers Linux arm/amd, Mac, Windows; the MVP only does Linux well. Finishing this means the product can be demonstrated; expansion and polish follow.

Real environments (clouds, real GPUs, etc.) are best effort within what is available. The user is a one-person company and cannot test every environment; customer deployments will surface remaining bugs, which get fixed then. The user wants such targets written as supported on the product side.

Support rule (agreed 2026-11-04): write "supported" when code is complete, tests pass and there is no known failure, even without the real target. A known failure means not supported until fixed, and the response is to fix it rather than relabel it. Dameng (`SQLConnect -70064`) is therefore to be made working, not dropped.

**Why:** A one-person company cannot buy or test every target, and earlier work spent heavily on evidence and recovery rather than usable features.

**How to apply:** Prioritize usable end-to-end features over qualification records. Do not let an unavailable target block work. See [[english-only-repository]].
