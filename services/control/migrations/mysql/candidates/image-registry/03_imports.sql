CREATE TABLE image_registry_imports (
 tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 pin_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 owner_token CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 phase VARCHAR(16) NOT NULL CHECK(phase IN('prepared','remote','completed','unknown')),
 PRIMARY KEY(tenant_id,workspace_id,pin_id),
 FOREIGN KEY(tenant_id,workspace_id,pin_id) REFERENCES image_registry_pins(tenant_id,workspace_id,id),
 CHECK(REGEXP_LIKE(owner_token,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
