CREATE TRIGGER image_registry_offline_ref_identity BEFORE UPDATE ON image_registry_offline_refs FOR EACH ROW
BEGIN
 IF NOT(OLD.id<=>NEW.id) OR NOT(OLD.tenant_id<=>NEW.tenant_id) OR NOT(OLD.workspace_id<=>NEW.workspace_id) OR NOT(OLD.pin_id<=>NEW.pin_id)
 OR NOT(OLD.created_by<=>NEW.created_by) OR NOT(OLD.created_at<=>NEW.created_at)
 OR OLD.released_at IS NOT NULL AND NOT(OLD.released_at<=>NEW.released_at) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='REGISTRY_IMMUTABLE_OFFLINE_REFERENCE'; END IF;
END;
