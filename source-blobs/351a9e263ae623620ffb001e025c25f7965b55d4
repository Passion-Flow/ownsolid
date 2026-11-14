CREATE TRIGGER private_cluster_run_identity BEFORE UPDATE ON private_cluster_runs FOR EACH ROW
BEGIN
 IF NOT(OLD.id <=> NEW.id) OR
 NOT(OLD.tenant_id <=> NEW.tenant_id) OR
 NOT(OLD.workspace_id <=> NEW.workspace_id) OR
 NOT(OLD.connection_id <=> NEW.connection_id) OR
 NOT(OLD.connection_revision <=> NEW.connection_revision) OR
 NOT(OLD.created_by <=> NEW.created_by) OR
 NOT(OLD.actor_epoch <=> NEW.actor_epoch) OR
 NOT(OLD.membership_authority <=> NEW.membership_authority) OR
 NOT(OLD.created_session_id <=> NEW.created_session_id) OR
 NOT(OLD.created_token_hash <=> NEW.created_token_hash) OR
 NOT(OLD.config_sha256 <=> NEW.config_sha256) OR
 NOT(OLD.request <=> NEW.request) OR
 NOT(OLD.spec_sha256 <=> NEW.spec_sha256) OR
 NOT(OLD.created_at <=> NEW.created_at) OR NEW.revision<>OLD.revision+1 OR OLD.phase='cleaned' OR NOT (OLD.phase=NEW.phase OR OLD.phase='prepared' AND NEW.phase IN('lease_pending','cleaned') OR OLD.phase='lease_pending' AND NEW.phase IN('prepared','lease_acquired') OR OLD.phase='lease_acquired' AND NEW.phase IN('job_pending','job_absent') OR OLD.phase='job_pending' AND NEW.phase IN('lease_acquired','observing') OR OLD.phase='observing' AND NEW.phase='job_delete_pending' OR OLD.phase='job_delete_pending' AND NEW.phase IN('observing','job_absent') OR OLD.phase='job_absent' AND NEW.phase='lease_delete_pending' OR OLD.phase='lease_delete_pending' AND NEW.phase IN('job_absent','cleaned'))
 OR OLD.result IS NOT NULL AND NOT(OLD.result <=> NEW.result)
 OR OLD.lease_uid IS NOT NULL AND NOT(OLD.lease_uid <=> NEW.lease_uid)
 OR OLD.job_uid IS NOT NULL AND NOT(OLD.job_uid <=> NEW.job_uid)
 OR OLD.pod_uid IS NOT NULL AND NOT(OLD.pod_uid <=> NEW.pod_uid) THEN
 SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='PRIVATE_CLUSTER_IMMUTABLE_RUN'; END IF;
END;
