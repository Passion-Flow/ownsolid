CREATE TABLE saml_identity_contexts (
 tenant_id uuid NOT NULL,
 connector_id uuid NOT NULL,
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 issuer text COLLATE "C" NOT NULL CHECK(char_length(issuer) BETWEEN 1 AND 1024 AND octet_length(issuer)<=4096),
 sp_entity text COLLATE "C" NOT NULL CHECK(char_length(sp_entity) BETWEEN 1 AND 1024 AND octet_length(sp_entity)<=4096),
 name_id_format text COLLATE "C" NOT NULL CHECK(name_id_format='urn:oasis:names:tc:SAML:2.0:nameid-format:persistent'),
 metadata_digest bytea NOT NULL CHECK(octet_length(metadata_digest)=32),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 enabled boolean NOT NULL DEFAULT true,
 PRIMARY KEY(tenant_id,connector_id),UNIQUE(tenant_id,id)
);
CREATE TABLE saml_persistent_identities (
 tenant_id uuid NOT NULL,
 context_id uuid NOT NULL,
 id uuid NOT NULL DEFAULT gen_random_uuid(),
 name_id_value text COLLATE "C" NOT NULL CHECK(char_length(name_id_value) BETWEEN 1 AND 256 AND octet_length(name_id_value)<=1024),
 PRIMARY KEY(tenant_id,id),UNIQUE(tenant_id,context_id,name_id_value),
 FOREIGN KEY(tenant_id,context_id) REFERENCES saml_identity_contexts(tenant_id,id)
);
CREATE FUNCTION saml_identity_context_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.tenant_id,OLD.connector_id,OLD.id,OLD.issuer,OLD.sp_entity,OLD.name_id_format)
    IS DISTINCT FROM ROW(NEW.tenant_id,NEW.connector_id,NEW.id,NEW.issuer,NEW.sp_entity,NEW.name_id_format) THEN
  RAISE EXCEPTION 'SAML_IDENTITY_CONTEXT_IMMUTABLE' USING ERRCODE='23514';
 END IF;
 IF NEW.revision<OLD.revision OR
    (ROW(OLD.enabled,OLD.metadata_digest) IS DISTINCT FROM ROW(NEW.enabled,NEW.metadata_digest) AND NEW.revision<=OLD.revision) THEN
  RAISE EXCEPTION 'SAML_IDENTITY_CONTEXT_REVISION_REQUIRED' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER saml_identity_context_immutable BEFORE UPDATE ON saml_identity_contexts
FOR EACH ROW EXECUTE FUNCTION saml_identity_context_immutable();
CREATE FUNCTION saml_persistent_identity_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD IS DISTINCT FROM NEW THEN
  RAISE EXCEPTION 'SAML_PERSISTENT_IDENTITY_IMMUTABLE' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER saml_persistent_identity_immutable BEFORE UPDATE ON saml_persistent_identities
FOR EACH ROW EXECUTE FUNCTION saml_persistent_identity_immutable();

