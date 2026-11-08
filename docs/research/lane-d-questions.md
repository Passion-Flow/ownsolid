# Lane D shared contracts

## GPU observations and event delivery

The device collectors produce physical GPU readings and same-boot Xid
observations. End-to-end reporting needs an architecture-owned protocol
and cursor delivery, and an authorized control-plane consumer. An
additive, bounded report operation is preferable to overloading heartbeat
or inference preflight fields. Keep the observations local until that
integration is owned and the delivery and quarantine properties can be
tested across the real allocation paths.

## Duplicate and unnamed public operations

The contract audit found four node-exporter operations in both the nodes
and components documents. Removing one declaration can remove an existing
SDK symbol. Adding explicit IDs to six cloud inventory operations can
rename generated client methods and the UI's reference entries. Resolve
the source of truth with the relevant API and UI owners, preserving or
migrating any released client symbols. Retain the current declarations
and report the findings until that coordinated change is available.
