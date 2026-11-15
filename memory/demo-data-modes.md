---
name: demo-data-modes
description: ownsolid-demo has no sample data and no mode switch; it starts as a fresh installation and real data comes from one RTX 4090
metadata:
  node_type: memory
  type: project
  originSessionId: 300495c2-1eae-4a3b-96b5-1445657ef07d
  modified: 2026-11-06T23:02:21.131Z
---

Decided by the user on 2026-11-07: ownsolid-demo looks exactly like the real product and starts as a just-deployed installation. There is no sample data, no sample/fresh switch, no "all features" page and no reference lists of roadmap items. The user will connect one RTX 4090 24G after the UI work, and the data from that testing is the demo data.

**Why:** The user wants it to read as the real product, not as a demo, and rejected built-in rows as fake.

**How to apply:** Never add built-in rows or figures; `scripts/check-coverage.mjs` fails on rows without a `source`. Actions with no engine show "not connected" and never claim success. The small backend in `ownsolid-demo/server` is deliberately separate; do not wire in the production backend. See [[sales-demo-build]].
