
## Gateway alias description successor

The original [GW-120 SQL](gateway-model-description.md) matches the
reviewed Gateway fixture. Its up checksum is
`158f874037201b0309d2f4dc579dcf988153a8d0d19e438bf92b328b3ca97466`.
Applying those bytes in a disposable schema after 58 created
`model_alias_description_intents` with zero of the two non-null cell
columns with defaults required by [ADR 0006](../decisions/0006-cell-and-site-scope.md).
The existing cell-owned table check therefore rejects this successor.

Activation requires the Gateway-owned replacement SQL to define both
scoped columns with the local defaults. Recompute the signed lineage
checksum, table count and all three catalog forms on the actual
predecessor, and rerun the ownership and logical restore tests against
those exact bytes.
