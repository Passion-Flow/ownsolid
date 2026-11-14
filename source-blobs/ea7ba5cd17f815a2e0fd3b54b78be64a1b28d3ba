-- M09 cumulative forward extension; the accepted fourteen-entry prefix is immutable.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai'));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1','inference.deploy.v1','inference.stop.v1','inference.remove.v1','inference.observe.v1','inference.verify.v1','training.run.v1'));
-- Typed training commands carry frozen input/resource identities, never source data or arbitrary code.
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_payload_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_payload_check CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=32768);

CREATE TABLE training_jobs (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 current_node_id uuid NOT NULL,
 model_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 dataset_version_id uuid NOT NULL,
 gpu_uuid varchar(64) NOT NULL CHECK(gpu_uuid ~ '^GPU-[a-f0-9-]{36}$'),
 configuration jsonb NOT NULL CHECK(jsonb_typeof(configuration)='object' AND octet_length(configuration::text)<=16384),
 configuration_sha256 varchar(64) NOT NULL CHECK(configuration_sha256 ~ '^[a-f0-9]{64}$'),
 execution_recipe jsonb NOT NULL CHECK(jsonb_typeof(execution_recipe)='object' AND octet_length(execution_recipe::text)<=32768),
 dataset_manifest_sha256 varchar(64) NOT NULL CHECK(dataset_manifest_sha256 ~ '^[a-f0-9]{64}$'),
 model_catalog_sha256 varchar(64) NOT NULL CHECK(model_catalog_sha256 ~ '^[a-f0-9]{64}$'),
 created_by uuid NOT NULL REFERENCES users(id),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 reason varchar(500) NOT NULL CHECK(length(btrim(reason))>0),
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 state varchar(24) NOT NULL CHECK(state IN ('queued','preparing','running','cancelling','succeeded','cancelled','failed','recovery_required')),
 wait_code varchar(80),
 next_dispatch_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 attempt_number integer NOT NULL DEFAULT 0 CHECK(attempt_number BETWEEN 0 AND 1000),
 current_attempt_id uuid,
 accepted_artifact_id uuid,
 pending_recovery_id uuid,
 cancel_requested_at timestamptz,
 error_code varchar(80),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 finished_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,node_id,id),
 UNIQUE(tenant_id,workspace_id,created_by,creation_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,current_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_versions(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,dataset_version_id) REFERENCES dataset_versions(tenant_id,workspace_id,dataset_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL)
);
CREATE INDEX training_job_scope_page_idx ON training_jobs(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE INDEX training_job_queue_idx ON training_jobs(next_dispatch_at,created_at,id) WHERE state='queued';

CREATE TABLE training_attempts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 node_id uuid NOT NULL,
 task_id uuid,
 attempt_number integer NOT NULL CHECK(attempt_number BETWEEN 1 AND 1000),
 state varchar(24) NOT NULL CHECK(state IN ('pending','preparing','running','stopping','succeeded','cancelled','failed','recovery_required','stopped')),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 input_inventory jsonb NOT NULL CHECK(jsonb_typeof(input_inventory)='object' AND octet_length(input_inventory::text)<=65536),
 resume_checkpoint_id uuid,
 last_event_sequence bigint NOT NULL DEFAULT 0 CHECK(last_event_sequence BETWEEN 0 AND 1000000),
 last_global_step integer NOT NULL DEFAULT 0 CHECK(last_global_step BETWEEN 0 AND 1000),
 stop_token_hash bytea CHECK(stop_token_hash IS NULL OR octet_length(stop_token_hash)=32),
 outcome_sha256 varchar(64) CHECK(outcome_sha256 IS NULL OR outcome_sha256 ~ '^[a-f0-9]{64}$'),
 outcome jsonb CHECK(outcome IS NULL OR jsonb_typeof(outcome)='object' AND octet_length(outcome::text)<=16777216),
 physical_stop_receipt jsonb CHECK(physical_stop_receipt IS NULL OR jsonb_typeof(physical_stop_receipt)='object' AND octet_length(physical_stop_receipt::text)<=32768),
 physical_stop_sha256 varchar(64) CHECK(physical_stop_sha256 IS NULL OR physical_stop_sha256 ~ '^[a-f0-9]{64}$'),
 physical_stopped_at timestamptz,
 error_code varchar(80),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 started_at timestamptz,
 finished_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,job_id,id),
 UNIQUE(job_id,attempt_number),
 UNIQUE(task_id),
 FOREIGN KEY(tenant_id,workspace_id,job_id) REFERENCES training_jobs(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((physical_stop_receipt IS NULL)=(physical_stop_sha256 IS NULL)),
 CHECK((physical_stop_receipt IS NULL)=(physical_stopped_at IS NULL)),
 CHECK((outcome IS NULL)=(outcome_sha256 IS NULL))
);
CREATE UNIQUE INDEX training_attempt_one_uncleared_job_idx ON training_attempts(job_id) WHERE state IN ('pending','preparing','running','stopping','recovery_required') AND physical_stopped_at IS NULL;
CREATE INDEX training_attempt_scope_page_idx ON training_attempts(tenant_id,workspace_id,job_id,created_at DESC,id DESC);
ALTER TABLE training_jobs ADD CONSTRAINT training_job_current_attempt_fk FOREIGN KEY(tenant_id,workspace_id,id,current_attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id);

