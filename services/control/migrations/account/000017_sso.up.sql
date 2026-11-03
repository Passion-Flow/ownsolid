-- M11 cumulative forward migration after the immutable M10 sixteen-entry catalog.
-- Existing source columns are retained; only the local-only restriction is replaced.
-- M11 explicit existing-account binding; provider tokens and authorization codes are never stored.
CREATE TABLE sso_connectors (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 name varchar(100) NOT NULL CHECK(length(btrim(name))>0),
 provider varchar(16) NOT NULL CHECK(provider='gitea'),
 origin varchar(2048) NOT NULL,
 client_id uuid NOT NULL,
 secret_key_id varchar(64) NOT NULL,
 secret_nonce bytea NOT NULL CHECK(octet_length(secret_nonce)=12),
 secret_ciphertext bytea NOT NULL CHECK(octet_length(secret_ciphertext) BETWEEN 17 AND 4112),
 enabled boolean NOT NULL,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 auth_version bigint NOT NULL DEFAULT 1 CHECK(auth_version BETWEEN 1 AND 9007199254740991),
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,id)
);
CREATE INDEX sso_connectors_page_idx ON sso_connectors(tenant_id,created_at DESC,id DESC);
CREATE TABLE sso_bindings (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 connector_id uuid NOT NULL,
 subject varchar(19) COLLATE "C" NOT NULL CHECK(subject~'^[1-9][0-9]{0,18}$' AND subject::numeric<=9223372036854775807),
 user_id uuid NOT NULL REFERENCES users(id),
 active boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,connector_id,subject),
 UNIQUE(tenant_id,connector_id,id,user_id),
 FOREIGN KEY(tenant_id,connector_id) REFERENCES sso_connectors(tenant_id,id)
);
CREATE INDEX sso_bindings_user_idx ON sso_bindings(tenant_id,user_id,created_at,id);
CREATE TABLE sso_flows (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 connector_id uuid NOT NULL,
 connector_revision bigint NOT NULL CHECK(connector_revision BETWEEN 1 AND 9007199254740991),
 auth_version bigint NOT NULL CHECK(auth_version BETWEEN 1 AND 9007199254740991),
 audience varchar(16) NOT NULL CHECK(audience IN ('portal','admin')),
 mode varchar(16) NOT NULL CHECK(mode IN ('login','link')),
 state varchar(16) NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','claimed','succeeded','failed','expired')),
 state_hash bytea NOT NULL UNIQUE CHECK(octet_length(state_hash)=32),
 browser_hash bytea NOT NULL CHECK(octet_length(browser_hash)=32),
 previous_cookie_hash bytea NOT NULL CHECK(octet_length(previous_cookie_hash)=32),
 previous_session_id uuid,
 initiating_user_id uuid REFERENCES users(id),
 initiating_session_id uuid,
 initiating_epoch bigint,
 credential_version bigint,
 reason varchar(500),
 verifier_key_id varchar(64),
 verifier_nonce bytea,
 verifier_ciphertext bytea,
 claim_id uuid,
 authenticated_user_id uuid REFERENCES users(id),
 issued_session_id uuid,
 error_code varchar(80),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 expires_at timestamptz NOT NULL,
 finished_at timestamptz,
 UNIQUE(tenant_id,connector_id,id),
 UNIQUE(tenant_id,connector_id,id,authenticated_user_id),
 FOREIGN KEY(tenant_id,connector_id) REFERENCES sso_connectors(tenant_id,id),
 CHECK(expires_at>created_at),
 CHECK((mode='login' AND initiating_user_id IS NULL AND initiating_session_id IS NULL AND initiating_epoch IS NULL AND credential_version IS NULL AND reason IS NULL)
    OR (mode='link' AND previous_session_id IS NOT NULL AND previous_session_id=initiating_session_id AND initiating_user_id IS NOT NULL AND initiating_session_id IS NOT NULL AND initiating_epoch IS NOT NULL AND credential_version IS NOT NULL AND reason IS NOT NULL AND initiating_epoch BETWEEN 1 AND 9007199254740991 AND credential_version BETWEEN 1 AND 9007199254740991 AND length(btrim(reason)) BETWEEN 1 AND 500)),
 CHECK((state='pending' AND verifier_key_id IS NOT NULL AND verifier_nonce IS NOT NULL AND octet_length(verifier_nonce)=12 AND verifier_ciphertext IS NOT NULL AND octet_length(verifier_ciphertext)=59 AND claim_id IS NULL)
    OR (state<>'pending' AND verifier_key_id IS NULL AND verifier_nonce IS NULL AND verifier_ciphertext IS NULL)),
 CHECK(state NOT IN ('claimed','succeeded') OR claim_id IS NOT NULL),
 CHECK((state='succeeded')=(authenticated_user_id IS NOT NULL)),
 CHECK(state='succeeded' OR issued_session_id IS NULL),
 CHECK((state IN ('succeeded','failed','expired'))=(finished_at IS NOT NULL)),
 CHECK((state IN ('failed','expired') AND error_code IS NOT NULL AND error_code~'^[A-Z][A-Z0-9_]{1,79}$') OR (state NOT IN ('failed','expired') AND error_code IS NULL))
);
CREATE INDEX sso_flows_expiry_idx ON sso_flows(expires_at,id) WHERE state IN ('pending','claimed');
CREATE INDEX sso_flows_connector_idx ON sso_flows(tenant_id,connector_id,state);
ALTER TABLE sessions DROP CONSTRAINT account_local_session_only;
ALTER TABLE sessions ADD CONSTRAINT sessions_sso_source_complete CHECK(
  (sso_tenant_id IS NULL AND sso_connector_id IS NULL AND sso_binding_id IS NULL AND sso_auth_version IS NULL AND sso_flow_id IS NULL)
  OR (sso_tenant_id IS NOT NULL AND sso_connector_id IS NOT NULL AND sso_binding_id IS NOT NULL AND sso_auth_version IS NOT NULL AND sso_auth_version BETWEEN 1 AND 9007199254740991 AND sso_flow_id IS NOT NULL)),
 ADD CONSTRAINT sessions_sso_binding_fk FOREIGN KEY(sso_tenant_id,sso_connector_id,sso_binding_id,user_id) REFERENCES sso_bindings(tenant_id,connector_id,id,user_id),
 ADD CONSTRAINT sessions_sso_flow_fk FOREIGN KEY(sso_tenant_id,sso_connector_id,sso_flow_id,user_id) REFERENCES sso_flows(tenant_id,connector_id,id,authenticated_user_id),
 ADD CONSTRAINT sessions_sso_flow_unique UNIQUE(sso_flow_id);
