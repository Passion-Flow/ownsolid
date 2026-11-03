CREATE TRIGGER sessions_sso_issuance BEFORE INSERT ON sessions FOR EACH ROW
BEGIN
    IF NEW.sso_flow_id IS NOT NULL THEN
        UPDATE sso_flows SET issued_session_id=NEW.id
        WHERE tenant_id=NEW.sso_tenant_id AND connector_id=NEW.sso_connector_id AND id=NEW.sso_flow_id
          AND authenticated_user_id=NEW.user_id AND auth_version=NEW.sso_auth_version AND state='succeeded' AND issued_session_id IS NULL;
        IF ROW_COUNT()<>1 THEN
            SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SSO_FLOW_ALREADY_ISSUED';
        END IF;
    END IF;
END;
