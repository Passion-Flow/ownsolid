# 0006: Cell and site scope

- Status: accepted
- Date: 2026-11-04

## Context

ARC-004 asks for a cell ID and site ID in every resource key, table and API path, even
with one cell, because retrofitting them across installed agents and databases later is
expensive. The plan already fixes two things this has to fit: one PostgreSQL database per
cell plus a thin global database (ARC-201), and a global layer for routing, quota,
identity, catalog and aggregates (ARC-301). A user or a tenant does not belong to a cell,
so "every" cannot mean literally every table.

## Decision

**Two layers.** Each table and API path belongs to the global layer or to a cell.

- Global: identity, sessions, SSO, invitations, mail, tenants, workspaces and their
  members and notes, preferences, notifications, licensing, release state, and the `sites`
  and `cells` registry.
- Cell: everything else, including nodes, tasks, deployments, models, datasets, training,
  workflows, gateway instances and audit events.

Tests make the choice mandatory. `TestEveryCellOwnedTableCarriesScope` fails on a table
that has no scope columns and is not on the global list.
`TestEveryContractPathHasOneLayer` fails on an OpenAPI path that is neither scoped nor on
the global list.

**Tables.** Migration 000030 adds `sites`, `cells`, and `site_id` and `cell_id` columns on
88 tables. Both columns default to `local_site_id()` and `local_cell_id()`, which read the
one `cells` row marked `is_local`. Three choices follow from ARC-201:

- The default is a function, not a literal, so the catalog digest is the same in every
  cell database and a cell's identity is one row of data.
- The columns are not part of primary keys. Resource IDs are UUIDs, unique across cells,
  and a cell's rows live in that cell's own database, so nothing needs a composite key.
- There is no foreign key to `cells`. Every insert on a hot table would take a key share
  lock on the same row, which is a known multixact bottleneck at fleet write rates. The
  `cells.id` format check and the column default keep the values correct.

**API paths.** Cell-owned paths are `/api/v1/sites/{site_id}/cells/{cell_id}/...`,
`/agent/v1/sites/{site_id}/cells/{cell_id}/nodes/...` and
`/gateway/v1/sites/{site_id}/cells/{cell_id}/instances/...`. Each resource has exactly one
public path: the unscoped form of a cell-owned path, a scope on a global path, and any
other cell all return `RESOURCE_NOT_FOUND`.

`internal/platform/cellroute` resolves this once, at the top of the request entry point,
and hands modules a cell-relative path. Modules serve one cell and do not know about
scope. This is the seam where ARC-206 forwards to other cells.

The audit read API, `/api/v1/admin/audit-events`, is on the global list although its rows
are cell-owned, because audit is read as one view across cells.

Three paths stay unscoped on purpose:

- `/agent/v1/enroll`: a node has no cell until it enrolls. The response returns its site
  and cell, which the agent keeps in its identity file.
- `/v1/chat/completions` and `/v1/models`: the OpenAI-compatible contract. Clients cannot
  add a scope, and each gateway instance already runs in one cell.
- `/health/*`: it describes the process that answers.

**Resource keys.** A resource's key is its scoped path. Keys in namespaces that more than
one cell can share also carry the scope: object store keys are
`sites/{site}/cells/{cell}/artifacts/...`, and task event subjects are
`projectone.tasks.v1.{site}.{cell}...`, with both IDs inside the event. Files in a cell's
own volumes, such as training `storage_key` values, are not shared and stay relative to
that volume.

**Identity is fixed for now.** The site and cell are both `default`, and `database.Check`
refuses a database that names anything else with `SCHEMA_CELL_IDENTITY_INVALID`.
ARC-202 replaces this with configured identities. IDs are immutable, so a customer's
first cell keeps the ID `default`; display names can change.

## Consequences

- **MySQL and Dameng do not have the columns.** Their chains are translations that were
  already behind this lineage and are not supported yet (CMP-201, CMP-202). Node reads
  return the default scope as a literal on those engines. Those items must add the
  columns.
- A database restored from the old catalog and then upgraded is no longer accepted: the
  precursor digest is gone. No customer installation exists and platform backup is absent
  until REL-002.
- Task events written before this migration have no scope and are refused by the broker
  path. Test installations are reinstalled.
- The agent identity file needs `site_id` and `cell_id`. Agents enrolled before this change
  must enroll again.
