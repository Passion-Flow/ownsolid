# 0003: One topology model and one schema lineage

- Status: accepted
- Date: 2026-11-04

## Context

The inherited control plane selected one of twenty application profiles at
startup (`account`, `workspaces`, `nodes` ... `releases-cache-v1`, `platform`,
`mvp-postgresql-cpu-v1`). Each subset profile served part of the product,
composed its own HTTP routes and recovery owners, and had its own PostgreSQL
migration chain. Two complete chains existed: the straight `platform` chain
(27 migrations) and the `mvp-postgresql-cpu-v1` chain (the 22-step account chain
plus 7 migrations), which is the one the offline delivery installed and which
the backup, restore, maintenance and upgrade tooling supported.

Both complete chains were applied to PostgreSQL 17 with pgvector 0.8.7 and
compared with `pg_dump --schema-only`: they create the same tables and
columns. The `mvp-postgresql-cpu-v1` chain carries stricter CHECK constraints
(locale tags, component configuration shape, capability lists per execution
profile, workflow graph versions) and two extra unique constraints.

## Decision

- **Every installation runs the complete platform.** Application profiles are
  removed from configuration, code, HTTP routing and tests. Optional
  integrations keep their own typed enable settings.
- **Topology is the only deployment shape.** `topology.size` is one of
  `single-node`, `ha`, `cell`, `multi-cell` or `multi-site`; it is optional and
  defaults to `single-node`. A size is accepted only when this release can run
  it. Today only `single-node` is supported; the other sizes fail startup with
  `CONFIG_TOPOLOGY_SIZE_NOT_SUPPORTED` until their roadmap items land. The public
  runtime configuration reports `topology.size` instead of `application_profile`.
- **One PostgreSQL lineage.** The `mvp-postgresql-cpu-v1` chain is kept, because
  it is stricter, it is the one installed in the field, and the operational
  tooling is built for it. Its files are flattened into
  `services/control/migrations/postgresql/000001` to `000029` with unchanged
  bytes, so applied ledgers and checksums of that chain stay valid. Migration,
  `check-schema` and serving always verify the complete ledger, catalog digest
  and grants; serving also requires a DML-only runtime principal.
- **Serving checks consistency, not store presence.** `serve` refuses plaintext
  browser transport without machine HTTPS and an HTTP budget at or below the portal
  commit budget. The default HTTP shutdown budget rises from 5 to 20 seconds so the
  defaults satisfy this. The delivered chain's other startup rules (release view and
  the model, dataset, training and preparation stores) are not enforced: requiring
  stores would stop a fresh installation from serving before first-run setup, because
  `BOOTSTRAP_REQUIRED_FOR_CONFIGURED_FEATURES` refuses them on an unbootstrapped
  database. DEL-001 and DEL-103 settle the final rule.
- **One upgrade command.** `upgrade-schema` and `inspect-schema-upgrade` replace
  the per-profile `upgrade-*` commands. They accept any installation whose ledger
  is an exact prefix of the lineage, append the rest, then grant the existing
  runtime principal the application tables, with resumable and commit-unknown
  safe phases.
- The former `platform` chain is kept only as frozen test data under
  `services/control/internal/platform/database/testdata/platform-lineage`,
  because the MySQL and Dameng translations were derived from it and their
  tests compare against those exact bytes.

## Consequences

- Databases created by the subset profiles or by the `platform` chain cannot be
  opened or upgraded. No customer installation exists; test installations are
  reinstalled.
- `application_profile` is now an unknown configuration key, and the
  `PROJECT_ONE_APPLICATION_PROFILE` environment key is refused. An existing
  installation keeps a valid database but must drop that key to start.
- **Offline delivery is not proven until DEL-001.** `deploy/offline` no longer writes
  `server.application_profile` for the current lineage and requires it to be absent,
  and its Linux unit tests match their earlier results. No offline bundle has been
  built or installed since this change: this repository has no image definitions yet,
  so a real install has not run. Upgrades from M16 `releases` installations stay
  unsupported because `upgrade-releases` is gone. `api/release` still names the lineage
  `mvp-postgresql-cpu-v1` and carries milestone delivery profiles; the control plane
  accepts only that lineage name there. DEL-001 replaces them with one typed
  configuration and one install path, which the P0 exit criterion needs.
