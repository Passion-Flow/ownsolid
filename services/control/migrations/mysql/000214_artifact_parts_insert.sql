CREATE TRIGGER artifact_parts_insert BEFORE INSERT ON artifact_object_parts FOR EACH ROW
BEGIN
 IF NEW.state<>'intent' OR NEW.etag IS NOT NULL OR NOT EXISTS(SELECT 1 FROM artifact_objects o WHERE o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.id=NEW.object_id AND o.state='uploading' AND o.remote_upload_id IS NOT NULL AND o.operation_id IS NULL AND (NEW.number-1)*o.part_bytes<o.expected_bytes AND NEW.bytes=LEAST(o.part_bytes,o.expected_bytes-(NEW.number-1)*o.part_bytes)) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='OBJECT_STORED_PROVENANCE_INVALID';
 END IF;
END;