CREATE INDEX sessions_sso_connector_live_idx ON sessions(sso_tenant_id,sso_connector_id) WHERE revoked_at IS NULL AND sso_connector_id IS NOT NULL;
CREATE INDEX sessions_sso_binding_live_idx ON sessions(sso_binding_id) WHERE revoked_at IS NULL AND sso_binding_id IS NOT NULL;
CREATE TABLE sso_mutation_intents (
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 actor_user_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 operation varchar(24) NOT NULL CHECK(operation IN ('create_connector','update_connector','unlink_binding')),
 target_id uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 result jsonb NOT NULL CHECK(jsonb_typeof(result)='object'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,actor_user_id,idempotency_key)
);
CREATE FUNCTION protect_sso_connector_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.provider,NEW.origin,NEW.created_by,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.provider,OLD.origin,OLD.created_by,OLD.created_at)
 THEN RAISE EXCEPTION 'SSO_CONNECTOR_IDENTITY_IMMUTABLE'; END IF;
 IF NEW.revision<>OLD.revision+1 OR NEW.auth_version NOT IN (OLD.auth_version,OLD.auth_version+1)
 OR ((NEW.enabled,NEW.client_id,NEW.secret_key_id,NEW.secret_nonce,NEW.secret_ciphertext) IS DISTINCT FROM (OLD.enabled,OLD.client_id,OLD.secret_key_id,OLD.secret_nonce,OLD.secret_ciphertext) AND NEW.auth_version<>OLD.auth_version+1)
 THEN RAISE EXCEPTION 'SSO_CONNECTOR_REVISION_INVALID'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER sso_connector_identity BEFORE UPDATE ON sso_connectors FOR EACH ROW EXECUTE FUNCTION protect_sso_connector_identity();
CREATE FUNCTION protect_sso_binding_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.connector_id,NEW.subject,NEW.user_id,NEW.created_at) IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.connector_id,OLD.subject,OLD.user_id,OLD.created_at)
 OR NEW.revision<>OLD.revision+1 THEN RAISE EXCEPTION 'SSO_BINDING_IDENTITY_IMMUTABLE'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER sso_binding_identity BEFORE UPDATE ON sso_bindings FOR EACH ROW EXECUTE FUNCTION protect_sso_binding_identity();
CREATE FUNCTION protect_sso_flow_transition() RETURNS trigger LANGUAGE plpgsql AS $$
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
CREATE TRIGGER sso_flow_transition BEFORE INSERT OR UPDATE ON sso_flows FOR EACH ROW EXECUTE FUNCTION protect_sso_flow_transition();
CREATE FUNCTION claim_sso_session_issuance() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.sso_flow_id IS NULL THEN RETURN NEW; END IF;
 UPDATE sso_flows SET issued_session_id=NEW.id
 WHERE tenant_id=NEW.sso_tenant_id AND connector_id=NEW.sso_connector_id AND id=NEW.sso_flow_id
 AND authenticated_user_id=NEW.user_id AND auth_version=NEW.sso_auth_version AND state='succeeded' AND issued_session_id IS NULL;
 IF NOT FOUND THEN RAISE EXCEPTION 'SSO_FLOW_ALREADY_ISSUED'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER sessions_sso_single_issuance BEFORE INSERT ON sessions FOR EACH ROW EXECUTE FUNCTION claim_sso_session_issuance();
CREATE FUNCTION protect_sso_intent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'SSO_INTENT_IMMUTABLE'; END;$$;
CREATE TRIGGER sso_intent_immutable BEFORE UPDATE OR DELETE ON sso_mutation_intents FOR EACH ROW EXECUTE FUNCTION protect_sso_intent();
CREATE TRIGGER sso_connector_retained BEFORE DELETE ON sso_connectors FOR EACH ROW EXECUTE FUNCTION protect_sso_intent();
CREATE TRIGGER sso_binding_retained BEFORE DELETE ON sso_bindings FOR EACH ROW EXECUTE FUNCTION protect_sso_intent();
