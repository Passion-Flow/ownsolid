CREATE TRIGGER task_event_outbox_delete BEFORE DELETE ON task_event_outbox FOR EACH ROW
BEGIN
 IF OLD.outcome_unknown<>0 OR OLD.claim_id IS NOT NULL OR OLD.state NOT IN('received','obsolete','dead') OR OLD.expires_at>UTC_TIMESTAMP(6) OR NOT EXISTS(SELECT 1 FROM node_tasks t WHERE t.tenant_id=OLD.tenant_id AND t.workspace_id=OLD.workspace_id AND t.node_id=OLD.node_id AND t.id=OLD.task_id AND t.state IN('succeeded','failed','cancelled')) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RETENTION_REFUSED'; END IF;
END;