CREATE TABLE training_events (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 sequence bigint NOT NULL CHECK(sequence BETWEEN 1 AND 1000000),
 kind varchar(16) NOT NULL CHECK(kind IN ('phase','metric','checkpoint')),
 global_step integer NOT NULL CHECK(global_step BETWEEN 0 AND 1000),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=16384),
 canonical_sha256 varchar(64) NOT NULL CHECK(canonical_sha256 ~ '^[a-f0-9]{64}$'),
 recorded_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(attempt_id,sequence),
 FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id)
);
CREATE INDEX training_event_job_page_idx ON training_events(tenant_id,workspace_id,job_id,attempt_id,sequence);
CREATE UNIQUE INDEX training_checkpoint_event_step_idx ON training_events(attempt_id,global_step) WHERE kind='checkpoint';

CREATE TABLE training_checkpoints (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 global_step integer NOT NULL CHECK(global_step BETWEEN 1 AND 1000),
 receipt_sha256 varchar(64) NOT NULL CHECK(receipt_sha256 ~ '^[a-f0-9]{64}$'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object' AND octet_length(receipt::text)<=1048576),
 file_inventory jsonb NOT NULL CHECK(jsonb_typeof(file_inventory)='object' AND octet_length(file_inventory::text)<=1048576),
 storage_key varchar(160) NOT NULL CHECK(storage_key ~ '^checkpoints/[a-f0-9-]{36}/[a-f0-9-]{36}/[a-f0-9-]{36}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,job_id,id),
 UNIQUE(attempt_id,global_step),
 FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id)
);
CREATE INDEX training_checkpoint_scope_page_idx ON training_checkpoints(tenant_id,workspace_id,job_id,created_at DESC,id DESC);
ALTER TABLE training_attempts ADD CONSTRAINT training_attempt_resume_checkpoint_fk FOREIGN KEY(tenant_id,workspace_id,job_id,resume_checkpoint_id) REFERENCES training_checkpoints(tenant_id,workspace_id,job_id,id);

CREATE TABLE training_artifacts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=1048576),
 file_inventory jsonb NOT NULL CHECK(jsonb_typeof(file_inventory)='object' AND octet_length(file_inventory::text)<=1048576),
 storage_key varchar(160) NOT NULL CHECK(storage_key ~ '^artifacts/[a-f0-9-]{36}/[a-f0-9-]{36}/[a-f0-9-]{36}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,job_id,id),
 UNIQUE(attempt_id),
 FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id)
);
ALTER TABLE training_jobs ADD CONSTRAINT training_job_accepted_artifact_fk FOREIGN KEY(tenant_id,workspace_id,id,accepted_artifact_id) REFERENCES training_artifacts(tenant_id,workspace_id,job_id,id);

CREATE TABLE training_recoveries (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 previous_attempt_id uuid,
 next_attempt_id uuid,
 checkpoint_id uuid,
 previous_node_id uuid NOT NULL,
 next_node_id uuid NOT NULL,
 mode varchar(16) NOT NULL CHECK(mode IN ('checkpoint','restart')),
 requested_by uuid NOT NULL REFERENCES users(id),
 reason varchar(500) NOT NULL CHECK(length(btrim(reason))>0),
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,job_id,id),
 UNIQUE(job_id,creation_key),
 FOREIGN KEY(tenant_id,workspace_id,job_id) REFERENCES training_jobs(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,job_id,previous_attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id),
 FOREIGN KEY(tenant_id,workspace_id,job_id,next_attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id),
 FOREIGN KEY(tenant_id,workspace_id,job_id,checkpoint_id) REFERENCES training_checkpoints(tenant_id,workspace_id,job_id,id),
 FOREIGN KEY(tenant_id,workspace_id,previous_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,next_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 CHECK((mode='checkpoint')=(checkpoint_id IS NOT NULL))
);
ALTER TABLE training_jobs ADD CONSTRAINT training_job_pending_recovery_fk FOREIGN KEY(tenant_id,workspace_id,id,pending_recovery_id) REFERENCES training_recoveries(tenant_id,workspace_id,job_id,id);

