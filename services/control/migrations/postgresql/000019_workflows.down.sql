-- Operator rollback only after preserving an isolated database-aware backup.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflows) OR EXISTS(SELECT 1 FROM workflow_mutation_intents) THEN
  RAISE EXCEPTION 'WORKFLOW_RETAINED_STATE_REQUIRES_RESTORE';
 END IF;
END $$;
DROP TABLE workflow_mutation_intents;
DROP TABLE workflow_node_traces;
DROP TABLE workflow_runs;
DROP FUNCTION workflow_run_identity();
DROP TABLE workflow_conversations;
ALTER TABLE workflows DROP CONSTRAINT workflow_current_version_scope;
DROP TABLE workflow_versions;
DROP FUNCTION workflow_immutable_publication();
DROP TABLE workflows;
