CREATE TABLE workspaces (
    id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    name VARCHAR(100) NOT NULL,
    status VARCHAR(16) NOT NULL CHECK(status IN('active','disabled')),
    version BIGINT NOT NULL DEFAULT 1 CHECK(version BETWEEN 1 AND 9007199254740991),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY workspaces_tenant_id(tenant_id,id),
    UNIQUE KEY workspaces_tenant_name(tenant_id,name),
    FOREIGN KEY(tenant_id) REFERENCES tenants(id),
    CONSTRAINT workspaces_id_canonical CHECK(REGEXP_LIKE(id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
