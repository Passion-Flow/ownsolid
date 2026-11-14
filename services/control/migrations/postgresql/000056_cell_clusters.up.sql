-- One Kubernetes cluster per cell, installed by ownsolid on nodes of the kubernetes_node
-- profile (ARC-104, ADR 0051). Control writes the tasks; each node's agent installs RKE2.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle','kubernetes_node'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle','kubernetes_node'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb) OR
 (execution_profile='systemd_maintenance' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]'::jsonb) OR
 (execution_profile='docker_inference' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1"]'::jsonb) OR
 (execution_profile='docker_ai' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1"]'::jsonb) OR
 (execution_profile='docker_model_lifecycle' AND capabilities='["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1","training.run.v1","model.prepare.v1","inference.derived.v1"]'::jsonb) OR
 (execution_profile='kubernetes_node' AND capabilities='["collect_facts.v1","kubernetes.join.v1","kubernetes.token.v1","kubernetes.observe.v1"]'::jsonb));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1','inference.deploy.v1','inference.stop.v1','inference.remove.v1','inference.observe.v1','inference.verify.v1','training.run.v1','model.prepare.v1','inference.derived.v1','kubernetes.join.v1','kubernetes.token.v1','kubernetes.observe.v1'));

-- The address a node's agent enrolled from. A Kubernetes node advertises it in its cluster,
-- and the agent refuses to join when no interface of the machine holds it.
ALTER TABLE managed_nodes ADD COLUMN enrolled_address inet;

CREATE TABLE cell_clusters (
 id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id() REFERENCES cells(id),
 name varchar(100) NOT NULL CHECK (length(btrim(name))>0),
 distribution_version varchar(32) NOT NULL,
 -- The SHA-256 of the server CA, which join tokens pin. Known once the server is ready.
 ca_sha256 char(64) CHECK (ca_sha256 ~ '^[0-9a-f]{64}$'),
 -- The bootstrap token workers join with, sealed with a key derived from the control
 -- secret and bound to this cluster. It expires on the server by itself.
 join_token_sealed bytea,
 join_token_expires_at timestamptz,
 -- The server's token task while it runs, so only one is asked for at a time.
 token_task_id uuid,
 created_by uuid NOT NULL REFERENCES users(id),
 idempotency_key uuid NOT NULL,
 request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint)=32),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (tenant_id,id),
 UNIQUE (tenant_id,idempotency_key),
 CHECK ((join_token_sealed IS NULL)=(join_token_expires_at IS NULL)),
 CHECK (join_token_sealed IS NULL OR ca_sha256 IS NOT NULL)
);
-- A cell has one cluster.
CREATE UNIQUE INDEX cell_clusters_cell_idx ON cell_clusters(cell_id);
CREATE INDEX cell_clusters_list_idx ON cell_clusters(tenant_id,created_at DESC,id DESC);

-- A node of a cluster. A node belongs to at most one cluster, and a cluster has one server
-- until ARC-115. The address is fixed when the member is added: a server whose address
-- changes after its first start loses its etcd membership.
CREATE TABLE cell_cluster_members (
 cluster_id uuid NOT NULL,
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 site_id varchar(32) NOT NULL DEFAULT local_site_id(),
 cell_id varchar(32) NOT NULL DEFAULT local_cell_id(),
 role varchar(16) NOT NULL CHECK (role IN ('server','worker')),
 node_address inet NOT NULL,
 state varchar(16) NOT NULL CHECK (state IN ('pending','joining','ready','not_ready','failed')),
 error_code varchar(64),
 -- The node task working for this member, while it runs. Finished tasks move to the
 -- history, so this keeps no foreign key.
 task_id uuid,
 -- Who asked for the member's latest join: the creator, or whoever retried it.
 requested_by uuid NOT NULL REFERENCES users(id),
 join_attempts integer NOT NULL DEFAULT 0 CHECK (join_attempts>=0),
 observed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (cluster_id,node_id),
 FOREIGN KEY (tenant_id,cluster_id) REFERENCES cell_clusters(tenant_id,id) ON DELETE CASCADE,
 FOREIGN KEY (tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 CHECK ((state='failed')=(error_code IS NOT NULL)),
 CHECK (state<>'joining' OR task_id IS NOT NULL)
);
CREATE UNIQUE INDEX cell_cluster_members_node_idx ON cell_cluster_members(node_id);
CREATE UNIQUE INDEX cell_cluster_members_server_idx ON cell_cluster_members(cluster_id) WHERE role='server';
CREATE UNIQUE INDEX cell_cluster_members_task_idx ON cell_cluster_members(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX cell_cluster_members_workspace_fk ON cell_cluster_members(tenant_id,workspace_id,node_id);
-- The reconciler visits only clusters that still have work.
CREATE INDEX cell_cluster_members_open_idx ON cell_cluster_members(cluster_id) WHERE state IN ('pending','joining');
