# Gateway lane boundaries

## GW-105 PostgreSQL key purpose

The existing schema does not record ordinary versus operation-only key
purpose. The verified candidate needs a nullable column and the matching
fresh and restored fingerprints in the shared database catalog. Reuse
the existing rotation and admission contracts. Do not infer purpose
from one-way MAC bytes or weaken the catalog check. Keep the change a
candidate until the database owner lands the migration and both
fingerprints together. The [key-purpose note](gateway-key-purpose.md)
has the exact verification and patch location.

## Remaining production interfaces

- GW-102 needs an approved offline serving recipe for each pooling task
  and a task-aware admission contract with input-only budgets. See the
  [pooling note](gateway-pooling.md).
- GW-104, GW-107 and GW-111 need the versioned capability, export and
  preview contracts respectively, plus the corresponding Console or
  Portal flows. The existing scoped list and internal cutover preview
  cover the server paths recorded in the roadmap.
- GW-105 and GW-106 need authoritative durable application assignments
  from control so a new or rotated key inherits the correct shared quota
  scope. The existing private policy can use explicit server-issued IDs.
- GW-108 and GW-121 need authorized replica destinations and the installed
  TLS entrance. GW-112 needs that entrance for installation-scale
  measurement. The one-node connection circuit keeps its current scope.
- GW-118 and GW-124 need agent-side credential replacement and certificate
  renewal delivery with replay and in-flight behavior. Keep the gateway
  alias and request identity stable. GW-122 and GW-123 likewise depend on
  the node's PKI and gateway delivery paths.
- GW-103 and GW-109 retain qualified GPU acceptance for tool round trips
  and tracing. The CPU protocol fixtures qualify the recorded scope only.
