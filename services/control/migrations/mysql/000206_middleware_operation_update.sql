CREATE TRIGGER middleware_operation_update BEFORE UPDATE ON middleware_operations FOR EACH ROW
BEGIN
 IF NOT(CAST(NEW.id AS BINARY) <=> CAST(OLD.id AS BINARY)) OR
    NOT(CAST(NEW.tenant_id AS BINARY) <=> CAST(OLD.tenant_id AS BINARY)) OR
    NOT(CAST(NEW.workspace_id AS BINARY) <=> CAST(OLD.workspace_id AS BINARY)) OR
    NOT(CAST(NEW.instance_id AS BINARY) <=> CAST(OLD.instance_id AS BINARY)) OR
    NOT(CAST(NEW.kind AS BINARY) <=> CAST(OLD.kind AS BINARY)) OR
    NOT(CAST(NEW.requested_by AS BINARY) <=> CAST(OLD.requested_by AS BINARY)) OR
    NOT(CAST(NEW.intent_key AS BINARY) <=> CAST(OLD.intent_key AS BINARY)) OR
    NOT(CAST(NEW.intent_fingerprint AS BINARY) <=> CAST(OLD.intent_fingerprint AS BINARY)) OR
    NOT(CAST(NEW.input_json AS BINARY) <=> CAST(OLD.input_json AS BINARY)) OR
    NOT(CAST(NEW.actor_json AS BINARY) <=> CAST(OLD.actor_json AS BINARY)) OR
    NOT(NEW.instance_version <=> OLD.instance_version) OR
    NOT(CAST(NEW.profile_fingerprint AS BINARY) <=> CAST(OLD.profile_fingerprint AS BINARY)) OR
    NOT(CAST(NEW.request_id AS BINARY) <=> CAST(OLD.request_id AS BINARY)) OR
    NOT(NEW.created_at <=> OLD.created_at) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MIDDLEWARE_INTENT_IMMUTABLE';
 END IF;
 IF OLD.state IN('succeeded','failed','unknown') AND (
    NOT(CAST(NEW.state AS BINARY) <=> CAST(OLD.state AS BINARY)) OR
    NOT(CAST(NEW.attempt_id AS BINARY) <=> CAST(OLD.attempt_id AS BINARY)) OR
    NOT(NEW.lease_expires_at <=> OLD.lease_expires_at) OR
    NOT(CAST(NEW.result_json AS BINARY) <=> CAST(OLD.result_json AS BINARY)) OR
    NOT(CAST(NEW.result_sha256 AS BINARY) <=> CAST(OLD.result_sha256 AS BINARY)) OR
    NOT(CAST(NEW.error_code AS BINARY) <=> CAST(OLD.error_code AS BINARY)) OR
    NOT(NEW.updated_at <=> OLD.updated_at) OR
    NOT(NEW.completed_at <=> OLD.completed_at)) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MIDDLEWARE_TERMINAL_IMMUTABLE';
 END IF;
END;