-- M04 explicit management enrollment, typed component tasks and bounded current observations.
ALTER TABLE node_join_tokens ADD COLUMN execution_profile varchar(32) NOT NULL DEFAULT 'read_only' CHECK (execution_profile IN ('read_only','systemd_components'));
ALTER TABLE managed_nodes ADD COLUMN execution_profile varchar(32) NOT NULL DEFAULT 'read_only' CHECK (execution_profile IN ('read_only','systemd_components'));
ALTER TABLE managed_nodes ADD COLUMN capabilities jsonb NOT NULL DEFAULT '["collect_facts.v1"]'::jsonb CHECK (jsonb_typeof(capabilities)='array' AND jsonb_array_length(capabilities)<=6);
ALTER TABLE node_tasks DROP CONSTRAINT node_tasks_operation_check;
ALTER TABLE node_tasks ADD CONSTRAINT node_tasks_operation_check CHECK (operation IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1'));
ALTER TABLE node_tasks ADD COLUMN payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload)='object' AND octet_length(payload::text)<=4096);
CREATE TABLE managed_components (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    node_id uuid NOT NULL,
    component varchar(32) NOT NULL CHECK (component='node_exporter'),
    version varchar(32) NOT NULL,
    desired_state varchar(16) NOT NULL CHECK (desired_state IN ('running','stopped','not_installed')),
    observed_state varchar(16) NOT NULL CHECK (observed_state IN ('running','stopped','not_installed','unknown')),
    managed boolean NOT NULL,
    observation jsonb NOT NULL CHECK (jsonb_typeof(observation)='object' AND octet_length(observation::text)<=8192),
    observed_at timestamptz NOT NULL,
    last_task_id uuid REFERENCES node_tasks(id),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (node_id,component),
    FOREIGN KEY (tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id)
);
CREATE INDEX components_scope_idx ON managed_components(tenant_id,workspace_id,node_id);
