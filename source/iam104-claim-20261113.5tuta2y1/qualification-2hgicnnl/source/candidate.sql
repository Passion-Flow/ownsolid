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
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','claimed')),
 claim_id uuid,
 PRIMARY KEY(tenant_id,id),
 FOREIGN KEY(tenant_id,connector_id) REFERENCES sso_connectors(tenant_id,id),
 UNIQUE(tenant_id,request_id),
 UNIQUE(tenant_id,connector_id,audience,relay_hash),
 CHECK(expires_at>created_at),
 CHECK((state='pending' AND claim_id IS NULL) OR (state='claimed' AND claim_id IS NOT NULL))
);
CREATE FUNCTION saml_claim_probe_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.connector_id,NEW.audience,NEW.callback,NEW.request_id,NEW.user_id,NEW.session_id,NEW.epoch,NEW.credential_version,NEW.connector_revision,NEW.auth_version,NEW.browser_hash,NEW.relay_hash,NEW.previous_hash,NEW.created_at,NEW.expires_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.connector_id,OLD.audience,OLD.callback,OLD.request_id,OLD.user_id,OLD.session_id,OLD.epoch,OLD.credential_version,OLD.connector_revision,OLD.auth_version,OLD.browser_hash,OLD.relay_hash,OLD.previous_hash,OLD.created_at,OLD.expires_at)
 OR OLD.state='claimed' THEN RAISE EXCEPTION 'immutable private request authority' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END; $$;
CREATE TRIGGER saml_claim_probe_immutable BEFORE UPDATE ON saml_claim_probe_flows FOR EACH ROW EXECUTE FUNCTION saml_claim_probe_immutable();
