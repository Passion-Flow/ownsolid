CREATE TRIGGER training_job_inputs_immutable BEFORE UPDATE ON training_jobs FOR EACH ROW
BEGIN
    IF NOT(NEW.id <=> OLD.id) OR
       NOT(NEW.tenant_id <=> OLD.tenant_id) OR
       NOT(NEW.workspace_id <=> OLD.workspace_id) OR
       NOT(NEW.node_id <=> OLD.node_id) OR
       NOT(NEW.model_id <=> OLD.model_id) OR
       NOT(NEW.dataset_id <=> OLD.dataset_id) OR
       NOT(NEW.dataset_version_id <=> OLD.dataset_version_id) OR
       NOT(NEW.gpu_uuid <=> OLD.gpu_uuid) OR
       NOT(NEW.configuration <=> OLD.configuration) OR
       NOT(NEW.configuration_sha256 <=> OLD.configuration_sha256) OR
       NOT(NEW.execution_recipe <=> OLD.execution_recipe) OR
       NOT(NEW.dataset_manifest_sha256 <=> OLD.dataset_manifest_sha256) OR
       NOT(NEW.model_catalog_sha256 <=> OLD.model_catalog_sha256) OR
       NOT(NEW.created_by <=> OLD.created_by) OR
       NOT(NEW.reason <=> OLD.reason) OR
       NOT(NEW.creation_key <=> OLD.creation_key) OR
       NOT(NEW.creation_fingerprint <=> OLD.creation_fingerprint) OR
       NOT(NEW.created_at <=> OLD.created_at) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TRAINING_INPUTS_IMMUTABLE';
    END IF;
END;
