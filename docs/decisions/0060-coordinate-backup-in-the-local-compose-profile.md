# Coordinate backup in the local Compose profile

Give the installer an explicit maintenance backup for the shipped local
Compose installation. Persist the original PostgreSQL connection limit
and exact control container identity before stopping the writer. A
PostgreSQL connection fence excludes ordinary clients. Compare the
restored logical dump with the live tables and detect changes to the
protected file inventory before publication.

Restore the original limit and container state with a separate recovery
deadline. The same protected operation record allows a later invocation
to reconcile an interrupted backup without reading its output. Expire
unacknowledged SQL fencing requests on the database so a delayed request
cannot close an installation after recovery has reopened it.

Reject stores beyond the installer's writer authority. The object owner
must supply an independently verifiable fence or version checkpoint
before a backup may claim object consistency.

See [REL-103 research](../research/coordinated-local-backup.md).
