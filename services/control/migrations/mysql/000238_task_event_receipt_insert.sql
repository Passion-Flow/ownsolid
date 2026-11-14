CREATE TRIGGER task_event_receipt_insert BEFORE INSERT ON task_event_receipts FOR EACH ROW
BEGIN
 DECLARE source_generation integer;
 SELECT generation INTO source_generation FROM task_event_outbox o WHERE o.id=NEW.event_id AND o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.node_id=NEW.node_id AND o.task_id=NEW.task_id AND o.logical_sha256=NEW.logical_sha256 AND o.delivery_id=NEW.delivery_id AND o.generation=NEW.generation AND o.broker_identity=NEW.broker_identity AND o.consumer_name=NEW.consumer_name AND o.state IN('sent','published','dead') AND o.created_at<=NEW.created_at FOR SHARE;
 IF source_generation IS NULL OR NOT EXISTS(SELECT 1 FROM node_tasks t WHERE t.tenant_id=NEW.tenant_id AND t.workspace_id=NEW.workspace_id AND t.node_id=NEW.node_id AND t.id=NEW.task_id AND t.state=NEW.task_state) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RECEIPT_INVALID'; END IF;
END;