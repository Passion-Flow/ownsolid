-- Inactive MySQL 8.4 source guards. Parent assigns separate actual migrations.
-- DELIMITER is a client envelope only; each CREATE TRIGGER is one database statement.
DELIMITER $$
CREATE TRIGGER task_event_outbox_insert BEFORE INSERT ON task_event_outbox FOR EACH ROW
BEGIN
 DECLARE source_time datetime(6); DECLARE source_operation varchar(64); DECLARE j JSON;
 SELECT t.created_at,t.operation INTO source_time,source_operation FROM node_tasks t JOIN managed_nodes n ON n.tenant_id=t.tenant_id AND n.workspace_id=t.workspace_id AND n.id=t.node_id JOIN workspaces w ON w.tenant_id=n.tenant_id AND w.id=n.workspace_id WHERE t.tenant_id=NEW.tenant_id AND t.workspace_id=NEW.workspace_id AND t.node_id=NEW.node_id AND t.id=NEW.task_id AND t.state='pending' AND t.attempt=0 AND n.status='active' AND w.status='active';
 IF source_time IS NULL OR NOT(source_operation<=>NEW.operation) OR NEW.generation<>1 OR NEW.state<>'prepared' OR NEW.outcome_unknown<>0 OR NEW.publish_attempts<>0 OR NEW.consume_attempts<>0 OR NEW.claim_id IS NOT NULL OR NEW.broker_sequence IS NOT NULL OR NEW.error_code IS NOT NULL OR NEW.not_before<>NEW.created_at OR NEW.updated_at<>NEW.created_at OR NEW.created_at<source_time OR NOT(CAST(SHA2(NEW.event_bytes,256) AS BINARY)<=>CAST(NEW.logical_sha256 AS BINARY)) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_SOURCE_INVALID'; END IF;
 IF NOT JSON_VALID(CONVERT(NEW.event_bytes USING utf8mb4)) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_SOURCE_INVALID'; END IF;
 SET j=CAST(CONVERT(NEW.event_bytes USING utf8mb4) AS JSON);
 IF JSON_TYPE(j)<>'OBJECT' COLLATE utf8mb4_bin OR JSON_LENGTH(j)<>11 OR JSON_TYPE(JSON_EXTRACT(j,'$.schema'))<>'INTEGER' COLLATE utf8mb4_bin OR NOT(CAST(JSON_UNQUOTE(JSON_EXTRACT(j,'$.schema')) AS BINARY)<=>CAST('1' AS BINARY)) OR NOT(CAST(JSON_UNQUOTE(JSON_EXTRACT(j,'$.event_id')) AS BINARY)<=>CAST(NEW.id AS BINARY)) OR NOT(CAST(JSON_UNQUOTE(JSON_EXTRACT(j,'$.namespace_id')) AS BINARY)<=>CAST(NEW.namespace_id AS BINARY)) OR NOT(CAST(JSON_UNQUOTE(JSON_EXTRACT(j,'$.tenant_id')) AS BINARY)<=>CAST(NEW.tenant_id AS BINARY)) OR NOT(CAST(JSON_UNQUOTE(JSON_EXTRACT(j,'$.workspace_id')) AS BINARY)<=>CAST(NEW.workspace_id AS BINARY)) OR NOT(CAST(JSON_UNQUOTE(JSON_EXTRACT(j,'$.node_id')) AS BINARY)<=>CAST(NEW.node_id AS BINARY)) OR NOT(CAST(JSON_UNQUOTE(JSON_EXTRACT(j,'$.task_id')) AS BINARY)<=>CAST(NEW.task_id AS BINARY)) OR NOT(CAST(JSON_UNQUOTE(JSON_EXTRACT(j,'$.kind')) AS BINARY)<=>CAST(NEW.event_kind AS BINARY)) OR NOT(CAST(JSON_UNQUOTE(JSON_EXTRACT(j,'$.operation')) AS BINARY)<=>CAST(NEW.operation AS BINARY)) OR NOT(CAST(JSON_UNQUOTE(JSON_EXTRACT(j,'$.created_at')) AS BINARY)<=>CAST(CONCAT(DATE_FORMAT(NEW.created_at,'%Y-%m-%dT%H:%i:%s'),IF(MICROSECOND(NEW.created_at)=0,'',CONCAT('.',TRIM(TRAILING '0' FROM DATE_FORMAT(NEW.created_at,'%f')))),'Z') AS BINARY)) OR NOT(CAST(JSON_UNQUOTE(JSON_EXTRACT(j,'$.task_created_at')) AS BINARY)<=>CAST(CONCAT(DATE_FORMAT(source_time,'%Y-%m-%dT%H:%i:%s'),IF(MICROSECOND(source_time)=0,'',CONCAT('.',TRIM(TRAILING '0' FROM DATE_FORMAT(source_time,'%f')))),'Z') AS BINARY)) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_SOURCE_INVALID'; END IF;
