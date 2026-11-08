# Product agent

Last reviewed: 2026-11-09. See [product assistant references](../../research/product-assistant-commands.md).

One assistant that operates the whole product: customers tell it to open a page, find
something, deploy a model or drain a node instead of clicking. Clicking always remains a
complete path.

- Owns: `services/control` agent service (new), console command bar and panel.
- Depends on: gateway, identity-and-access, audit-and-compliance, every module's API.

## P1 Enterprise release

- [ ] AGT-101 Command bar and docked panel in console and portal; "ask about this" on any error (P1)
- [ ] AGT-102 Deterministic command layer that works with no model: navigation and common actions by keyword (P1)
- [ ] AGT-103 Tool catalog generated from the OpenAPI contracts, with a reviewed overlay of risk tier and permission per tool (P1)
- [ ] AGT-104 Risk tiers enforced on the server: navigate, read, sensitive read, reversible write (P1)
- [ ] AGT-105 Plan then confirm: the server renders the preview from a signed plan bound to exact arguments (P1)
- [ ] AGT-106 Acts with the signed-in user's permissions only; never a service account (P1)
- [ ] AGT-107 Every action audited with the user as subject and the agent as actor (P1)
- [ ] AGT-108 Injection defenses: tool output treated as data, secrets masked, no outbound fetch, no free-form shell (P1)
- [ ] AGT-109 Results link to the page where the effect is visible (P1)
- [ ] AGT-110 Runs on a model served by the product's own gateway; model qualification suite assigns capability tiers (P1)
- [ ] AGT-111 Chinese and English prompts with server-side name resolution to canonical IDs (P1)
- [ ] AGT-112 Budgets, loop guard and kill switch per user and per installation (P1)

## P2 Fleet operations

- [ ] AGT-201 Destructive tier with typed confirmation and optional second approver (P2)
- [ ] AGT-202 Fleet actions as plans with blast-radius preview and staged execution (P2)
- [ ] AGT-203 Incident investigation: correlate alerts, logs and changes into a summary (P2)

## P4 Breadth

- [ ] AGT-401 Approved autonomous runbooks (P4)
- [ ] AGT-402 ownsolid as an MCP server for outside agents (P4)
