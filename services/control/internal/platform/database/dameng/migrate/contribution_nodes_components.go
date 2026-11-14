package migrate

import "strings"

// nodesComponentsFragment is a native schema contribution, not registration.
// It depends only on foundation identities; downstream model/task-event graphs
// may reference these keys after this fragment. No reverse FK or cycle is needed.
func nodesComponentsFragment(current map[string]table) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "nodes_components_v2", Requires: []string{"foundation_v1"}}
	known := make(map[string]table, len(current)+5)
	for name, value := range current {
		known[name] = cloneTable(value)
	}
	for _, t := range []table{nodesManagedTable(), nodesJoinTable(), nodesTaskTable(), nodesComponentTable(), nodesComponentLogsTable()} {
		f.Changes = append(f.Changes, schemaChange{ID: t.Name, Kind: "table", SQL: extensionTableSQL(t, known, nil), After: &t})
		known[t.Name] = cloneTable(t)
	}
	indexes := []index{
		{Name: "NODES_LIVE_NAME_IDX", Table: "MANAGED_NODES", Unique: true, Columns: []string{"LIVE_TENANT_ID", "LIVE_NAME"}, Descending: []bool{false, false}},
		{Name: "NODES_SCOPE_LIST_IDX", Table: "MANAGED_NODES", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, Descending: []bool{false, false, true, true}},
		{Name: "NODE_JOIN_SCOPE_IDX", Table: "NODE_JOIN_TOKENS", Columns: []string{"TENANT_ID", "CREATED_AT", "ID"}, Descending: []bool{false, true, true}},
		{Name: "TASKS_ONE_INFLIGHT_IDX", Table: "NODE_TASKS", Unique: true, Columns: []string{"INFLIGHT_NODE_ID"}, Descending: []bool{false}},
		{Name: "TASKS_SCOPE_LIST_IDX", Table: "NODE_TASKS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "CREATED_AT", "ID"}, Descending: []bool{false, false, false, true, true}},
		{Name: "TASKS_CLAIM_IDX", Table: "NODE_TASKS", Columns: []string{"NODE_ID", "STATE", "CREATED_AT"}, Descending: []bool{false, false, false}},
		{Name: "COMPONENTS_SCOPE_IDX", Table: "MANAGED_COMPONENTS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID"}, Descending: []bool{false, false, false}},
		{Name: "COMPONENT_LOGS_SCOPE_IDX", Table: "COMPONENT_LOG_SNAPSHOTS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID"}, Descending: []bool{false, false, false}},
	}
	for _, idx := range indexes {
		f.Changes = append(f.Changes, schemaChange{ID: idx.Name, Kind: "index", SQL: idx.ddl(), Index: &idx})
	}
	t := nodesDerivedTaskTrigger()
	f.Changes = append(f.Changes, schemaChange{ID: t.Name, Kind: "trigger", SQL: `CREATE TRIGGER "NODE_DERIVED_INFERENCE_IMMUTABLE" BEFORE UPDATE ON "NODE_TASKS" FOR EACH ROW ` + t.Body, Trigger: &t})
	return f
}

