CREATE TABLE image_registry_grants (
 id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
 tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 pin_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 node_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 deployment_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 task_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 revision BIGINT NOT NULL CHECK(revision>=1), created_by CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 actor_epoch BIGINT NOT NULL CHECK(actor_epoch>=1),
 -- Historical exact session facts do not reference sessions: ordinary retention
 -- may remove an expired session; every pull then fails its live session check.
 created_session_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 created_token_hash VARBINARY(32) NOT NULL CHECK(OCTET_LENGTH(created_token_hash)=32),
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), expires_at DATETIME(6) NOT NULL, revoked_at DATETIME(6),
 active_task_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin GENERATED ALWAYS AS(CASE WHEN revoked_at IS NULL THEN task_id ELSE NULL END) STORED,
 UNIQUE(tenant_id,workspace_id,node_id,active_task_id),
 FOREIGN KEY(tenant_id,workspace_id,pin_id,node_id,deployment_id) REFERENCES image_registry_deployment_refs(tenant_id,workspace_id,pin_id,node_id,deployment_id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,task_id) REFERENCES node_tasks(tenant_id,workspace_id,node_id,id), FOREIGN KEY(created_by) REFERENCES users(id),
 CHECK(expires_at>created_at),
 CHECK(REGEXP_LIKE(created_session_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CHECK(REGEXP_LIKE(id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
