CREATE TABLE workflow_node_traces (
 tenant_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workspace_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 workflow_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 run_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 node_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 kind VARCHAR(16) NOT NULL CONSTRAINT workflow_node_traces_kind_check CHECK(kind IN('start','retrieval','answer','end')),
 state VARCHAR(16) NOT NULL CHECK(state IN('not_started','running','succeeded','failed','cancelled','interrupted')),
 result_metadata JSON NOT NULL DEFAULT (JSON_OBJECT()) CHECK(JSON_TYPE(result_metadata)='OBJECT' AND OCTET_LENGTH(CAST(result_metadata AS CHAR CHARACTER SET utf8mb4))<=65536),
 error_code VARCHAR(100),
 started_at DATETIME(6),
 finished_at DATETIME(6),
 PRIMARY KEY(run_id,node_id),
 UNIQUE(run_id,kind),
 FOREIGN KEY(tenant_id,workspace_id,workflow_id,run_id) REFERENCES workflow_runs(tenant_id,workspace_id,workflow_id,id) ON DELETE CASCADE,
 CHECK((state='not_started' AND started_at IS NULL AND finished_at IS NULL) OR (state='running' AND started_at IS NOT NULL AND finished_at IS NULL) OR (state IN('succeeded','failed','cancelled','interrupted') AND finished_at IS NOT NULL)),
 CONSTRAINT workflow_node_traces_node_id_canonical CHECK(REGEXP_LIKE(node_id,_ascii'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;
