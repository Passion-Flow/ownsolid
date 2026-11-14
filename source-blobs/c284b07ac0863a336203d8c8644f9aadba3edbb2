CREATE TRIGGER training_recovery_inputs_immutable BEFORE UPDATE ON training_recoveries FOR EACH ROW
BEGIN
    IF NOT(NEW.id <=> OLD.id) OR
       NOT(NEW.tenant_id <=> OLD.tenant_id) OR
       NOT(NEW.workspace_id <=> OLD.workspace_id) OR
       NOT(NEW.job_id <=> OLD.job_id) OR
       NOT(NEW.previous_attempt_id <=> OLD.previous_attempt_id) OR
       NOT(NEW.checkpoint_id <=> OLD.checkpoint_id) OR
       NOT(NEW.previous_node_id <=> OLD.previous_node_id) OR
       NOT(NEW.next_node_id <=> OLD.next_node_id) OR
       NOT(NEW.mode <=> OLD.mode) OR
       NOT(NEW.requested_by <=> OLD.requested_by) OR
       NOT(NEW.reason <=> OLD.reason) OR
       NOT(NEW.creation_key <=> OLD.creation_key) OR
       NOT(NEW.creation_fingerprint <=> OLD.creation_fingerprint) OR
       NOT(NEW.created_at <=> OLD.created_at) OR
       (OLD.next_attempt_id IS NOT NULL AND NOT(NEW.next_attempt_id <=> OLD.next_attempt_id)) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='TRAINING_RECOVERY_INPUTS_IMMUTABLE';
    END IF;
END;
