CREATE TRIGGER image_registry_pin_identity BEFORE UPDATE ON image_registry_pins FOR EACH ROW
BEGIN
 IF NOT(OLD.id<=>NEW.id) OR NOT(OLD.tenant_id<=>NEW.tenant_id) OR NOT(OLD.workspace_id<=>NEW.workspace_id)
 OR NOT(OLD.asset_id<=>NEW.asset_id) OR NOT(OLD.source_archive_sha256<=>NEW.source_archive_sha256) OR NOT(OLD.source_manifest_sha256<=>NEW.source_manifest_sha256)
 OR NOT(OLD.backend_identity<=>NEW.backend_identity) OR NOT(OLD.created_by<=>NEW.created_by) OR NOT(OLD.actor_epoch<=>NEW.actor_epoch) OR NOT(OLD.created_at<=>NEW.created_at)
 OR OLD.inventory IS NOT NULL AND (NOT(OLD.manifest_digest<=>NEW.manifest_digest) OR NOT(OLD.config_digest<=>NEW.config_digest) OR NOT(OLD.archive_sha256<=>NEW.archive_sha256) OR NOT(OLD.archive_bytes<=>NEW.archive_bytes) OR NOT(OLD.inventory<=>NEW.inventory) OR NOT(OLD.inventory_sha256<=>NEW.inventory_sha256))
 OR NEW.revision<>OLD.revision+1 OR OLD.state='deleted' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='REGISTRY_IMMUTABLE_IDENTITY'; END IF;
END;