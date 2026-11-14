-- External rollback candidate; execute only inside one operator-owned transaction.
-- A feature with retained authority cannot be downgraded by deleting its state.
LOCK TABLE sso_connectors,sso_saml_profiles,sso_saml_identities,sso_saml_requests,sso_saml_results,sessions IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM sso_connectors WHERE provider='saml')
 OR EXISTS(SELECT 1 FROM sso_saml_profiles)
 OR EXISTS(SELECT 1 FROM sso_saml_identities)
 OR EXISTS(SELECT 1 FROM sso_saml_requests)
 OR EXISTS(SELECT 1 FROM sso_saml_results)
 THEN RAISE EXCEPTION 'SAML_DOWNGRADE_REQUIRES_EMPTY_AUTHORITY' USING ERRCODE='23514'; END IF;
END; $$;
CREATE OR REPLACE FUNCTION claim_sso_session_issuance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.sso_flow_id IS NULL THEN RETURN NEW; END IF;
 UPDATE sso_flows SET issued_session_id=NEW.id
 WHERE tenant_id=NEW.sso_tenant_id AND connector_id=NEW.sso_connector_id AND id=NEW.sso_flow_id
 AND authenticated_user_id=NEW.user_id AND auth_version=NEW.sso_auth_version AND state='succeeded' AND issued_session_id IS NULL;
 IF NOT FOUND THEN RAISE EXCEPTION 'SSO_FLOW_ALREADY_ISSUED'; END IF;
 RETURN NEW;
END;$$;

ALTER TABLE sessions ADD CONSTRAINT sessions_sso_binding_fk FOREIGN KEY(sso_tenant_id,sso_connector_id,sso_binding_id,user_id) REFERENCES sso_bindings(tenant_id,connector_id,id,user_id),
 ADD CONSTRAINT sessions_sso_flow_fk FOREIGN KEY(sso_tenant_id,sso_connector_id,sso_flow_id,user_id) REFERENCES sso_flows(tenant_id,connector_id,id,authenticated_user_id);
DROP TRIGGER sso_flow_source_parent_retained ON sso_flows;
DROP TABLE sso_saml_results;
DROP TABLE sso_saml_requests;
DROP TABLE sso_saml_identities;
DROP TABLE sso_saml_profiles;
DROP FUNCTION saml_result_transition_guard();
DROP FUNCTION saml_claim_probe_immutable();
DROP FUNCTION saml_persistent_identity_immutable();
DROP FUNCTION saml_identity_context_immutable();
DROP FUNCTION saml_profile_protocol_guard();
DROP FUNCTION saml_probe_retain_source_parent();
ALTER TABLE sso_connectors DROP CONSTRAINT sso_connector_protocol_credentials,
 DROP CONSTRAINT sso_connectors_provider_check,
 ALTER COLUMN client_id SET NOT NULL,
 ALTER COLUMN secret_key_id SET NOT NULL,
 ALTER COLUMN secret_nonce SET NOT NULL,
 ALTER COLUMN secret_ciphertext SET NOT NULL,
 ADD CONSTRAINT sso_connectors_provider_check CHECK(provider='gitea');
