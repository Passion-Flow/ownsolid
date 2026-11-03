CREATE TRIGGER model_preparation_immutable_update BEFORE UPDATE ON model_preparations FOR EACH ROW
BEGIN
    IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.workspace_id<=>OLD.workspace_id) OR NOT(NEW.node_id<=>OLD.node_id) OR NOT(NEW.model_id<=>OLD.model_id) OR NOT(NEW.training_job_id<=>OLD.training_job_id) OR NOT(NEW.training_attempt_id<=>OLD.training_attempt_id) OR NOT(NEW.training_artifact_id<=>OLD.training_artifact_id) OR NOT(NEW.heldout_id<=>OLD.heldout_id) OR NOT(NEW.gpu_uuid<=>OLD.gpu_uuid) OR NOT(NEW.input_snapshot<=>OLD.input_snapshot) OR NOT(NEW.input_sha256<=>OLD.input_sha256) OR NOT(NEW.configuration<=>OLD.configuration) OR NOT(NEW.configuration_sha256<=>OLD.configuration_sha256) OR NOT(NEW.execution_recipe<=>OLD.execution_recipe) OR NOT(NEW.created_by<=>OLD.created_by) OR NOT(NEW.creation_audience<=>OLD.creation_audience) OR NOT(NEW.reason<=>OLD.reason) OR NOT(NEW.creation_key<=>OLD.creation_key) OR NOT(NEW.creation_fingerprint<=>OLD.creation_fingerprint) OR NOT(NEW.created_at<=>OLD.created_at) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_INPUTS_IMMUTABLE';
    END IF;
END;
