-- M04 cumulative evolution. Entries 1-8 are immutable installed M03 history.
-- Reuse the already-present repository columns; admit only the M04 authority.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1'));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_payload_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_payload_check CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=4096);
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_request_fingerprint_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_request_fingerprint_check CHECK(request_fingerprint IS NULL OR octet_length(request_fingerprint)=32);
-- Compatibility fields satisfy the shared current repository, without M05 authority.
CREATE TABLE managed_components (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 component varchar(32) NOT NULL CHECK(component='node_exporter'),
 version varchar(32) NOT NULL CHECK(version='1.12.1'),
 desired_state varchar(16) NOT NULL CHECK(desired_state IN ('running','stopped','not_installed')),
 observed_state varchar(16) NOT NULL CHECK(observed_state IN ('running','stopped','not_installed','unknown')),
 managed boolean NOT NULL,
 observation jsonb NOT NULL CHECK(jsonb_typeof(observation)='object' AND octet_length(observation::text)<=8192),
 observed_at timestamptz NOT NULL,
 last_task_id uuid,
 updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 desired_version varchar(32) NOT NULL DEFAULT '1.12.1' CHECK(desired_version='1.12.1'),
 desired_config jsonb DEFAULT '{"log_level":"info","max_scrape_requests":40}'::jsonb CHECK(desired_config IS NULL OR desired_config='{"log_level":"info","max_scrape_requests":40}'::jsonb),
 applied_config jsonb CHECK(applied_config IS NULL OR applied_config='{"log_level":"info","max_scrape_requests":40}'::jsonb),
 previous_version varchar(32) CHECK(previous_version IS NULL),
 last_verified_at timestamptz,
 PRIMARY KEY(node_id,component),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,last_task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id)
);
CREATE INDEX components_scope_idx ON managed_components(tenant_id,workspace_id,node_id);
