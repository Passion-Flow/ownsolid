# Developer experience

How engineers and integrators use ownsolid programmatically.

- Owns: `api/openapi`, `tools/`, CLI and SDK packages (new).
- Depends on: every module's API.

Reviewed 2026-11-09 against Kubernetes CLI, GitHub webhooks, JupyterHub,
VS Code Dev Containers and BuildKit. The review added DEV-206 for inspecting
and replaying webhook attempts. See [developer experience research](../../research/developer-experience.md).

## P1 Enterprise release

- [ ] DEV-101 Complete public REST API documented by OpenAPI (P1) - Catalog: F236
- [ ] DEV-102 Command-line tool for every common operation (P1) - Catalog: F236
- [x] DEV-103 Python and Go SDKs generated from the API (P1) - Evidence: [offline SDK verification](../../research/sdk.md#integration-evidence) at 5876679. Reproduced Go and Python clients for 412 operations across 27 domains and exercised both clients against offline Admin TLS and PostgreSQL.
- [ ] DEV-104 Webhooks for job, deployment, node and alert events (P1)
- [ ] DEV-105 Notebooks with GPU access inside workspaces (P1) - Catalog: F593
- [ ] DEV-106 SSH and IDE access to development containers (P1)
- [ ] DEV-107 Custom image building offline from approved bases (P1)
- [ ] DEV-108 Compute layer for another AI platform: delegated identity, quota mapping, a job API with result callbacks and an integration guide, so an existing enterprise AI platform can run on ownsolid (P1)

## P2 Fleet operations

- [ ] DEV-201 Terraform provider (P2)
- [ ] DEV-202 Adapter SDK and contract tests for new engines, storage and devices (P2) - Catalog: F237, F655
- [ ] DEV-203 Pipelines: data, training, evaluation and deployment steps chained (P2)
- [ ] DEV-204 API versioning and deprecation policy (P2) - Catalog: F653, F655
- [ ] DEV-205 Laboratory system connectors: an ELN and LIMS adapter contract to import experiment records and export results with links back, first adapters chosen by research, laboratory data standards such as Allotrope and SiLA 2 evaluated (P2)
- [ ] DEV-206 Webhook delivery history, receiver verification and scoped, audited redelivery (P2)

## P4 Breadth

- [ ] DEV-401 Adapter certification program for partners (P4) - Catalog: F655

Catalog rows absorbed: F236, F237, F593, F653, F655.
