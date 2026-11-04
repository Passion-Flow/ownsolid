# 0004: One install path

- Status: accepted
- Date: 2026-11-04

## Context

ADR 0003 left this repository with no image definitions and no way to install. The
inherited offline operator (`deploy/offline/cmd/operator`) exposes over a hundred separate
commands and installs only from a signed bundle. Producing that bundle needs manufacturing
tools that were not migrated and about 25 asset types, including trained models and
training images. Each inherited delivery profile named a set of milestones, not a
deployment shape.

The P0 exit criterion needs one install path that produces every former profile. Since ADR
0003 every installation runs the complete platform, so the former profiles differ only in
which optional integrations are configured.

## Decision

- **One command and one typed configuration.** `ownsolid-install apply --config
  install.yaml` is the install path. It validates the whole file first, generates missing
  secrets, renders the control plane configuration and Compose variables, then runs ordered
  steps. Every step is safe to repeat, so a failed run is retried by running it again.
- **`topology.size` is the only shape.** The installer takes the same enum as the control
  plane and refuses a size this release cannot run before changing anything. Today that
  means `single-node`. REL-101, ARC-104, ARC-202 and ARC-301 add the other sizes behind the
  same configuration and command.
- **One image and one Compose file.** `deploy/images/control/Dockerfile` builds the control
  plane from source, and `deploy/compose/compose.yaml` runs it with PostgreSQL. Optional
  control plane settings go through `control_settings` and are validated by the control
  process.
- **Nothing reaches a registry.** Every service has `pull_policy: never`. The installer loads
  an image archive or requires the images to be present.
- **The installer drives the existing control plane commands.** Migration, runtime access
  and first-run setup stay in the control binary; the installer only orders them. Serving
  never mounts the privileged database identity.
- **Proof runs with no network.** `tests/e2e/run-single-node.sh` runs the real installer in a
  Docker-in-Docker container started with `--network none`.
- **Web assets come later.** `web/` does not exist yet, and `serve` needs both asset roots. The
  image carries a static page stating that the console is not included, and the API is the
  interface until UX-001 replaces it. This is stated on the page, in the install guide and
  in the status table.
- **Stores stay optional when serving.** This settles the rule ADR 0003 left open:
  `serve` does not require the model, dataset, training or preparation stores, because a
  fresh installation has to serve before those stores exist. The owning roadmap items set
  them up.

## Consequences

- A root-run installer is required, because it creates files owned by the service users.
- Container paths keep the inherited `/run/projectone` and `/opt/projectone` names, because
  the backup tooling binds to those exact paths. Renaming them is separate work.
- PostgreSQL and the control plane share one host, and the connection between them is
  unencrypted on an internal Docker network. HA (REL-101) has to revisit this.
- ADR 0005 removes the inherited operator.
