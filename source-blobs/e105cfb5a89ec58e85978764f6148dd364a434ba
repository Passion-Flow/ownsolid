# Protected installation and recovery operations

These packages implement explicit operator work selected by [cmd/server](../../cmd/server/README.md). They do not provide general-purpose browser administration or alternate user/Workspace authorization.

| Owner | Concrete responsibility |
| --- | --- |
| [healthprobe](healthprobe/README.md) | Read-only selected-origin health client with explicit dial/deadline/private CA, without database/worker construction; see its scoped acceptance |
| [servingcheck](servingcheck/README.md) | Read-only comparison of explicit public deployment intent with effective typed transport and actual protected TLS material, before SQL/worker construction |
| [bootstrap](bootstrap/README.md) | Initial administrator/bootstrap packet creation, private files and vendor-specific initial state |
| [databaseaccess](databaseaccess/README.md) | Runtime database principal/grants, catalog binding, migration access and restored-installation validation |
| [storeinitialization](storeinitialization/README.md) | Explicit private artifact namespace initialization and namespace locks, including selected MySQL paths |
| [mysqloperator](mysqloperator/README.md) | Protected connector inputs and fresh/selected runtime context for MySQL operator commands |
| [mysqlbackup](mysqlbackup/README.md) | Native database-aware backup/import, source manifests, owned store inventories and independent restore receipts |
| [postgresmaintenance](postgresmaintenance/README.md) | Same-session installation-fenced PostgreSQL releases quiescence; no state mutation or standalone backup authority |
| [postgresbackup](postgresbackup/README.md) | Continuously installation-fenced synchronized matched backup, explicit fresh-target SQL/four-store restoration, original recovery fencing and read-only committed-result reconciliation; runtime role, served authority and Kubernetes qualification retain separate stages |
| [recovery](recovery/README.md) | Existing inference/training/preparation/object/task-event/middleware/private-cluster state invalidation and restored-store reconciliation |

Configuration, credential references, installation identity, selected schema and source/artifact inventories are inputs, not values inferred from unrelated local files. Database access must preserve the fixed least-privilege catalog. Backups must use the owning database-aware implementation; copying a live database directory is not a substitute.

Recovery callbacks such as restore-within paths participate in the caller's transaction and cannot commit it independently. Fresh destination roots, exact stored identities and original uncertain outcomes remain part of the contract. Restoring bytes does not authorize a stale worker, gateway, node or external operation to resume; the owning domain must invalidate or reconcile its authority.

Change operator input schemas/examples, selected database commands, offline maintenance libraries, domain integrity/snapshot hooks and failure/restart tests together. See [M17](../../../../docs/milestones/m17-offline-compose), [M20](../../../../docs/milestones/m20-mysql), [M26](../../../../docs/milestones/m26-middleware) and [release configuration examples](../../../../config/examples/release).

Run a focused package check for the affected owner, for example `go -C services/control test ./internal/operations/storeinitialization`. Actual principal grants, native dump/import, isolated restore and process interruption require their explicit fixtures; an output manifest alone does not establish successful restoration.
