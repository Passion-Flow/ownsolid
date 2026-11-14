-- M05 cumulative evolution. Entries 1-9 remain immutable installed M04 history.
-- Reuse the already-present repository columns; admit only the M05 authority.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(
 (execution_profile='read_only' AND capabilities='["collect_facts.v1"]'::jsonb) OR
 (execution_profile='systemd_components' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1"]'::jsonb) OR
 (execution_profile='systemd_maintenance' AND capabilities='["collect_facts.v1","node_exporter.install.v1","node_exporter.start.v1","node_exporter.stop.v1","node_exporter.remove.v1","node_exporter.observe.v1","node_exporter.configure.v1","node_exporter.restart.v1","node_exporter.upgrade.v1","node_exporter.rollback.v1","node_exporter.logs.v1"]'::jsonb));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1'));
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_payload_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_payload_check CHECK(jsonb_typeof(payload)='object' AND octet_length(payload::text)<=4096);
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_request_fingerprint_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_request_fingerprint_check CHECK(request_fingerprint IS NULL OR octet_length(request_fingerprint)=32);
ALTER TABLE managed_components DROP CONSTRAINT managed_components_version_check;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_version_check CHECK(version IN ('1.12.0','1.12.1'));
ALTER TABLE managed_components DROP CONSTRAINT managed_components_desired_version_check;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_desired_version_check CHECK(desired_version IN ('1.12.0','1.12.1'));
ALTER TABLE managed_components DROP CONSTRAINT managed_components_previous_version_check;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_previous_version_check CHECK(previous_version IS NULL OR previous_version IN ('1.12.0','1.12.1'));
ALTER TABLE managed_components DROP CONSTRAINT managed_components_desired_config_check;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_desired_config_check CHECK(desired_config IS NULL OR (jsonb_typeof(desired_config)='object' AND desired_config ? 'log_level' AND desired_config ? 'max_scrape_requests' AND desired_config - 'log_level' - 'max_scrape_requests'='{}'::jsonb AND desired_config->>'log_level' IN ('debug','info','warn','error') AND jsonb_typeof(desired_config->'max_scrape_requests')='number' AND desired_config->>'max_scrape_requests' ~ '^([1-9]|[1-3][0-9]|40)$'));
ALTER TABLE managed_components DROP CONSTRAINT managed_components_applied_config_check;
ALTER TABLE managed_components ADD CONSTRAINT managed_components_applied_config_check CHECK(applied_config IS NULL OR (jsonb_typeof(applied_config)='object' AND applied_config ? 'log_level' AND applied_config ? 'max_scrape_requests' AND applied_config - 'log_level' - 'max_scrape_requests'='{}'::jsonb AND applied_config->>'log_level' IN ('debug','info','warn','error') AND jsonb_typeof(applied_config->'max_scrape_requests')='number' AND applied_config->>'max_scrape_requests' ~ '^([1-9]|[1-3][0-9]|40)$'));
CREATE TABLE component_log_snapshots (
 tenant_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 node_id uuid NOT NULL,
 component varchar(32) NOT NULL CHECK(component='node_exporter'),
 task_id uuid NOT NULL,
 snapshot jsonb NOT NULL CHECK(jsonb_typeof(snapshot)='object' AND octet_length(snapshot::text)<=16384),
 received_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(node_id,component),
 FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id)
);
CREATE INDEX component_logs_scope_idx ON component_log_snapshots(tenant_id,workspace_id,node_id);
