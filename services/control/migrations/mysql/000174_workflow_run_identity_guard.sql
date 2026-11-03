CREATE TRIGGER workflow_run_identity_guard BEFORE UPDATE ON workflow_runs FOR EACH ROW
BEGIN
 IF NOT ((NEW.id <=> OLD.id) AND (NEW.tenant_id <=> OLD.tenant_id) AND (NEW.workspace_id <=> OLD.workspace_id) AND (NEW.workflow_id <=> OLD.workflow_id) AND (NEW.owner_id <=> OLD.owner_id) AND (NEW.conversation_id <=> OLD.conversation_id) AND (NEW.turn_number <=> OLD.turn_number) AND (NEW.version_id <=> OLD.version_id) AND (NEW.draft_revision <=> OLD.draft_revision) AND (NEW.execution_authority <=> OLD.execution_authority) AND (NEW.membership_authority <=> OLD.membership_authority) AND (NEW.requested_audience <=> OLD.requested_audience) AND (NEW.graph_snapshot <=> OLD.graph_snapshot) AND (NEW.graph_sha256 <=> OLD.graph_sha256) AND (NEW.question <=> OLD.question) AND (NEW.deadline_at <=> OLD.deadline_at) AND (NEW.created_at <=> OLD.created_at)) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='WORKFLOW_RUN_IDENTITY_IMMUTABLE';
 END IF;
END;
