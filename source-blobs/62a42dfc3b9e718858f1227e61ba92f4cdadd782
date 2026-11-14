CREATE TABLE artifact_object_parts (
 tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, object_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 number INT NOT NULL CHECK(number BETWEEN 1 AND 10000), bytes BIGINT NOT NULL CHECK(bytes BETWEEN 1 AND 5368709120),
 sha256 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(sha256,'^[a-f0-9]{64}$','c')),
 etag VARCHAR(1024), state VARCHAR(16) NOT NULL CHECK(state IN('intent','uploaded')), stage_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 PRIMARY KEY(object_id,number), FOREIGN KEY(tenant_id,workspace_id,object_id) REFERENCES artifact_objects(tenant_id,workspace_id,id),
 CHECK((state='uploaded')=(etag IS NOT NULL)),
 CONSTRAINT apart_tenant_id CHECK(REGEXP_LIKE(tenant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT apart_workspace_id CHECK(REGEXP_LIKE(workspace_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT apart_object_id CHECK(REGEXP_LIKE(object_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT apart_stage_id CHECK(REGEXP_LIKE(stage_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;