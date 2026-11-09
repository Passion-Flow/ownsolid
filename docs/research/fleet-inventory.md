# Fleet inventory and rack identity

Research for FLT-101 and the rack identity needed by ARC-112. The current node
record holds site, cell, workspace, status and the latest bounded agent facts.
The enrollment limiter groups by source subnet until an operator can assign a
physical rack before a node joins.

## Upstream models

- [NetBox locations and facilities](https://netbox.readthedocs.io/en/stable/features/facilities/)
  distinguish rooms and rows from racks. Its [device model](https://netbox.readthedocs.io/en/stable/models/dcim/device/)
  keeps device serials separate from local asset tags and records mounting position
  and face as optional attributes.
- [DMTF Redfish chassis placement](https://redfish.dmtf.org/schemas/v1/DSP2046_2025.3.html)
  keeps row, rack and rack offset beside the chassis serial. The resource schema
  also identifies adapter and drive assemblies independently of their host.
- [Kubernetes topology spread](https://kubernetes.io/docs/concepts/scheduling-eviction/topology-spread-constraints/)
  consumes node labels as projections of failure domains. Its
  [NodeRestriction rules](https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/#noderestriction)
  keep a node from assigning itself labels reserved for the operator.

No new package, vendor schema, model or data set is required for the P1 inventory.
The upstream projects and Redfish schemas are design references. Keep Redfish access
in FLT-202 and use locally generated records in tests. The existing PostgreSQL
[license](https://www.postgresql.org/about/licence/) allows commercial use and
redistribution with its notice, with no online dependency.

## Proposed model

Use stable internal IDs scoped to the installation. The physical tree goes from
site to room, row and rack. Cell membership remains an operational attribute of
a node, so moving a workload boundary cannot change the identity of a physical
rack. Keep node placement and its reserved mounting extent operator-owned.
Managed nodes can remain unplaced, including every enrolled node after migration.

Use separate device records for GPUs, NICs and disks. The server binds observations
to its authenticated node and records their last seen time. Use an available
manufacturer serial, device UUID or stable local bus identity as evidence within
that node, with a separate field for the reported slot. A missing serial and a
changed device do not rewrite the identity of another device or free a rack slot.
FLT-103 and the REL device collection work define the available observations.

Bind rack placement to an authorized join token or a node assignment before
enrollment. ARC-112 can use that stable rack key without trusting a claimed rack
or serial from the agent. Keep the existing subnet limit for tokens with no
placement. An operator's rack rename does not change the key or reset its budget.

A single hierarchical label string was rejected because changing a room or
rack name would change authorization and rate keys. Matching enrollment by source
address or a hardware serial alone would let NAT, reuse and copied observations
assign the wrong physical location. Copying a DCIM deployment into ownsolid would
add an offline dependency and a second inventory authority.

## Tests and completion

- An administrator creates a room, row and rack, places a node, and sees its
  devices and serial evidence through the console. An existing unplaced agent
  still joins and continues to report facts over v1.
- Concurrent placements of the same rack extent cannot both succeed. A failed
  placement leaves the previous one intact. A node report cannot move a node or
  assign a device to another node. An unrelated workspace cannot read or modify
  placement.
- Rack labels can change independently of rack identity. Token assignment and
  retirement are audited. List reads are paginated and use indexes on the scope
  and parent keys. Future ARC-112 tests must prove distinct racks do not share
  a rate budget and one rack cannot evade its budget by changing a label.
- The node and inventory HTTP contracts, migrations and console need to land
  together with offline acceptance. The ARC lane can own node storage, but this
  item remains open under the current API and web ownership boundary.

## Readable observations on the current schema

Reviewed 2026-11-09 against the device identity and placement rules linked above.
The Nodes owner can read an observation from `managed_nodes.facts` through the
same ordered session, installation and workspace fences as node detail. The
site and cell come from that scoped row. Do not accept them from agent facts,
observation payloads or query parameters. Legacy engines that project the
installation's default site and cell keep that behavior. Keep the v1 wire
contract and existing node HTTP response unchanged.

- The persisted v1 facts provide OS, architecture, hostname, logical CPU count,
  total memory, agent version and one filesystem sample at the configured path.
  The [Linux statfs interface](https://man7.org/linux/man-pages/man2/statfs.2.html)
  reports filesystem blocks available to the caller. A filesystem sample cannot
  establish a physical disk, drive serial or mount topology. Report the original
  observation time. A stored missing or null available count is unknown. A stored
  zero is a measured value. An incomplete stored object yields no host
  observation. Corrupt stored JSON keeps the existing storage error. Existing agents already send the available count, including zero.
- The Deployments owner already exposes `HardwareWithin` for the current node,
  workspace and installation in the same repository transaction. Use its bounded
  freshness window and validated NVIDIA observation. A missing, stale or
  unavailable probe is unknown. A successful empty list is a measured empty
  list. The current agent can report a populated list and an engine-readiness
  error together, so retain positively observed devices. Project hardware identity
  and capacity without the deployment envelope or scheduling metrics. Sort by
  UUID for deterministic output. These facts neither establish durable device
  identity nor qualify the hardware for scheduling.
- Exercise this slice through authenticated reads on real PostgreSQL, including
  a v1 enrollment, a scoped inference heartbeat, absent facts and probe failure,
  measured zero and empty, stale observation, forged placement in persisted
  payload, revoked membership and foreign tenant/workspace refusals. The normal
  Agent needs no new capability or software update. There is no new dependency or
  license beyond those already identified above.

On local PostgreSQL 18.6 with pgvector 0.8.7, `TestActualObservedInventory`,
`TestActualNodeReadCatalog` and `TestActualNodeReadAuthority` passed with a
loopback database and synthetic enrollment and GPU observations. The existing
Linux arm64 collector independently passed in a network-disconnected, unprivileged
container, reading its actual OS, memory, CPU and filesystem. This does not
establish a physical GPU inventory or an end-to-end console flow.

## Handoff for placement and the inventory entrance

The next persistence change needs operator-owned room, row, rack and node
placement records with installation and parent foreign keys, nullable placement
for old nodes, and a transactional non-overlap rule for rack positions. Assign
placement through existing actor authorization, explicit node or join-token
scope, idempotency and audit. The Agent observation owner must supply optional
NIC, physical disk, serial and slot observations with their own stable local
identities, timestamps and absent-versus-empty semantics before those categories
can appear in inventory. Keep observation refresh separate from placement writes.

The Nodes API and Web owners can expose `Service.ObservedInventory` as a typed
read after agreeing the additive OpenAPI route, generated client and presentation
of observation timestamps and unknown categories. The physical placement API and
console need the schema and operator permissions described above. Do not close
FLT-101 until real enrollment, authorized placement and reads complete offline
through the console and the isolation and concurrency checks pass.

## Rack occupancy prototype

[NetBox device placement](https://netbox.readthedocs.io/en/stable/models/dcim/device/)
records rack, position and face separately from the device's tenant. Use the
operator-owned rack identity to scope placement, and store the occupied units
and faces independently of Agent observations. A full-depth node reserves both
faces. Each occupancy row references its exact scoped assignment.

A PostgreSQL [unique key](https://www.postgresql.org/docs/18/ddl-constraints.html#DDL-CONSTRAINTS-UNIQUE-CONSTRAINTS)
over the rack's scope, rack ID, unit and face can refuse overlap during a
concurrent insert. A move removes the previous units and reserves the next
units in the same transaction. Lock rack and node records in a consistent order
and check capacity within that transaction. A
[range exclusion](https://www.postgresql.org/docs/18/rangetypes.html#RANGETYPES-CONSTRAINT)
can express overlap directly, but combining it with rack equality adds an
operator-class dependency. Unit occupancy uses the existing PostgreSQL image,
license and synthetic data. It adds no software or data dependency.

A disconnected PostgreSQL 18.6 Linux arm64 prototype occupied both faces of
one unit in a destination rack. A concurrent move waited on the unique key
and was refused after the holder committed. Rollback preserved both source
units and left no partial destination reservation. Independent scoped racks
and opposite faces of a free unit accepted placements. The fixture's tenant
and rack keys represent separate rack identities. Production placement still
needs trusted parent and node relations, bounded capacity, authorization,
idempotency, audit and console acceptance.
