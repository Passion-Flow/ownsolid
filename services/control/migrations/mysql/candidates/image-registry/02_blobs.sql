CREATE TABLE image_registry_blobs (
 tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 pin_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 ordinal INT NOT NULL CHECK(ordinal>=0), digest VARCHAR(71) CHARACTER SET ascii COLLATE ascii_bin NOT NULL CHECK(REGEXP_LIKE(digest,'^sha256:[a-f0-9]{64}$','c')),
 size BIGINT NOT NULL CHECK(size>0), media_type VARCHAR(160) NOT NULL CHECK(CHAR_LENGTH(media_type)>0), kind VARCHAR(16) NOT NULL CHECK(kind IN('manifest','config','layer')),
 PRIMARY KEY(tenant_id,workspace_id,pin_id,ordinal),
 FOREIGN KEY(tenant_id,workspace_id,pin_id) REFERENCES image_registry_pins(tenant_id,workspace_id,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
