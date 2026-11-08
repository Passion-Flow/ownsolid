# App catalog

Upstream review: 2026-11-09. Retain packaged
[Helm charts](https://helm.sh/docs/topics/charts/) and
[Compose projects](https://docs.docker.com/reference/cli/docker/compose/up/)
with local image inventories, following the air-gapped source model of
[OpenShift](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html/disconnected_environments/olm-restricted-networks).
Add a rendered workload admission gate to make privileges reviewable before an
install or upgrade, following the distinction between preview and enforcement in
[Kubernetes Pod Security Admission](https://kubernetes.io/docs/tasks/configure-pod-container/enforce-standards-namespace-labels/).

One-click applications that run on ownsolid infrastructure and automatically use its
models, sign-in, monitoring and logs. DeepParser is the first partner app and the only
knowledge product.

- Owns: `catalog/`, `services/control` catalog service (new), `api/schemas` catalog schema.
- Depends on: delivery-and-upgrades, identity-and-access, gateway, in-product-operations.

## P1 Enterprise release

- [ ] CAT-101 App manifest format over Helm 4 and Compose: metadata, versions, localized forms, compatibility range (P1)
- [ ] CAT-102 Typed dependencies with a binding contract: models, databases, storage; credentials delivered as secret references (P1)
- [ ] CAT-103 Automatic gateway key scoped to the app's models and workspace, rotated by ownsolid (P1)
- [ ] CAT-104 Automatic sign-in wiring to the ownsolid OIDC provider (P1)
- [ ] CAT-105 Install, configure, upgrade along a version graph, roll back, uninstall with a data-retention choice (P1)
- [ ] CAT-106 App status, health checks, logs and terminal inside the console (P1)
- [ ] CAT-107 Signed offline bundle per app with images and charts (P1)
- [ ] CAT-108 Quota and GPU reservation check before install; per-tenant visibility; license notes per app (P1)
- [ ] CAT-109 Preview rendered workload privileges and intranet access before install or upgrade; enforce the approved plan and reject changes outside it (P1)

## P2 Fleet operations

- [ ] CAT-201 DeepParser app package, after the rest of the product is complete (P2)
- [ ] CAT-202 DeepParser identity pass-through, citations and trace links (P2) - Catalog: F576
- [ ] CAT-203 DeepParser zero-egress and domestic-stack test (P2)
- [ ] CAT-204 Private catalogs and partner packaging kit (P2)
- [ ] CAT-205 Fleet-wide app rollout across cells (P2)
- [ ] CAT-206 App backups and restore hooks (P2)
- [ ] CAT-207 Knowledge permissions follow ownsolid projects: DeepParser receives project and business-unit membership so search, citation, API and download access match ownsolid permissions, with cross-project negative tests (P2)

Knowledge-base rows F009 to F033 and F576 to F579 are retired as native features and
replaced by the DeepParser items above.
