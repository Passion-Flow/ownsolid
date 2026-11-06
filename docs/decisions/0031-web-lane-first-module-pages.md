# 0031: The web lane moves on to the first module pages

Status: accepted.

## Context

Lane B finished the lists in [ADR 0015](0015-web-lane-after-p0.md) and
[ADR 0026](0026-web-lane-second-foundations.md), so the console's patterns exist. P0 is still open
in lane A, and the roadmap defines P1 lanes only when it closes. The console itself is still almost
empty: an administrator who signs in sees a welcome card, tasks, email delivery and settings, while
the control plane already serves nodes, users, the license and agent pacing.

The patterns were built so that module pages could use them. The next step that helps a person is
those pages, on endpoints that already exist.

## Decision

Lane B continues, in order, with:

1. **FLT-114** the Nodes page: list, filters, details and adding a node. Nothing else can be set up
   before a node exists, and the overview needs somewhere to send a person.
2. **FLT-113** the agent pacing page, whose API has run in production paths since ARC-007.
3. **IAM-115** the Users page: accounts, invitations and disabling an account.
4. **DEL-116** the License page: status, import and diagnostics. DEL-109 stays open for renewal
   and migration.
5. **UX-101** the console overview, which links to the pages above instead of to dead ends.

An item that needs a server change makes it in the module that owns the endpoint, small and additive,
and stays out of the agent session, claim and heartbeat paths lane A is working in.

## Consequences

- FLT-114, IAM-115 and DEL-116 are new roadmap items: no existing item named these pages, and their
  modules' larger items (FLT-102, IAM-101, DEL-109) stay open for what they add.
- These pages show what the API reports today. Live CPU, memory and accelerator figures in the
  prototype's node table need FLT-103 and the monitoring items, and arrive with them.
- The P1 lane plan that follows P0 starts from whatever of this list is still open.
