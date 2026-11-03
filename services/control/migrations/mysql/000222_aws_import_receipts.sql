CREATE TABLE aws_import_receipts (
 tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, idempotency_key CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, actor_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 connection_name VARCHAR(48) NOT NULL, account_id VARCHAR(12) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(account_id,'^[0-9]{12}$','c')),
 region VARCHAR(64) NOT NULL, started_at DATETIME(6) NOT NULL, completed_at DATETIME(6) NOT NULL,
 imported_count INT NOT NULL CHECK(imported_count>=0), PRIMARY KEY(tenant_id,workspace_id,idempotency_key),
 FOREIGN KEY(actor_id) REFERENCES users(id), FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CHECK(completed_at>=started_at),
 KEY aws_receipt_snapshot(tenant_id,workspace_id,account_id,region,started_at),
 CONSTRAINT awsreceipt_tenant_id CHECK(REGEXP_LIKE(tenant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT awsreceipt_workspace_id CHECK(REGEXP_LIKE(workspace_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT awsreceipt_idempotency_key CHECK(REGEXP_LIKE(idempotency_key,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT awsreceipt_actor_id CHECK(REGEXP_LIKE(actor_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;