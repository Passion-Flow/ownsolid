CREATE TRIGGER model_preparation_recovery_immutable_update BEFORE UPDATE ON model_preparation_recoveries FOR EACH ROW
BEGIN
    IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.workspace_id<=>OLD.workspace_id) OR NOT(NEW.preparation_id<=>OLD.preparation_id) OR NOT(NEW.previous_attempt_id<=>OLD.previous_attempt_id) OR NOT(NEW.previous_node_id<=>OLD.previous_node_id) OR NOT(NEW.next_node_id<=>OLD.next_node_id) OR NOT(NEW.next_attempt_number<=>OLD.next_attempt_number) OR NOT(NEW.requested_by<=>OLD.requested_by) OR NOT(NEW.requested_audience<=>OLD.requested_audience) OR NOT(NEW.requested_epoch<=>OLD.requested_epoch) OR NOT(NEW.membership_authority<=>OLD.membership_authority) OR NOT(NEW.reason<=>OLD.reason) OR NOT(NEW.creation_key<=>OLD.creation_key) OR NOT(NEW.creation_fingerprint<=>OLD.creation_fingerprint) OR NOT(NEW.created_at<=>OLD.created_at) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_RECOVERY_IMMUTABLE';
    END IF;
    IF OLD.applied_at IS NOT NULL AND (NOT(NEW.dispatched_attempt_id<=>OLD.dispatched_attempt_id) OR NOT(NEW.applied_at<=>OLD.applied_at)) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_PREPARATION_RECOVERY_APPLIED_IMMUTABLE';
    END IF;
END;
