# Delivery and upgrades

Module reviewed: 2026-11-09.

Fully offline installation, upgrades and supply-chain integrity, for Docker Compose on a
single host and Helm on Kubernetes cells, from the same artifacts and configuration.

- Owns: `deploy/`, `tools/release/`, `deploy/offline`.
- Depends on: architecture-and-scale (topology model).

## P0 Foundation

- [x] DEL-001 One typed configuration and one install path produce every topology size; delete per-profile Compose files and Dockerfiles (P0) - Catalog: F217 - Evidence: 3eed593; zero-egress install on the image with the console and portal, `ownsolid-cpu`, 2026-11-04
  - Waits for UX-001 (owner decision, 2026-11-04). Tick only when the image serves the console and portal built from `web/` and the end-to-end install passes on that image. A placeholder page does not count.
  - Scope of the evidence: `ownsolid-install` installs `single-node` with no network route, and the image serves the console and portal built from `web/`. `ha`, `cell`, `multi-cell` and `multi-site` are refused until REL-101, ARC-104, ARC-202 and ARC-301. The old placeholder file `deploy/images/control/assets/index.html` is unused and awaits removal by the owner.
- [x] DEL-002 Zero-egress test harness: install and run with no network route, recording any outbound attempt (P0) - Catalog: F502, F510 - Evidence: commit 1d8fcdd (ADR 0008)
- [x] DEL-003 Real egress denial for workload containers on nodes, not only offline flags (found on real GPU, 2026-11-04) (P0) - Catalog: F234 - Evidence: be0f52c (ADR 0017); real-kernel guard tests and the zero-egress harness on `ownsolid-cpu`, 2026-11-05
  - Scope of the evidence: Docker nodes, with probe containers in place of vLLM; a run with a real model on a GPU node is still to come. Kubernetes cells are NET-106.

## P1 Enterprise release

- [ ] DEL-101 Signed offline bundle per architecture: images, charts, models, plugins, fonts, docs; SBOM and checksums (P1) - Catalog: F215, F221, F490, F505
  - The optional signed RKE2 Linux amd64 inventory passed disconnected preparation with real upstream artifacts and a temporary signer. See the [status evidence](../status.md) and [ADR 0057](../../decisions/0057-bind-rke2-artifacts-to-the-approved-agent-release.md). The production inventory is still open.
  - New RKE2 builds require the reviewed [component SPDX and notices](../../research/rke2-component-inventory.md), including verified image and layer identities, source notices and the available publisher statements. The strict reader passed the disconnected signing and refusal drill. Production licence correspondence and the remaining release inputs are still open.
