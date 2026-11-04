CREATE TABLE platform_license_state (
    id smallint PRIMARY KEY CHECK (id = 1),
    deployment_id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO platform_license_state(id) VALUES (1);

CREATE TABLE platform_license_artifacts (
    license_id uuid PRIMARY KEY,
    sequence integer NOT NULL UNIQUE CHECK (sequence BETWEEN 1 AND 1024),
    deployment_id uuid NOT NULL REFERENCES platform_license_state(deployment_id) ON DELETE RESTRICT,
    artifact_sha256 varchar(64) NOT NULL UNIQUE CHECK (artifact_sha256 ~ '^[a-f0-9]{64}$'),
    artifact bytea NOT NULL CHECK (octet_length(artifact) BETWEEN 1 AND 65536),
    predecessor_id uuid REFERENCES platform_license_artifacts(license_id) ON DELETE RESTRICT,
    imported_by uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    imported_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    reason varchar(500) NOT NULL CHECK (length(reason) > 0),
    CHECK (predecessor_id IS NULL OR predecessor_id <> license_id)
);
