CREATE TRIGGER task_event_dead_letter_insert BEFORE INSERT ON task_event_dead_letters FOR EACH ROW
BEGIN
 DECLARE source_generation integer;
 IF NEW.retry_key IS NOT NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RETRY_INVALID'; END IF;
 IF NEW.event_id IS NOT NULL THEN
  SELECT generation INTO source_generation FROM task_event_outbox o WHERE o.id=NEW.event_id AND o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.node_id=NEW.node_id AND o.task_id=NEW.task_id AND o.generation=NEW.generation AND o.delivery_id=NEW.delivery_id AND o.broker_identity=NEW.broker_identity AND o.consumer_name=NEW.consumer_name AND o.state NOT IN('received','obsolete') AND o.created_at<=NEW.created_at AND CAST(NEW.envelope_sha256 AS BINARY)=CAST(SHA2(CONCAT('{"delivery_id":"',o.delivery_id,'","event":',o.event_bytes,'}'),256) AS BINARY) FOR SHARE;
  IF source_generation IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_DLQ_INVALID'; END IF;
 ELSEIF NEW.kind<>'poison' OR NEW.broker_sequence IS NULL OR NEW.outcome_unknown<>0 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_DLQ_INVALID'; END IF;
END;