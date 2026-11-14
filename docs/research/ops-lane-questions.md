# Operations lane integration boundaries

## OPS-104 live delivery

The [component log settlement note](component-log-settlement.md) specifies
the existing Admin snapshot and its acceptance. Live delivery requires
the task producer and log store to supply a scoped cursor or content
reference that becomes visible after settlement and survives retries.
The shared route and console owners can then register an authenticated
resumable stream with an authorization check at delivery. The current
component route remains a bounded on-demand snapshot.

## CAT-106 installed application operations

The [Compose launcher](../../deploy/offline/internal/install/catalog_compose.go)
accepts the admitted source and model digests and returns the launch
outcome. The [applications API](../../api/openapi/v1/applications/openapi.json)
lists Portal workflow apps and conversations. Neither supplies an
installed workload identity or a source to query for an installed app.

The installation owner must retain an installation ID and exact release
pin, the server-authorized tenant, workspace, site and cell, and the
admitted engine project or namespace and workload IDs. A log producer
must resolve that identity to its scoped source and a committed cursor.
The shared API and console owners must register health, log and terminal
operations against that installation. Terminal access requires a current
permission check, an audit record and explicit operator confirmation.
Admission digests and caller-supplied project names cannot establish
workload ownership.

## CAT-104 automatic sign-in

[IAM-106](../product/roadmap/identity-and-access.md) owns the local provider.
The current [SSO module](../../services/control/internal/modules/sso/README.md)
consumes a configured Gitea provider for ownsolid sign-in. Catalog wiring
requires an issuer-owned operation that registers and revokes one client
per authorized installation, binds the exact approved callback, and
returns a client ID and a reference to any confidential credentials.
The installer projects those credentials and revokes the registration on
uninstall. [OpenID Connect client metadata](https://openid.net/specs/openid-connect-registration-1_0.html#ClientMetadata)
defines the exact redirect match.

## CAT-108 quota, reservation and tenant visibility

[Signed license notes](app-license-notes.md) are already available in the
catalog metadata. The [scheduling module](../../services/control/internal/modules/scheduling/README.md)
offers a read-only submission estimate and attempt-scoped reservations in
a workload owner's transaction. Before starting an app, the installer
needs an authorized installation attempt and admitted demand so the
owner can reserve and release resources against the exact installation.
Catalog reads need a server-owned tenant visibility decision before
opening signed release metadata. A successful quota estimate is not a
reservation, and a valid publisher signature is not a tenant grant.

## CAT-105 installation lifecycle

The [version selector](app-version-transitions.md) verifies exact install,
upgrade and rollback pins. The installation owner must persist the authorized
app and workspace, current pin, known-good predecessor, configuration recovery
reference and uninstall retention choice. It must serialize retries against
that record and reconcile the engine outcome before advancing the current
pin. The existing Compose adapter returns its command outcome. The next
integration requires this durable owner and its registered lifecycle routes.
[Helm release history](https://helm.sh/docs/helm/helm_history/),
[Compose startup](https://docs.docker.com/reference/cli/docker/compose/up/).

## CAT-101 and CAT-107 package and engine prerequisites

The signed metadata, compatibility range, exact OCI inventory and Compose
image plan remain implemented in `catalog/apps`. Completing the user path needs
the authorized catalog route and the durable installation owner above.
The [application chart consumer handoff](app-bundle-verification.md#application-chart-consumer)
records the exact app inventory fields, Helm 4.3.1 boundary, offline dependency
and schema closure, image delivery mapping and publisher-approved package needed
by DEL. The existing platform Helm fixture consumes the platform release group.
The app acceptance must consume the app's own signed release and trusted publisher.

## Workstation acceptance storage

Use a short, canonical APFS scratch path for the full workstation gate when
the system volume falls below the object store's free-space threshold.
The full gate and supplemental catalog build, race tests and vet passed
with that setup at candidate `2a8c0261` over main `203109c7`.
The Chromium suites passed 1,075 Console and 389 Portal cases, with 28 and
9 conditional skips respectively.

The initial HFS+ scratch run failed both restored-content change-time
checks in `TestPreparedSnapshotRejectsPostreadChanges`. The long APFS
mount path then prevented the Compose fixture's Unix sockets from binding.
APFS storage at a short path passed both complete packages. Preserve these
failure logs when reproducing the gate. The native Linux results in the
topic notes remain historical acceptance. Their external raw logs were
lost from temporary storage after the workstation restart.
