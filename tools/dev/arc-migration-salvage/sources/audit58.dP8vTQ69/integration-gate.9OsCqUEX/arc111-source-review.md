# ARC-111 standalone Agent gateway database role

The dedicated process accepts session streams and readiness probes. The
session backend reaches peer verification, heartbeat, claim and acknowledgement.
The gateway starts liveness and wake workers. The normal graph may delegate
claims to Training, Model Preparation and managed cell tasks, and a heartbeat
can persist Component and Inference reports. The role cannot be derived from
the three stream message kinds alone.

The same Control DSN and pool settings are currently opened by the standalone
command. Startup calls `database.CheckRuntimeWithin` and
`installationBootstrapped`. After Control changes its installation lock holder,
`installationFence.check` calls `database.CheckRuntimeReady` under a bounded
lock timeout. Both paths use `currentPrivilegesSQL`, which requires business
SELECT, INSERT, UPDATE and DELETE in the current schema. A separate password
or a smaller `CONNECTION LIMIT` with the same grant profile does not isolate
database authority. Passing the narrow role through the current checker would
refuse startup or fence the gateway on a Control restart.

Use a dedicated, non-inheriting login and a separate typed DSN file and pool
budget for the standalone process. Preserve the common schema and bootstrap
checks. Give the gateway a specific privilege policy and use it in both
initial and restart readiness. Derive permissions from the normal graph and
owner fixtures, including optional profiles, Audit append, wake reads and
liveness writes. Keep the current embedded Control listener and its existing
serving role. Verify privilege and connection limits separately from schema
fingerprints, and require explicit grant preparation from the installer
owner. No new dependency or dataset is needed. PostgreSQL's licence permits
commercial and offline deployment and redistribution with its notice.

Primary references:

- [PostgreSQL 18 CREATE ROLE](https://www.postgresql.org/docs/18/sql-createrole.html)
- [PostgreSQL 18 GRANT](https://www.postgresql.org/docs/18/sql-grant.html)
- [PostgreSQL 18 privilege inquiry functions](https://www.postgresql.org/docs/18/functions-info.html)
- [PostgreSQL license](https://www.postgresql.org/about/licence/)

Verify with a normal standalone TLS process and a real, disconnected
PostgreSQL database. Prove the needed exchanges and workers work on the
narrow role. Deny unrelated admin writes, DDL, grant option, ownership,
membership, cross-schema reads and direct chain-head access. Saturate
gateway connections while a separate Control connection still works.
Revalidate the role on restart. Missing grants and changed role scope
must fail closed. Keep the installer, active schema and workload owner
dependencies explicit before implementation.