END$$
CREATE TRIGGER task_event_outbox_update BEFORE UPDATE ON task_event_outbox FOR EACH ROW
BEGIN
 IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.workspace_id<=>OLD.workspace_id) OR NOT(NEW.node_id<=>OLD.node_id) OR NOT(NEW.task_id<=>OLD.task_id) OR NOT(NEW.namespace_id<=>OLD.namespace_id) OR NOT(NEW.event_kind<=>OLD.event_kind) OR NOT(NEW.operation<=>OLD.operation) OR NOT(NEW.event_bytes<=>OLD.event_bytes) OR NOT(NEW.logical_sha256<=>OLD.logical_sha256) OR NOT(NEW.created_at<=>OLD.created_at) OR NOT(NEW.expires_at<=>OLD.expires_at) OR NOT(NEW.broker_identity<=>OLD.broker_identity) OR NOT(NEW.consumer_name<=>OLD.consumer_name) OR NEW.updated_at<OLD.updated_at OR OLD.state IN('received','obsolete') THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_IMMUTABLE'; END IF;
 IF NEW.generation<>OLD.generation THEN
  IF NEW.generation<>OLD.generation+1 OR NEW.delivery_id=OLD.delivery_id OR OLD.state<>'dead' OR NEW.state<>'prepared' OR NEW.outcome_unknown<>0 OR NEW.publish_attempts<>0 OR NEW.consume_attempts<>0 OR NEW.broker_sequence IS NOT NULL OR NEW.claim_id IS NOT NULL OR NEW.error_code IS NOT NULL OR NEW.not_before<>NEW.updated_at OR NOT EXISTS(SELECT 1 FROM task_event_dead_letters d WHERE d.event_id=OLD.id AND d.generation=OLD.generation AND d.retry_generation=NEW.generation AND d.retry_delivery_id=NEW.delivery_id AND d.retried_at=NEW.updated_at) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RETRY_INVALID'; END IF;
 ELSE
  IF NEW.delivery_id<>OLD.delivery_id OR NEW.publish_attempts<OLD.publish_attempts OR NEW.consume_attempts<OLD.consume_attempts OR NOT((OLD.state='prepared' AND NEW.state IN('prepared','sent','dead')) OR (OLD.state='sent' AND NEW.state IN('sent','published','received','obsolete','dead')) OR (OLD.state='published' AND NEW.state IN('published','received','obsolete','dead')) OR (OLD.state='dead' AND NEW.state IN('dead','received','obsolete'))) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_TRANSITION_INVALID'; END IF;
 END IF;
 IF NEW.state IN('received','obsolete') AND NOT EXISTS(SELECT 1 FROM task_event_receipts r WHERE r.event_id=NEW.id AND r.logical_sha256=NEW.logical_sha256 AND r.delivery_id=NEW.delivery_id AND r.generation=NEW.generation AND r.broker_identity=NEW.broker_identity AND r.consumer_name=NEW.consumer_name AND r.broker_sequence=NEW.broker_sequence AND r.outcome=NEW.state) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RECEIPT_INVALID'; END IF;
 IF NEW.state='dead' AND NOT EXISTS(SELECT 1 FROM task_event_dead_letters d WHERE d.event_id=NEW.id AND d.generation=NEW.generation AND d.error_code=NEW.error_code) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_DLQ_INVALID'; END IF;
END$$
CREATE TRIGGER task_event_outbox_delete BEFORE DELETE ON task_event_outbox FOR EACH ROW
BEGIN
 IF OLD.outcome_unknown<>0 OR OLD.claim_id IS NOT NULL OR OLD.state NOT IN('received','obsolete','dead') OR OLD.expires_at>UTC_TIMESTAMP(6) OR NOT EXISTS(SELECT 1 FROM node_tasks t WHERE t.tenant_id=OLD.tenant_id AND t.workspace_id=OLD.workspace_id AND t.node_id=OLD.node_id AND t.id=OLD.task_id AND t.state IN('succeeded','failed','cancelled')) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RETENTION_REFUSED'; END IF;
END$$
CREATE TRIGGER task_event_receipt_insert BEFORE INSERT ON task_event_receipts FOR EACH ROW
BEGIN
 DECLARE source_generation integer;
 SELECT generation INTO source_generation FROM task_event_outbox o WHERE o.id=NEW.event_id AND o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.node_id=NEW.node_id AND o.task_id=NEW.task_id AND o.logical_sha256=NEW.logical_sha256 AND o.delivery_id=NEW.delivery_id AND o.generation=NEW.generation AND o.broker_identity=NEW.broker_identity AND o.consumer_name=NEW.consumer_name AND o.state IN('sent','published','dead') AND o.created_at<=NEW.created_at FOR SHARE;
 IF source_generation IS NULL OR NOT EXISTS(SELECT 1 FROM node_tasks t WHERE t.tenant_id=NEW.tenant_id AND t.workspace_id=NEW.workspace_id AND t.node_id=NEW.node_id AND t.id=NEW.task_id AND t.state=NEW.task_state) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RECEIPT_INVALID'; END IF;
