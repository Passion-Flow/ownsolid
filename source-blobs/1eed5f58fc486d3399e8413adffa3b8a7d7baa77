CREATE TRIGGER model_preparation_attempt_immutable_update BEFORE UPDATE ON model_preparation_attempts FOR EACH ROW
BEGIN
    IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.workspace_id<=>OLD.workspace_id) OR NOT(NEW.preparation_id<=>OLD.preparation_id) OR NOT(NEW.node_id<=>OLD.node_id) OR NOT(NEW.task_id<=>OLD.task_id) OR NOT(NEW.attempt_number<=>OLD.attempt_number) OR NOT(NEW.requested_by<=>OLD.requested_by) OR NOT(NEW.requested_audience<=>OLD.requested_audience) OR NOT(NEW.requested_epoch<=>OLD.requested_epoch) OR NOT(NEW.membership_authority<=>OLD.membership_authority) OR NOT(NEW.command_sha256<=>OLD.command_sha256) OR NOT(NEW.created_at<=>OLD.created_at) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_ATTEMPT_INPUTS_IMMUTABLE';
    END IF;
    IF OLD.stop_token_hash IS NOT NULL AND (NOT(NEW.stop_token_hash<=>OLD.stop_token_hash)) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_STOP_AUTHORITY_IMMUTABLE';
    END IF;
    IF OLD.physical_stopped_at IS NOT NULL AND (NOT(NEW.physical_stopped_at<=>OLD.physical_stopped_at) OR NOT(NEW.physical_stop_receipt<=>OLD.physical_stop_receipt) OR NOT(NEW.physical_stop_sha256<=>OLD.physical_stop_sha256)) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_STOP_PROOF_IMMUTABLE';
    END IF;
    IF OLD.outcome_sha256 IS NOT NULL AND (NOT(NEW.outcome<=>OLD.outcome) OR NOT(NEW.outcome_bytes<=>OLD.outcome_bytes) OR NOT(NEW.outcome_sha256<=>OLD.outcome_sha256)) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_OUTCOME_IMMUTABLE';
    END IF;
END;
