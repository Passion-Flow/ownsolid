CREATE TABLE idempotency_records (
    actor_user_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    operation_id VARCHAR(64) NOT NULL CHECK(operation_id IN('createInvitation','resendInvitation')),
    idempotency_key CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    request_hash VARBINARY(32) NOT NULL CHECK(OCTET_LENGTH(request_hash)=32),
    response_status INT NOT NULL CHECK(response_status IN(200,201)),
    response_body JSON NOT NULL,
    created_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    PRIMARY KEY(actor_user_id,operation_id,idempotency_key),
    KEY idempotency_expiry_idx(expires_at),
    FOREIGN KEY(actor_user_id) REFERENCES users(id),
    CHECK(expires_at>created_at),
    CHECK(JSON_TYPE(response_body)='OBJECT'),
    CHECK(OCTET_LENGTH(CAST(response_body AS CHAR CHARACTER SET utf8mb4))<=16384),
    CONSTRAINT idempotency_key_canonical CHECK(REGEXP_LIKE(idempotency_key,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
