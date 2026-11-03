CREATE TABLE model_import_chunks (
    import_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    byte_offset BIGINT NOT NULL CHECK(byte_offset>=0 AND MOD(byte_offset,8388608)=0),
    size_bytes INT NOT NULL CHECK(size_bytes BETWEEN 1 AND 8388608),
    sha256 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(sha256,'^[a-f0-9]{64}$','c')),
    PRIMARY KEY(import_id,byte_offset),
    FOREIGN KEY(import_id) REFERENCES model_imports(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
