-- M10 is mutable until its complete local acceptance gate. Migrations1-9 remain frozen.
-- A new enrollment explicitly selects lifecycle authority. Existing rows/capabilities are never upgraded.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle'));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1','inference.deploy.v1','inference.stop.v1','inference.remove.v1','inference.observe.v1','inference.verify.v1','training.run.v1','model.prepare.v1','inference.derived.v1'));
CREATE TABLE model_holdouts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 dataset_id uuid NOT NULL,
 dataset_version_id uuid NOT NULL,
 name varchar(100) NOT NULL CHECK(length(btrim(name))>0),
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 effective_source_sha256 varchar(64) NOT NULL CHECK(effective_source_sha256 ~ '^[a-f0-9]{64}$'),
 row_mapping_sha256 varchar(64) NOT NULL CHECK(row_mapping_sha256 ~ '^[a-f0-9]{64}$'),
 source_bytes bigint NOT NULL CHECK(source_bytes BETWEEN 1 AND 67108864),
 row_count bigint NOT NULL CHECK(row_count BETWEEN 3 AND 256),
 label_counts jsonb NOT NULL CHECK(jsonb_typeof(label_counts)='object' AND octet_length(label_counts::text)<=1024),
 recipe_id varchar(80) NOT NULL,
 recipe_sha256 varchar(64) NOT NULL CHECK(recipe_sha256 ~ '^[a-f0-9]{64}$'),
 instruction_sha256 varchar(64) NOT NULL CHECK(instruction_sha256 ~ '^[a-f0-9]{64}$'),
 fingerprint_algorithm varchar(100) NOT NULL,
 created_by uuid NOT NULL REFERENCES users(id),
 audience varchar(8) NOT NULL CHECK(audience IN ('portal','admin')),
 reason varchar(500) NOT NULL CHECK(length(btrim(reason))>0),
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,id),
 CONSTRAINT model_holdout_version_unique UNIQUE(tenant_id,workspace_id,dataset_version_id,recipe_id),
 CONSTRAINT model_holdout_creation_unique UNIQUE(tenant_id,workspace_id,created_by,audience,creation_key),
 CHECK(label_counts ?& ARRAY['account','billing','technical'] AND label_counts - ARRAY['account','billing','technical'] = '{}'::jsonb),
 CHECK(jsonb_typeof(label_counts->'account')='number' AND jsonb_typeof(label_counts->'billing')='number' AND jsonb_typeof(label_counts->'technical')='number'),
 CHECK((label_counts->>'account') ~ '^[1-9][0-9]*$' AND (label_counts->>'billing') ~ '^[1-9][0-9]*$' AND (label_counts->>'technical') ~ '^[1-9][0-9]*$'),
 CHECK((label_counts->>'account')::bigint BETWEEN 1 AND 256 AND (label_counts->>'billing')::bigint BETWEEN 1 AND 256 AND (label_counts->>'technical')::bigint BETWEEN 1 AND 256),
 CHECK((label_counts->>'account')::bigint+(label_counts->>'billing')::bigint+(label_counts->>'technical')::bigint=row_count),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 FOREIGN KEY(tenant_id,workspace_id,dataset_id,dataset_version_id) REFERENCES dataset_versions(tenant_id,workspace_id,dataset_id,id)
);
CREATE INDEX model_holdout_page_idx ON model_holdouts(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE model_holdout_sources (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 holdout_id uuid NOT NULL,
 source_sha256 bytea NOT NULL CHECK(octet_length(source_sha256)=32),
 PRIMARY KEY(tenant_id,workspace_id,holdout_id,source_sha256),
 FOREIGN KEY(tenant_id,workspace_id,holdout_id) REFERENCES model_holdouts(tenant_id,workspace_id,id)
);
CREATE INDEX model_holdout_source_reservation_idx ON model_holdout_sources(tenant_id,workspace_id,source_sha256);
CREATE TABLE model_holdout_prompts (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 holdout_id uuid NOT NULL,
 prompt_sha256 bytea NOT NULL CHECK(octet_length(prompt_sha256)=32),
 PRIMARY KEY(tenant_id,workspace_id,holdout_id,prompt_sha256),
 FOREIGN KEY(tenant_id,workspace_id,holdout_id) REFERENCES model_holdouts(tenant_id,workspace_id,id)
);
CREATE INDEX model_holdout_prompt_reservation_idx ON model_holdout_prompts(tenant_id,workspace_id,prompt_sha256);
CREATE FUNCTION protect_model_holdout_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'MODEL_HOLDOUT_IMMUTABLE';
END;
$$;
CREATE TRIGGER model_holdout_immutable BEFORE UPDATE OR DELETE ON model_holdouts FOR EACH ROW EXECUTE FUNCTION protect_model_holdout_immutable();
CREATE TRIGGER model_holdout_source_immutable BEFORE UPDATE OR DELETE ON model_holdout_sources FOR EACH ROW EXECUTE FUNCTION protect_model_holdout_immutable();
CREATE TRIGGER model_holdout_prompt_immutable BEFORE UPDATE OR DELETE ON model_holdout_prompts FOR EACH ROW EXECUTE FUNCTION protect_model_holdout_immutable();

CREATE TABLE model_preparations (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 current_node_id uuid NOT NULL,
 model_id uuid NOT NULL,
 training_job_id uuid NOT NULL,
 training_attempt_id uuid NOT NULL,
 training_artifact_id uuid NOT NULL,
 heldout_id uuid NOT NULL,
 gpu_uuid varchar(64) NOT NULL CHECK(gpu_uuid ~ '^GPU-[a-f0-9-]{36}$'),
 input_snapshot jsonb NOT NULL CHECK(jsonb_typeof(input_snapshot)='object' AND octet_length(input_snapshot::text)<=65536),
 input_sha256 varchar(64) NOT NULL CHECK(input_sha256 ~ '^[a-f0-9]{64}$'),
 configuration jsonb NOT NULL CHECK(jsonb_typeof(configuration)='object' AND octet_length(configuration::text)<=16384),
 configuration_sha256 varchar(64) NOT NULL CHECK(configuration_sha256 ~ '^[a-f0-9]{64}$'),
 execution_recipe jsonb NOT NULL CHECK(jsonb_typeof(execution_recipe)='object' AND octet_length(execution_recipe::text)<=32768),
 created_by uuid NOT NULL REFERENCES users(id),
 requested_by uuid NOT NULL REFERENCES users(id),
 creation_audience varchar(8) NOT NULL CHECK(creation_audience IN ('portal','admin')),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 reason varchar(500) NOT NULL CHECK(length(btrim(reason))>0),
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 state varchar(24) NOT NULL CHECK(state IN ('queued','preparing','running','cancelling','recovery_required','quality_failed','awaiting_review','approved','rejected','cancelled','failed')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 wait_code varchar(80) CHECK(wait_code IS NULL OR wait_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 error_code varchar(80) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 cancel_requested_at timestamptz,
 cancel_requested_by uuid REFERENCES users(id),
 next_dispatch_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 finished_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 CONSTRAINT model_preparation_creation_unique UNIQUE(tenant_id,workspace_id,created_by,creation_audience,creation_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,current_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_versions(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,training_job_id) REFERENCES training_jobs(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,training_job_id,training_attempt_id) REFERENCES training_attempts(tenant_id,workspace_id,job_id,id),
 FOREIGN KEY(tenant_id,workspace_id,training_job_id,training_artifact_id) REFERENCES training_artifacts(tenant_id,workspace_id,job_id,id),
 FOREIGN KEY(tenant_id,workspace_id,heldout_id) REFERENCES model_holdouts(tenant_id,workspace_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((cancel_requested_at IS NULL)=(cancel_requested_by IS NULL))
);
CREATE INDEX model_preparation_scope_page_idx ON model_preparations(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE INDEX model_preparation_queue_idx ON model_preparations(next_dispatch_at,created_at,id) WHERE state='queued';
CREATE FUNCTION protect_model_preparation_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_PREPARATION_IMMUTABLE'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.model_id,NEW.training_job_id,NEW.training_attempt_id,NEW.training_artifact_id,NEW.heldout_id,NEW.gpu_uuid,NEW.input_snapshot,NEW.input_sha256,NEW.configuration,NEW.configuration_sha256,NEW.execution_recipe,NEW.created_by,NEW.creation_audience,NEW.reason,NEW.creation_key,NEW.creation_fingerprint,NEW.created_at)
 IS DISTINCT FROM
 (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.model_id,OLD.training_job_id,OLD.training_attempt_id,OLD.training_artifact_id,OLD.heldout_id,OLD.gpu_uuid,OLD.input_snapshot,OLD.input_sha256,OLD.configuration,OLD.configuration_sha256,OLD.execution_recipe,OLD.created_by,OLD.creation_audience,OLD.reason,OLD.creation_key,OLD.creation_fingerprint,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_INPUTS_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER model_preparation_inputs_immutable BEFORE UPDATE OR DELETE ON model_preparations FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_inputs();

ALTER TABLE model_preparations ADD COLUMN attempt_number integer NOT NULL DEFAULT 0 CHECK(attempt_number BETWEEN 0 AND 1000);
ALTER TABLE model_preparations ADD COLUMN current_attempt_id uuid;
CREATE TABLE model_preparation_attempts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 node_id uuid NOT NULL,
 task_id uuid NOT NULL UNIQUE,
 attempt_number integer NOT NULL CHECK(attempt_number BETWEEN 1 AND 1000),
 state varchar(24) NOT NULL CHECK(state IN ('pending','preparing','running','stopping','recovery_required','stopped','prepared','cancelled','failed')),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 command_sha256 varchar(64) NOT NULL CHECK(command_sha256 ~ '^[a-f0-9]{64}$'),
 last_event_sequence bigint NOT NULL DEFAULT 0 CHECK(last_event_sequence BETWEEN 0 AND 1024),
 stop_token_hash bytea CHECK(stop_token_hash IS NULL OR octet_length(stop_token_hash)=32),
 outcome_sha256 varchar(64) CHECK(outcome_sha256 IS NULL OR outcome_sha256 ~ '^[a-f0-9]{64}$'),
 outcome jsonb CHECK(outcome IS NULL OR jsonb_typeof(outcome)='object' AND octet_length(outcome::text)<=8388608),
 physical_stop_receipt jsonb CHECK(physical_stop_receipt IS NULL OR jsonb_typeof(physical_stop_receipt)='object' AND octet_length(physical_stop_receipt::text)<=32768),
 physical_stop_sha256 varchar(64) CHECK(physical_stop_sha256 IS NULL OR physical_stop_sha256 ~ '^[a-f0-9]{64}$'),
 physical_stopped_at timestamptz,
 error_code varchar(80) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 started_at timestamptz,
 finished_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,preparation_id,id),
 UNIQUE(preparation_id,attempt_number),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id) REFERENCES model_preparations(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((outcome IS NULL)=(outcome_sha256 IS NULL)),
 CHECK((physical_stop_receipt IS NULL)=(physical_stop_sha256 IS NULL)),
 CHECK((physical_stop_receipt IS NULL)=(physical_stopped_at IS NULL))
);
CREATE UNIQUE INDEX model_preparation_one_uncleared_attempt_idx ON model_preparation_attempts(preparation_id) WHERE physical_stopped_at IS NULL AND state IN ('pending','preparing','running','stopping','recovery_required');
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_current_attempt_fk FOREIGN KEY(tenant_id,workspace_id,id,current_attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id);
ALTER TABLE gpu_leases ADD COLUMN model_preparation_id uuid UNIQUE;
ALTER TABLE gpu_leases DROP CONSTRAINT gpu_lease_exact_owner_check;
ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_exact_owner_check CHECK(num_nonnulls(deployment_id,training_job_id,model_preparation_id)=1);
ALTER TABLE gpu_leases ADD CONSTRAINT gpu_lease_preparation_scope_fk FOREIGN KEY(tenant_id,workspace_id,model_preparation_id) REFERENCES model_preparations(tenant_id,workspace_id,id);
CREATE FUNCTION protect_model_preparation_attempt_inputs() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_PREPARATION_ATTEMPT_IMMUTABLE'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.preparation_id,NEW.node_id,NEW.task_id,NEW.attempt_number,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.command_sha256,NEW.created_at)
 IS DISTINCT FROM
 (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.preparation_id,OLD.node_id,OLD.task_id,OLD.attempt_number,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.command_sha256,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_ATTEMPT_INPUTS_IMMUTABLE'; END IF;
 IF OLD.stop_token_hash IS NOT NULL AND NEW.stop_token_hash IS DISTINCT FROM OLD.stop_token_hash THEN RAISE EXCEPTION 'MODEL_PREPARATION_STOP_AUTHORITY_IMMUTABLE'; END IF;
 IF OLD.physical_stopped_at IS NOT NULL AND (NEW.physical_stopped_at,NEW.physical_stop_receipt,NEW.physical_stop_sha256) IS DISTINCT FROM (OLD.physical_stopped_at,OLD.physical_stop_receipt,OLD.physical_stop_sha256) THEN RAISE EXCEPTION 'MODEL_PREPARATION_STOP_PROOF_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER model_preparation_attempt_inputs_immutable BEFORE UPDATE OR DELETE ON model_preparation_attempts FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_attempt_inputs();

ALTER TABLE model_preparation_attempts ADD COLUMN event_bytes bigint NOT NULL DEFAULT 0 CHECK(event_bytes BETWEEN 0 AND 16777216);
CREATE TABLE model_preparation_events (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 sequence bigint NOT NULL CHECK(sequence BETWEEN 1 AND 1024),
 kind varchar(24) NOT NULL CHECK(kind IN ('phase','evaluation_progress')),
 payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=16384),
 canonical_sha256 varchar(64) NOT NULL CHECK(canonical_sha256 ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(attempt_id,sequence),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id)
);
CREATE FUNCTION protect_model_preparation_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'MODEL_PREPARATION_EVENT_IMMUTABLE'; END;
$$;
CREATE TRIGGER model_preparation_event_immutable BEFORE UPDATE OR DELETE ON model_preparation_events FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_event();

CREATE TABLE model_preparation_recoveries (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 previous_attempt_id uuid NOT NULL,
 previous_node_id uuid NOT NULL,
 next_node_id uuid NOT NULL,
 next_attempt_number integer NOT NULL CHECK(next_attempt_number BETWEEN 2 AND 1000),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_audience varchar(8) NOT NULL CHECK(requested_audience IN ('portal','admin')),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 membership_authority uuid,
 reason varchar(500) NOT NULL CHECK(length(btrim(reason))>0),
 creation_key uuid NOT NULL,
 creation_fingerprint bytea NOT NULL CHECK(octet_length(creation_fingerprint)=32),
 dispatched_attempt_id uuid,
 applied_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,preparation_id,id),
 UNIQUE(preparation_id,requested_by,requested_audience,creation_key),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id) REFERENCES model_preparations(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,previous_attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id),
 FOREIGN KEY(tenant_id,workspace_id,previous_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,next_node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,dispatched_attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id),
 CHECK(requested_audience='admin' OR membership_authority IS NOT NULL),
 CHECK((dispatched_attempt_id IS NULL)=(applied_at IS NULL))
);
ALTER TABLE model_preparations ADD COLUMN pending_recovery_id uuid;
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_pending_recovery_fk FOREIGN KEY(tenant_id,workspace_id,id,pending_recovery_id) REFERENCES model_preparation_recoveries(tenant_id,workspace_id,preparation_id,id);
CREATE FUNCTION protect_model_preparation_recovery() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_PREPARATION_RECOVERY_IMMUTABLE'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.preparation_id,NEW.previous_attempt_id,NEW.previous_node_id,NEW.next_node_id,NEW.next_attempt_number,NEW.requested_by,NEW.requested_audience,NEW.requested_epoch,NEW.membership_authority,NEW.reason,NEW.creation_key,NEW.creation_fingerprint,NEW.created_at)
 IS DISTINCT FROM
 (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.preparation_id,OLD.previous_attempt_id,OLD.previous_node_id,OLD.next_node_id,OLD.next_attempt_number,OLD.requested_by,OLD.requested_audience,OLD.requested_epoch,OLD.membership_authority,OLD.reason,OLD.creation_key,OLD.creation_fingerprint,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_RECOVERY_IMMUTABLE'; END IF;
 IF OLD.applied_at IS NOT NULL AND (NEW.dispatched_attempt_id,NEW.applied_at) IS DISTINCT FROM (OLD.dispatched_attempt_id,OLD.applied_at) THEN RAISE EXCEPTION 'MODEL_PREPARATION_RECOVERY_APPLIED_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER model_preparation_recovery_immutable BEFORE UPDATE OR DELETE ON model_preparation_recoveries FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_recovery();

ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_attempt_presence_check CHECK((attempt_number=0)=(current_attempt_id IS NULL));
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_recovery_presence_check CHECK(pending_recovery_id IS NULL OR attempt_number>0);

CREATE TABLE model_preparation_uploads (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 attempt_id uuid NOT NULL UNIQUE,
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=8388608),
 manifest_bytes bytea NOT NULL CHECK(octet_length(manifest_bytes) BETWEEN 2 AND 8388608),
 descriptor jsonb NOT NULL CHECK(jsonb_typeof(descriptor)='object' AND octet_length(descriptor::text)<=32768),
 state varchar(16) NOT NULL DEFAULT 'uploading' CHECK(state IN ('uploading','verifying','accepted','failed')),
 active_chunk_id uuid,
 active_asset varchar(100),
 active_offset bigint CHECK(active_offset IS NULL OR active_offset BETWEEN 0 AND 2147483648),
 active_expires_at timestamptz,
 error_code varchar(80) CHECK(error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{1,79}$'),
 accepted_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,preparation_id,attempt_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id),
 CHECK((active_chunk_id IS NULL)=(active_asset IS NULL)),
 CHECK((active_chunk_id IS NULL)=(active_offset IS NULL)),
 CHECK((active_chunk_id IS NULL)=(active_expires_at IS NULL)),
 CHECK((state='accepted')=(accepted_at IS NOT NULL))
);
CREATE TABLE model_preparation_upload_chunks (
 upload_id uuid NOT NULL REFERENCES model_preparation_uploads(id),
 asset varchar(100) NOT NULL CHECK(asset ~ '^[A-Za-z0-9_.-]{1,100}$' AND asset NOT IN ('.','..')),
 byte_offset bigint NOT NULL CHECK(byte_offset BETWEEN 0 AND 2147483648 AND byte_offset % 8388608=0),
 size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 8388608),
 sha256 varchar(64) NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(upload_id,asset,byte_offset)
);
CREATE TABLE model_preparation_artifacts (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 attempt_id uuid NOT NULL UNIQUE,
 upload_id uuid NOT NULL UNIQUE,
 manifest_sha256 varchar(64) NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 descriptor jsonb NOT NULL CHECK(jsonb_typeof(descriptor)='object' AND octet_length(descriptor::text)<=32768),
 report jsonb NOT NULL CHECK(jsonb_typeof(report)='object' AND octet_length(report::text)<=8388608),
 report_bytes bytea NOT NULL CHECK(octet_length(report_bytes) BETWEEN 2 AND 8388608),
 storage_path varchar(512) NOT NULL,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,preparation_id,attempt_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id) REFERENCES model_preparation_attempts(tenant_id,workspace_id,preparation_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id,upload_id) REFERENCES model_preparation_uploads(tenant_id,workspace_id,preparation_id,attempt_id,id)
);
ALTER TABLE model_preparation_attempts ADD COLUMN outcome_bytes bytea CHECK(outcome_bytes IS NULL OR octet_length(outcome_bytes) BETWEEN 2 AND 8388608);
ALTER TABLE model_preparation_attempts ADD CONSTRAINT model_preparation_outcome_bytes_presence_check CHECK((outcome IS NULL)=(outcome_bytes IS NULL));
CREATE FUNCTION protect_model_preparation_upload() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_PREPARATION_UPLOAD_IMMUTABLE'; END IF;
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.preparation_id,NEW.attempt_id,NEW.manifest_sha256,NEW.manifest,NEW.manifest_bytes,NEW.descriptor,NEW.created_at)
 IS DISTINCT FROM
 (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.preparation_id,OLD.attempt_id,OLD.manifest_sha256,OLD.manifest,OLD.manifest_bytes,OLD.descriptor,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_PREPARATION_UPLOAD_IMMUTABLE'; END IF;
 IF OLD.accepted_at IS NOT NULL AND (NEW.state,NEW.accepted_at) IS DISTINCT FROM (OLD.state,OLD.accepted_at) THEN RAISE EXCEPTION 'MODEL_PREPARATION_ACCEPTED_UPLOAD_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER model_preparation_upload_immutable BEFORE UPDATE OR DELETE ON model_preparation_uploads FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_upload();
CREATE FUNCTION protect_model_preparation_output_record() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE'; END;
$$;
CREATE TRIGGER model_preparation_chunk_immutable BEFORE UPDATE OR DELETE ON model_preparation_upload_chunks FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_output_record();
CREATE TRIGGER model_preparation_artifact_immutable BEFORE UPDATE OR DELETE ON model_preparation_artifacts FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_output_record();
CREATE FUNCTION protect_model_preparation_outcome() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.outcome_sha256 IS NOT NULL AND (NEW.outcome,NEW.outcome_bytes,NEW.outcome_sha256) IS DISTINCT FROM (OLD.outcome,OLD.outcome_bytes,OLD.outcome_sha256) THEN RAISE EXCEPTION 'MODEL_PREPARATION_OUTCOME_IMMUTABLE'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER model_preparation_outcome_immutable BEFORE UPDATE ON model_preparation_attempts FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_outcome();

-- Explicit Admin review publishes a scoped local derivative, never an approved-base revision.
ALTER TABLE model_versions ADD COLUMN kind varchar(24) NOT NULL DEFAULT 'approved_base' CHECK(kind IN ('approved_base','merged_lora'));
ALTER TABLE model_versions ADD CONSTRAINT model_version_catalog_identity UNIQUE(tenant_id,workspace_id,id,catalog_digest);
CREATE TABLE model_preparation_reviews (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 artifact_id uuid NOT NULL,
 report_sha256 varchar(64) NOT NULL CHECK(report_sha256 ~ '^[a-f0-9]{64}$'),
 expected_revision bigint NOT NULL CHECK(expected_revision BETWEEN 1 AND 9007199254740991),
 decision varchar(16) NOT NULL CHECK(decision IN ('approve','reject')),
 model_id uuid,
 model_name varchar(100) NOT NULL,
 reviewed_by uuid NOT NULL REFERENCES users(id),
 audience varchar(16) NOT NULL CHECK(audience='admin'),
 reason varchar(500) NOT NULL,
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 CHECK((decision='approve' AND model_id=artifact_id AND length(model_name)>0) OR (decision='reject' AND model_id IS NULL AND model_name='')),
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,preparation_id,attempt_id),
 UNIQUE(tenant_id,workspace_id,reviewed_by,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id,artifact_id) REFERENCES model_preparation_artifacts(tenant_id,workspace_id,preparation_id,attempt_id,id),
 -- The decision and owning model registration commit together; neither may survive alone.
 FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_versions(tenant_id,workspace_id,id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE model_derivations (
 model_id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 preparation_id uuid NOT NULL,
 attempt_id uuid NOT NULL,
 artifact_id uuid NOT NULL UNIQUE,
 base_model_id uuid NOT NULL,
 review_id uuid NOT NULL UNIQUE,
 catalog_digest varchar(64) NOT NULL CHECK(catalog_digest ~ '^[a-f0-9]{64}$'),
 catalog jsonb NOT NULL CHECK(jsonb_typeof(catalog)='object' AND octet_length(catalog::text)<=16384),
 report_sha256 varchar(64) NOT NULL CHECK(report_sha256 ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 CHECK(model_id=artifact_id AND model_id<>base_model_id),
 UNIQUE(tenant_id,workspace_id,model_id),
 FOREIGN KEY(tenant_id,workspace_id,model_id,catalog_digest) REFERENCES model_versions(tenant_id,workspace_id,id,catalog_digest),
 FOREIGN KEY(tenant_id,workspace_id,base_model_id) REFERENCES model_versions(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,review_id) REFERENCES model_preparation_reviews(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,preparation_id,attempt_id,artifact_id) REFERENCES model_preparation_artifacts(tenant_id,workspace_id,preparation_id,attempt_id,id)
);
ALTER TABLE model_preparations ADD COLUMN review_id uuid;
ALTER TABLE model_preparations ADD COLUMN derived_model_id uuid;
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_review_scope FOREIGN KEY(tenant_id,workspace_id,review_id) REFERENCES model_preparation_reviews(tenant_id,workspace_id,id);
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_derived_scope FOREIGN KEY(tenant_id,workspace_id,derived_model_id) REFERENCES model_versions(tenant_id,workspace_id,id);
ALTER TABLE model_preparations ADD CONSTRAINT model_preparation_review_presence CHECK(
 (state='approved' AND review_id IS NOT NULL AND derived_model_id IS NOT NULL) OR
 (state='rejected' AND review_id IS NOT NULL AND derived_model_id IS NULL) OR
 (state NOT IN ('approved','rejected') AND review_id IS NULL AND derived_model_id IS NULL)
);
CREATE TRIGGER model_preparation_review_immutable BEFORE UPDATE OR DELETE ON model_preparation_reviews FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_output_record();
CREATE TRIGGER model_derivation_immutable BEFORE UPDATE OR DELETE ON model_derivations FOR EACH ROW EXECUTE FUNCTION protect_model_preparation_output_record();
CREATE FUNCTION protect_derived_model_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.kind='merged_lora' THEN
  IF TG_OP='DELETE' THEN RAISE EXCEPTION 'MODEL_DERIVED_IDENTITY_IMMUTABLE';END IF;
  IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.kind,NEW.name,NEW.model_key,NEW.catalog_digest,NEW.manifest,NEW.created_by,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.kind,OLD.name,OLD.model_key,OLD.catalog_digest,OLD.manifest,OLD.created_by,OLD.created_at)
  THEN RAISE EXCEPTION 'MODEL_DERIVED_IDENTITY_IMMUTABLE';END IF;
 ELSIF TG_OP='UPDATE' AND NEW.kind IS DISTINCT FROM OLD.kind THEN RAISE EXCEPTION 'MODEL_KIND_IMMUTABLE';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD;END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER model_derived_identity_immutable BEFORE UPDATE OR DELETE ON model_versions FOR EACH ROW EXECUTE FUNCTION protect_derived_model_identity();
CREATE FUNCTION require_derived_model_review_graph() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.kind='merged_lora' AND NOT EXISTS(
  SELECT 1 FROM model_derivations d
  JOIN model_preparation_reviews r ON r.tenant_id=d.tenant_id AND r.workspace_id=d.workspace_id AND r.id=d.review_id
  JOIN model_preparations p ON p.tenant_id=d.tenant_id AND p.workspace_id=d.workspace_id AND p.id=d.preparation_id
  WHERE d.model_id=NEW.id AND d.tenant_id=NEW.tenant_id AND d.workspace_id=NEW.workspace_id AND d.catalog_digest=NEW.catalog_digest AND d.catalog=NEW.manifest
   AND r.decision='approve' AND r.model_id=NEW.id AND r.reviewed_by=NEW.created_by AND r.artifact_id=d.artifact_id AND r.report_sha256=d.report_sha256
   AND p.state='approved' AND p.derived_model_id=NEW.id AND p.review_id=r.id AND p.current_attempt_id=d.attempt_id
 ) THEN RAISE EXCEPTION 'MODEL_DERIVED_REVIEW_GRAPH_REQUIRED';END IF;
 RETURN NEW;
END;$$;
CREATE CONSTRAINT TRIGGER model_derived_review_graph AFTER INSERT OR UPDATE ON model_versions DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION require_derived_model_review_graph();

-- Only explicitly selected derived tasks capture current administrator delegation.
ALTER TABLE node_tasks ADD COLUMN inference_requested_epoch bigint;
ALTER TABLE node_tasks ADD CONSTRAINT node_derived_delegation_presence CHECK(
 (operation='inference.derived.v1' AND inference_requested_epoch IS NOT NULL AND inference_requested_epoch>=1)
 OR (operation<>'inference.derived.v1' AND inference_requested_epoch IS NULL)
);
CREATE FUNCTION protect_derived_inference_task() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.operation='inference.derived.v1' OR NEW.operation='inference.derived.v1' THEN
  IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.operation,NEW.payload,NEW.requested_by,NEW.inference_requested_epoch,NEW.idempotency_key,NEW.request_fingerprint,NEW.created_at)
    IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.operation,OLD.payload,OLD.requested_by,OLD.inference_requested_epoch,OLD.idempotency_key,OLD.request_fingerprint,OLD.created_at)
  THEN RAISE EXCEPTION 'DERIVED_INFERENCE_COMMAND_IMMUTABLE';END IF;
 END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER node_derived_inference_immutable BEFORE UPDATE ON node_tasks FOR EACH ROW EXECUTE FUNCTION protect_derived_inference_task();

