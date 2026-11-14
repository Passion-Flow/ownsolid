CREATE TABLE model_preparation_upload_chunks (
    upload_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    asset VARCHAR(100) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(asset,'^[A-Za-z0-9_.-]{1,100}$','c') AND asset NOT IN ('.','..')),
    byte_offset bigint NOT NULL CHECK(byte_offset BETWEEN 0 AND 2147483648 AND byte_offset % 8388608=0),
    size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 8388608),
    sha256 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(sha256,'^[a-f0-9]{64}$','c')),
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY(upload_id,asset,byte_offset),
    FOREIGN KEY(upload_id) REFERENCES model_preparation_uploads(id),
    CONSTRAINT model_preparation_upload_chunks_upload_id_canonical CHECK(REGEXP_LIKE(upload_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
