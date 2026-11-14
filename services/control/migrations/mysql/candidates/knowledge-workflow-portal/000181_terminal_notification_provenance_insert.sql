CREATE TRIGGER terminal_notification_provenance_insert BEFORE INSERT ON terminal_notifications FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE r.tenant_id=NEW.tenant_id AND r.workspace_id=NEW.workspace_id AND r.workflow_id=NEW.workflow_id AND r.id=NEW.run_id AND r.owner_id=NEW.recipient_id AND r.state=NEW.outcome AND (r.error_code <=> NEW.error_code) AND r.finished_at IS NOT NULL) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='NOTIFICATION_RUN_PROVENANCE_REQUIRED';
 END IF;
END;