-- Deployment metadata owns an immutable exact model/recipe variant.
ALTER TABLE model_versions ADD CONSTRAINT model_version_kind_catalog_identity UNIQUE(tenant_id,workspace_id,id,kind,catalog_digest);
ALTER TABLE deployments ADD COLUMN model_kind varchar(20) NOT NULL DEFAULT 'approved_base';
ALTER TABLE deployments ADD COLUMN catalog_digest char(64) NOT NULL DEFAULT '4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc';
ALTER TABLE deployments ADD COLUMN serving_recipe_sha256 char(64);
ALTER TABLE deployments ADD CONSTRAINT deployment_model_variant CHECK(
 (model_kind='approved_base' AND catalog_digest='4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc' AND serving_recipe_sha256 IS NULL)
 OR (model_kind='merged_lora' AND catalog_digest~'^[a-f0-9]{64}$' AND catalog_digest<>'4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc' AND serving_recipe_sha256~'^[a-f0-9]{64}$' AND serving_recipe_sha256 IS NOT NULL)
);
ALTER TABLE deployments ADD CONSTRAINT deployment_model_catalog_scope FOREIGN KEY(tenant_id,workspace_id,model_id,model_kind,catalog_digest) REFERENCES model_versions(tenant_id,workspace_id,id,kind,catalog_digest);
CREATE FUNCTION protect_deployment_model_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.model_id,NEW.gpu_uuid,NEW.model_kind,NEW.catalog_digest,NEW.serving_recipe_sha256)
 IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.model_id,OLD.gpu_uuid,OLD.model_kind,OLD.catalog_digest,OLD.serving_recipe_sha256)
 THEN RAISE EXCEPTION 'DEPLOYMENT_MODEL_IDENTITY_IMMUTABLE';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER deployment_model_identity_immutable BEFORE UPDATE ON deployments FOR EACH ROW EXECUTE FUNCTION protect_deployment_model_identity();

