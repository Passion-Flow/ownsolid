CREATE TABLE workflow_plugin_references (
tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workflow_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workflow_version_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 plugin_version_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 grant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 publication_grant_authority CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 PRIMARY KEY(workflow_version_id,plugin_version_id),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,workflow_version_id) REFERENCES workflow_versions(tenant_id,workspace_id,workflow_id,id),
 FOREIGN KEY(tenant_id,workspace_id,grant_id,plugin_version_id) REFERENCES plugin_grants(tenant_id,workspace_id,id,plugin_version_id),
 CONSTRAINT workflow_plugin_references_tenant_id_canonical CHECK(REGEXP_LIKE(tenant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT workflow_plugin_references_workspace_id_canonical CHECK(REGEXP_LIKE(workspace_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT workflow_plugin_references_workflow_id_canonical CHECK(REGEXP_LIKE(workflow_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT workflow_plugin_references_workflow_version_id_canonical CHECK(REGEXP_LIKE(workflow_version_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT workflow_plugin_references_plugin_version_id_canonical CHECK(REGEXP_LIKE(plugin_version_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT workflow_plugin_references_grant_id_canonical CHECK(REGEXP_LIKE(grant_id,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')),
 CONSTRAINT workflow_plugin_references_publication_grant_authority_canonical CHECK(REGEXP_LIKE(publication_grant_authority,'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
