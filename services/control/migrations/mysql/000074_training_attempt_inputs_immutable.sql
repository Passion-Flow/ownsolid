CREATE TRIGGER training_attempt_inputs_immutable BEFORE UPDATE ON training_attempts FOR EACH ROW
BEGIN
    IF NOT(NEW.id <=> OLD.id) OR
       NOT(NEW.tenant_id <=> OLD.tenant_id) OR
       NOT(NEW.workspace_id <=> OLD.workspace_id) OR
       NOT(NEW.job_id <=> OLD.job_id) OR
       NOT(NEW.node_id <=> OLD.node_id) OR
       NOT(NEW.task_id <=> OLD.task_id) OR
       NOT(NEW.attempt_number <=> OLD.attempt_number) OR
       NOT(NEW.requested_by <=> OLD.requested_by) OR
       NOT(NEW.requested_audience <=> OLD.requested_audience) OR
       NOT(NEW.requested_epoch <=> OLD.requested_epoch) OR
       NOT(NEW.membership_authority <=> OLD.membership_authority) OR
       NOT(NEW.input_inventory <=> OLD.input_inventory) OR
       NOT(NEW.resume_checkpoint_id <=> OLD.resume_checkpoint_id) OR
       NOT(NEW.created_at <=> OLD.created_at) OR
       (OLD.outcome IS NOT NULL AND (NOT(NEW.outcome <=> OLD.outcome) OR NOT(NEW.outcome_sha256 <=> OLD.outcome_sha256))) OR
       (OLD.stop_token_hash IS NOT NULL AND (NOT(NEW.stop_token_hash <=> OLD.stop_token_hash))) OR
       (OLD.physical_stop_receipt IS NOT NULL AND (NOT(NEW.physical_stop_receipt <=> OLD.physical_stop_receipt) OR NOT(NEW.physical_stop_sha256 <=> OLD.physical_stop_sha256) OR NOT(NEW.physical_stopped_at <=> OLD.physical_stopped_at))) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TRAINING_ATTEMPT_INPUTS_IMMUTABLE';
    END IF;
END;
