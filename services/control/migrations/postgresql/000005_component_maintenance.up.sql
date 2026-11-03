-- M05 explicit maintenance authority, immutable request binding and bounded latest logs.
ALTER TABLE node_join_tokens DROP CONSTRAINT node_join_tokens_execution_profile_check;
ALTER TABLE node_join_tokens ADD CONSTRAINT node_join_tokens_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_execution_profile_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_execution_profile_check CHECK(execution_profile IN ('read_only','systemd_components','systemd_maintenance'));
ALTER TABLE managed_nodes DROP CONSTRAINT managed_nodes_capabilities_check;
ALTER TABLE managed_nodes ADD CONSTRAINT managed_nodes_capabilities_check CHECK(jsonb_typeof(capabilities)='array' AND jsonb_array_length(capabilities)<=11);
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK(operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1'));
ALTER TABLE node_tasks ADD COLUMN request_fingerprint bytea CHECK(request_fingerprint IS NULL OR octet_length(request_fingerprint)=32);
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_scope_identity_key UNIQUE(tenant_id,workspace_id,node_id,id);
ALTER TABLE managed_components ADD CONSTRAINT components_task_scope_fk FOREIGN KEY(tenant_id,workspace_id,node_id,last_task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id);
ALTER TABLE managed_components ADD COLUMN desired_version varchar(32) NOT NULL DEFAULT '1.12.1' CHECK(desired_version IN ('1.12.0','1.12.1'));
ALTER TABLE managed_components ADD COLUMN desired_config jsonb DEFAULT '{"log_level":"info","max_scrape_requests":40}'::jsonb CHECK(desired_config IS NULL OR jsonb_typeof(desired_config)='object' AND octet_length(desired_config::text)<=512);
ALTER TABLE managed_components ADD COLUMN applied_config jsonb CHECK(applied_config IS NULL OR jsonb_typeof(applied_config)='object' AND octet_length(applied_config::text)<=512);
ALTER TABLE managed_components ADD COLUMN last_verified_at timestamptz;
ALTER TABLE managed_components ADD COLUMN previous_version varchar(32) CHECK(previous_version IS NULL OR previous_version IN ('1.12.0','1.12.1'));
UPDATE managed_components SET applied_config='{"log_level":"info","max_scrape_requests":40}'::jsonb,last_verified_at=observed_at WHERE managed AND observed_state IN ('running','stopped');
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