- [x] DEL-102 Installer with preflight (OS, kernel, ports, disk, memory, time, DNS, drivers), idempotent and resumable (P1) - Catalog: F220, F498 - Evidence: b8535ef. The Linux amd64 isolated installation resumed after SIGKILL with both entrances ready and the same administrator, tenant, workspace and credential.
- [ ] DEL-103 First-run setup: administrator, tenant, workspace, certificates; setup endpoint closes afterwards (P1) - Catalog: F499
  - The [disconnected Linux amd64 drill](../../research/first-run-bootstrap.md#linux-amd64-acceptance) exercised the existing Apply and control operator path through write failure, lost commit response, TLS key repair, and repeated bootstrap refusal. The shared setup HTTP contract and its first-run UI still need installed acceptance.
- [x] DEL-104 Offline upgrade with required stops, pre-upgrade backup and database migration plan (P1) - Catalog: F222, F503 - Evidence: c54fed2. A disconnected Compose installation verified its recovery backup before migrating PostgreSQL from schema 52 to 54.
- [x] DEL-105 Rollback of application versions, separated from database restore (P1) - Catalog: F533 - Evidence: 2bd0729. An injected candidate startup failure was followed by a rollback to the previous signed image with the same PostgreSQL container and schema.
- [x] DEL-106 Incremental patch bundles verified against the installed baseline (P1) - Catalog: F504 - Evidence: 4e22094 and c54fed2. The isolated installer refused the wrong baseline and verified and applied the patch against the exact installed signed release.
- [ ] DEL-107 Helm chart with values schema, image-address rewrite, secret references, ingress and TLS (P1) - Catalog: F216, F228, F492
  - The single-writer chart passed disconnected Linux amd64 rendering and native file and TLS admission. It uses the signed site Registry reference and exact SNI TLS passthrough. The actual chart install on a cold RKE2 worker, both browser entrances and persisted data after Pod replacement still need verification. The [joint signed fixture](../../research/offline-control-helm.md#joint-fixture-acceptance) is ready for that drill. See [status evidence](../status.md) and [ADR 0062](../../decisions/0062-run-helm-control-with-direct-site-tls.md).
- [ ] DEL-108 In-site OCI registry bootstrapped from the bundle; images pinned by digest (P1) - Catalog: F494
  - The signed-release Registry path passed the disconnected Linux amd64 drill. It refused missing and truncated inputs, retained its container across repeats, and served all four signed image digests to a cold private Docker node over authenticated TLS. See [status evidence](../status.md) and [ADR 0061](../../decisions/0061-seed-a-site-registry-from-the-signed-release.md). Leave open for the production release inventory and the managed RKE2 worker pull path in ADR 0051 and ARC-116. The [prepared proof handoff](../../research/signed-registry-bootstrap.md#linux-amd64-proof-acceptance-and-net-102-handoff) verified the signed site group and current-profile consumption boundary. The [joint preflight](../../research/offline-control-helm.md#joint-fixture-acceptance) used the same archive for the Registry, strict RKE2 and Chart inputs.
- [ ] DEL-109 Offline license activation, renewal and migration with the ownsolid-entitlement file format (P1) - Catalog: F443, F501, F646
  - The existing [installer runtime policy input](../../../deploy/offline/README.md#configure-license-import-capacity)
    selects control_settings.runtime.licensing.maximum_imports. File materialization
    preserves it across retries and explicit changes. Installed quota and complete
    issuer-signed lifecycle acceptance remain open.
  - Before starting this item: freeze the license format in `pkg/license`, then tell the
    owner it is time to start `ownsolid-entitlement` in a separate chat. That repository
    first gets its own roadmap (customers, issue, renew, revoke, export, audit, console),
    starting from the old issuer CLI in project-one `tools/release/licensing`. Test this
    item against licenses signed by that issuer.
- [ ] DEL-110 Third-party license and notice inventory shipped in every bundle (P1) - Catalog: F226, F356, F535
- [x] DEL-111 Diagnostic support bundle command that collects host, cluster and application state with secrets redacted and works while the console is down (P1) - Evidence: 35bda91 and b8535ef. The disconnected installer produced a private support archive with host, service and PostgreSQL status while control was stopped.
- [x] DEL-112 Bundle signature verification against a pinned offline trust root, with signed root rotation and a clear refusal for expired or unknown roots (P1) - Evidence: 38efd30 and b23f6bc. The disconnected factory verified the signature threshold, rejected replay and verified a release under the rotated trust root.
- [ ] DEL-113 Upgrade bundle upload through the console as well as the command line, both ending at the same preflight screen (P1)
  - The [disconnected CLI checks](../../research/upgrade-preflight.md#apply-admission-for-full-and-incremental-media) verified that the plan, full upgrade and patch report pending restore or backup recovery before candidate admission. The authenticated upload and common Console preflight still need integration.
- [ ] DEL-114 Install configuration covers the whole address and transport matrix: a hostname per entrance, published ports, deployments reached by IP address with the address as a certificate name, plain HTTP for an isolated laboratory, and TLS terminated by a trusted proxy with explicit proxy ranges (P1)
  - The [installer listener projection research](../../research/installer-entrance-listeners.md) consumes ARC-110's accepted optional Portal bind and serving-check contract. The first direct-HTTPS projection and its installed qualification are still open.
- [ ] DEL-115 Change an entrance address after install: reissue the certificate, reconcile single sign-on callbacks, invalidate affected sessions and roll back, without editing files by hand (P1)
- [x] DEL-116 License page: status, limits and expiry, import of a license file, diagnostics that say why a file was refused (P1) - Evidence: 5f0110a; real Chromium on an offline install on `ownsolid-cpu` refused real signed files for the real reason, imported a real renewal that survived a reload and downloaded diagnostics, three runs in a row, 2026-11-07
- [x] DEL-117 The installer sets up the model store: `data/models` mounted into the control plane and configured, carried by `backup` and put back by `restore` ([ADR 0044](../../decisions/0044-web-lane-follows-the-build-plan.md)) (P1) - Evidence: f424d36; a fresh install and an upgrade in place on `ownsolid-cpu` gave serving the store, and a backup and a restore over the running installation brought back the real model's lost weights, 2026-11-07

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
