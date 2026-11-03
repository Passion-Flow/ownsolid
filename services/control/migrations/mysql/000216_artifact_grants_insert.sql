CREATE TRIGGER artifact_grants_insert BEFORE INSERT ON artifact_object_grants FOR EACH ROW
BEGIN
 IF NEW.revoked_at IS NOT NULL OR NOT EXISTS(SELECT 1 FROM artifact_objects o WHERE o.tenant_id=NEW.tenant_id AND o.workspace_id=NEW.workspace_id AND o.id=NEW.object_id AND o.state='ready' AND NEW.expires_at<=o.expires_at) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='OBJECT_STORED_PROVENANCE_INVALID';
 END IF;
END;