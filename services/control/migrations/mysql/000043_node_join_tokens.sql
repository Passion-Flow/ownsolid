CREATE TABLE node_join_tokens (
    id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    node_name VARCHAR(100) NOT NULL,
    token_hash VARBINARY(32) NOT NULL UNIQUE CHECK(OCTET_LENGTH(token_hash)=32),
    created_by CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    expires_at DATETIME(6) NOT NULL,
    consumed_at DATETIME(6),
    revoked_at DATETIME(6),
    execution_profile VARCHAR(32) NOT NULL DEFAULT 'read_only' CHECK(execution_profile IN('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle')),
    node_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    KEY node_join_scope_idx(tenant_id,created_at DESC,id DESC),
    FOREIGN KEY(created_by) REFERENCES users(id),
    FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
    FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id),
    CONSTRAINT node_join_id_canonical CHECK(REGEXP_LIKE(id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
