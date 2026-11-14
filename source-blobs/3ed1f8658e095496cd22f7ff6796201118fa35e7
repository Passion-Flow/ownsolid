CREATE TABLE model_holdout_prompts (
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    holdout_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    prompt_sha256 VARBINARY(32) NOT NULL CHECK(OCTET_LENGTH(prompt_sha256)=32),
    PRIMARY KEY(tenant_id,workspace_id,holdout_id,prompt_sha256),
    INDEX model_holdout_prompt_reservation_idx(tenant_id,workspace_id,prompt_sha256),
    FOREIGN KEY(tenant_id,workspace_id,holdout_id) REFERENCES model_holdouts(tenant_id,workspace_id,id),
    CONSTRAINT model_holdout_prompts_tenant_id_canonical CHECK(REGEXP_LIKE(tenant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_holdout_prompts_workspace_id_canonical CHECK(REGEXP_LIKE(workspace_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_holdout_prompts_holdout_id_canonical CHECK(REGEXP_LIKE(holdout_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
