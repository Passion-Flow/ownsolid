CREATE TRIGGER sso_flow_transition BEFORE UPDATE ON sso_flows FOR EACH ROW
BEGIN
    IF NOT(OLD.state='succeeded' AND NEW.state='succeeded' AND OLD.issued_session_id IS NULL AND NEW.issued_session_id IS NOT NULL
       AND (NEW.id<=>OLD.id) AND (NEW.tenant_id<=>OLD.tenant_id) AND (NEW.connector_id<=>OLD.connector_id) AND (NEW.connector_revision<=>OLD.connector_revision) AND (NEW.auth_version<=>OLD.auth_version)
       AND (NEW.audience<=>OLD.audience) AND (NEW.mode<=>OLD.mode) AND (NEW.state_hash<=>OLD.state_hash) AND (NEW.browser_hash<=>OLD.browser_hash) AND (NEW.previous_cookie_hash<=>OLD.previous_cookie_hash)
       AND (NEW.previous_session_id<=>OLD.previous_session_id) AND (NEW.initiating_user_id<=>OLD.initiating_user_id) AND (NEW.initiating_session_id<=>OLD.initiating_session_id) AND (NEW.initiating_epoch<=>OLD.initiating_epoch) AND (NEW.credential_version<=>OLD.credential_version) AND (NEW.reason<=>OLD.reason)
       AND (NEW.verifier_key_id<=>OLD.verifier_key_id) AND (NEW.verifier_nonce<=>OLD.verifier_nonce) AND (NEW.verifier_ciphertext<=>OLD.verifier_ciphertext) AND (NEW.claim_id<=>OLD.claim_id) AND (NEW.authenticated_user_id<=>OLD.authenticated_user_id) AND (NEW.error_code<=>OLD.error_code)
       AND (NEW.created_at<=>OLD.created_at) AND (NEW.expires_at<=>OLD.expires_at) AND (NEW.finished_at<=>OLD.finished_at)) AND (
       NOT(NEW.id<=>OLD.id) OR NOT(NEW.tenant_id<=>OLD.tenant_id) OR NOT(NEW.connector_id<=>OLD.connector_id) OR NOT(NEW.connector_revision<=>OLD.connector_revision) OR NOT(NEW.auth_version<=>OLD.auth_version)
       OR NOT(NEW.audience<=>OLD.audience) OR NOT(NEW.mode<=>OLD.mode) OR NOT(NEW.state_hash<=>OLD.state_hash) OR NOT(NEW.browser_hash<=>OLD.browser_hash) OR NOT(NEW.previous_cookie_hash<=>OLD.previous_cookie_hash)
       OR NOT(NEW.previous_session_id<=>OLD.previous_session_id) OR NOT(NEW.initiating_user_id<=>OLD.initiating_user_id) OR NOT(NEW.initiating_session_id<=>OLD.initiating_session_id) OR NOT(NEW.initiating_epoch<=>OLD.initiating_epoch) OR NOT(NEW.credential_version<=>OLD.credential_version) OR NOT(NEW.reason<=>OLD.reason)
       OR NOT(NEW.created_at<=>OLD.created_at) OR NOT(NEW.expires_at<=>OLD.expires_at) OR NOT(NEW.issued_session_id<=>OLD.issued_session_id)
       OR NOT((OLD.state='pending' AND NEW.state IN('claimed','failed','expired')) OR (OLD.state='claimed' AND NEW.state IN('succeeded','failed','expired')))
       OR (OLD.claim_id IS NOT NULL AND NOT(NEW.claim_id<=>OLD.claim_id))) THEN
        SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SSO_FLOW_TRANSITION_INVALID';
    END IF;
END;
