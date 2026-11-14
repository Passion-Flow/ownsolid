# FLT-101 rack placement constraints

NetBox records a device's physical rack, position and face independently of
its tenant. The ownsolid placement key should keep the authenticated scope,
rack ID and occupied units separately from the Agent's observed properties.
A stable rack ID is also the enrollment rate key when a scoped token has
an operator-approved placement.

PostgreSQL range exclusion can express an overlap directly. Combining range
overlap with equality on a scoped rack UUID generally adds the bundled
btree_gist extension and its release admission. A unique key on normalized
rack units can enforce overlap without installing an extension or taking a
fleet-wide lock. A bounded product placement command would insert each
occupied position in one transaction and take consistent rack and node
locks for moves and capacity changes. A full-depth reservation consumes
both faces. A positional row links to its exact scoped node assignment.
Agent observations never write these rows.

The first prototype proves uniqueness, concurrent rejection, rollback of
a failed move and separation by rack, scope and face. It uses synthetic
IDs and the existing PostgreSQL licence. The production model still needs
parent and node foreign keys, a typed capacity bound, the operator API,
authorization, audit and the console. This prototype never assigns a
migration number.

## Primary sources

- [NetBox device placement](https://netbox.readthedocs.io/en/stable/models/dcim/device/)
- [PostgreSQL 18 constraints](https://www.postgresql.org/docs/18/ddl-constraints.html#DDL-CONSTRAINTS-UNIQUE-CONSTRAINTS)
- [PostgreSQL 18 range constraints](https://www.postgresql.org/docs/18/rangetypes.html#RANGETYPES-CONSTRAINT)
- [PostgreSQL license](https://www.postgresql.org/about/licence/)

## Disconnected uniqueness probe

A disposable PostgreSQL 18.6 Linux arm64 fixture reproduced lock contention
between two writes. A transaction claimed both faces of one destination unit.
A concurrent move tried the occupied face and failed on the scoped primary key
after the first transaction committed. Rollback retained both source units and
left no partial destination reservation. The same unit and face in another
rack or tenant, and opposite faces of a free unit, were accepted. The
fixture used Unix sockets with networking disabled. These are storage
properties of the prototype. Production placement requires the scoped parent
relations and authorized transactional write path above.
