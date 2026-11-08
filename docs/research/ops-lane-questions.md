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
| CAT-107 | Approved app signing trust and installer integration with local image import. | Preserve digest-pinned manifests and require signed bundle verification before workloads may be rendered or admitted. |

The current screen plan has a node drawer and an app-catalog placeholder. The
full operator flows also need their screen specifications. Keep 1Panel strictly
a behavior reference. Use the platform's own agent connection and Kubernetes
identities for execution.
