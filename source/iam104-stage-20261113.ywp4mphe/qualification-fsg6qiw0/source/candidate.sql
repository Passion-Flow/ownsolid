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
