CREATE TRIGGER artifact_objects_update BEFORE UPDATE ON artifact_objects FOR EACH ROW
BEGIN
 IF (OLD.remote_upload_id IS NOT NULL AND NOT(CAST(NEW.remote_upload_id AS BINARY) <=> CAST(OLD.remote_upload_id AS BINARY))) OR (OLD.remote_etag IS NOT NULL AND NOT(CAST(NEW.remote_etag AS BINARY) <=> CAST(OLD.remote_etag AS BINARY))) OR (OLD.state IN('deleted','aborted') AND NEW.operation_id IS NOT NULL) OR NOT(CAST(NEW.id AS BINARY) <=> CAST(OLD.id AS BINARY)) OR
    NOT(CAST(NEW.tenant_id AS BINARY) <=> CAST(OLD.tenant_id AS BINARY)) OR
    NOT(CAST(NEW.workspace_id AS BINARY) <=> CAST(OLD.workspace_id AS BINARY)) OR
    NOT(CAST(NEW.created_by AS BINARY) <=> CAST(OLD.created_by AS BINARY)) OR
    NOT(CAST(NEW.requested_audience AS BINARY) <=> CAST(OLD.requested_audience AS BINARY)) OR
    NOT(NEW.requested_epoch <=> OLD.requested_epoch) OR
    NOT(CAST(NEW.membership_authority AS BINARY) <=> CAST(OLD.membership_authority AS BINARY)) OR
    NOT(CAST(NEW.creation_key AS BINARY) <=> CAST(OLD.creation_key AS BINARY)) OR
    NOT(CAST(NEW.fingerprint AS BINARY) <=> CAST(OLD.fingerprint AS BINARY)) OR
    NOT(CAST(NEW.store_id AS BINARY) <=> CAST(OLD.store_id AS BINARY)) OR
    NOT(CAST(NEW.name AS BINARY) <=> CAST(OLD.name AS BINARY)) OR
    NOT(CAST(NEW.purpose AS BINARY) <=> CAST(OLD.purpose AS BINARY)) OR
    NOT(NEW.expected_bytes <=> OLD.expected_bytes) OR
    NOT(CAST(NEW.sha256 AS BINARY) <=> CAST(OLD.sha256 AS BINARY)) OR
    NOT(NEW.part_bytes <=> OLD.part_bytes) OR
    NOT(NEW.created_at <=> OLD.created_at) OR NEW.upload_expires_at>OLD.upload_expires_at OR NEW.expires_at>OLD.expires_at OR NEW.revision<OLD.revision OR NOT(NEW.state=OLD.state OR (OLD.state='creating' AND NEW.state IN('uploading','aborting')) OR (OLD.state='uploading' AND NEW.state IN('completing','aborting')) OR (OLD.state='completing' AND NEW.state IN('ready','aborting')) OR (OLD.state='ready' AND NEW.state='deleting') OR (OLD.state='deleting' AND NEW.state IN('deleted','aborting')) OR (OLD.state='aborting' AND NEW.state='aborted')) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='OBJECT_STORED_PROVENANCE_INVALID';
 END IF;
END;