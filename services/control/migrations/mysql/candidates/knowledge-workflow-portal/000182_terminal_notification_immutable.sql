CREATE TRIGGER terminal_notification_immutable BEFORE UPDATE ON terminal_notifications FOR EACH ROW
BEGIN
 IF NOT ((NEW.id <=> OLD.id) AND (NEW.tenant_id <=> OLD.tenant_id) AND (NEW.workspace_id <=> OLD.workspace_id) AND (NEW.workflow_id <=> OLD.workflow_id) AND (NEW.run_id <=> OLD.run_id) AND (NEW.recipient_id <=> OLD.recipient_id) AND (NEW.outcome <=> OLD.outcome) AND (NEW.error_code <=> OLD.error_code) AND (NEW.occurred_at <=> OLD.occurred_at)) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='NOTIFICATION_PROVENANCE_IMMUTABLE';
 END IF;
 IF OLD.read_at IS NOT NULL AND NOT (NEW.read_at <=> OLD.read_at) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='NOTIFICATION_READ_TIME_IMMUTABLE';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM workflow_runs r WHERE r.tenant_id=NEW.tenant_id AND r.workspace_id=NEW.workspace_id AND r.workflow_id=NEW.workflow_id AND r.id=NEW.run_id AND r.owner_id=NEW.recipient_id AND r.state=NEW.outcome AND (r.error_code <=> NEW.error_code) AND r.finished_at IS NOT NULL) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='NOTIFICATION_RUN_PROVENANCE_REQUIRED';
 END IF;
END;
