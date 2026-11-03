CREATE TABLE installation_state (
    id SMALLINT PRIMARY KEY CHECK(id=1),
    bootstrap_complete BOOLEAN NOT NULL DEFAULT FALSE CHECK(bootstrap_complete IN(0,1)),
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    default_workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    primary_admin_user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    bootstrapped_at DATETIME(6),
    FOREIGN KEY(tenant_id) REFERENCES tenants(id),
    FOREIGN KEY(primary_admin_user_id) REFERENCES users(id),
    FOREIGN KEY(tenant_id,default_workspace_id) REFERENCES workspaces(tenant_id,id),
    CHECK((bootstrap_complete=1 AND tenant_id IS NOT NULL AND default_workspace_id IS NOT NULL AND primary_admin_user_id IS NOT NULL AND bootstrapped_at IS NOT NULL)
       OR (bootstrap_complete=0 AND tenant_id IS NULL AND default_workspace_id IS NULL AND primary_admin_user_id IS NULL AND bootstrapped_at IS NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
