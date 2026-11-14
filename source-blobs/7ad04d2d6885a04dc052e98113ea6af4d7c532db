# 0001: Repository layout

- Status: accepted
- Date: 2026-11-04

## Context

The previous `project-one` repository accumulated about 8,000 files, many outside any
defined responsibility: per-milestone deployment profiles, 114 environment examples,
dated execution logs and verification receipts mixed with product code.

## Decision

- ownsolid and the license issuance platform are separate repositories, because they
  have different audiences and the signing code must never reach customers.
- Every file has exactly one home, defined in AGENTS.md section 6. The layout follows
  common polyglot monorepo conventions: `services/`, `runtimes/`, `web/`, `api/`,
  `deploy/`, `docs/`, `tools/`, `tests/`.
- `tools/lint/layout/check_layout.py` enforces the root allowlist, defined
  subdirectories, environment file placement, lowercase names and the English-only rule.
- Code moves from `project-one` in batches, each compiling before the next starts.
  Execution logs, receipts and historical milestone documents stay in `project-one`.

## Consequences

- New directories require an AGENTS.md change, which keeps the structure deliberate.
- Git history starts fresh here; earlier history remains in `project-one`.
