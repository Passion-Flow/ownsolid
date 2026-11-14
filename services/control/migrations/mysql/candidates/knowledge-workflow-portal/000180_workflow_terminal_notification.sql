CREATE TRIGGER workflow_terminal_notification AFTER UPDATE ON workflow_runs FOR EACH ROW
BEGIN
 IF OLD.state IN('pending','running') AND NEW.state IN('succeeded','failed','cancelled','interrupted') THEN
  IF EXISTS(SELECT 1 FROM terminal_notifications WHERE run_id=NEW.id) THEN
   IF NOT EXISTS(SELECT 1 FROM terminal_notifications n WHERE n.tenant_id=NEW.tenant_id AND n.workspace_id=NEW.workspace_id AND n.workflow_id=NEW.workflow_id AND n.run_id=NEW.id AND n.recipient_id=NEW.owner_id AND n.outcome=NEW.state AND n.error_code <=> NEW.error_code) THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='NOTIFICATION_RUN_PROVENANCE_REQUIRED';
   END IF;
  ELSE
   INSERT INTO terminal_notifications(tenant_id,workspace_id,workflow_id,run_id,recipient_id,outcome,error_code)
   VALUES(NEW.tenant_id,NEW.workspace_id,NEW.workflow_id,NEW.id,NEW.owner_id,NEW.state,NEW.error_code);
  END IF;
 END IF;
END;
