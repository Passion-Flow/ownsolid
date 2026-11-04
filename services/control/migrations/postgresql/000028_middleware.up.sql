-- New account22-derived MVP lineage; immutable legacy assets are unchanged.
-- M26 fixed PostgreSQL maintenance instances, immutable intents and history.
CREATE TABLE middleware_instances (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 profile_key varchar(63) NOT NULL CHECK(profile_key ~ '^[a-z][a-z0-9_]{0,62}$'),
 kind varchar(24) NOT NULL CHECK(kind IN('platform_owned','customer_external')),
 name varchar(100) NOT NULL CHECK(length(name)>0),
 version bigint NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
 state varchar(16) NOT NULL DEFAULT 'active' CHECK(state IN('active','archived')),
 profile_fingerprint char(64) NOT NULL CHECK(profile_fingerprint ~ '^[a-f0-9]{64}$'),
 created_by uuid NOT NULL REFERENCES users(id),
 creation_key uuid NOT NULL,
 creation_fingerprint char(64) NOT NULL CHECK(creation_fingerprint ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,profile_key),
 UNIQUE(tenant_id,workspace_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE TABLE middleware_operations (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 instance_id uuid NOT NULL,
 kind varchar(24) NOT NULL CHECK(kind IN('health','backup','isolated_restore','reconcile')),
 state varchar(16) NOT NULL DEFAULT 'queued' CHECK(state IN('queued','running','succeeded','failed','unknown')),
 requested_by uuid NOT NULL REFERENCES users(id),
 intent_key uuid NOT NULL,
 intent_fingerprint char(64) NOT NULL CHECK(intent_fingerprint ~ '^[a-f0-9]{64}$'),
 input_json text NOT NULL CHECK(octet_length(input_json)<=65536 AND jsonb_typeof(input_json::jsonb)='object'),
 actor_json text NOT NULL CHECK(octet_length(actor_json)<=65536 AND jsonb_typeof(actor_json::jsonb)='object'),
 instance_version bigint NOT NULL CHECK(instance_version BETWEEN 1 AND 9007199254740991),
 profile_fingerprint char(64) NOT NULL CHECK(profile_fingerprint ~ '^[a-f0-9]{64}$'),
 request_id uuid NOT NULL,
 attempt_id uuid,
 lease_expires_at timestamptz,
 result_json text CHECK(result_json IS NULL OR (octet_length(result_json)<=16777216 AND jsonb_typeof(result_json::jsonb)='object')),
 result_sha256 char(64) CHECK(result_sha256 IS NULL OR result_sha256 ~ '^[a-f0-9]{64}$'),
 error_code varchar(96),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 completed_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,requested_by,intent_key),
 FOREIGN KEY(tenant_id,workspace_id,instance_id) REFERENCES middleware_instances(tenant_id,workspace_id,id),
 CHECK((state='running' AND attempt_id IS NOT NULL AND lease_expires_at IS NOT NULL) OR (state<>'running' AND lease_expires_at IS NULL)),
 CHECK((state='succeeded' AND result_json IS NOT NULL AND result_sha256 IS NOT NULL AND error_code IS NULL AND completed_at IS NOT NULL) OR (state<>'succeeded' AND result_json IS NULL AND result_sha256 IS NULL)),
 CHECK((state IN('failed','unknown') AND error_code IS NOT NULL AND completed_at IS NOT NULL) OR (state NOT IN('failed','unknown') AND error_code IS NULL)),
 CHECK(state<>'queued' OR (attempt_id IS NULL AND completed_at IS NULL))
);
CREATE INDEX middleware_operations_claim ON middleware_operations(state,created_at,id);
CREATE INDEX middleware_operations_expiry ON middleware_operations(state,lease_expires_at,id);
CREATE INDEX middleware_operations_history ON middleware_operations(tenant_id,workspace_id,instance_id,id);
CREATE FUNCTION middleware_operation_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MIDDLEWARE_HISTORY_IMMUTABLE'; END IF;
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.instance_id,NEW.kind,NEW.requested_by,NEW.intent_key,NEW.intent_fingerprint,NEW.input_json,NEW.actor_json,NEW.instance_version,NEW.profile_fingerprint,NEW.request_id,NEW.created_at) IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.instance_id,OLD.kind,OLD.requested_by,OLD.intent_key,OLD.intent_fingerprint,OLD.input_json,OLD.actor_json,OLD.instance_version,OLD.profile_fingerprint,OLD.request_id,OLD.created_at) THEN RAISE EXCEPTION 'MIDDLEWARE_INTENT_IMMUTABLE'; END IF;
 IF OLD.state IN('succeeded','failed','unknown') AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'MIDDLEWARE_TERMINAL_IMMUTABLE'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER middleware_operation_update BEFORE UPDATE OR DELETE ON middleware_operations FOR EACH ROW EXECUTE FUNCTION middleware_operation_immutable();
