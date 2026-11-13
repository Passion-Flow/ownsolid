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
| DAT-105, S-503 | Stage 5 gives Datasets a summary entry only. The [screen and HTTPS handoff](dataset-console.md) records the list, import, review, publication and test download contract. Adopt the full plan entries before wiring the Training navigation to the new screens. |
| POR-101 | The shared shell can group navigation and switch workspaces. S-301 leaves the continue links for POR-102 and POR-103 and real usage for POR-111. |
| POR-102, POR-103 | The portal has conversation and run operations and a read-only published applications list. S-302 and S-303 have summary entries only. The [S-302 draft](portal-conversations.md) records the proposed surfaces, states and reusable contracts for the plan owner. The applications contract has no portal install operation. |
| POR-104 | The model catalogue already reads the published names, deployments and model metadata. The full item needs prices (TEN-107) and same-source installed description acceptance (GW-120); the description API and bilingual Web consumer are implemented. |
| POR-105 | S-305 has a summary entry only. The portal uses a same-origin Content Security Policy and the separate gateway accepts API keys. The contracts offer no session-bound portal operation to run a prompt. A browser call that carries a pasted key across origins would change the trust boundary. |
| POR-107 to POR-110 | There are no portal screen entries for these items. The plan's Stage 5 entries describe administrator surfaces. |
| POR-111 | S-307 has a summary entry only. S-301 explicitly treats usage as unavailable until the workspace-scoped usage and quota data exist. |
| POR-112 | The account page already handles profile and password. S-308 needs a full entry, and identity has a current-session read but no portal list and revocation operations for a person's other sessions. |
| POR-114 | Model, key and home reads already have workspace-scope evidence. New portal surfaces need the same refusal tests as they ship before this item can be closed. |

Keep P2 behind the requested P1 work. Stay on the existing server-scoped paths and
leave a capability unavailable until its own plan and contract support its user flow.

## Current audit consumer and installation boundary

AUD-108's explicit read recovery is published at
`124520b94be27f673bdb32a3c5d825d1d337f75d`. Its Web tree is
`8deed02cddb981a00c88d70a16bc841d4637be89`; the older a4f/e5c and be62 assets do
not contain the fix and remain immutable. A separate archive preserves all 1,230
new production asset files, their hashes and the original-author qualification.

The complete gate at `c70e9f5950f421e7bef194ae5f5ced0efaf1ca7f` passed
`make check`, `make build` and `make test`: 1,191 Console and 410 Portal Chromium
cases passed, with 28/10 existing conditional skips, no unexpected/flaky/retried
case, and 41 explicit-recovery cases. Eighteen screenshots were actually read.
The published source has byte-identical Web, API, fixture, generator, capability
and Makefile inputs. Native checks/build/tests also passed at the intermediate
integration source `4b432dbf249e01ff9af447b3e5efc354a8682403`.

The newer source's complete native combination still needs its own qualified
window. The shared Help assertion correction is now published at
`44ab52293e516128ba2efc0edc2cb2053acc3462`. Its four EN/ZH focused cases passed;
two saved-query controls rejected the old bare-URL assertion after confirming
the restored input. Both earlier MODEL full-run failures and the negative traces
remain separate. The [Help qualification](in-product-help.md#seeded-restoration-qualification)
does not relabel the prior c70 pass as a complete corrected-helper result.

A new independent client bound to that exact source passed TypeScript, eighteen
input guards, four output-preservation guards and one GW120/Help/Scope discovery
each. It reuses the byte-identical 8dee Web runtime assets; only the reviewed test
helper changes the full Web tree. Older a4f/e5c and be62 packages remain immutable.
One authorized kernel wait used the permanent carrier's same parent descriptor;
the focused/browser/client execution then took about 28 seconds and released it.
No acquisition loop, replacement carrier or new long suite ran in that narrow slot.

Actual GW120, Help, Scope and independent SQL still require DEL's new immutable
installation and fresh private fixtures. Client discovery is not installed browser
acceptance, and source schema constants are not live SQL observations. WEB's
`gw120` description browser flow precedes Help and Scope; the GW owner's independent
same-source API/permission/DB check follows WEB and the independent SQL proof.
Keep the corresponding complete items unticked.

The next independent correction is the existing UX-127 activity window: the
creation-to-now query is rejected once it exceeds configured audit bounds.
The [bounded-history research](resource-detail-pages.md#bounded-history-review)
records the source contract, eight passing isolated cache assertions, chosen
approach and product acceptance cases. Its UI correction is not implemented yet.