-- Recoverable alias pause/cutover retains exact historical model identities and existing scoped keys.
ALTER TABLE model_alias_history DROP CONSTRAINT model_alias_history_action_check;
ALTER TABLE model_alias_history ADD CONSTRAINT model_alias_history_action_check CHECK(action IN ('published','disabled','paused','resumed','retargeted','rolled_back'));
CREATE TABLE model_alias_target_versions (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 alias_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 source_revision bigint,
 action varchar(20) NOT NULL CHECK(action IN ('migration_current','published','disabled','paused','resumed','retargeted','rolled_back')),
 deployment_id uuid NOT NULL,
 deployment_revision bigint NOT NULL CHECK(deployment_revision BETWEEN 1 AND 9007199254740991),
 model_id uuid NOT NULL,
 model_kind varchar(20) NOT NULL CHECK(model_kind IN ('approved_base','merged_lora')),
 catalog_digest char(64) NOT NULL CHECK(catalog_digest~'^[a-f0-9]{64}$'),
 serving_recipe_sha256 char(64),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,alias_id,revision),
 FOREIGN KEY(tenant_id,workspace_id,alias_id,revision) REFERENCES model_alias_history(tenant_id,workspace_id,alias_id,revision),
 FOREIGN KEY(tenant_id,workspace_id,alias_id,source_revision) REFERENCES model_alias_target_versions(tenant_id,workspace_id,alias_id,revision),
 CHECK((action='rolled_back' AND source_revision IS NOT NULL AND source_revision<revision AND source_revision>=1) OR (action<>'rolled_back' AND source_revision IS NULL)),
 FOREIGN KEY(tenant_id,workspace_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_id,model_kind,catalog_digest) REFERENCES model_versions(tenant_id,workspace_id,id,kind,catalog_digest),
 CHECK((model_kind='approved_base' AND serving_recipe_sha256 IS NULL) OR (model_kind='merged_lora' AND serving_recipe_sha256 IS NOT NULL AND serving_recipe_sha256~'^[a-f0-9]{64}$'))
);
INSERT INTO model_alias_target_versions(id,tenant_id,workspace_id,alias_id,revision,action,deployment_id,deployment_revision,model_id,model_kind,catalog_digest,serving_recipe_sha256)
 SELECT gen_random_uuid(),a.tenant_id,a.workspace_id,a.id,a.revision,'migration_current',d.id,a.deployment_revision,d.model_id,d.model_kind,d.catalog_digest,d.serving_recipe_sha256
 FROM model_aliases a JOIN deployments d ON d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.id=a.deployment_id;
