CREATE TRIGGER image_registry_grant_identity BEFORE UPDATE ON image_registry_grants FOR EACH ROW
BEGIN
 IF NOT(OLD.id<=>NEW.id) OR NOT(OLD.tenant_id<=>NEW.tenant_id) OR NOT(OLD.workspace_id<=>NEW.workspace_id) OR NOT(OLD.pin_id<=>NEW.pin_id)
 OR NOT(OLD.node_id<=>NEW.node_id) OR NOT(OLD.deployment_id<=>NEW.deployment_id) OR NOT(OLD.task_id<=>NEW.task_id) OR NOT(OLD.revision<=>NEW.revision)
 OR NOT(OLD.created_session_id<=>NEW.created_session_id) OR NOT(OLD.created_token_hash<=>NEW.created_token_hash)
 OR NOT(OLD.created_by<=>NEW.created_by) OR NOT(OLD.actor_epoch<=>NEW.actor_epoch) OR NOT(OLD.created_at<=>NEW.created_at) OR NOT(OLD.expires_at<=>NEW.expires_at)
 OR OLD.revoked_at IS NOT NULL AND NOT(OLD.revoked_at<=>NEW.revoked_at) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='REGISTRY_IMMUTABLE_GRANT'; END IF;
END;