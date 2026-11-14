CREATE TRIGGER model_preparation_upload_immutable_update BEFORE UPDATE ON model_preparation_uploads FOR EACH ROW
BEGIN
    IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.workspace_id<=>OLD.workspace_id) OR NOT(NEW.preparation_id<=>OLD.preparation_id) OR NOT(NEW.attempt_id<=>OLD.attempt_id) OR NOT(NEW.manifest_sha256<=>OLD.manifest_sha256) OR NOT(NEW.manifest<=>OLD.manifest) OR NOT(NEW.manifest_bytes<=>OLD.manifest_bytes) OR NOT(NEW.descriptor<=>OLD.descriptor) OR NOT(NEW.created_at<=>OLD.created_at) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_UPLOAD_IMMUTABLE';
    END IF;
    IF OLD.accepted_at IS NOT NULL AND (NOT(NEW.state<=>OLD.state) OR NOT(NEW.accepted_at<=>OLD.accepted_at)) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_ACCEPTED_UPLOAD_IMMUTABLE';
    END IF;
END;
