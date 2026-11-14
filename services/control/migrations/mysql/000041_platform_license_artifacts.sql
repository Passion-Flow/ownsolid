CREATE TABLE platform_license_artifacts (
    license_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    sequence INT NOT NULL UNIQUE CHECK(sequence BETWEEN 1 AND 1024),
    deployment_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    artifact_sha256 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE,
    artifact MEDIUMBLOB NOT NULL CHECK(OCTET_LENGTH(artifact) BETWEEN 1 AND 65536),
    predecessor_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin,
    imported_by CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    imported_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    reason VARCHAR(500) NOT NULL CHECK(CHAR_LENGTH(reason)>0),
    FOREIGN KEY(deployment_id) REFERENCES platform_license_state(deployment_id),
    FOREIGN KEY(predecessor_id) REFERENCES platform_license_artifacts(license_id),
    FOREIGN KEY(imported_by) REFERENCES users(id),
    CONSTRAINT license_id_canonical CHECK(REGEXP_LIKE(license_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT license_hash_canonical CHECK(REGEXP_LIKE(artifact_sha256,'^[a-f0-9]{64}$','c')),
    CONSTRAINT license_artifact_deployment_canonical CHECK(REGEXP_LIKE(deployment_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT license_predecessor_canonical CHECK(predecessor_id IS NULL OR REGEXP_LIKE(predecessor_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT license_predecessor_distinct CHECK(predecessor_id IS NULL OR predecessor_id<>license_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
