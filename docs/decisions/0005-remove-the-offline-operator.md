# 0005: Remove the offline operator

- Status: accepted
- Date: 2026-11-04

## Context

ADR 0004 made `ownsolid-install` the install path and kept the inherited operator
(`deploy/offline` scope engine, about 90,000 lines with its tests) because
`modules/middleware` backup and restore call it. The operator tracks one installation
through a private scope it creates itself, so it cannot act on anything `ownsolid-install`
produces. That left its milestone delivery profiles (M16, M17, M18, M23 and the old platform
profile), a second install path nobody can run, and a middleware feature that fails on every
real installation.

Trimming the profiles out one by one would mean rewriting code with no remaining user: the
M17 upgrade chain, the account22 restore certificate and their tests.

## Decision

- **Delete the operator.** That covers `deploy/offline/cmd/operator`, its internal packages,
  `pkg/maintenance`, `api/release/restorecompat`, the restore certificate issuer and the
  example files only they read. Git history and the archived `project-one` keep them.
- **One release profile.** `api/release` keeps one profile and no milestone branches. The factory
  and image proofs stay, because the control plane and agent verify images with them.
- **Middleware keeps health only.** Platform-owned profiles are refused with
  `MIDDLEWARE_PLATFORM_OWNED_NOT_SUPPORTED`, and backup, restore and reconcile requests are
  refused. The HTTP contract keeps those fields and stays stable until REL-002 settles what
  replaces them.

## Consequences

- No backup of the platform exists until REL-002, which is P0 and builds on
  `internal/operations/postgresbackup`.
- Upgrades (DEL-104), the signed bundle (DEL-101) and the installed release view are built
  on `ownsolid-install`. `modules/releases` reads a projection nothing writes today, so it
  stays dormant until DEL-104.
- The Kubernetes and native PostgreSQL paths in `postgresbackup` stay, and REL-002 decides
  which of them to keep.