ALTER TABLE saml_identity_contexts ADD FOREIGN KEY(tenant_id,connector_id) REFERENCES sso_connectors(tenant_id,id);
CREATE TABLE saml_claim_probe_flows (
 id uuid NOT NULL,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 connector_id uuid NOT NULL,
 audience text NOT NULL CHECK(audience IN ('admin','portal')),
 callback text COLLATE "C" NOT NULL CHECK(octet_length(callback) BETWEEN 1 AND 2048),
 request_id text COLLATE "C" NOT NULL CHECK(request_id ~ '^_[0-9a-f-]{36}$'),
 user_id uuid NOT NULL REFERENCES users(id),
 session_id uuid NOT NULL REFERENCES sessions(id),
 epoch bigint NOT NULL CHECK(epoch>=1),
 credential_version bigint NOT NULL CHECK(credential_version>=1),
 connector_revision bigint NOT NULL CHECK(connector_revision>=1),
 auth_version bigint NOT NULL CHECK(auth_version>=1),
 browser_hash bytea NOT NULL CHECK(octet_length(browser_hash)=32),
 relay_hash bytea NOT NULL CHECK(octet_length(relay_hash)=32),
 previous_hash bytea NOT NULL CHECK(octet_length(previous_hash)=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 context_id uuid NOT NULL,
 context_revision bigint NOT NULL CHECK(context_revision>=1),
 metadata_digest bytea NOT NULL CHECK(octet_length(metadata_digest)=32),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','claimed','verified')),
 claim_id uuid,
 PRIMARY KEY(tenant_id,id),
 FOREIGN KEY(tenant_id,context_id) REFERENCES saml_identity_contexts(tenant_id,id),
 FOREIGN KEY(tenant_id,connector_id) REFERENCES sso_connectors(tenant_id,id),
 UNIQUE(tenant_id,request_id),
 UNIQUE(tenant_id,connector_id,audience,relay_hash),
 CHECK(expires_at>created_at),
 CHECK((state='pending' AND claim_id IS NULL) OR (state IN ('claimed','verified') AND claim_id IS NOT NULL))
);
CREATE FUNCTION saml_claim_probe_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.connector_id,NEW.audience,NEW.callback,NEW.request_id,NEW.user_id,NEW.session_id,NEW.epoch,NEW.credential_version,NEW.connector_revision,NEW.auth_version,NEW.browser_hash,NEW.relay_hash,NEW.previous_hash,NEW.created_at,NEW.expires_at,NEW.context_id,NEW.context_revision,NEW.metadata_digest)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.connector_id,OLD.audience,OLD.callback,OLD.request_id,OLD.user_id,OLD.session_id,OLD.epoch,OLD.credential_version,OLD.connector_revision,OLD.auth_version,OLD.browser_hash,OLD.relay_hash,OLD.previous_hash,OLD.created_at,OLD.expires_at,OLD.context_id,OLD.context_revision,OLD.metadata_digest)
 OR OLD.state='verified' OR (OLD.state='claimed' AND NEW.state<>'verified') OR (OLD.claim_id IS NOT NULL AND NEW.claim_id IS DISTINCT FROM OLD.claim_id) THEN RAISE EXCEPTION 'immutable private request authority' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER saml_claim_probe_immutable BEFORE UPDATE ON saml_claim_probe_flows FOR EACH ROW EXECUTE FUNCTION saml_claim_probe_immutable();

CREATE TABLE saml_probe_verified_results (
 tenant_id uuid NOT NULL,
 flow_id uuid NOT NULL,
 context_id uuid NOT NULL,
 identity_id uuid NOT NULL,
 assertion_id text COLLATE "C" NOT NULL CHECK(char_length(assertion_id) BETWEEN 1 AND 128 AND assertion_id ~ '^[A-Za-z_][A-Za-z0-9_.-]*$'),
 request_id text COLLATE "C" NOT NULL,
 continuation_hash bytea NOT NULL CHECK(octet_length(continuation_hash)=32),
 authn_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,flow_id),
 UNIQUE(tenant_id,context_id,assertion_id),
 UNIQUE(tenant_id,continuation_hash),
 FOREIGN KEY(tenant_id,flow_id) REFERENCES saml_claim_probe_flows(tenant_id,id),
 FOREIGN KEY(tenant_id,identity_id) REFERENCES saml_persistent_identities(tenant_id,id),
 FOREIGN KEY(tenant_id,context_id) REFERENCES saml_identity_contexts(tenant_id,id)
);

-- External admission candidate: preserve the legacy protocol's nullable rules
-- while permitting actual SAML connectors without OAuth credential placeholders.
ALTER TABLE sso_connectors DROP CONSTRAINT sso_connectors_provider_check;
ALTER TABLE sso_connectors ADD CONSTRAINT sso_connectors_provider_check CHECK(provider IN ('gitea','saml'));
ALTER TABLE sso_connectors ALTER COLUMN client_id DROP NOT NULL,
 ALTER COLUMN secret_key_id DROP NOT NULL,
 ALTER COLUMN secret_nonce DROP NOT NULL,
 ALTER COLUMN secret_ciphertext DROP NOT NULL;
