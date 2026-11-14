# Control internals

These packages are private to the control Go module. They are not a public SDK or a collection of independently deployed services.

| Area | Ownership | Dependency rule |
| --- | --- | --- |
| [modules](modules/README.md) | Domain authority, state transitions, repositories and HTTP adapters | Call owning domain methods; borrow selected transactions and platform transports |
| [platform](platform/README.md) | Typed configuration, SQL adapters, HTTP, mail, offline localization and bounded infrastructure | No new user/Workspace authorization shortcut or independent business commit |
| [operations](operations/README.md) | Bootstrap, runtime DB access, store initialization, backup and recovery commands | Explicit protected operator inputs, installation ownership and isolated restore checks |
| [testing](testing) | Controlled/real integration support | Test fixtures must not become runtime authority or release evidence by import alone |

[cmd/server](../cmd/server/README.md) owns process construction and shutdown. A callback borrowed by a transaction cannot commit/rollback the owner's transaction. An injected transport cannot derive trusted tenant, Workspace or object scope from client strings. The [database guide](platform/database/README.md) separates connection, migration and transaction owners; [config](platform/config/README.md) owns typed deployment input.

The directory labels currently have exceptions: identity/http is the product composition hub; mail/retention import the audit domain repository; runtime consumers use shared offline libraries under tools. These are documented in the [backend audit](../../../docs/architecture/current-product/backend-audit.md), not silently “fixed” by this documentation.

When adding an internal owner, document its input/output contract, upstream consumers, data tables/files, lock ordering, failure/uncertain outcome, configuration source and focused checks. Preserve the selected PostgreSQL/MySQL boundary and existing actual adapters; do not add a second pool or generic environment reader inside a module.
