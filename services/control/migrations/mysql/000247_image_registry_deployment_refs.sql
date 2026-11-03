CREATE TABLE image_registry_deployment_refs (
 tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 pin_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 node_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 deployment_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 created_revision BIGINT NOT NULL CHECK(created_revision>=1), released_at DATETIME(6), created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 PRIMARY KEY(tenant_id,workspace_id,deployment_id), UNIQUE(tenant_id,workspace_id,pin_id,node_id,deployment_id),
 FOREIGN KEY(tenant_id,workspace_id,pin_id) REFERENCES image_registry_pins(tenant_id,workspace_id,id),
 FOREIGN KEY(tenant_id,workspace_id,node_id,deployment_id) REFERENCES deployments(tenant_id,workspace_id,node_id,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;