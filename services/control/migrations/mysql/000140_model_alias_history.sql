CREATE TABLE model_alias_history (
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    alias_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    revision BIGINT NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
    action VARCHAR(16) NOT NULL CHECK(action IN('published','disabled','paused','resumed','retargeted','rolled_back')),
    actor_user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    reason VARCHAR(500) NOT NULL CHECK(CHAR_LENGTH(TRIM(reason))>0),
    occurred_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY(tenant_id,workspace_id,alias_id,revision),
    FOREIGN KEY(tenant_id,workspace_id,alias_id) REFERENCES model_aliases(tenant_id,workspace_id,id),
    FOREIGN KEY(actor_user_id) REFERENCES users(id),
    CONSTRAINT model_alias_history_tenant_id_canonical CHECK(tenant_id IS NULL OR REGEXP_LIKE(tenant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_alias_history_workspace_id_canonical CHECK(workspace_id IS NULL OR REGEXP_LIKE(workspace_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_alias_history_alias_id_canonical CHECK(alias_id IS NULL OR REGEXP_LIKE(alias_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_alias_history_actor_user_id_canonical CHECK(actor_user_id IS NULL OR REGEXP_LIKE(actor_user_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