CREATE TABLE training_uploads (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 job_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 kind varchar(16) NOT NULL CHECK(kind IN ('checkpoint','artifact')),
 global_step integer NOT NULL CHECK(global_step BETWEEN 0 AND 1000),
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=1048576),
 state varchar(16) NOT NULL CHECK(state IN ('uploading','accepted','failed')),
 manifest_bytes bytea NOT NULL CHECK(octet_length(manifest_bytes) BETWEEN 2 AND 1048576),
 active_chunk_id uuid,
 active_asset varchar(100) CHECK(active_asset IS NULL OR active_asset ~ '^[A-Za-z0-9_.-]{1,100}$'),
 active_offset bigint CHECK(active_offset IS NULL OR active_offset>=0 AND active_offset%8388608=0),
 active_expires_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,job_id,attempt_id,id),
 UNIQUE(attempt_id,kind,global_step),
 FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id),
 CHECK(num_nonnulls(active_chunk_id,active_asset,active_offset,active_expires_at) IN (0,4))
);
CREATE TABLE training_upload_chunks (
 upload_id uuid NOT NULL REFERENCES training_uploads(id),
 asset varchar(100) NOT NULL CHECK(asset ~ '^[A-Za-z0-9_.-]{1,100}$'),
 byte_offset bigint NOT NULL CHECK(byte_offset>=0 AND byte_offset%8388608=0),
 size_bytes integer NOT NULL CHECK(size_bytes BETWEEN 1 AND 8388608),
 sha256 varchar(64) NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'),
 PRIMARY KEY(upload_id,asset,byte_offset)
);
ALTER TABLE training_checkpoints ADD CONSTRAINT training_checkpoint_upload_fk FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id,id) REFERENCES training_uploads(tenant_id,workspace_id,job_id,attempt_id,id);
ALTER TABLE training_artifacts ADD CONSTRAINT training_artifact_upload_fk FOREIGN KEY(tenant_id,workspace_id,job_id,attempt_id,id) REFERENCES training_uploads(tenant_id,workspace_id,job_id,attempt_id,id);

-- Preserve deployment lookup/identity while extending the same physical reservation uniqueness domain.
ALTER TABLE gpu_leases DROP CONSTRAINT gpu_lease_identity_check;
ALTER TABLE gpu_leases ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE gpu_leases ALTER COLUMN id SET NOT NULL;
ALTER TABLE gpu_leases DROP CONSTRAINT gpu_leases_pkey;
ALTER TABLE gpu_leases ADD PRIMARY KEY(id);
ALTER TABLE gpu_leases ALTER COLUMN deployment_id DROP NOT NULL;
ALTER TABLE gpu_leases ADD UNIQUE(deployment_id);
ALTER TABLE gpu_leases ADD COLUMN training_job_id uuid UNIQUE;
ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_exact_owner_check CHECK(num_nonnulls(deployment_id,training_job_id)=1);
ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_training_scope_fk FOREIGN KEY(tenant_id,workspace_id,training_job_id) REFERENCES training_jobs(tenant_id,workspace_id,id);
ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_node_scope_fk FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id);

CREATE FUNCTION protect_training_job_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.model_id,NEW.dataset_id,NEW.dataset_version_id,NEW.gpu_uuid,NEW.configuration,NEW.configuration_sha256,NEW.execution_recipe,NEW.dataset_manifest_sha256,NEW.model_catalog_sha256,NEW.created_by,NEW.reason,NEW.creation_key,NEW.creation_fingerprint,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.model_id,OLD.dataset_id,OLD.dataset_version_id,OLD.gpu_uuid,OLD.configuration,OLD.configuration_sha256,OLD.execution_recipe,OLD.dataset_manifest_sha256,OLD.model_catalog_sha256,OLD.created_by,OLD.reason,OLD.creation_key,OLD.creation_fingerprint,OLD.created_at)
 THEN RAISE EXCEPTION 'TRAINING_INPUTS_IMMUTABLE' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END$$;
CREATE TRIGGER training_job_inputs_immutable BEFORE UPDATE ON training_jobs FOR EACH ROW EXECUTE FUNCTION protect_training_job_inputs();

