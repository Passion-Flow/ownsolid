CREATE TABLE gpu_leases (
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    node_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    gpu_uuid VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    deployment_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    revision BIGINT NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
    state VARCHAR(24) NOT NULL CHECK(state IN('held','released','recovery_required')),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    reserved_node_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin GENERATED ALWAYS AS(CASE WHEN state IN('held','recovery_required') THEN node_id ELSE NULL END) STORED,
    reserved_gpu_uuid VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin GENERATED ALWAYS AS(CASE WHEN state IN('held','recovery_required') THEN gpu_uuid ELSE NULL END) STORED,
    UNIQUE KEY gpu_exclusive_node_idx(reserved_node_id),
    UNIQUE KEY gpu_exclusive_uuid_idx(reserved_gpu_uuid),
    FOREIGN KEY(tenant_id,workspace_id,node_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,node_id,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
