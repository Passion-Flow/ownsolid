ALTER TABLE workflow_runs
 ADD COLUMN result_kind VARCHAR(16) NOT NULL DEFAULT 'answer' CONSTRAINT workflow_result_kind_check CHECK(result_kind IN('answer','model_list')),
 ADD COLUMN tool_result JSON CONSTRAINT workflow_tool_result_object CHECK(tool_result IS NULL OR JSON_TYPE(tool_result)='OBJECT' AND OCTET_LENGTH(CAST(tool_result AS CHAR CHARACTER SET utf8mb4))<=1048576),
 ADD COLUMN tool_proof JSON CONSTRAINT workflow_tool_proof_object CHECK(tool_proof IS NULL OR JSON_TYPE(tool_proof)='OBJECT' AND OCTET_LENGTH(CAST(tool_proof AS CHAR CHARACTER SET utf8mb4))<=65536),
 DROP CHECK workflow_non_success_result_absent,
 DROP CHECK workflow_success_result_required,
 ADD CONSTRAINT workflow_tool_non_success_result_absent CHECK(state='succeeded' OR (answer IS NULL AND tool_result IS NULL AND tool_proof IS NULL)),
 ADD CONSTRAINT workflow_tool_success_result_required CHECK(state<>'succeeded' OR (error_code IS NULL AND ((result_kind='answer' AND answer IS NOT NULL AND evidence IS NOT NULL AND alias_snapshot IS NOT NULL AND `usage` IS NOT NULL AND tool_result IS NULL AND tool_proof IS NULL) OR (result_kind='model_list' AND answer IS NULL AND evidence IS NULL AND alias_snapshot IS NULL AND `usage` IS NULL AND tool_result IS NOT NULL AND tool_proof IS NOT NULL)))),
 ADD CONSTRAINT workflow_result_graph_kind CHECK((result_kind='answer' AND JSON_UNQUOTE(JSON_EXTRACT(graph_snapshot,'$.schema_version'))='1') OR (result_kind='model_list' AND JSON_UNQUOTE(JSON_EXTRACT(graph_snapshot,'$.schema_version'))='2'));