func nodesNativeStamp(name string, nullable, defaultNow bool) column {
	c := stamp(name, nullable, false)
	if defaultNow {
		c.Default = "SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))"
	}
	return c
}
func nodesNativeProfile() column {
	c := text("EXECUTION_PROFILE", 32, false)
	c.Default = "'read_only'"
	return c
}
func nodesProfileCheck(name string) constraint {
	return check(name, `"EXECUTION_PROFILE" IN ('read_only','systemd_components','systemd_maintenance','docker_inference','docker_ai','docker_model_lifecycle')`)
}
func nodesHashCheck(name, field string, nullable bool) constraint {
	expr := "OCTET_LENGTH(" + quoted(field) + ")=32"
	if nullable {
		expr = quoted(field) + " IS NULL OR " + expr
	}
	return check(name, expr)
}
func nodesJSONCheck(name, field, kind, bound string, nullable bool) constraint {
	q := quoted(field)
	expr := "JSON_TYPE(" + q + ") IS NOT NULL AND JSON_TYPE(" + q + ")='" + kind + "'"
	if bound != "" {
		expr += " AND OCTET_LENGTH(" + q + ")<=" + bound
	}
	if nullable {
		expr = q + " IS NULL OR (" + expr + ")"
	}
	return check(name, expr)
}
func nodesManagedTable() table {
	capabilities := col("CAPABILITIES", "CLOB", 0, false, `'["collect_facts.v1"]'`)
	liveTenant := uuid("LIVE_TENANT_ID", true)
	liveTenant.Virtual = `CASE WHEN "STATUS"<>'removed' THEN "TENANT_ID" ELSE NULL END`
	liveName := text("LIVE_NAME", 100, true)
	liveName.Virtual = `CASE WHEN "STATUS"<>'removed' THEN "NAME" ELSE NULL END`
	// Both columns become NULL for retired identities. Native composite unique
	// keys must not retain a non-NULL tenant when admitting multiple retired names.
	return nativeTable("MANAGED_NODES", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("NAME", 100, false), text("STATUS", 16, false), version("VERSION"), col("CREDENTIAL_HASH", "VARBINARY", 32, true, ""), text("PROTOCOL_VERSION", 16, false), col("FACTS", "CLOB", 0, false, ""), nodesNativeStamp("LAST_HEARTBEAT_AT", false, false), nodesNativeStamp("CREATED_AT", false, true), nodesNativeStamp("UPDATED_AT", false, true), nodesNativeProfile(), capabilities, nodesNativeStamp("INFERENCE_FENCED_AT", true, false), liveTenant, liveName,
	}, pk("MANAGED_NODES_PK", "ID"), unique("NODES_SCOPE_IDENTITY", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("NODES_CREDENTIAL_UK", "CREDENTIAL_HASH"), fk("NODES_WORKSPACE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), bounded("NODES_VERSION", "VERSION"), check("NODES_STATUS", `"STATUS" IN ('active','disabled','removed')`), nodesHashCheck("NODES_CREDENTIAL_LENGTH", "CREDENTIAL_HASH", true), check("NODES_CREDENTIAL_STATE", `("STATUS"='removed' AND "CREDENTIAL_HASH" IS NULL) OR ("STATUS"<>'removed' AND "CREDENTIAL_HASH" IS NOT NULL)`), nodesProfileCheck("NODES_PROFILE"), nodesJSONCheck("NODES_FACTS", "FACTS", "OBJECT", "", false), check("NODES_CAPABILITIES", `JSON_TYPE("CAPABILITIES") IS NOT NULL AND JSON_TYPE("CAPABILITIES")='ARRAY' AND JSON_CONTAINS_PATH("CAPABILITIES",'ONE','$[11]')=0`))
}
func nodesJoinTable() table {
	return nativeTable("NODE_JOIN_TOKENS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("NODE_NAME", 100, false), col("TOKEN_HASH", "VARBINARY", 32, false, ""), uuid("CREATED_BY", false), nodesNativeStamp("CREATED_AT", false, true), nodesNativeStamp("EXPIRES_AT", false, false), nodesNativeStamp("CONSUMED_AT", true, false), nodesNativeStamp("REVOKED_AT", true, false), nodesNativeProfile(), uuid("NODE_ID", true),
	}, pk("NODE_JOIN_TOKENS_PK", "ID"), unique("NODE_JOIN_TOKEN_UK", "TOKEN_HASH"), fk("NODE_JOIN_ACTOR_FK", "USERS", "USERS_PK", "CREATED_BY"), fk("NODE_JOIN_WORKSPACE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), fk("NODE_JOIN_NODE_FK", "MANAGED_NODES", "NODES_SCOPE_IDENTITY", "TENANT_ID", "WORKSPACE_ID", "NODE_ID"), nodesHashCheck("NODE_JOIN_TOKEN_LENGTH", "TOKEN_HASH", false), nodesProfileCheck("NODE_JOIN_PROFILE"))
}
func nodesTaskTable() table {
	inflight := uuid("INFLIGHT_NODE_ID", true)
	inflight.Virtual = `CASE WHEN "STATE" IN ('pending','running') THEN "NODE_ID" ELSE NULL END`
	return nativeTable("NODE_TASKS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("NODE_ID", false), text("OPERATION", 64, false), text("STATE", 16, false), uuid("IDEMPOTENCY_KEY", false), uuid("REQUESTED_BY", false), col("ATTEMPT", "INTEGER", 0, false, "0"), col("LEASE_TOKEN_HASH", "VARBINARY", 32, true, ""), nodesNativeStamp("LEASE_EXPIRES_AT", true, false), text("ERROR_CODE", 64, true), col("COMPLETED_LEASE_HASH", "VARBINARY", 32, true, ""), col("RESULT_HASH", "VARBINARY", 32, true, ""), nodesNativeStamp("CREATED_AT", false, true), nodesNativeStamp("UPDATED_AT", false, true), nodesNativeStamp("DEADLINE_AT", false, false), nodesNativeStamp("COMPLETED_AT", true, false), col("PAYLOAD", "CLOB", 0, false, "'{}'"), col("REQUEST_FINGERPRINT", "VARBINARY", 32, true, ""), col("INFERENCE_REQUESTED_EPOCH", "BIGINT", 0, true, ""), inflight,
	}, pk("NODE_TASKS_PK", "ID"), unique("TASKS_IDEMPOTENCY_IDENTITY", "NODE_ID", "IDEMPOTENCY_KEY"), unique("TASKS_SCOPE_IDENTITY", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"), fk("TASKS_ACTOR_FK", "USERS", "USERS_PK", "REQUESTED_BY"), fk("TASKS_NODE_FK", "MANAGED_NODES", "NODES_SCOPE_IDENTITY", "TENANT_ID", "WORKSPACE_ID", "NODE_ID"), check("TASKS_OPERATION", `"OPERATION" IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1','inference.deploy.v1','inference.stop.v1','inference.remove.v1','inference.observe.v1','inference.verify.v1','training.run.v1','model.prepare.v1','inference.derived.v1')`), check("TASKS_STATE", `"STATE" IN ('pending','running','succeeded','failed','cancelled')`), check("TASKS_ATTEMPT", `"ATTEMPT" BETWEEN 0 AND 3`), nodesHashCheck("TASKS_LEASE_LENGTH", "LEASE_TOKEN_HASH", true), nodesHashCheck("TASKS_COMPLETED_LEASE_LENGTH", "COMPLETED_LEASE_HASH", true), nodesHashCheck("TASKS_RESULT_LENGTH", "RESULT_HASH", true), nodesHashCheck("TASKS_REQUEST_LENGTH", "REQUEST_FINGERPRINT", true), nodesJSONCheck("TASKS_PAYLOAD", "PAYLOAD", "OBJECT", "32768", false), check("TASKS_RUNNING_LEASE", `("STATE"='running' AND "LEASE_TOKEN_HASH" IS NOT NULL AND "LEASE_EXPIRES_AT" IS NOT NULL) OR ("STATE"<>'running' AND ("LEASE_TOKEN_HASH" IS NULL OR "LEASE_EXPIRES_AT" IS NULL))`), check("TASKS_COMPLETION", `("STATE" IN ('succeeded','failed','cancelled') AND "COMPLETED_AT" IS NOT NULL) OR ("STATE" IN ('pending','running') AND "COMPLETED_AT" IS NULL)`), check("NODE_DERIVED_DELEGATION_PRESENCE", `("OPERATION"='inference.derived.v1' AND "INFERENCE_REQUESTED_EPOCH" IS NOT NULL AND "INFERENCE_REQUESTED_EPOCH">=1) OR ("OPERATION"<>'inference.derived.v1' AND "INFERENCE_REQUESTED_EPOCH" IS NULL)`))
}
func nodesComponentTable() table {
	return nativeTable("MANAGED_COMPONENTS", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("NODE_ID", false), text("COMPONENT", 32, false), text("VERSION", 32, false), text("DESIRED_STATE", 16, false), text("OBSERVED_STATE", 16, false), col("MANAGED", "BIT", 0, false, ""), col("OBSERVATION", "CLOB", 0, false, ""), nodesNativeStamp("OBSERVED_AT", false, false), uuid("LAST_TASK_ID", true), nodesNativeStamp("UPDATED_AT", false, true), nodesComponentDesiredVersion(), col("DESIRED_CONFIG", "CLOB", 0, true, `'{"log_level":"info","max_scrape_requests":40}'`), col("APPLIED_CONFIG", "CLOB", 0, true, ""), nodesNativeStamp("LAST_VERIFIED_AT", true, false), text("PREVIOUS_VERSION", 32, true),
	}, pk("MANAGED_COMPONENTS_PK", "NODE_ID", "COMPONENT"), fk("COMPONENTS_NODE_SCOPE_FK", "MANAGED_NODES", "NODES_SCOPE_IDENTITY", "TENANT_ID", "WORKSPACE_ID", "NODE_ID"), fk("COMPONENTS_TASK_ID_FK", "NODE_TASKS", "NODE_TASKS_PK", "LAST_TASK_ID"), fk("COMPONENTS_TASK_SCOPE_FK", "NODE_TASKS", "TASKS_SCOPE_IDENTITY", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "LAST_TASK_ID"), check("COMPONENTS_COMPONENT", `"COMPONENT"='node_exporter'`), check("COMPONENTS_DESIRED_STATE", `"DESIRED_STATE" IN ('running','stopped','not_installed')`), check("COMPONENTS_OBSERVED_STATE", `"OBSERVED_STATE" IN ('running','stopped','not_installed','unknown')`), check("COMPONENTS_MANAGED", `"MANAGED" IN (0,1)`), nodesJSONCheck("COMPONENTS_OBSERVATION", "OBSERVATION", "OBJECT", "8192", false), check("COMPONENTS_DESIRED_VERSION", `"DESIRED_VERSION" IN ('1.12.0','1.12.1')`), nodesJSONCheck("COMPONENTS_DESIRED_CONFIG", "DESIRED_CONFIG", "OBJECT", "512", true), nodesJSONCheck("COMPONENTS_APPLIED_CONFIG", "APPLIED_CONFIG", "OBJECT", "512", true), check("COMPONENTS_PREVIOUS_VERSION", `"PREVIOUS_VERSION" IS NULL OR "PREVIOUS_VERSION" IN ('1.12.0','1.12.1')`))
}
func nodesComponentDesiredVersion() column {
	c := text("DESIRED_VERSION", 32, false)
	c.Default = "'1.12.1'"
	return c
}
func nodesComponentLogsTable() table {
	return nativeTable("COMPONENT_LOG_SNAPSHOTS", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("NODE_ID", false), text("COMPONENT", 32, false), uuid("TASK_ID", false), col("SNAPSHOT", "CLOB", 0, false, ""), nodesNativeStamp("RECEIVED_AT", false, true),
	}, pk("COMPONENT_LOG_SNAPSHOTS_PK", "NODE_ID", "COMPONENT"), fk("COMPONENT_LOGS_NODE_SCOPE_FK", "MANAGED_NODES", "NODES_SCOPE_IDENTITY", "TENANT_ID", "WORKSPACE_ID", "NODE_ID"), fk("COMPONENT_LOGS_TASK_SCOPE_FK", "NODE_TASKS", "TASKS_SCOPE_IDENTITY", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID"), check("COMPONENT_LOGS_COMPONENT", `"COMPONENT"='node_exporter'`), nodesJSONCheck("COMPONENT_LOGS_SNAPSHOT", "SNAPSHOT", "OBJECT", "16384", false))
}

func nodesDerivedTaskTrigger() trigger {
	changes := []string{}
	for _, field := range []string{"ID", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "OPERATION", "REQUESTED_BY", "INFERENCE_REQUESTED_EPOCH", "IDEMPOTENCY_KEY", "REQUEST_FINGERPRINT", "CREATED_AT"} {
		n, o := ":NEW."+quoted(field), ":OLD."+quoted(field)
		changes = append(changes, "(("+n+" IS NULL AND "+o+" IS NOT NULL) OR ("+n+" IS NOT NULL AND "+o+" IS NULL) OR ("+n+" IS NOT NULL AND "+o+" IS NOT NULL AND "+n+"<>"+o+"))")
	}
	// DM CLOB comparison is explicit; ordinary <> is not portable for a CLOB.
	changes = append(changes, `NVL(DBMS_LOB.COMPARE(:NEW."PAYLOAD",:OLD."PAYLOAD"),1)<>0`)
	body := `BEGIN IF :OLD."OPERATION"='inference.derived.v1' OR :NEW."OPERATION"='inference.derived.v1' THEN IF ` + strings.Join(changes, " OR ") + ` THEN RAISE_APPLICATION_ERROR(-20031,'DERIVED_INFERENCE_COMMAND_IMMUTABLE'); END IF; END IF; END;`
	return trigger{Name: "NODE_DERIVED_INFERENCE_IMMUTABLE", Table: "NODE_TASKS", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: body}
}
