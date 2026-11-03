# Cumulative MVP PostgreSQL maintenance selection

Use the existing complete [MVP typed Control configuration](../control/mvp-postgresql-cpu-v1.example.yaml)
and its [explicit environment mapping](../control/mvp-postgresql-cpu-v1.env.example).
The source-bound native operator configuration must select
`server.application_profile: mvp-postgresql-cpu-v1`; retain PostgreSQL and the
same installation identity, credentials, four roots and original lock session.
No additional boolean, worker, listener, scheduler or configuration authority is
created by this owner. A stored maintenance DTO grants no future lock authority.

The check reads the exact new29-entry/108-table catalog and original14 plus8
additional SQL workload/custody predicates. Terminal history is retained.
Uncertain physical leases, external operations and incomplete module state must
be reconciled by their original owner; timestamps never imply physical release.
The [native procedure](../../../docs/runbooks/postgresql/mvp-postgresql-cpu-v1-native-recovery.md)
distinguishes the standalone historical observation from continuous native export.
