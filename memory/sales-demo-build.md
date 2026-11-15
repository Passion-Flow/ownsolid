---
name: sales-demo-build
description: "Separate click-through demo at Development/ownsolid-demo replacing novatra-demo on the demo server, for a customer meeting on Tuesday 2026-11-10"
metadata:
  type: project
---

Requested 2026-11-05: a demo separate from mainline where every roadmap capability can be clicked and seen, used as the external showcase until the full product exists. It replaces the old `novatra-demo` on 118.89.84.188 (public port 8090). The customer meeting may be Tuesday 2026-11-10.

Progress and remaining steps live in `Development/ownsolid-demo/README.md`. Login uses the key `~/.ssh/ownsolid_demo_ed25519`; the password is never stored.

**Why:** Core features alone would not look like enough to a prospect.

**How to apply:** Do not touch ownsolid mainline for this. Stop the old demo without deleting it. The server also runs Hermes, browser-vnc and a shared Caddy that must stay untouched. There is no sample data and no "simulated data" label any more; see [[demo-data-modes]]. Do not show planned load tests as results. See [[product-direction]].
