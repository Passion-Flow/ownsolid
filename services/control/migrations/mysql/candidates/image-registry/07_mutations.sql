CREATE TABLE image_registry_mutations (
 tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 actor_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 operation VARCHAR(32) NOT NULL CHECK(operation IN('import','reconcile','remove','revoke','offline.hold','offline.release','reference.release','abandon','discard','grant')),
 idempotency_key CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, fingerprint VARBINARY(32) NOT NULL CHECK(OCTET_LENGTH(fingerprint)=32),
 target_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 PRIMARY KEY(tenant_id,workspace_id,actor_id,operation,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id), FOREIGN KEY(actor_id) REFERENCES users(id),
 CHECK(REGEXP_LIKE(idempotency_key,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CHECK(REGEXP_LIKE(target_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
