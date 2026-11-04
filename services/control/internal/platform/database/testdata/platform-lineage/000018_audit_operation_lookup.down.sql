-- Explicit scoped operator rollback only; reconcile version18 metadata in the same locked transaction.
DROP INDEX audit_operation_lookup_idx;
