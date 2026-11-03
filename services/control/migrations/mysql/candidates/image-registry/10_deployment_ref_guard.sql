CREATE TRIGGER image_registry_deployment_ref_identity BEFORE UPDATE ON image_registry_deployment_refs FOR EACH ROW
BEGIN
 IF NOT(OLD.tenant_id<=>NEW.tenant_id) OR NOT(OLD.workspace_id<=>NEW.workspace_id) OR NOT(OLD.pin_id<=>NEW.pin_id) OR NOT(OLD.node_id<=>NEW.node_id)
 OR NOT(OLD.deployment_id<=>NEW.deployment_id) OR NOT(OLD.created_revision<=>NEW.created_revision) OR NOT(OLD.created_at<=>NEW.created_at)
 OR OLD.released_at IS NOT NULL AND NOT(OLD.released_at<=>NEW.released_at) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='REGISTRY_IMMUTABLE_REFERENCE'; END IF;
END;
