CREATE TABLE component_log_snapshots (
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    node_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    component VARCHAR(32) NOT NULL CHECK(component='node_exporter'),
    task_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    snapshot JSON NOT NULL CHECK(JSON_TYPE(snapshot)='OBJECT' AND OCTET_LENGTH(CAST(snapshot AS CHAR CHARACTER SET utf8mb4))<=16384),
    received_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY(node_id,component),
    KEY component_logs_scope_idx(tenant_id,workspace_id,node_id),
    CONSTRAINT component_logs_node_scope_fk FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
    CONSTRAINT component_logs_task_scope_fk FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id),
    CONSTRAINT component_log_snapshots_tenant_id_canonical CHECK(tenant_id IS NULL OR REGEXP_LIKE(tenant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT component_log_snapshots_workspace_id_canonical CHECK(workspace_id IS NULL OR REGEXP_LIKE(workspace_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT component_log_snapshots_node_id_canonical CHECK(node_id IS NULL OR REGEXP_LIKE(node_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT component_log_snapshots_task_id_canonical CHECK(task_id IS NULL OR REGEXP_LIKE(task_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
