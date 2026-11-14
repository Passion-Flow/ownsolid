CREATE TRIGGER alias_identity_immutable BEFORE UPDATE ON model_aliases FOR EACH ROW
BEGIN
    IF NOT (NEW.id<=>OLD.id AND NEW.tenant_id<=>OLD.tenant_id AND NEW.workspace_id<=>OLD.workspace_id AND NEW.node_id<=>OLD.node_id AND NEW.gateway_id<=>OLD.gateway_id AND NEW.name<=>OLD.name AND NEW.created_by<=>OLD.created_by AND NEW.idempotency_key<=>OLD.idempotency_key AND NEW.fingerprint<=>OLD.fingerprint AND NEW.created_at<=>OLD.created_at) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='MODEL_ALIAS_IDENTITY_IMMUTABLE';
    END IF;
END;