ALTER TABLE sso_connectors ADD CONSTRAINT sso_connector_protocol_credentials CHECK(
 (provider='gitea' AND client_id IS NOT NULL AND secret_key_id IS NOT NULL AND secret_nonce IS NOT NULL AND secret_ciphertext IS NOT NULL)
 OR (provider='saml' AND client_id IS NULL AND secret_key_id IS NULL AND secret_nonce IS NULL AND secret_ciphertext IS NULL));
ALTER TABLE saml_identity_contexts ADD trust_until timestamptz NOT NULL,
 ADD maximum_session_seconds integer NOT NULL CHECK(maximum_session_seconds BETWEEN 1 AND 86400);
CREATE OR REPLACE FUNCTION saml_identity_context_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.tenant_id,OLD.connector_id,OLD.id,OLD.issuer,OLD.sp_entity,OLD.name_id_format)
 IS DISTINCT FROM ROW(NEW.tenant_id,NEW.connector_id,NEW.id,NEW.issuer,NEW.sp_entity,NEW.name_id_format)
 OR NEW.revision<OLD.revision OR
 (ROW(OLD.enabled,OLD.metadata_digest,OLD.trust_until,OLD.maximum_session_seconds) IS DISTINCT FROM ROW(NEW.enabled,NEW.metadata_digest,NEW.trust_until,NEW.maximum_session_seconds) AND NEW.revision<=OLD.revision)
 THEN RAISE EXCEPTION 'SAML_IDENTITY_CONTEXT_REVISION_REQUIRED' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
ALTER TABLE saml_persistent_identities ADD bound_user_id uuid REFERENCES users(id),
 ADD binding_version bigint NOT NULL DEFAULT 1 CHECK(binding_version BETWEEN 1 AND 9007199254740991),
 ADD active boolean NOT NULL DEFAULT false,
 ADD CHECK(NOT active OR bound_user_id IS NOT NULL),
 ADD UNIQUE(tenant_id,context_id,id);
CREATE OR REPLACE FUNCTION saml_persistent_identity_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.tenant_id,OLD.context_id,OLD.id,OLD.name_id_value) IS DISTINCT FROM ROW(NEW.tenant_id,NEW.context_id,NEW.id,NEW.name_id_value)
 OR (OLD.bound_user_id IS NOT NULL AND NEW.bound_user_id IS DISTINCT FROM OLD.bound_user_id)
 OR NEW.binding_version<>OLD.binding_version+1
 THEN RAISE EXCEPTION 'SAML_BINDING_TRANSITION_REFUSED' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
ALTER TABLE saml_claim_probe_flows DROP CONSTRAINT saml_claim_probe_flows_state_check;
ALTER TABLE saml_claim_probe_flows ADD CONSTRAINT saml_claim_probe_flows_state_check CHECK(state IN ('pending','claimed','verified','succeeded')),
 ADD authenticated_user_id uuid REFERENCES users(id),
 ADD issued_session_id uuid,
 ADD finished_at timestamptz,
 ADD CHECK((state='succeeded')=(authenticated_user_id IS NOT NULL)),
 ADD CHECK((state='succeeded')=(finished_at IS NOT NULL)),
 ADD CHECK(state='succeeded' OR issued_session_id IS NULL);
-- Replace only the previous fixture claim-id check, whose anonymous name is
-- located by its definition instead of depending on a generated ordinal.
DO $$ DECLARE item record; BEGIN
 FOR item IN SELECT conname FROM pg_constraint WHERE conrelid='saml_claim_probe_flows'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%claim_id IS NULL%'
 LOOP EXECUTE format('ALTER TABLE saml_claim_probe_flows DROP CONSTRAINT %I',item.conname); END LOOP;
