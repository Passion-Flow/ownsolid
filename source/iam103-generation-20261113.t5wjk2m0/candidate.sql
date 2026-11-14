CREATE TABLE iam_directory_runs (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 connector_id uuid NOT NULL,
 connector_revision bigint NOT NULL CHECK(connector_revision>0),
 auth_version bigint NOT NULL CHECK(auth_version>0),
 actor_user_id uuid NOT NULL REFERENCES users(id),
 actor_session_id uuid NOT NULL REFERENCES sessions(id),
 actor_epoch bigint NOT NULL CHECK(actor_epoch>0),
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 state text NOT NULL CHECK(state IN ('running','succeeded','failed')),
 error_code text CHECK(error_code IN ('DIRECTORY_SCAN_FAILED','DIRECTORY_RUN_EXPIRED')),
 completion_digest bytea CHECK(octet_length(completion_digest)=32),
 created_at timestamptz NOT NULL,
 run_until timestamptz NOT NULL CHECK(run_until>created_at),
 valid_until timestamptz NOT NULL CHECK(valid_until>run_until),
 finished_at timestamptz,
 UNIQUE(tenant_id,connector_id,id),
 UNIQUE(tenant_id,actor_user_id,idempotency_key),
 FOREIGN KEY(tenant_id,connector_id) REFERENCES sso_connectors(tenant_id,id),
 CHECK((state='running')=(finished_at IS NULL)),
 CHECK((state='running')=(completion_digest IS NULL)),
 CHECK((state='failed')=(error_code IS NOT NULL))
);
CREATE UNIQUE INDEX iam_directory_one_run ON iam_directory_runs(tenant_id,connector_id) WHERE state='running';
CREATE TABLE iam_directory_groups (
 tenant_id uuid NOT NULL,
 connector_id uuid NOT NULL,
 run_id uuid NOT NULL,
 external_group_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,connector_id,run_id,external_group_id),
 FOREIGN KEY(tenant_id,connector_id,run_id) REFERENCES iam_directory_runs(tenant_id,connector_id,id)
);
CREATE TABLE iam_directory_members (
 tenant_id uuid NOT NULL,
 connector_id uuid NOT NULL,
 run_id uuid NOT NULL,
 external_group_id uuid NOT NULL,
 external_user_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,connector_id,run_id,external_group_id,external_user_id),
 FOREIGN KEY(tenant_id,connector_id,run_id,external_group_id) REFERENCES iam_directory_groups(tenant_id,connector_id,run_id,external_group_id)
);
CREATE TABLE iam_directory_heads (
 tenant_id uuid NOT NULL,
 connector_id uuid NOT NULL,
 run_id uuid NOT NULL,
 PRIMARY KEY(tenant_id,connector_id),
 FOREIGN KEY(tenant_id,connector_id,run_id) REFERENCES iam_directory_runs(tenant_id,connector_id,id)
);
CREATE FUNCTION iam_directory_run_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.connector_id,NEW.connector_revision,NEW.auth_version,NEW.actor_user_id,NEW.actor_session_id,NEW.actor_epoch,NEW.idempotency_key,NEW.fingerprint,NEW.created_at,NEW.run_until,NEW.valid_until)
 IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.connector_id,OLD.connector_revision,OLD.auth_version,OLD.actor_user_id,OLD.actor_session_id,OLD.actor_epoch,OLD.idempotency_key,OLD.fingerprint,OLD.created_at,OLD.run_until,OLD.valid_until)
 OR OLD.state<>'running' OR NEW.state='running' THEN RAISE EXCEPTION 'DIRECTORY_RUN_TRANSITION_REFUSED'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER iam_directory_run_guard BEFORE UPDATE ON iam_directory_runs FOR EACH ROW EXECUTE FUNCTION iam_directory_run_guard();
CREATE FUNCTION iam_directory_rows_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_state text;
BEGIN
 IF TG_OP<>'INSERT' THEN RAISE EXCEPTION 'DIRECTORY_GENERATION_IMMUTABLE'; END IF;
 SELECT state INTO current_state FROM iam_directory_runs WHERE tenant_id=NEW.tenant_id AND connector_id=NEW.connector_id AND id=NEW.run_id;
 IF current_state IS DISTINCT FROM 'running' THEN RAISE EXCEPTION 'DIRECTORY_GENERATION_NOT_RUNNING'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER iam_directory_groups_guard BEFORE INSERT OR UPDATE OR DELETE ON iam_directory_groups FOR EACH ROW EXECUTE FUNCTION iam_directory_rows_guard();
CREATE TRIGGER iam_directory_members_guard BEFORE INSERT OR UPDATE OR DELETE ON iam_directory_members FOR EACH ROW EXECUTE FUNCTION iam_directory_rows_guard();
CREATE FUNCTION iam_directory_head_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM iam_directory_runs WHERE tenant_id=NEW.tenant_id AND connector_id=NEW.connector_id AND id=NEW.run_id AND state='succeeded') THEN RAISE EXCEPTION 'DIRECTORY_HEAD_NOT_COMPLETE'; END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER iam_directory_head_guard BEFORE INSERT OR UPDATE ON iam_directory_heads FOR EACH ROW EXECUTE FUNCTION iam_directory_head_guard();
