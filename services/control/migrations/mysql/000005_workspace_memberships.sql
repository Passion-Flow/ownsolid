CREATE TABLE workspace_memberships (
    user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    role VARCHAR(16) NOT NULL CHECK(role IN('owner','member','viewer')),
    version BIGINT NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY(user_id,workspace_id),
    KEY memberships_scope_idx(tenant_id,workspace_id,user_id),
    FOREIGN KEY(user_id) REFERENCES users(id),
    FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