END; $$;
ALTER TABLE saml_claim_probe_flows ADD CHECK((state='pending' AND claim_id IS NULL) OR (state IN ('claimed','verified','succeeded') AND claim_id IS NOT NULL));
CREATE OR REPLACE FUNCTION saml_claim_probe_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.state='succeeded' AND NEW.state='succeeded' AND OLD.issued_session_id IS NULL AND NEW.issued_session_id IS NOT NULL
 AND (to_jsonb(NEW)-'issued_session_id')=(to_jsonb(OLD)-'issued_session_id') THEN RETURN NEW; END IF;
 IF ROW(NEW.id,NEW.tenant_id,NEW.connector_id,NEW.audience,NEW.callback,NEW.request_id,NEW.user_id,NEW.session_id,NEW.epoch,NEW.credential_version,NEW.connector_revision,NEW.auth_version,NEW.browser_hash,NEW.relay_hash,NEW.previous_hash,NEW.created_at,NEW.expires_at,NEW.context_id,NEW.context_revision,NEW.metadata_digest)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.connector_id,OLD.audience,OLD.callback,OLD.request_id,OLD.user_id,OLD.session_id,OLD.epoch,OLD.credential_version,OLD.connector_revision,OLD.auth_version,OLD.browser_hash,OLD.relay_hash,OLD.previous_hash,OLD.created_at,OLD.expires_at,OLD.context_id,OLD.context_revision,OLD.metadata_digest)
 OR NOT ((OLD.state='pending' AND NEW.state='claimed') OR (OLD.state='claimed' AND NEW.state='verified') OR (OLD.state='verified' AND NEW.state='succeeded'))
 OR (OLD.claim_id IS NOT NULL AND NEW.claim_id IS DISTINCT FROM OLD.claim_id)
 OR NEW.issued_session_id IS DISTINCT FROM OLD.issued_session_id
 THEN RAISE EXCEPTION 'SAML_REQUEST_TRANSITION_REFUSED' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
ALTER TABLE saml_probe_verified_results ADD source_until timestamptz NOT NULL,
 ADD completed_binding_version bigint CHECK(completed_binding_version BETWEEN 1 AND 9007199254740991),
 ADD FOREIGN KEY(tenant_id,context_id,identity_id) REFERENCES saml_persistent_identities(tenant_id,context_id,id);
ALTER TABLE sessions DROP CONSTRAINT sessions_sso_binding_fk,
 DROP CONSTRAINT sessions_sso_flow_fk;
CREATE OR REPLACE FUNCTION claim_sso_session_issuance() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE legacy boolean; saml boolean;
BEGIN
 IF NEW.sso_flow_id IS NULL THEN RETURN NEW; END IF;
 SELECT EXISTS(SELECT 1 FROM sso_flows WHERE tenant_id=NEW.sso_tenant_id AND connector_id=NEW.sso_connector_id AND id=NEW.sso_flow_id) INTO legacy;
 SELECT EXISTS(SELECT 1 FROM saml_claim_probe_flows WHERE tenant_id=NEW.sso_tenant_id AND connector_id=NEW.sso_connector_id AND id=NEW.sso_flow_id) INTO saml;
 IF legacy=saml THEN RAISE EXCEPTION 'SSO_SOURCE_AMBIGUOUS_OR_UNKNOWN' USING ERRCODE='23503'; END IF;
 IF legacy THEN
  PERFORM 1 FROM sso_bindings WHERE tenant_id=NEW.sso_tenant_id AND connector_id=NEW.sso_connector_id AND id=NEW.sso_binding_id AND user_id=NEW.user_id AND active FOR KEY SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION 'SSO_BINDING_REQUIRED' USING ERRCODE='23503'; END IF;
  UPDATE sso_flows SET issued_session_id=NEW.id WHERE tenant_id=NEW.sso_tenant_id AND connector_id=NEW.sso_connector_id AND id=NEW.sso_flow_id AND authenticated_user_id=NEW.user_id AND auth_version=NEW.sso_auth_version AND state='succeeded' AND issued_session_id IS NULL;
 ELSE
  PERFORM 1 FROM saml_persistent_identities b JOIN saml_identity_contexts p ON p.tenant_id=b.tenant_id AND p.id=b.context_id
   JOIN sso_connectors c ON c.tenant_id=p.tenant_id AND c.id=p.connector_id
   JOIN saml_probe_verified_results r ON r.tenant_id=b.tenant_id AND r.identity_id=b.id AND r.context_id=b.context_id
   JOIN saml_claim_probe_flows f ON f.tenant_id=r.tenant_id AND f.id=r.flow_id AND f.connector_id=p.connector_id
   WHERE b.tenant_id=NEW.sso_tenant_id AND p.connector_id=NEW.sso_connector_id AND b.id=NEW.sso_binding_id AND f.id=NEW.sso_flow_id
   AND b.active AND b.bound_user_id=NEW.user_id AND r.completed_binding_version=b.binding_version
   AND f.authenticated_user_id=NEW.user_id AND f.state='succeeded' AND f.issued_session_id IS NULL
   AND c.provider='saml' AND c.enabled AND c.auth_version=NEW.sso_auth_version AND c.auth_version=f.auth_version
   AND p.enabled AND p.revision=f.context_revision AND p.metadata_digest=f.metadata_digest AND p.trust_until>clock_timestamp() AND r.source_until>clock_timestamp()
   FOR SHARE OF b,p,c,f,r;
  IF NOT FOUND THEN RAISE EXCEPTION 'SAML_SOURCE_REQUIRED' USING ERRCODE='23503'; END IF;
  UPDATE saml_claim_probe_flows SET issued_session_id=NEW.id WHERE tenant_id=NEW.sso_tenant_id AND connector_id=NEW.sso_connector_id AND id=NEW.sso_flow_id AND state='succeeded' AND issued_session_id IS NULL;
 END IF;
 IF NOT FOUND THEN RAISE EXCEPTION 'SSO_FLOW_ALREADY_ISSUED' USING ERRCODE='23503'; END IF;
 RETURN NEW;
