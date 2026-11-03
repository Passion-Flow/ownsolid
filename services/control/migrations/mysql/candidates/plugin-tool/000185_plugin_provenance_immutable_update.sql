CREATE TRIGGER plugin_provenance_immutable_update BEFORE UPDATE ON plugin_versions FOR EACH ROW
BEGIN
 IF NOT ((NEW.id <=> OLD.id) AND (NEW.tenant_id <=> OLD.tenant_id) AND (NEW.plugin_id <=> OLD.plugin_id) AND (NEW.release_version <=> OLD.release_version) AND (NEW.version_major <=> OLD.version_major) AND (NEW.version_minor <=> OLD.version_minor) AND (NEW.version_patch <=> OLD.version_patch) AND (NEW.abi <=> OLD.abi) AND (NEW.executor <=> OLD.executor) AND (NEW.capsule <=> OLD.capsule) AND (NEW.capsule_sha256 <=> OLD.capsule_sha256) AND (NEW.manifest <=> OLD.manifest) AND (NEW.manifest_sha256 <=> OLD.manifest_sha256) AND (NEW.key_id <=> OLD.key_id) AND (NEW.imported_by <=> OLD.imported_by) AND (NEW.created_at <=> OLD.created_at)) THEN
  SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PLUGIN_PROVENANCE_IMMUTABLE';
 END IF;
END;
