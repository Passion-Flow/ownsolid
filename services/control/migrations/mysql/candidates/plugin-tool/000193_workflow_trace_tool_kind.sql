ALTER TABLE workflow_node_traces
 DROP CHECK workflow_node_traces_kind_check,
 ADD CONSTRAINT workflow_node_traces_tool_kind_check CHECK(kind IN('start','retrieval','answer','end','tool'));
