DROP TABLE workflow_mutation_intents;
DROP TABLE workflow_node_traces;
DROP TABLE workflow_runs;
DROP FUNCTION workflow_run_identity();
DROP TABLE workflow_conversations;
ALTER TABLE workflows DROP CONSTRAINT workflow_current_version_scope;
DROP TABLE workflow_versions;
DROP FUNCTION workflow_immutable_publication();
DROP TABLE workflows;
