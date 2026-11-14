CREATE TRIGGER artifact_objects_insert BEFORE INSERT ON artifact_objects FOR EACH ROW
BEGIN
 IF NEW.state<>'creating' OR NEW.remote_upload_id IS NOT NULL OR NEW.remote_etag IS NOT NULL OR NEW.operation_id IS NULL OR NEW.operation_kind<>'create' OR NEW.operation_phase<>'prepared' OR NEW.operation_authority IS NULL OR NEW.execution_epoch<>NEW.requested_epoch OR NOT(NEW.execution_membership <=> NEW.membership_authority) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='OBJECT_STORED_PROVENANCE_INVALID';
 END IF;
END;