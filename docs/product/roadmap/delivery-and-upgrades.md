# Delivery and upgrades

Module reviewed: 2026-11-04.

Fully offline installation, upgrades and supply-chain integrity, for Docker Compose on a
single host and Helm on Kubernetes cells, from the same artifacts and configuration.

- Owns: `deploy/`, `tools/release/`, `deploy/offline`.
- Depends on: architecture-and-scale (topology model).

## P0 Foundation

- [ ] DEL-001 One typed configuration and one install path produce every topology size; delete per-profile Compose files and Dockerfiles (P0) - Catalog: F217
  - Waits for UX-001 (owner decision, 2026-11-04). Tick only when the image serves the console and portal built from `web/` and the end-to-end install passes on that image. A placeholder page does not count.
  - Done so far: `ownsolid-install` installs `single-node` with no network route, API only (commit e38f5cf). `ha`, `cell`, `multi-cell` and `multi-site` are refused until REL-101, ARC-104, ARC-202 and ARC-301.
- [ ] DEL-002 Zero-egress test harness: install and run with no network route, recording any outbound attempt (P0) - Catalog: F502, F510
- [ ] DEL-003 Real egress denial for workload containers on nodes, not only offline flags (found on real GPU, 2026-11-04) (P0) - Catalog: F234

## P1 Enterprise release

- [ ] DEL-101 Signed offline bundle per architecture: images, charts, models, plugins, fonts, docs; SBOM and checksums (P1) - Catalog: F215, F221, F490, F505
- [ ] DEL-102 Installer with preflight (OS, kernel, ports, disk, memory, time, DNS, drivers), idempotent and resumable (P1) - Catalog: F220, F498
- [ ] DEL-103 First-run setup: administrator, tenant, workspace, certificates; setup endpoint closes afterwards (P1) - Catalog: F499
- [ ] DEL-104 Offline upgrade with required stops, pre-upgrade backup and database migration plan (P1) - Catalog: F222, F503
- [ ] DEL-105 Rollback of application versions, separated from database restore (P1) - Catalog: F533
- [ ] DEL-106 Incremental patch bundles verified against the installed baseline (P1) - Catalog: F504
- [ ] DEL-107 Helm chart with values schema, image-address rewrite, secret references, ingress and TLS (P1) - Catalog: F216, F228, F492
- [ ] DEL-108 In-site OCI registry bootstrapped from the bundle; images pinned by digest (P1) - Catalog: F494
- [ ] DEL-109 Offline license activation, renewal and migration with the ownsolid-entitlement file format (P1) - Catalog: F443, F501, F646
  - Before starting this item: freeze the license format in `pkg/license`, then tell the
    owner it is time to start `ownsolid-entitlement` in a separate chat. That repository
    first gets its own roadmap (customers, issue, renew, revoke, export, audit, console),
    starting from the old issuer CLI in project-one `tools/release/licensing`. Test this
    item against licenses signed by that issuer.
- [ ] DEL-110 Third-party license and notice inventory shipped in every bundle (P1) - Catalog: F226, F356, F535
- [ ] DEL-111 Diagnostic support bundle command that collects host, cluster and application state with secrets redacted and works while the console is down (P1)
- [ ] DEL-112 Bundle signature verification against a pinned offline trust root, with signed root rotation and a clear refusal for expired or unknown roots (P1)
- [ ] DEL-113 Upgrade bundle upload through the console as well as the command line, both ending at the same preflight screen (P1)

## P2 Fleet operations

- [ ] DEL-201 Offline vulnerability database import and image scanning (P2) - Catalog: F506
- [ ] DEL-202 Air-gap data exchange: import models, plugins and patches from removable media with signature and malware checks (P2) - Catalog: F509
- [ ] DEL-203 Partner delivery kit: install checklist, acceptance tests, diagnostic bundle (P2) - Catalog: F647, F648
- [ ] DEL-204 Container runtime and OS package offline path for hosts without Docker (P2) - Catalog: F491

## P3 Multi-site

- [ ] DEL-301 Fleet-wide staged upgrades across cells and sites (P3) - Catalog: F645

## P4 Breadth

- [ ] DEL-401 Additional OS and architecture bundles from the compatibility matrix (P4) - Catalog: F225, F517

Catalog rows absorbed: F215 to F229, F489 to F511, F532, F533, F643, F645 to F649.
