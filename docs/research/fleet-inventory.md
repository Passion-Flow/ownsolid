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
