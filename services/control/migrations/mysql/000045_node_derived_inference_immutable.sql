CREATE TRIGGER node_derived_inference_immutable BEFORE UPDATE ON node_tasks FOR EACH ROW
BEGIN
    IF OLD.operation='inference.derived.v1' OR NEW.operation='inference.derived.v1' THEN
        IF NOT (NEW.id <=> OLD.id)
        OR NOT (NEW.tenant_id <=> OLD.tenant_id)
        OR NOT (NEW.workspace_id <=> OLD.workspace_id)
        OR NOT (NEW.node_id <=> OLD.node_id)
        OR NOT (NEW.operation <=> OLD.operation)
        OR NOT (NEW.payload <=> OLD.payload)
        OR NOT (NEW.requested_by <=> OLD.requested_by)
        OR NOT (NEW.inference_requested_epoch <=> OLD.inference_requested_epoch)
        OR NOT (NEW.idempotency_key <=> OLD.idempotency_key)
        OR NOT (NEW.request_fingerprint <=> OLD.request_fingerprint)
        OR NOT (NEW.created_at <=> OLD.created_at) THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='DERIVED_INFERENCE_COMMAND_IMMUTABLE';
        END IF;
    END IF;
END;
