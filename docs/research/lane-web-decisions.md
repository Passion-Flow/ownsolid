# Web lane boundaries

The browser and visual checks run on the workstation. The lane can consume the current
contracts and change the web client. A shared contract or a service change belongs to
the owner of that contract. The [build plan](../product/plan/README.md) requires each
screen's entry to be expanded before work on that screen starts.

| Item | Boundary |
| --- | --- |
| ARC-104 | Stage 1 specifies nodes and enrollment but has no managed-cluster screen entry. The [cluster console draft](cell-cluster-console.md) proposes the singleton detail, create and add-worker flows and their seven states. The Nodes contract still needs to describe the Kubernetes enrollment profile before the candidate and enrollment paths can consume it. |
| UX-106, UX-122 | Workspace model search already opens the Model library. Application search and the `I` action depend on the admin catalog (S-309). Its plan entry gives the archetype and action but lacks the content, click paths and empty states. |
| UX-117 | Existing Playwright covers model upload interruption, resume and retirement. The remaining production-browser scenarios include recovery from an already verified immutable object, plus the application, plugin, dataset, training and evaluation flows. The old Go harness belongs outside this lane. |
| UX-131 | Operation audience declarations already generate the reference. At the reviewed contract revision (`3f94669`), all 480 HTTP operations declare an audience and 360 lack an error declaration, including 30 in the excluded knowledge contract. Error codes belong in the versioned OpenAPI operations and must match their handlers. A browser-side list would drift from the contract. |
| POR-101 | The shared shell can group navigation and switch workspaces. S-301 leaves the continue links for POR-102 and POR-103 and real usage for POR-111. |
| POR-102, POR-103 | The portal has conversation and run operations and a read-only published applications list. S-302 and S-303 have summary entries only. The [S-302 draft](portal-conversations.md) records the proposed surfaces, states and reusable contracts for the plan owner. The applications contract has no portal install operation. |
| POR-104 | The model catalogue already reads the published names, deployments and model metadata. The full item needs prices (TEN-107) and administrator-authored descriptions (GW-120). |
| POR-105 | S-305 has a summary entry only. The portal uses a same-origin Content Security Policy and the separate gateway accepts API keys. The contracts offer no session-bound portal operation to run a prompt. A browser call that carries a pasted key across origins would change the trust boundary. |
| POR-107 to POR-110 | There are no portal screen entries for these items. The plan's Stage 5 entries describe administrator surfaces. |
| POR-111 | S-307 has a summary entry only. S-301 explicitly treats usage as unavailable until the workspace-scoped usage and quota data exist. |
| POR-112 | The account page already handles profile and password. S-308 needs a full entry, and identity has a current-session read but no portal list and revocation operations for a person's other sessions. |
| POR-114 | Model, key and home reads already have workspace-scope evidence. New portal surfaces need the same refusal tests as they ship before this item can be closed. |

Keep P2 behind the requested P1 work. Stay on the existing server-scoped paths and
leave a capability unavailable until its own plan and contract support its user flow.
