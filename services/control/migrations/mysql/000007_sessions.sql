CREATE TABLE sessions (
    id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    audience VARCHAR(16) NOT NULL CHECK(audience IN('portal','admin')),
    token_hash VARBINARY(32) NOT NULL UNIQUE CHECK(OCTET_LENGTH(token_hash)=32),
    auth_epoch BIGINT NOT NULL CHECK(auth_epoch BETWEEN 1 AND 9007199254740991),
    created_at DATETIME(6) NOT NULL,
    last_seen_at DATETIME(6) NOT NULL,
    absolute_expires_at DATETIME(6) NOT NULL,
    revoked_at DATETIME(6),
    revoke_reason VARCHAR(48),
    KEY sessions_user_live_idx(user_id,revoked_at),
    KEY sessions_expiry_idx(absolute_expires_at),
    FOREIGN KEY(user_id) REFERENCES users(id),
    CHECK(absolute_expires_at>created_at),
    CHECK(last_seen_at>=created_at),
    CHECK((revoked_at IS NULL)=(revoke_reason IS NULL)),
    CONSTRAINT sessions_id_canonical CHECK(REGEXP_LIKE(id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