CREATE TABLE model_alias_cutover_intents (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 actor_user_id uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 alias_id uuid NOT NULL,
 result_revision bigint NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(tenant_id,workspace_id,actor_user_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id,alias_id,result_revision) REFERENCES model_alias_target_versions(tenant_id,workspace_id,alias_id,revision)
);
CREATE INDEX model_alias_target_page_idx ON model_alias_target_versions(tenant_id,workspace_id,alias_id,created_at DESC,id DESC);
CREATE FUNCTION protect_alias_cutover_record() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'ALIAS_TARGET_HISTORY_IMMUTABLE';END;$$;
CREATE TRIGGER model_alias_target_immutable BEFORE UPDATE OR DELETE ON model_alias_target_versions FOR EACH ROW EXECUTE FUNCTION protect_alias_cutover_record();
CREATE TRIGGER model_alias_intent_immutable BEFORE UPDATE OR DELETE ON model_alias_cutover_intents FOR EACH ROW EXECUTE FUNCTION protect_alias_cutover_record();
CREATE TRIGGER model_alias_history_immutable BEFORE UPDATE OR DELETE ON model_alias_history FOR EACH ROW EXECUTE FUNCTION protect_alias_cutover_record();
CREATE FUNCTION protect_alias_identity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.tenant_id,NEW.workspace_id,NEW.node_id,NEW.gateway_id,NEW.name,NEW.created_by,NEW.idempotency_key,NEW.fingerprint,NEW.created_at)
 IS DISTINCT FROM (OLD.id,OLD.tenant_id,OLD.workspace_id,OLD.node_id,OLD.gateway_id,OLD.name,OLD.created_by,OLD.idempotency_key,OLD.fingerprint,OLD.created_at)
 THEN RAISE EXCEPTION 'MODEL_ALIAS_IDENTITY_IMMUTABLE';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER model_alias_identity_immutable BEFORE UPDATE ON model_aliases FOR EACH ROW EXECUTE FUNCTION protect_alias_identity();

CREATE FUNCTION require_alias_target_snapshot() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM model_aliases a JOIN deployments d ON d.tenant_id=a.tenant_id AND d.workspace_id=a.workspace_id AND d.node_id=a.node_id AND d.id=a.deployment_id
 WHERE a.tenant_id=NEW.tenant_id AND a.workspace_id=NEW.workspace_id AND a.id=NEW.alias_id AND a.revision=NEW.revision AND a.deployment_revision=NEW.deployment_revision
 AND d.id=NEW.deployment_id AND d.model_id=NEW.model_id AND d.model_kind=NEW.model_kind AND d.catalog_digest=NEW.catalog_digest AND d.serving_recipe_sha256 IS NOT DISTINCT FROM NEW.serving_recipe_sha256)
 THEN RAISE EXCEPTION 'ALIAS_CURRENT_TARGET_SNAPSHOT_REQUIRED';END IF;
 RETURN NEW;
END;$$;
CREATE TRIGGER model_alias_current_target_snapshot BEFORE INSERT ON model_alias_target_versions FOR EACH ROW EXECUTE FUNCTION require_alias_target_snapshot();
