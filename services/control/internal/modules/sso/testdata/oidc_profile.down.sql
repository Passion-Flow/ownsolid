-- A retained OIDC identity cannot be reinterpreted or deleted during rollback.
-- The caller must execute the entire file in one transaction.
LOCK TABLE sso_connectors,sso_bindings,sso_flows IN ACCESS EXCLUSIVE MODE;
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM sso_connectors WHERE provider='oidc') THEN
  RAISE EXCEPTION 'SSO_OIDC_RESTORE_REQUIRED: Restore a consistent pre-upgrade backup before downgrading an installation with retained OIDC connectors.';
 END IF;
END;$$;
CREATE OR REPLACE FUNCTION protect_sso_connector_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.provider,NEW.origin,NEW.created_by,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.provider,OLD.origin,OLD.created_by,OLD.created_at)
 THEN RAISE EXCEPTION 'SSO_CONNECTOR_IDENTITY_IMMUTABLE'; END IF;
 IF NEW.revision<>OLD.revision+1 OR NEW.auth_version NOT IN (OLD.auth_version,OLD.auth_version+1)
 OR ((NEW.enabled,NEW.client_id,NEW.secret_key_id,NEW.secret_nonce,NEW.secret_ciphertext) IS DISTINCT FROM (OLD.enabled,OLD.client_id,OLD.secret_key_id,OLD.secret_nonce,OLD.secret_ciphertext) AND NEW.auth_version<>OLD.auth_version+1)
 THEN RAISE EXCEPTION 'SSO_CONNECTOR_REVISION_INVALID'; END IF;
 RETURN NEW;
END;$$;
CREATE OR REPLACE FUNCTION protect_sso_binding_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.connector_id,NEW.subject,NEW.user_id,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.connector_id,OLD.subject,OLD.user_id,OLD.created_at)
 OR NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'SSO_BINDING_IDENTITY_IMMUTABLE'; END IF;
 RETURN NEW;
END;$$;
CREATE OR REPLACE FUNCTION protect_sso_flow_transition() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' THEN RAISE EXCEPTION 'SSO_FLOW_MUST_BEGIN_PENDING'; END IF;
  RETURN NEW;
 END IF;
 IF OLD.state='succeeded' AND NEW.state='succeeded' AND OLD.issued_session_id IS NULL AND NEW.issued_session_id IS NOT NULL
 AND (to_jsonb(NEW)-'issued_session_id')=(to_jsonb(OLD)-'issued_session_id') THEN RETURN NEW; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.connector_id,NEW.connector_revision,NEW.auth_version,NEW.audience,NEW.mode,NEW.state_hash,NEW.browser_hash,NEW.previous_cookie_hash,NEW.previous_session_id,NEW.initiating_user_id,NEW.initiating_session_id,NEW.initiating_epoch,NEW.credential_version,NEW.reason,NEW.created_at,NEW.expires_at)
 IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.connector_id,OLD.connector_revision,OLD.auth_version,OLD.audience,OLD.mode,OLD.state_hash,OLD.browser_hash,OLD.previous_cookie_hash,OLD.previous_session_id,OLD.initiating_user_id,OLD.initiating_session_id,OLD.initiating_epoch,OLD.credential_version,OLD.reason,OLD.created_at,OLD.expires_at)
 OR NEW.issued_session_id IS DISTINCT FROM OLD.issued_session_id
 OR NOT ((OLD.state='pending' AND NEW.state IN ('claimed','failed','expired')) OR (OLD.state='claimed' AND NEW.state IN ('succeeded','failed','expired')))
 OR (OLD.claim_id IS NOT NULL AND NEW.claim_id IS DISTINCT FROM OLD.claim_id)
 THEN RAISE EXCEPTION 'SSO_FLOW_TRANSITION_INVALID'; END IF;
 RETURN NEW;
END;$$;
DROP INDEX sso_flows_oidc_nonce_uidx;
ALTER TABLE sso_flows DROP CONSTRAINT sso_flows_nonce_check;
ALTER TABLE sso_flows DROP CONSTRAINT sso_flows_profile_fk;
ALTER TABLE sso_flows DROP COLUMN oidc_nonce_hash;
ALTER TABLE sso_flows DROP COLUMN provider;
ALTER TABLE sso_bindings DROP CONSTRAINT sso_bindings_profile_fk;
ALTER TABLE sso_bindings DROP CONSTRAINT sso_bindings_subject_check;
ALTER TABLE sso_bindings DROP COLUMN provider;
ALTER TABLE sso_bindings ALTER COLUMN subject TYPE varchar(19) COLLATE "C";
ALTER TABLE sso_bindings ADD CONSTRAINT sso_bindings_subject_check CHECK(subject~'^[1-9][0-9]{0,18}$' AND subject::numeric<=9223372036854775807);
ALTER TABLE sso_connectors DROP CONSTRAINT sso_connectors_profile_key;
ALTER TABLE sso_connectors DROP CONSTRAINT sso_connectors_profile_check;
ALTER TABLE sso_connectors DROP CONSTRAINT sso_connectors_provider_check;
ALTER TABLE sso_connectors DROP COLUMN issuer;
ALTER TABLE sso_connectors ALTER COLUMN client_id TYPE uuid USING client_id::uuid;
ALTER TABLE sso_connectors ADD CONSTRAINT sso_connectors_provider_check CHECK(provider='gitea');
