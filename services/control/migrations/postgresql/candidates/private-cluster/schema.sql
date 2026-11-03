-- SOURCE CANDIDATE ONLY. No active migration number or installed-schema claim.
CREATE TABLE private_cluster_connections (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL,
 profile_id varchar(63) NOT NULL, cluster_uid uuid NOT NULL, namespace_uid uuid NOT NULL,
 namespace_name varchar(63) NOT NULL, installation_id uuid NOT NULL, profile varchar(64) NOT NULL,
 config_sha256 char(64) NOT NULL, credential_sha256 char(64) NOT NULL, manifest_sha256 char(64) NOT NULL,
 image_digest varchar(71) NOT NULL, config_digest varchar(71) NOT NULL,
 state varchar(16) NOT NULL CHECK(state IN('active','disabled','restore_fenced')),
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991), active_run_id uuid,
 observation jsonb, created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,workspace_id,id), UNIQUE(cluster_uid,namespace_uid), UNIQUE(tenant_id,workspace_id,profile_id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CHECK(cluster_uid<>namespace_uid), CHECK(namespace_name LIKE 'projectone-test-%'),
 CHECK(profile IN('kubernetes1.36.4-linux-amd64','local-kind1.36.4-linux-arm64')),
 CHECK(config_sha256~'^[a-f0-9]{64}$' AND credential_sha256~'^[a-f0-9]{64}$' AND manifest_sha256~'^[a-f0-9]{64}$'),
 CHECK(image_digest~'^sha256:[a-f0-9]{64}$' AND config_digest~'^sha256:[a-f0-9]{64}$'),
 CHECK(observation IS NULL OR jsonb_typeof(observation)='object')
);
CREATE TABLE private_cluster_runs (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, connection_id uuid NOT NULL,
 connection_revision bigint NOT NULL CHECK(connection_revision BETWEEN 1 AND 9007199254740991),
 created_by uuid NOT NULL REFERENCES users(id), actor_epoch bigint NOT NULL CHECK(actor_epoch>=1),
 membership_authority uuid, created_session_id uuid NOT NULL, created_token_hash bytea NOT NULL CHECK(octet_length(created_token_hash)=32),
 config_sha256 char(64) NOT NULL CHECK(config_sha256~'^[a-f0-9]{64}$'),
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'), spec_sha256 char(64) NOT NULL CHECK(spec_sha256~'^[a-f0-9]{64}$'),
 phase varchar(24) NOT NULL CHECK(phase IN('prepared','lease_pending','lease_acquired','job_pending','observing','job_delete_pending','job_absent','lease_delete_pending','cleaned')),
 result varchar(16) CHECK(result IN('succeeded','failed','cancelled')),
 lease_uid uuid, job_uid uuid, pod_uid uuid, remote_state jsonb,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(tenant_id,workspace_id,connection_id,id), UNIQUE(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,connection_id) REFERENCES private_cluster_connections(tenant_id,workspace_id,id),
 CHECK(job_uid IS NULL OR lease_uid IS NOT NULL), CHECK(pod_uid IS NULL OR job_uid IS NOT NULL),
 CHECK(phase NOT IN('lease_acquired','job_pending','observing','job_delete_pending','job_absent','lease_delete_pending') OR lease_uid IS NOT NULL),
 CHECK(phase NOT IN('observing','job_delete_pending') OR job_uid IS NOT NULL),
 CHECK(phase<>'cleaned' OR result IS NOT NULL),
 CHECK(remote_state IS NULL OR jsonb_typeof(remote_state)='object')
);
ALTER TABLE private_cluster_connections ADD CONSTRAINT private_cluster_active_run FOREIGN KEY(tenant_id,workspace_id,id,active_run_id) REFERENCES private_cluster_runs(tenant_id,workspace_id,connection_id,id);
CREATE INDEX private_cluster_history ON private_cluster_runs(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE private_cluster_intents (
 tenant_id uuid NOT NULL, workspace_id uuid NOT NULL, actor_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL, operation varchar(24) NOT NULL CHECK(operation IN('bind','rebind','disable','observe','start','continue','reconcile','cancel','cleanup')),
 fingerprint char(64) NOT NULL CHECK(fingerprint~'^[a-f0-9]{64}$'), target_id uuid NOT NULL,
 result_revision bigint NOT NULL CHECK(result_revision BETWEEN 1 AND 9007199254740991),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(tenant_id,workspace_id,actor_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE FUNCTION private_cluster_connection_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.profile_id,OLD.cluster_uid,OLD.namespace_uid,OLD.namespace_name,OLD.installation_id,OLD.profile,OLD.created_by,OLD.created_at)
 IS DISTINCT FROM ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.profile_id,NEW.cluster_uid,NEW.namespace_uid,NEW.namespace_name,NEW.installation_id,NEW.profile,NEW.created_by,NEW.created_at)
 OR NEW.revision<>OLD.revision+1 OR OLD.active_run_id IS NOT NULL AND ROW(OLD.config_sha256,OLD.manifest_sha256,OLD.image_digest,OLD.config_digest) IS DISTINCT FROM ROW(NEW.config_sha256,NEW.manifest_sha256,NEW.image_digest,NEW.config_digest)
 THEN RAISE EXCEPTION 'PRIVATE_CLUSTER_IMMUTABLE_CONNECTION'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER private_cluster_connection_identity BEFORE UPDATE ON private_cluster_connections FOR EACH ROW EXECUTE FUNCTION private_cluster_connection_guard();
CREATE FUNCTION private_cluster_run_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.connection_id,OLD.connection_revision,OLD.created_by,OLD.actor_epoch,OLD.membership_authority,OLD.created_session_id,OLD.created_token_hash,OLD.config_sha256,OLD.request,OLD.spec_sha256,OLD.created_at)
 IS DISTINCT FROM ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.connection_id,NEW.connection_revision,NEW.created_by,NEW.actor_epoch,NEW.membership_authority,NEW.created_session_id,NEW.created_token_hash,NEW.config_sha256,NEW.request,NEW.spec_sha256,NEW.created_at)
 OR NEW.revision<>OLD.revision+1 OR OLD.phase='cleaned' OR NOT (OLD.phase=NEW.phase OR OLD.phase='prepared' AND NEW.phase IN('lease_pending','cleaned') OR OLD.phase='lease_pending' AND NEW.phase IN('prepared','lease_acquired') OR OLD.phase='lease_acquired' AND NEW.phase IN('job_pending','job_absent') OR OLD.phase='job_pending' AND NEW.phase IN('lease_acquired','observing') OR OLD.phase='observing' AND NEW.phase='job_delete_pending' OR OLD.phase='job_delete_pending' AND NEW.phase IN('observing','job_absent') OR OLD.phase='job_absent' AND NEW.phase='lease_delete_pending' OR OLD.phase='lease_delete_pending' AND NEW.phase IN('job_absent','cleaned'))
 OR OLD.result IS NOT NULL AND OLD.result IS DISTINCT FROM NEW.result
 OR OLD.lease_uid IS NOT NULL AND OLD.lease_uid IS DISTINCT FROM NEW.lease_uid
 OR OLD.job_uid IS NOT NULL AND OLD.job_uid IS DISTINCT FROM NEW.job_uid
 OR OLD.pod_uid IS NOT NULL AND OLD.pod_uid IS DISTINCT FROM NEW.pod_uid
 THEN RAISE EXCEPTION 'PRIVATE_CLUSTER_IMMUTABLE_RUN'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER private_cluster_run_identity BEFORE UPDATE ON private_cluster_runs FOR EACH ROW EXECUTE FUNCTION private_cluster_run_guard();
CREATE FUNCTION private_cluster_intent_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'PRIVATE_CLUSTER_IMMUTABLE_INTENT'; END $$;
CREATE TRIGGER private_cluster_intent_identity BEFORE UPDATE OR DELETE ON private_cluster_intents FOR EACH ROW EXECUTE FUNCTION private_cluster_intent_guard();
CREATE FUNCTION private_cluster_history_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'PRIVATE_CLUSTER_IMMUTABLE_HISTORY'; END $$;
CREATE TRIGGER private_cluster_connection_delete BEFORE DELETE ON private_cluster_connections FOR EACH ROW EXECUTE FUNCTION private_cluster_history_guard();
CREATE TRIGGER private_cluster_run_delete BEFORE DELETE ON private_cluster_runs FOR EACH ROW EXECUTE FUNCTION private_cluster_history_guard();
