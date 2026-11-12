# Gateway lane boundaries

## GW-105 PostgreSQL key purpose

The existing schema does not record ordinary versus operation-only key
purpose. The verified candidate needs a nullable column and the matching
fresh and restored fingerprints in the shared database catalog. Reuse
the existing rotation and admission contracts. Do not infer purpose
from one-way MAC bytes or weaken the catalog check. Keep the change a
candidate until the database owner lands the migration and fingerprints.
The [key-purpose note](gateway-key-purpose.md) retains the exact evidence.

## Remaining production interfaces

- GW-102 needs an approved offline serving recipe for each pooling task
  and task-aware admission with input-only budgets. See the
  [pooling note](gateway-pooling.md).
- GW-104 has a retained scoped read producer/composition candidate.
  ARC must prepare trusted startup wiring at the real identity factory;
  GW then qualifies the exact assembled source and DEV publishes the
  matching 0.12.0 OpenAPI/SDK/Help contract. Production publication remains
  disabled until those inputs match. See the
  [producer and startup boundary](gateway-alias-capabilities.md#producer-and-typed-startup-integration).
- GW-107's CSV operations and GW-111's cutover preview contract are
  published. Their remaining work is the production continuation link
  and UI/installed flow, not defining another export or preview format.
  The isolated metering projection additionally awaits the formal ledger
  and same-source activation. See the [usage contract](gateway-usage-csv-contract.md)
  and [preview qualification](gateway-cutover-preview.md).
- GW-105 and GW-106 need authoritative durable application assignments
  from control so new and rotated keys inherit the correct shared quota
  scope. Explicit server-issued test IDs do not establish a CAT-103
  installation identity or a production app-key association.
- GW-108 and GW-121 need authorized replica destinations and the installed
  TLS entrance. GW-112 needs that entrance for installation-scale
  measurement. The one-node circuit and retained component/native retry
  measurements keep their recorded scope.
- GW-110 needs a control-owned external target/provider and secret-file
  authority that can enter normal alias admission. The present target
  binds a managed node/deployment/model; substituting an external URL
  behind that binding would misstate placement and bypass its authority.
  Define and qualify that additive contract before implementing an
  external transport. Leave connected-site upstreams optional and keep
  ordinary offline serving independent.
- GW-118 and GW-124 need agent-side credential replacement and certificate
  renewal delivery with replay and in-flight behavior. Keep gateway alias
  and request identity stable. GW-122 needs node PKI delivery.
- GW-123 has concrete [agent-profile gates](gateway-delivery.md#additional-inference-capable-enrollment-profiles-gw-123):
  the shared capability contract, agent configuration, lease validation
  and runtime manager creation still permit only docker-inference.
  ARC/AGT must integrate AI/lifecycle profiles before GW can qualify them.
- GW-103's foreground text adapters are qualified within their recorded
  native protocol scope. Tool round trips require the approved serving
  parser/template and named-call envelope, versioned adapter contracts
  and actual coding-tool interoperability. A capability read does not
  enable those adapters. See the [tool research](gateway-tool-roundtrip.md).
- GW-109 retains caller/request/deployment correlation and synthetic GPU
  placement evidence. Actual GPU execution, distributed spans and the
  complete UI path remain distinct acceptance work.
- GW-120's formal schema 59 and native Linux amd64 backend evidence are
  retained independently. Console editing and actual Portal S-304 are
  WEB's next installed acceptance; they do not require GW-104 activation.
