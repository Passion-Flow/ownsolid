CREATE TABLE sso_bindings (
    id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    connector_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    subject VARCHAR(19) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE CHECK(active IN(0,1)),
    revision BIGINT NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY sso_bindings_subject(tenant_id,connector_id,subject),
    UNIQUE KEY sso_bindings_user_identity(tenant_id,connector_id,id,user_id),
    KEY sso_bindings_user_idx(tenant_id,user_id,created_at,id),
    FOREIGN KEY(tenant_id,connector_id) REFERENCES sso_connectors(tenant_id,id),
    FOREIGN KEY(user_id) REFERENCES users(id),
    CONSTRAINT sso_subject_domain CHECK(REGEXP_LIKE(subject,'^[1-9][0-9]{0,18}$','c') AND NOT REGEXP_LIKE(subject,'[^0-9]','c') AND (CHAR_LENGTH(subject)<19 OR subject<='9223372036854775807')),
    CONSTRAINT sso_bindings_id_canonical CHECK(REGEXP_LIKE(id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
