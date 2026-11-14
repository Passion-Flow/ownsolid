CREATE TRIGGER sso_binding_identity BEFORE UPDATE ON sso_bindings FOR EACH ROW
BEGIN
    IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.connector_id<=>OLD.connector_id) OR NOT(NEW.subject<=>OLD.subject) OR NOT(NEW.user_id<=>OLD.user_id) OR NOT(NEW.created_at<=>OLD.created_at) OR NEW.revision<>OLD.revision+1 THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SSO_BINDING_IDENTITY_IMMUTABLE';
    END IF;
END;