CREATE FUNCTION protect_training_attempt_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.job_id,NEW.node_id,NEW.task_id,NEW.attempt_number,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.input_inventory,NEW.resume_checkpoint_id,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.job_id,OLD.node_id,OLD.task_id,OLD.attempt_number,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.input_inventory,OLD.resume_checkpoint_id,OLD.created_at)
 OR OLD.outcome IS NOT NULL AND ROW(NEW.outcome,NEW.outcome_sha256) IS DISTINCT FROM ROW(OLD.outcome,OLD.outcome_sha256)
 OR OLD.stop_token_hash IS NOT NULL AND NEW.stop_token_hash IS DISTINCT FROM OLD.stop_token_hash
 OR OLD.physical_stop_receipt IS NOT NULL AND ROW(NEW.physical_stop_receipt,NEW.physical_stop_sha256,NEW.physical_stopped_at) IS DISTINCT FROM ROW(OLD.physical_stop_receipt,OLD.physical_stop_sha256,OLD.physical_stopped_at)
 THEN RAISE EXCEPTION 'TRAINING_ATTEMPT_INPUTS_IMMUTABLE' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END$$;
CREATE TRIGGER training_attempt_inputs_immutable BEFORE UPDATE ON training_attempts FOR EACH ROW EXECUTE FUNCTION protect_training_attempt_inputs();

CREATE FUNCTION reject_training_immutable_change() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'TRAINING_ACCEPTED_METADATA_IMMUTABLE' USING ERRCODE='23514';
END$$;
CREATE TRIGGER training_checkpoint_immutable BEFORE UPDATE OR DELETE ON training_checkpoints FOR EACH ROW EXECUTE FUNCTION reject_training_immutable_change();
CREATE TRIGGER training_artifact_immutable BEFORE UPDATE OR DELETE ON training_artifacts FOR EACH ROW EXECUTE FUNCTION reject_training_immutable_change();
CREATE TRIGGER training_event_immutable BEFORE UPDATE OR DELETE ON training_events FOR EACH ROW EXECUTE FUNCTION reject_training_immutable_change();
CREATE TRIGGER training_chunk_immutable BEFORE UPDATE OR DELETE ON training_upload_chunks FOR EACH ROW EXECUTE FUNCTION reject_training_immutable_change();

CREATE FUNCTION protect_training_upload_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.job_id,NEW.attempt_id,NEW.kind,NEW.global_step,NEW.manifest_sha256,NEW.manifest,NEW.manifest_bytes,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.job_id,OLD.attempt_id,OLD.kind,OLD.global_step,OLD.manifest_sha256,OLD.manifest,OLD.manifest_bytes,OLD.created_at)
 OR OLD.state='accepted' AND NEW.state IS DISTINCT FROM OLD.state
 THEN RAISE EXCEPTION 'TRAINING_UPLOAD_INPUTS_IMMUTABLE' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END$$;
CREATE TRIGGER training_upload_inputs_immutable BEFORE UPDATE ON training_uploads FOR EACH ROW EXECUTE FUNCTION protect_training_upload_inputs();

CREATE FUNCTION protect_training_recovery_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR ROW(NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.job_id,NEW.previous_attempt_id,NEW.checkpoint_id,NEW.previous_node_id,NEW.next_node_id,NEW.mode,NEW.requested_by,NEW.reason,NEW.creation_key,NEW.creation_fingerprint,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.job_id,OLD.previous_attempt_id,OLD.checkpoint_id,OLD.previous_node_id,OLD.next_node_id,OLD.mode,OLD.requested_by,OLD.reason,OLD.creation_key,OLD.creation_fingerprint,OLD.created_at)
 OR OLD.next_attempt_id IS NOT NULL AND NEW.next_attempt_id IS DISTINCT FROM OLD.next_attempt_id
 THEN RAISE EXCEPTION 'TRAINING_RECOVERY_INPUTS_IMMUTABLE' USING ERRCODE='23514'; END IF;
 RETURN NEW;
END$$;
CREATE TRIGGER training_recovery_inputs_immutable BEFORE UPDATE OR DELETE ON training_recoveries FOR EACH ROW EXECUTE FUNCTION protect_training_recovery_inputs();

ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_inference_identity CHECK(deployment_id IS NULL OR id=deployment_id);

ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb) OR
 (execution_profile='systemd_maintenance' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]'::jsonb) OR
 (execution_profile='docker_inference' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1"]'::jsonb) OR
 (execution_profile='docker_ai' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1"]'::jsonb));
