CREATE TRIGGER task_event_dead_letter_delete BEFORE DELETE ON task_event_dead_letters FOR EACH ROW
BEGIN
 IF OLD.event_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM task_event_outbox o JOIN node_tasks t ON t.tenant_id=o.tenant_id AND t.workspace_id=o.workspace_id AND t.node_id=o.node_id AND t.id=o.task_id WHERE o.id=OLD.event_id AND o.outcome_unknown=0 AND o.claim_id IS NULL AND o.state IN('received','obsolete','dead') AND o.expires_at<=UTC_TIMESTAMP(6) AND t.state IN('succeeded','failed','cancelled')) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RETENTION_REFUSED'; END IF;
END;