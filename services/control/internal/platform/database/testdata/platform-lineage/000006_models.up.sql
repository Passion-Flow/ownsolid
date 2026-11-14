-- M06 immutable scoped model imports; deployment/resource additions follow in this migration before freeze.
CREATE TABLE model_versions (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 name varchar(100) NOT NULL,
 model_key varchar(64) NOT NULL CHECK(model_key='qwen3-0.6b'),
 catalog_digest varchar(64) NOT NULL CHECK(catalog_digest ~ '^[a-f0-9]{64}$'),
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=16384),
 state varchar(16) NOT NULL CHECK(state IN ('ready','deleted')),
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 deleted_at timestamptz,
 UNIQUE(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
);
CREATE UNIQUE INDEX model_ready_catalog_idx ON model_versions(tenant_id,workspace_id,catalog_digest) WHERE state='ready';
CREATE TABLE model_imports (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 name varchar(100) NOT NULL,
 catalog_digest varchar(64) NOT NULL CHECK(catalog_digest ~ '^[a-f0-9]{64}$'),
 expected_bytes bigint NOT NULL CHECK(expected_bytes BETWEEN 1 AND 2147483648),
 uploaded_bytes bigint NOT NULL DEFAULT 0 CHECK(uploaded_bytes>=0 AND uploaded_bytes<=expected_bytes),
 state varchar(16) NOT NULL CHECK(state IN ('uploading','queued','verifying','ready','failed','cancelled')),
 error_code varchar(64),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 3),
 requested_by uuid NOT NULL REFERENCES users(id),
 requested_epoch bigint NOT NULL CHECK(requested_epoch>=0),
 idempotency_key uuid NOT NULL,
 fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
 reason varchar(500) NOT NULL,
 model_id uuid,
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_versions(tenant_id,workspace_id,id)
);
CREATE UNIQUE INDEX model_import_active_workspace_idx ON model_imports(tenant_id,workspace_id) WHERE state NOT IN ('ready','cancelled');
CREATE TABLE model_import_chunks (
 import_id uuid NOT NULL REFERENCES model_imports(id),
 byte_offset bigint NOT NULL CHECK(byte_offset>=0 AND byte_offset%8388608=0),
 size_bytes integer NOT NULL CHECK(size_bytes BETWEEN 1 AND 8388608),
 sha256 varchar(64) NOT NULL CHECK(sha256 ~ '^[a-f0-9]{64}$'),
 PRIMARY KEY(import_id,byte_offset)
);
CREATE INDEX model_import_queue_idx ON model_imports(state,created_at) WHERE state IN ('queued','verifying');
-- Inference authority is explicit; no existing enrollment gains capabilities.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference'));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1','inference.deploy.v1','inference.stop.v1','inference.remove.v1','inference.observe.v1','inference.verify.v1'));
CREATE TABLE inference_nodes (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid PRIMARY KEY,
 observation jsonb NOT NULL CHECK(jsonb_typeof(observation)='object' AND octet_length(observation::text)<=32768),
 received_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id)
);
CREATE TABLE deployments (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 model_id uuid NOT NULL,
 gpu_uuid varchar(64) NOT NULL CHECK(gpu_uuid ~ '^GPU-[a-f0-9-]{36}$'),
 desired_state varchar(16) NOT NULL CHECK(desired_state IN ('running','stopped','removed')),
 observed_state varchar(16) NOT NULL DEFAULT 'unknown' CHECK(observed_state IN ('running','loading','stopped','not_installed','unknown','failed')),
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 observation jsonb CHECK(observation IS NULL OR jsonb_typeof(observation)='object' AND octet_length(observation::text)<=8192),
 observed_at timestamptz,
 last_task_id uuid,
 removed_at timestamptz,
 created_by uuid NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE(tenant_id,workspace_id,id),
 UNIQUE(tenant_id,workspace_id,node_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,model_id) REFERENCES model_versions(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,last_task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id)
);
CREATE UNIQUE INDEX deployment_one_retained_node_idx ON deployments(node_id) WHERE removed_at IS NULL;
CREATE INDEX deployment_scope_idx ON deployments(tenant_id,workspace_id,created_at DESC,id DESC);
CREATE TABLE gpu_leases (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 gpu_uuid varchar(64) NOT NULL,
 deployment_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 state varchar(24) NOT NULL CHECK(state IN ('held','released','recovery_required')),
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(deployment_id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,node_id,id)
);
CREATE UNIQUE INDEX gpu_exclusive_node_idx ON gpu_leases(node_id) WHERE state IN ('held','recovery_required');
ALTER TABLE managed_nodes ADD COLUMN inference_fenced_at timestamptz;
ALTER TABLE deployments ADD COLUMN recovery_receipt_sha256 varchar(64) CHECK(recovery_receipt_sha256 IS NULL OR recovery_receipt_sha256 ~ '^[a-f0-9]{64}$');
-- A new node identity cannot bypass an old uncertain reservation of the same physical GPU.
CREATE UNIQUE INDEX gpu_exclusive_uuid_idx ON gpu_leases(gpu_uuid) WHERE state IN ('held','recovery_required');
