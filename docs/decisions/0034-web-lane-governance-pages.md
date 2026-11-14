# 0034: The web lane moves on to the governance pages

Status: accepted.

## Context

Lane B finished the list in [ADR 0031](0031-web-lane-first-module-pages.md), so an administrator can
add nodes, invite people, import a license and land on an overview that links to all of them. P0 is
still open in lane A, and the roadmap defines P1 lanes only when it closes. The navigation's
Governance group is mostly disabled, while the control plane already serves the audit trail,
workspaces and single sign-on connectors, so these reach people soonest.

## Decision

Lane B continues, in order, with:

1. **AUD-110** the Audit trail page. Every privileged action is already recorded and no one can read
   it from the console, which is the first thing a security review asks for.
2. **IAM-116** the Workspaces page: the switcher lists workspaces that nothing in the console manages.
3. **IAM-117** the Single sign-on page, on the connector API. IAM-113 stays open for the installer.
4. **UX-122** single-key shortcuts with a way to turn them off, now that their targets exist.
5. **UX-106** global search across what those pages list, growing with each module.

An item that needs a server change makes it in the module that owns the endpoint, small and additive,
and stays out of the agent session, claim and heartbeat paths lane A is working in.

## Consequences

- AUD-110, IAM-116 and IAM-117 are new roadmap items: their modules' larger items (AUD-101, IAM-107,
  IAM-102) stay open for what they add.
- The P1 lane plan that follows P0 starts from whatever of this list is still open.
