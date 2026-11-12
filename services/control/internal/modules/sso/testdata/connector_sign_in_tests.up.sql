-- Candidate IAM-118 fragment; production admission owns numbering and grants.
CREATE TABLE sso_connector_sign_in_tests (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 connector_id uuid NOT NULL,
 connector_revision bigint NOT NULL CHECK(connector_revision BETWEEN 1 AND 9007199254740991),
 auth_version bigint NOT NULL CHECK(auth_version BETWEEN 1 AND 9007199254740991),
 actor_user_id uuid NOT NULL REFERENCES users(id),
 actor_session_id uuid NOT NULL,
 actor_epoch bigint NOT NULL CHECK(actor_epoch BETWEEN 1 AND 9007199254740991),
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 reason varchar(2000) NOT NULL CHECK(length(btrim(reason)) BETWEEN 1 AND 500),
 state varchar(16) NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','claimed','succeeded','failed','expired')),
 state_hash bytea NOT NULL UNIQUE CHECK(octet_length(state_hash)=32),
 browser_hash bytea NOT NULL CHECK(octet_length(browser_hash)=32),
 secret_key_id varchar(64),
 secret_nonce bytea,
 secret_ciphertext bytea,
 claim_id uuid,
 subject varchar(19) COLLATE "C" CHECK(subject~'^[1-9][0-9]{0,18}$' AND subject::numeric<=9223372036854775807),
 login varchar(256),
 display_name varchar(256),
 error_code varchar(80),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 expires_at timestamptz NOT NULL,
 finished_at timestamptz,
 UNIQUE(tenant_id,id),
 UNIQUE(tenant_id,actor_user_id,idempotency_key),
 FOREIGN KEY(tenant_id,connector_id) REFERENCES sso_connectors(tenant_id,id),
 CHECK(expires_at>created_at),
 CHECK((state='pending' AND claim_id IS NULL AND secret_key_id IS NOT NULL AND secret_nonce IS NOT NULL AND secret_ciphertext IS NOT NULL AND octet_length(secret_nonce)=12 AND octet_length(secret_ciphertext)=155)
 OR (state<>'pending' AND secret_key_id IS NULL AND secret_nonce IS NULL AND secret_ciphertext IS NULL)),
 CHECK(state NOT IN ('claimed','succeeded') OR claim_id IS NOT NULL),
 CHECK((state='succeeded')=(subject IS NOT NULL)),
 CHECK(state='succeeded' OR (login IS NULL AND display_name IS NULL)),
 CHECK((state IN ('succeeded','failed','expired'))=(finished_at IS NOT NULL)),
 CHECK((state IN ('failed','expired') AND error_code~'^[A-Z][A-Z0-9_]{1,79}$' AND error_code IS NOT NULL) OR (state NOT IN ('failed','expired') AND error_code IS NULL))
);
CREATE INDEX sso_connector_sign_in_tests_pending_idx ON sso_connector_sign_in_tests(tenant_id,expires_at,id) WHERE state IN ('pending','claimed');
CREATE INDEX sso_connector_sign_in_tests_retention_idx ON sso_connector_sign_in_tests(tenant_id,finished_at,id) WHERE state IN ('succeeded','failed','expired');
CREATE FUNCTION protect_sso_connector_sign_in_test() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.state<>'pending' THEN RAISE EXCEPTION 'SSO_TEST_MUST_BEGIN_PENDING'; END IF;
  RETURN NEW;
 END IF;
 IF (to_jsonb(NEW)-ARRAY['state','secret_key_id','secret_nonce','secret_ciphertext','claim_id','subject','login','display_name','error_code','finished_at'])
 IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['state','secret_key_id','secret_nonce','secret_ciphertext','claim_id','subject','login','display_name','error_code','finished_at'])
 OR NOT ((OLD.state='pending' AND NEW.state IN ('claimed','failed','expired')) OR (OLD.state='claimed' AND NEW.state IN ('succeeded','failed','expired')))
 OR (OLD.claim_id IS NOT NULL AND NEW.claim_id IS DISTINCT FROM OLD.claim_id)
 THEN RAISE EXCEPTION 'SSO_TEST_TRANSITION_INVALID'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER sso_connector_sign_in_test_transition BEFORE INSERT OR UPDATE ON sso_connector_sign_in_tests FOR EACH ROW EXECUTE FUNCTION protect_sso_connector_sign_in_test();
