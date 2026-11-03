CREATE TABLE aws_discovered_resources (
 id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 account_id VARCHAR(12) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(account_id,'^[0-9]{12}$','c')),
 region VARCHAR(64) NOT NULL, instance_id VARCHAR(32) NOT NULL,
 observation JSON NOT NULL CHECK(JSON_TYPE(observation)='OBJECT'), observed_at DATETIME(6) NOT NULL, missing BOOLEAN NOT NULL DEFAULT 0 CHECK(missing IN(0,1)),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id), UNIQUE(tenant_id,workspace_id,account_id,region,instance_id),
 KEY aws_resource_scope(tenant_id,workspace_id,id),
 CONSTRAINT awsresource_id CHECK(REGEXP_LIKE(id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT awsresource_tenant_id CHECK(REGEXP_LIKE(tenant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT awsresource_workspace_id CHECK(REGEXP_LIKE(workspace_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;