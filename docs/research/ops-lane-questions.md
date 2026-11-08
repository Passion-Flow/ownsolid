# Operations lane integration boundaries

The lane owns `services/control/internal/modules/components`, `catalog` and its
two roadmap files. The paths in the module headers describe the finished product.
The lane can add contract and component behavior where it owns the code, and
exercise it in isolation. The following end-to-end work depends on integration
in the owning paths. Leave the boxes open while those boundaries apply.

| Items | Owning integration needed | Lane action |
| --- | --- | --- |
| OPS-101, OPS-103 | Node-agent PTY execution and revocation, a bounded stream on the outbound agent connection, role and audit integration, and a console terminal. | Preserve the outbound-only and permission boundaries. A request record alone cannot start or terminate a terminal. |
| OPS-102, OPS-107 | The cell's Kubernetes client and delegated identities, namespaced authorization and console shell or resource browser. The private-cluster discovery credential explicitly lacks exec authority. | Leave existing discovery credentials scoped to discovery. Kubernetes must authorize the impersonated user and namespace. |
| OPS-104 | An authorized server log hub with budgets, new sources and the console live viewer. | Strengthen the existing component snapshot path inside its current contract. |
| OPS-105, OPS-106, OPS-108, OPS-110, OPS-111, OPS-114 | Host-side collection or execution, fleet transport and console flows. Some facts already travel through the fleet service. | Reuse the existing facts and lifecycle contracts when their owners add these paths. Keep host reads separate from mutation authority. |
| OPS-109, OPS-113 | A durable scheduler and dispatch policy in the task owner, explicit target and command confirmation, per-node output and console recovery. | Existing component lifecycle commands are typed for node-exporter. Keep that authority scoped to the current component. |
| OPS-112 | The installer and backup owner provide the existing local backup and restore. S3 and SFTP targets, app data and a product flow require their own integration. | Preserve the current local path while the owning code gains the missing targets. |
| CAT-101, CAT-102 | Catalog service, publisher and installer, console form and dependency selection. | Build and exercise the manifest and typed binding contracts in the catalog lane. |
| CAT-103, CAT-104, CAT-105, CAT-106, CAT-108, CAT-109 | Scoped gateway and OIDC issuers, approved local deployers, quota and workload policy, operational streams and console. | Keep key issuance, identity and install authority behind their owning services. |
| CAT-107 | Approved app signing trust and installer integration with local image import. | Verify the signed inventory and fixed local payloads. The owning importer still needs to verify the OCI closure and connect this gate to installation. |

The current screen plan has a node drawer and an app-catalog placeholder. The
full operator flows also need their screen specifications. Keep 1Panel strictly
a behavior reference. Use the platform's own agent connection and Kubernetes
identities for execution.

## Repository acceptance gate

The local browser harness reserves ports 4310 and 4311. Other worktrees can
occupy either port and stop `make test` before the suite starts. An alternate
preview on private ports exercised part of both browser suites. Full acceptance
still requires the normal harness on free ports.

Running the non-browser gate and then repeating the focused tests with a
resolved macOS temporary directory also exposed failures in the unchanged agent
packages `internal/modelpreparations` and `internal/training`. The shutdown and
revocation tests reported cancellation or changed output while verifying
ownership of the container. These tests belong to the agent owner. The OPS
increment has separate passing catalog and component evidence. The repository
gate remains open.

The standalone control suite also reported failures in dataset import memory,
password admission deadlines, backup restore roots and TLS file mode checks.
Those paths are outside OPS ownership. Gateway tests and the isolated i18n
rerun passed. The fixed-port and independent runs are distinct from the
PostgreSQL and VictoriaLogs component integration run.
