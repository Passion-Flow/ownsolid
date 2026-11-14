CREATE TRIGGER training_upload_inputs_immutable BEFORE UPDATE ON training_uploads FOR EACH ROW
BEGIN
    IF NOT(NEW.id <=> OLD.id) OR
       NOT(NEW.tenant_id <=> OLD.tenant_id) OR
       NOT(NEW.workspace_id <=> OLD.workspace_id) OR
       NOT(NEW.job_id <=> OLD.job_id) OR
       NOT(NEW.attempt_id <=> OLD.attempt_id) OR
       NOT(NEW.kind <=> OLD.kind) OR
       NOT(NEW.global_step <=> OLD.global_step) OR
       NOT(NEW.manifest_sha256 <=> OLD.manifest_sha256) OR
       NOT(NEW.manifest <=> OLD.manifest) OR
       NOT(NEW.manifest_bytes <=> OLD.manifest_bytes) OR
       NOT(NEW.created_at <=> OLD.created_at) OR
       (OLD.state='accepted' AND NOT(NEW.state <=> OLD.state)) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TRAINING_UPLOAD_INPUTS_IMMUTABLE';
    END IF;
END;
