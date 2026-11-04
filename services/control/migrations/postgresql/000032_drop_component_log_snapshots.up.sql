-- Component log snapshots live in the log store (OBS-002, ADR 0012). Nothing reads or
-- writes this table any more. It held only the last snapshot per node, which the
-- administrator can fetch again with the logs action, so its rows are not carried over.
DROP TABLE component_log_snapshots;
