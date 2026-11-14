CREATE TABLE password_reset_tokens (
    id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    audience VARCHAR(16) NOT NULL CHECK(audience IN('portal','admin')),
    token_hash VARBINARY(32) UNIQUE CHECK(token_hash IS NULL OR OCTET_LENGTH(token_hash)=32),
    created_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    consumed_at DATETIME(6),
    revoked_at DATETIME(6),
    UNIQUE KEY reset_tokens_id_user(id,user_id),
    KEY reset_tokens_user_live_idx(user_id,consumed_at,revoked_at,expires_at),
    FOREIGN KEY(user_id) REFERENCES users(id),
    CHECK(expires_at>created_at),
    CHECK(NOT(consumed_at IS NOT NULL AND revoked_at IS NOT NULL)),
    CHECK((consumed_at IS NULL AND revoked_at IS NULL)=(token_hash IS NOT NULL)),
    CONSTRAINT reset_tokens_id_canonical CHECK(REGEXP_LIKE(id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
