# APP lane integration boundaries

The application module owns portal projections. Its existing reads borrow the
workflow owner for configuration and use the workflow tables as projections. A
second owner for workflow execution or conversation mutations would bypass the
existing publication, lease and authority fences.

| Items | Required integration | Safe scope in this lane |
| --- | --- | --- |
| APP-101, APP-102 | A browser editor and a versioned workflow graph and runtime that admit the same nodes and edges | Preserve the two inherited graph schemas and their behavior. |
| APP-103, APP-104, APP-105, APP-107, APP-111 | Workflow state, execution, tool invocation and run history in `modules/workflows`, with browser controls | Keep one workflow owner and no parallel execution state. See [durable human approval](workflow-human-approval.md). |
| APP-106, APP-108, APP-109, APP-110 | Workflow version and conversation writes, publication, gateway or portal UI, depending on the item | Keep application projections read-only. Use their current authority for assistant reads. |
| AGT-101, AGT-109 | The console and portal command bar, panels and route integration | Keep command destinations aligned with the currently shipped routes. |
| AGT-102 to AGT-108, AGT-110 to AGT-112 | A session-authenticated assistant entry point, audited write adapters and gateway admission | Implement owned components where they can be tested independently. Enable a write only when the same transaction can enforce current permission, idempotency and audit attribution. |
| APP-201 to APP-207, AGT-201 to AGT-203 | Workflow execution or a shipped product-assistant entrance and the preceding P1 authority guarantees | Leave release items open until their P1 prerequisites are in place. |

The narrow integration option is additive ownership of workflow, tool and UI work
by their respective lanes. Moving those responsibilities into this lane would
require changing the explicit path boundary. Replacing them with an application
projection side channel would risk competing ownership of the same rows and
would still leave the release without a usable browser path.