END$$
CREATE TRIGGER task_event_receipt_update BEFORE UPDATE ON task_event_receipts FOR EACH ROW
BEGIN
 SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RECEIPT_IMMUTABLE';
END$$
CREATE TRIGGER task_event_receipt_delete BEFORE DELETE ON task_event_receipts FOR EACH ROW
BEGIN
 IF NOT EXISTS(SELECT 1 FROM task_event_outbox o JOIN node_tasks t ON t.tenant_id=o.tenant_id AND t.workspace_id=o.workspace_id AND t.node_id=o.node_id AND t.id=o.task_id WHERE o.id=OLD.event_id AND o.outcome_unknown=0 AND o.claim_id IS NULL AND o.state IN('received','obsolete','dead') AND o.expires_at<=UTC_TIMESTAMP(6) AND t.state IN('succeeded','failed','cancelled')) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RETENTION_REFUSED'; END IF;
END$$
CREATE TRIGGER task_event_dead_letter_insert BEFORE INSERT ON task_event_dead_letters FOR EACH ROW
BEGIN
 DECLARE source_generation integer;
 IF NEW.retry_key IS NOT NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RETRY_INVALID'; END IF;
 IF NEW.event_id IS NOT NULL THEN
  SELECT generation INTO source_generation FROM task_event_outbox o WHERE o.id=NEW.event_id AND o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.node_id=NEW.node_id AND o.task_id=NEW.task_id AND o.generation=NEW.generation AND o.delivery_id=NEW.delivery_id AND o.broker_identity=NEW.broker_identity AND o.consumer_name=NEW.consumer_name AND o.state NOT IN('received','obsolete') AND o.created_at<=NEW.created_at AND CAST(NEW.envelope_sha256 AS BINARY)=CAST(SHA2(CONCAT('{"delivery_id":"',o.delivery_id,'","event":',o.event_bytes,'}'),256) AS BINARY) FOR SHARE;
  IF source_generation IS NULL THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_DLQ_INVALID'; END IF;
 ELSEIF NEW.kind<>'poison' OR NEW.broker_sequence IS NULL OR NEW.outcome_unknown<>0 THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_DLQ_INVALID'; END IF;
END$$
CREATE TRIGGER task_event_dead_letter_update BEFORE UPDATE ON task_event_dead_letters FOR EACH ROW
BEGIN
 IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.event_id<=>OLD.event_id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.workspace_id<=>OLD.workspace_id) OR NOT(NEW.node_id<=>OLD.node_id) OR NOT(NEW.task_id<=>OLD.task_id) OR NOT(NEW.generation<=>OLD.generation) OR NOT(NEW.delivery_id<=>OLD.delivery_id) OR NOT(NEW.kind<=>OLD.kind) OR NOT(NEW.outcome_unknown<=>OLD.outcome_unknown) OR NOT(NEW.broker_identity<=>OLD.broker_identity) OR NOT(NEW.broker_sequence<=>OLD.broker_sequence) OR NOT(NEW.consumer_name<=>OLD.consumer_name) OR NOT(NEW.envelope_sha256<=>OLD.envelope_sha256) OR NOT(NEW.error_code<=>OLD.error_code) OR NOT(NEW.created_at<=>OLD.created_at) OR OLD.retry_key IS NOT NULL OR NEW.retry_key IS NULL OR NEW.retry_generation<>OLD.generation+1 OR NOT EXISTS(SELECT 1 FROM users u WHERE u.id=NEW.retry_actor AND u.status='active' AND u.is_platform_admin=1) OR NOT EXISTS(SELECT 1 FROM task_event_outbox o WHERE o.id=OLD.event_id AND o.generation=OLD.generation AND o.state='dead' AND o.expires_at>NEW.retried_at) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RETRY_INVALID'; END IF;
END$$
CREATE TRIGGER task_event_dead_letter_delete BEFORE DELETE ON task_event_dead_letters FOR EACH ROW
BEGIN
 IF OLD.event_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM task_event_outbox o JOIN node_tasks t ON t.tenant_id=o.tenant_id AND t.workspace_id=o.workspace_id AND t.node_id=o.node_id AND t.id=o.task_id WHERE o.id=OLD.event_id AND o.outcome_unknown=0 AND o.claim_id IS NULL AND o.state IN('received','obsolete','dead') AND o.expires_at<=UTC_TIMESTAMP(6) AND t.state IN('succeeded','failed','cancelled')) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TASK_EVENT_RETENTION_REFUSED'; END IF;
END$$
DELIMITER ;
