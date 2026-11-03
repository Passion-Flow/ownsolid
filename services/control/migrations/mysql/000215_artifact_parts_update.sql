CREATE TRIGGER artifact_parts_update BEFORE UPDATE ON artifact_object_parts FOR EACH ROW
BEGIN
 IF NOT(CAST(NEW.tenant_id AS BINARY) <=> CAST(OLD.tenant_id AS BINARY)) OR
    NOT(CAST(NEW.workspace_id AS BINARY) <=> CAST(OLD.workspace_id AS BINARY)) OR
    NOT(CAST(NEW.object_id AS BINARY) <=> CAST(OLD.object_id AS BINARY)) OR
    NOT(NEW.number <=> OLD.number) OR
    NOT(NEW.bytes <=> OLD.bytes) OR
    NOT(CAST(NEW.sha256 AS BINARY) <=> CAST(OLD.sha256 AS BINARY)) OR
    NOT(CAST(NEW.stage_id AS BINARY) <=> CAST(OLD.stage_id AS BINARY)) OR OLD.state='uploaded' AND (NEW.state<>'uploaded' OR NOT(CAST(NEW.etag AS BINARY) <=> CAST(OLD.etag AS BINARY))) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='OBJECT_STORED_PROVENANCE_INVALID';
 END IF;
END;