CREATE TRIGGER sso_connector_identity BEFORE UPDATE ON sso_connectors FOR EACH ROW
BEGIN
    IF NOT(NEW.id<=>OLD.id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.provider<=>OLD.provider) OR NOT(NEW.origin<=>OLD.origin) OR NOT(NEW.created_by<=>OLD.created_by) OR NOT(NEW.created_at<=>OLD.created_at) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SSO_CONNECTOR_IDENTITY_IMMUTABLE';
    END IF;
    IF NEW.revision<>OLD.revision+1 OR NEW.auth_version NOT IN(OLD.auth_version,OLD.auth_version+1)
       OR ((NOT(NEW.enabled<=>OLD.enabled) OR NOT(NEW.client_id<=>OLD.client_id) OR NOT(NEW.secret_key_id<=>OLD.secret_key_id) OR NOT(NEW.secret_nonce<=>OLD.secret_nonce) OR NOT(NEW.secret_ciphertext<=>OLD.secret_ciphertext)) AND NEW.auth_version<>OLD.auth_version+1) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SSO_CONNECTOR_REVISION_INVALID';
    END IF;
END;
