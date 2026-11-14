-- A used M14 database requires its verified pre-upgrade backup; never discard
-- signed provenance or tool workflow data silently during application rollback.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM plugin_versions) OR EXISTS(SELECT 1 FROM plugin_grants) OR EXISTS(SELECT 1 FROM plugin_mutation_intents) OR EXISTS(SELECT 1 FROM workflow_plugin_references) OR EXISTS(SELECT 1 FROM workflows WHERE draft_graph->>'schema_version'='2') OR EXISTS(SELECT 1 FROM workflow_versions WHERE graph->>'schema_version'='2') OR EXISTS(SELECT 1 FROM workflow_runs WHERE result_kind<>'answer') THEN
  RAISE EXCEPTION 'PLUGIN_DOWNGRADE_REQUIRES_VERIFIED_BACKUP';
 END IF;
END $$;
DROP TABLE workflow_plugin_references;
DROP TABLE plugin_mutation_intents;
DROP TABLE plugin_grants;
DROP TABLE plugin_versions;
DROP FUNCTION plugin_immutable_provenance();
ALTER TABLE workflow_runs DROP CONSTRAINT workflow_result_graph_kind;
ALTER TABLE workflow_runs DROP CONSTRAINT workflow_answer_bytes;
ALTER TABLE workflow_runs DROP CONSTRAINT workflow_non_success_result_absent;
ALTER TABLE workflow_runs DROP CONSTRAINT workflow_success_result_required;
ALTER TABLE workflow_runs DROP COLUMN tool_proof;
ALTER TABLE workflow_runs DROP COLUMN tool_result;
ALTER TABLE workflow_runs DROP COLUMN result_kind;
ALTER TABLE workflow_runs ADD CHECK(octet_length(answer)<=1048576);
ALTER TABLE workflow_runs ADD CHECK(state='succeeded' OR answer IS NULL);
ALTER TABLE workflow_runs ADD CHECK(state<>'succeeded' OR (answer IS NOT NULL AND evidence IS NOT NULL AND alias_snapshot IS NOT NULL AND usage IS NOT NULL AND error_code IS NULL));
ALTER TABLE workflow_node_traces DROP CONSTRAINT workflow_node_traces_kind_check;
ALTER TABLE workflow_node_traces ADD CONSTRAINT workflow_node_traces_kind_check CHECK(kind IN ('start','retrieval','answer','end'));
CREATE OR REPLACE FUNCTION workflow_run_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.workflow_id,NEW.owner_id,NEW.conversation_id,NEW.turn_number,NEW.version_id,NEW.draft_revision,NEW.execution_authority,NEW.membership_authority,NEW.requested_audience,NEW.graph_snapshot,NEW.graph_sha256,NEW.question,NEW.deadline_at,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.workflow_id,OLD.owner_id,OLD.conversation_id,OLD.turn_number,OLD.version_id,OLD.draft_revision,OLD.execution_authority,OLD.membership_authority,OLD.requested_audience,OLD.graph_snapshot,OLD.graph_sha256,OLD.question,OLD.deadline_at,OLD.created_at) THEN
  RAISE EXCEPTION 'WORKFLOW_RUN_IDENTITY_IMMUTABLE';
 END IF;
 RETURN NEW;
END $$;
