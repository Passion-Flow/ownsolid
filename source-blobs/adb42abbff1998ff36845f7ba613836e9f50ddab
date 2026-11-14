CREATE TABLE schema_migrations (
    step INT PRIMARY KEY CHECK(step>=1),
    source_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    object_name VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    object_sha256 CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    state VARCHAR(8) NOT NULL CHECK(state IN('applying','applied')),
    started_at DATETIME(6) NOT NULL,
    applied_at DATETIME(6),
    CHECK((state='applied')=(applied_at IS NOT NULL)),
    CHECK(REGEXP_LIKE(source_sha256,'^[a-f0-9]{64}$','c')),
    CHECK(REGEXP_LIKE(object_sha256,'^[a-f0-9]{64}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
