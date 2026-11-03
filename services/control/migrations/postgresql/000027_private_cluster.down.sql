-- Stopped isolated rollback only; retained execution/intent history must be empty.
LOCK TABLE private_cluster_connections, private_cluster_runs, private_cluster_intents IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM private_cluster_connections)
 OR EXISTS(SELECT 1 FROM private_cluster_runs)
 OR EXISTS(SELECT 1 FROM private_cluster_intents) THEN
  RAISE EXCEPTION 'PRIVATE_CLUSTER_ROLLBACK_REQUIRES_EMPTY_STATE';
 END IF;
END $$;
ALTER TABLE private_cluster_connections DROP CONSTRAINT private_cluster_active_run;
DROP TABLE private_cluster_intents;
DROP TABLE private_cluster_runs;
DROP TABLE private_cluster_connections;
DROP FUNCTION private_cluster_connection_guard();
DROP FUNCTION private_cluster_run_guard();
DROP FUNCTION private_cluster_intent_guard();
DROP FUNCTION private_cluster_history_guard();
