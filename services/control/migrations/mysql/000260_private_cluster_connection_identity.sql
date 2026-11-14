CREATE TRIGGER private_cluster_connection_identity BEFORE UPDATE ON private_cluster_connections FOR EACH ROW
BEGIN
 IF NOT(OLD.id <=> NEW.id) OR
 NOT(OLD.tenant_id <=> NEW.tenant_id) OR
 NOT(OLD.workspace_id <=> NEW.workspace_id) OR
 NOT(OLD.profile_id <=> NEW.profile_id) OR
 NOT(OLD.cluster_uid <=> NEW.cluster_uid) OR
 NOT(OLD.namespace_uid <=> NEW.namespace_uid) OR
 NOT(OLD.namespace_name <=> NEW.namespace_name) OR
 NOT(OLD.installation_id <=> NEW.installation_id) OR
 NOT(OLD.profile <=> NEW.profile) OR
 NOT(OLD.created_by <=> NEW.created_by) OR
 NOT(OLD.created_at <=> NEW.created_at) OR NEW.revision<>OLD.revision+1
 OR OLD.active_run_id IS NOT NULL AND (NOT(OLD.config_sha256 <=> NEW.config_sha256) OR
 NOT(OLD.manifest_sha256 <=> NEW.manifest_sha256) OR
 NOT(OLD.image_digest <=> NEW.image_digest) OR
 NOT(OLD.config_digest <=> NEW.config_digest)) THEN
 SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PRIVATE_CLUSTER_IMMUTABLE_CONNECTION'; END IF;
END;