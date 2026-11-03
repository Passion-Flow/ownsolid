CREATE TABLE platform_license_state (
    id SMALLINT PRIMARY KEY CHECK(id=1),
    deployment_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL UNIQUE DEFAULT (UUID()),
    observed_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT license_deployment_canonical CHECK(REGEXP_LIKE(deployment_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
    CONSTRAINT license_observed_range CHECK(observed_at>='1970-01-01 00:00:00')
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