END; $$;
CREATE FUNCTION saml_probe_retain_source_parent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='saml_persistent_identities' THEN
  RAISE EXCEPTION 'SSO_SOURCE_PARENT_RETAINED' USING ERRCODE='23503';
 ELSIF TG_TABLE_NAME='sso_flows' THEN
  IF EXISTS(SELECT 1 FROM sessions WHERE sso_flow_id=OLD.id) THEN RAISE EXCEPTION 'SSO_SOURCE_PARENT_RETAINED' USING ERRCODE='23503'; END IF;
 ELSIF TG_TABLE_NAME='saml_claim_probe_flows' THEN
  IF EXISTS(SELECT 1 FROM sessions WHERE sso_tenant_id=OLD.tenant_id AND sso_flow_id=OLD.id) THEN RAISE EXCEPTION 'SSO_SOURCE_PARENT_RETAINED' USING ERRCODE='23503'; END IF;
 ELSIF TG_TABLE_NAME='saml_probe_verified_results' THEN
  IF EXISTS(SELECT 1 FROM sessions WHERE sso_tenant_id=OLD.tenant_id AND sso_flow_id=OLD.flow_id) THEN RAISE EXCEPTION 'SSO_SOURCE_PARENT_RETAINED' USING ERRCODE='23503'; END IF;
 ELSE RAISE EXCEPTION 'SSO_SOURCE_PARENT_TABLE_REFUSED' USING ERRCODE='23503';
 END IF;
 RETURN OLD;
END; $$;
CREATE TRIGGER sso_flow_source_parent_retained BEFORE DELETE ON sso_flows FOR EACH ROW EXECUTE FUNCTION saml_probe_retain_source_parent();
CREATE TRIGGER saml_request_source_parent_retained BEFORE DELETE ON saml_claim_probe_flows FOR EACH ROW EXECUTE FUNCTION saml_probe_retain_source_parent();
CREATE TRIGGER saml_result_source_parent_retained BEFORE DELETE ON saml_probe_verified_results FOR EACH ROW EXECUTE FUNCTION saml_probe_retain_source_parent();
CREATE TRIGGER saml_identity_source_parent_retained BEFORE DELETE ON saml_persistent_identities FOR EACH ROW EXECUTE FUNCTION saml_probe_retain_source_parent();
CREATE FUNCTION saml_profile_protocol_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM sso_connectors WHERE tenant_id=NEW.tenant_id AND id=NEW.connector_id AND provider='saml' FOR KEY SHARE;
 IF NOT FOUND THEN RAISE EXCEPTION 'SAML_CONNECTOR_PROTOCOL_REQUIRED' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER saml_profile_protocol_guard BEFORE INSERT OR UPDATE ON saml_identity_contexts FOR EACH ROW EXECUTE FUNCTION saml_profile_protocol_guard();
