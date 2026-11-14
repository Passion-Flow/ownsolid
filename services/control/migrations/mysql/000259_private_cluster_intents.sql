CREATE TABLE private_cluster_intents (
 tenant_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, workspace_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, actor_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 idempotency_key char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, operation varchar(24) NOT NULL CHECK(operation IN('bind','rebind','disable','observe','start','continue','reconcile','cancel','cleanup')),
 fingerprint char(64) NOT NULL CHECK(REGEXP_LIKE(fingerprint,'^[a-f0-9]{64}$','c')), target_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 result_revision bigint NOT NULL CHECK(result_revision BETWEEN 1 AND 9007199254740991),
 created_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 FOREIGN KEY(actor_id) REFERENCES users(id),
 PRIMARY KEY(tenant_id,workspace_id,actor_id,idempotency_key),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id)
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;