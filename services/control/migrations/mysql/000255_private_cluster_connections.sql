CREATE TABLE private_cluster_connections (
 id char(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY, tenant_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, workspace_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 profile_id varchar(63) NOT NULL, cluster_uid char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, namespace_uid char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 namespace_name varchar(63) NOT NULL, installation_id char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, profile varchar(64) NOT NULL,
 config_sha256 char(64) NOT NULL, credential_sha256 char(64) NOT NULL, manifest_sha256 char(64) NOT NULL,
 image_digest varchar(71) NOT NULL, config_digest varchar(71) NOT NULL,
 state varchar(16) NOT NULL CHECK(state IN('active','disabled','restore_fenced')),
 revision bigint NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991), active_run_id char(36) CHARACTER SET ascii COLLATE ascii_bin,
 observation json, created_by char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 created_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), updated_at datetime(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 FOREIGN KEY(created_by) REFERENCES users(id),
 UNIQUE(tenant_id,workspace_id,id), UNIQUE(cluster_uid,namespace_uid), UNIQUE(tenant_id,workspace_id,profile_id),
 FOREIGN KEY(tenant_id,workspace_id) REFERENCES workspaces(tenant_id,id),
 CHECK(cluster_uid<>namespace_uid), CHECK(namespace_name LIKE 'projectone-test-%'),
 CHECK(profile IN('kubernetes1.36.4-linux-amd64','local-kind1.36.4-linux-arm64')),
 CHECK(REGEXP_LIKE(config_sha256,'^[a-f0-9]{64}$','c') AND REGEXP_LIKE(credential_sha256,'^[a-f0-9]{64}$','c') AND REGEXP_LIKE(manifest_sha256,'^[a-f0-9]{64}$','c')),
 CHECK(REGEXP_LIKE(image_digest,'^sha256:[a-f0-9]{64}$','c') AND REGEXP_LIKE(config_digest,'^sha256:[a-f0-9]{64}$','c')),
 CHECK(observation IS NULL OR JSON_TYPE(observation)='OBJECT' COLLATE utf8mb4_bin)
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;