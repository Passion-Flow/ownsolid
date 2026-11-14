CREATE TABLE model_holdouts (
    id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    dataset_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    dataset_version_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    name VARCHAR(100) NOT NULL CHECK(CHAR_LENGTH(TRIM(name))>0),
    manifest_sha256 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(manifest_sha256,'^[a-f0-9]{64}$','c')),
    effective_source_sha256 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(effective_source_sha256,'^[a-f0-9]{64}$','c')),
    row_mapping_sha256 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(row_mapping_sha256,'^[a-f0-9]{64}$','c')),
    source_bytes bigint NOT NULL CHECK(source_bytes BETWEEN 1 AND 67108864),
    row_count bigint NOT NULL CHECK(row_count BETWEEN 3 AND 256),
    label_counts JSON NOT NULL CHECK(JSON_TYPE(label_counts)='OBJECT' AND OCTET_LENGTH(CAST(label_counts AS CHAR CHARACTER SET utf8mb4))<=1024),
    recipe_id VARCHAR(80) NOT NULL CHECK(CHAR_LENGTH(TRIM(recipe_id))>0),
    recipe_sha256 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(recipe_sha256,'^[a-f0-9]{64}$','c')),
    instruction_sha256 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(instruction_sha256,'^[a-f0-9]{64}$','c')),
    fingerprint_algorithm VARCHAR(100) NOT NULL CHECK(CHAR_LENGTH(TRIM(fingerprint_algorithm))>0),
    created_by CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    audience VARCHAR(8) NOT NULL CHECK(audience IN ('portal','admin')),
    reason VARCHAR(500) NOT NULL CHECK(CHAR_LENGTH(TRIM(reason))>0),
    creation_key CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    creation_fingerprint VARBINARY(32) NOT NULL CHECK(OCTET_LENGTH(creation_fingerprint)=32),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE(tenant_id,workspace_id,id),
    CONSTRAINT model_holdout_version_unique UNIQUE(tenant_id,workspace_id,dataset_version_id,recipe_id),
    CONSTRAINT model_holdout_creation_unique UNIQUE(tenant_id,workspace_id,created_by,audience,creation_key),
    INDEX model_holdout_page_idx(tenant_id,workspace_id,created_at DESC,id DESC),
    FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
    FOREIGN KEY(tenant_id,workspace_id,dataset_id,dataset_version_id) REFERENCES dataset_versions(tenant_id,workspace_id,dataset_id,id),
    FOREIGN KEY(created_by) REFERENCES users(id),
    CONSTRAINT model_holdout_label_shape CHECK(
        CASE WHEN JSON_SCHEMA_VALID('{"type":"object","properties":{"account":{"type":"integer","minimum":1,"maximum":256},"billing":{"type":"integer","minimum":1,"maximum":256},"technical":{"type":"integer","minimum":1,"maximum":256}},"required":["account","billing","technical"],"additionalProperties":false}',label_counts)
          AND JSON_TYPE(JSON_EXTRACT(label_counts,'$.account'))='INTEGER'
          AND JSON_TYPE(JSON_EXTRACT(label_counts,'$.billing'))='INTEGER'
          AND JSON_TYPE(JSON_EXTRACT(label_counts,'$.technical'))='INTEGER'
        THEN CAST(JSON_UNQUOTE(JSON_EXTRACT(label_counts,'$.account')) AS UNSIGNED)
           + CAST(JSON_UNQUOTE(JSON_EXTRACT(label_counts,'$.billing')) AS UNSIGNED)
           + CAST(JSON_UNQUOTE(JSON_EXTRACT(label_counts,'$.technical')) AS UNSIGNED)=row_count
        ELSE FALSE END = TRUE
    ),
    CONSTRAINT model_holdouts_id_canonical CHECK(REGEXP_LIKE(id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_holdouts_tenant_id_canonical CHECK(REGEXP_LIKE(tenant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_holdouts_workspace_id_canonical CHECK(REGEXP_LIKE(workspace_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_holdouts_dataset_id_canonical CHECK(REGEXP_LIKE(dataset_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_holdouts_dataset_version_id_canonical CHECK(REGEXP_LIKE(dataset_version_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_holdouts_created_by_canonical CHECK(REGEXP_LIKE(created_by,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT model_holdouts_creation_key_canonical CHECK(REGEXP_LIKE(creation_key,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
