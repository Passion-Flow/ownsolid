package migrate

import "strings"

// applicationsFragment defines the retained workflow/Portal persistence model
// directly in native DM. It has no runtime registration or vendor SQL rewriting.
// The workflow/current-version cycle is closed by one later ALTER in this same
// fragment. Signed plugin provenance/grants are included because published tool
// references need their real scoped FK targets; this defines no execution adapter.
func applicationsFragment(current map[string]table) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "applications_v2", Requires: []string{"foundation_v1"}}
	known := make(map[string]table, len(current))
	for name, t := range current {
		known[name] = cloneTable(t)
	}
	add := func(change schemaChange) {
		f.Changes = append(f.Changes, change)
		if change.After != nil {
			known[change.After.Name] = cloneTable(*change.After)
		}
	}
	tableStep := func(t table, rules map[string]string) {
		add(schemaChange{ID: t.Name, Kind: "table", SQL: extensionTableSQL(t, known, rules), After: &t, DeleteRules: rules})
	}
	indexStep := func(name, owner string, columns []string, descending []bool, unique bool) {
		if descending == nil {
			descending = make([]bool, len(columns))
		}
		i := index{Name: name, Table: owner, Unique: unique, Columns: columns, Descending: descending}
		add(schemaChange{ID: name, Kind: "index", SQL: i.ddl(), Index: &i})
	}
	triggerStep := func(name, owner, event, timing, body string) {
		t := trigger{Name: name, Table: owner, Event: event, Timing: timing + " EACH ROW", Body: body}
		add(schemaChange{ID: name, Kind: "trigger", SQL: "CREATE TRIGGER " + quoted(name) + " " + timing + " " + event + " ON " + quoted(owner) + " FOR EACH ROW " + body, Trigger: &t})
	}

	tableStep(applicationsWorkflowTable(), nil)
	tableStep(applicationsVersionTable(), nil)
	before := cloneTable(known["WORKFLOWS"])
	after := cloneTable(before)
	after.Constraints = append(after.Constraints, fk("WORKFLOW_CURRENT_VERSION_SCOPE_FK", "WORKFLOW_VERSIONS", "WORKFLOW_VERSIONS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID", "CURRENT_VERSION_ID"))
	add(schemaChange{ID: "WORKFLOW_CURRENT_VERSION_SCOPE_FK", Kind: "alter", Before: &before, After: &after, SQL: `ALTER TABLE "WORKFLOWS" ADD CONSTRAINT "WORKFLOW_CURRENT_VERSION_SCOPE_FK" FOREIGN KEY ("TENANT_ID","WORKSPACE_ID","ID","CURRENT_VERSION_ID") REFERENCES "WORKFLOW_VERSIONS" ("TENANT_ID","WORKSPACE_ID","WORKFLOW_ID","ID")`})
	tableStep(applicationsConversationTable(), nil)
	tableStep(applicationsRunTable(), nil)
	tableStep(applicationsTraceTable(), map[string]string{"WORKFLOW_NODE_TRACES_RUN_FK": "CASCADE"})
	tableStep(applicationsIntentTable(), nil)
	tableStep(applicationsPluginVersionTable(), nil)
	tableStep(applicationsPluginGrantTable(), nil)
	tableStep(applicationsPluginIntentTable(), nil)
	tableStep(applicationsPluginReferenceTable(), nil)
	tableStep(applicationsNotificationTable(), map[string]string{"TERMINAL_NOTIFICATIONS_RUN_FK": "CASCADE"})

	indexStep("WORKFLOW_NAME_IDX", "WORKFLOWS", []string{"TENANT_ID", "WORKSPACE_ID", "NAME_KEY"}, nil, true)
	indexStep("WORKFLOW_PAGE_IDX", "WORKFLOWS", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, []bool{false, false, true, true}, false)
	indexStep("WORKFLOW_CONVERSATION_PAGE_IDX", "WORKFLOW_CONVERSATIONS", []string{"TENANT_ID", "WORKSPACE_ID", "OWNER_ID", "DELETED_AT", "CREATED_AT", "ID"}, []bool{false, false, false, false, true, true}, false)
	indexStep("APPLICATION_CONVERSATION_UPDATED_IDX", "WORKFLOW_CONVERSATIONS", []string{"TENANT_ID", "WORKSPACE_ID", "OWNER_ID", "DELETED_AT", "UPDATED_AT", "ID"}, []bool{false, false, false, false, true, true}, false)
	indexStep("WORKFLOW_ONE_ACTIVE_TURN_IDX", "WORKFLOW_RUNS", []string{"ACTIVE_CONVERSATION_ID"}, nil, true)
	indexStep("WORKFLOW_ACTIVE_WORKSPACE_IDX", "WORKFLOW_RUNS", []string{"TENANT_ID", "WORKSPACE_ID", "STATE", "LEASE_EXPIRES_AT"}, nil, false)
	indexStep("WORKFLOW_RUN_PAGE_IDX", "WORKFLOW_RUNS", []string{"TENANT_ID", "WORKSPACE_ID", "OWNER_ID", "CREATED_AT", "ID"}, []bool{false, false, false, true, true}, false)
	indexStep("PLUGIN_VERSION_PAGE_IDX", "PLUGIN_VERSIONS", []string{"TENANT_ID", "CREATED_AT", "ID"}, []bool{false, true, true}, false)
	indexStep("PLUGIN_GRANT_PAGE_IDX", "PLUGIN_GRANTS", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, []bool{false, false, true, true}, false)
	indexStep("PLUGIN_INTENT_RETENTION_IDX", "PLUGIN_MUTATION_INTENTS", []string{"TENANT_ID", "CREATED_AT", "IDEMPOTENCY_KEY"}, nil, false)
	indexStep("TERMINAL_NOTIFICATION_RECIPIENT_PAGE_IDX", "TERMINAL_NOTIFICATIONS", []string{"RECIPIENT_ID", "OCCURRED_AT", "ID"}, []bool{false, true, true}, false)
	indexStep("TERMINAL_NOTIFICATION_UNREAD_IDX", "TERMINAL_NOTIFICATIONS", []string{"RECIPIENT_ID", "READ_AT", "OCCURRED_AT", "ID"}, []bool{false, false, true, true}, false)
	indexStep("TERMINAL_NOTIFICATION_RETENTION_IDX", "TERMINAL_NOTIFICATIONS", []string{"TENANT_ID", "OCCURRED_AT", "ID"}, nil, false)

	immutable := `BEGIN RAISE_APPLICATION_ERROR(-20041,'WORKFLOW_VERSION_IMMUTABLE'); END;`
	triggerStep("WORKFLOW_PUBLICATION_IMMUTABLE_UPDATE", "WORKFLOW_VERSIONS", "UPDATE", "BEFORE", immutable)
	triggerStep("WORKFLOW_PUBLICATION_IMMUTABLE_DELETE", "WORKFLOW_VERSIONS", "DELETE", "BEFORE", immutable)
	triggerStep("WORKFLOW_RUN_IDENTITY_GUARD", "WORKFLOW_RUNS", "UPDATE", "BEFORE", applicationsRunIdentityBody())
	triggerStep("PLUGIN_PROVENANCE_IMMUTABLE_UPDATE", "PLUGIN_VERSIONS", "UPDATE", "BEFORE", applicationsPluginIdentityBody())
	triggerStep("PLUGIN_PROVENANCE_IMMUTABLE_DELETE", "PLUGIN_VERSIONS", "DELETE", "BEFORE", `BEGIN RAISE_APPLICATION_ERROR(-20046,'PLUGIN_PROVENANCE_IMMUTABLE'); END;`)
	triggerStep("WORKFLOW_PLUGIN_REFERENCE_IMMUTABLE_UPDATE", "WORKFLOW_PLUGIN_REFERENCES", "UPDATE", "BEFORE", immutable)
	triggerStep("WORKFLOW_PLUGIN_REFERENCE_IMMUTABLE_DELETE", "WORKFLOW_PLUGIN_REFERENCES", "DELETE", "BEFORE", immutable)
	triggerStep("TERMINAL_NOTIFICATION_PROVENANCE_INSERT", "TERMINAL_NOTIFICATIONS", "INSERT", "BEFORE", applicationsNoticeProvenanceBody(false))
	triggerStep("TERMINAL_NOTIFICATION_IMMUTABLE", "TERMINAL_NOTIFICATIONS", "UPDATE", "BEFORE", applicationsNoticeProvenanceBody(true))
	// Install the producer last, after every target constraint and guard exists.
	triggerStep("WORKFLOW_TERMINAL_NOTIFICATION", "WORKFLOW_RUNS", "UPDATE", "AFTER", applicationsNoticeProducerBody())
	return f
}

func applicationsStamp(name string, nullable, defaultNow bool) column {
	c := stamp(name, nullable, false)
	if defaultNow {
		c.Default = "SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))"
	}
	return c
}

func applicationsJSON(name, field string, nullable bool, maximum string) constraint {
	q := quoted(field)
	expr := `JSON_TYPE(` + q + `) IS NOT NULL AND JSON_TYPE(` + q + `)='OBJECT'`
	if maximum != "" {
		expr += " AND OCTET_LENGTH(" + q + ")<=" + maximum
	}
	if nullable {
		expr = q + " IS NULL OR (" + expr + ")"
	}
	return check(name, expr)
}

func applicationsHash(name, field string) constraint {
	return check(name, "REGEXP_LIKE("+quoted(field)+`, '^[a-f0-9]{64}$', 'c')`)
}

func applicationsWorkflowTable() table {
	return nativeTable("WORKFLOWS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("NAME", 120, false), text("NAME_KEY", 240, false),
		col("ENABLED", "BIT", 0, false, "1"), uuid("EXECUTION_AUTHORITY", false), version("REVISION"), col("DRAFT_GRAPH", "CLOB", 0, false, ""), text("DRAFT_SHA256", 64, false), uuid("CURRENT_VERSION_ID", true), uuid("CREATED_BY", false), applicationsStamp("CREATED_AT", false, true), applicationsStamp("UPDATED_AT", false, true),
	}, pk("WORKFLOWS_PK", "ID"), unique("WORKFLOWS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), fk("WORKFLOWS_WORKSPACE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), fk("WORKFLOWS_CREATOR_FK", "USERS", "USERS_PK", "CREATED_BY"),
		check("WORKFLOWS_NAME", `LENGTH(TRIM("NAME"))>0 AND LENGTH(TRIM("NAME_KEY"))>0`), bounded("WORKFLOWS_REVISION", "REVISION"), applicationsJSON("WORKFLOWS_DRAFT_GRAPH", "DRAFT_GRAPH", false, "1048576"), applicationsHash("WORKFLOWS_DRAFT_HASH", "DRAFT_SHA256"))
}

func applicationsVersionTable() table {
	return nativeTable("WORKFLOW_VERSIONS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("WORKFLOW_ID", false), col("VERSION_NUMBER", "INTEGER", 0, false, ""), col("DRAFT_REVISION", "BIGINT", 0, false, ""), col("GRAPH", "CLOB", 0, false, ""), text("GRAPH_SHA256", 64, false), uuid("PUBLISHED_BY", false), applicationsStamp("PUBLISHED_AT", false, true),
	}, pk("WORKFLOW_VERSIONS_PK", "ID"), unique("WORKFLOW_VERSIONS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "ID"), unique("WORKFLOW_VERSIONS_NUMBER_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "VERSION_NUMBER"), fk("WORKFLOW_VERSIONS_WORKFLOW_FK", "WORKFLOWS", "WORKFLOWS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID"), fk("WORKFLOW_VERSIONS_PUBLISHER_FK", "USERS", "USERS_PK", "PUBLISHED_BY"),
		check("WORKFLOW_VERSIONS_NUMBER", `"VERSION_NUMBER">0`), bounded("WORKFLOW_VERSIONS_DRAFT_REVISION", "DRAFT_REVISION"), applicationsJSON("WORKFLOW_VERSIONS_GRAPH", "GRAPH", false, "1048576"), applicationsHash("WORKFLOW_VERSIONS_GRAPH_HASH", "GRAPH_SHA256"))
}

func applicationsConversationTable() table {
	return nativeTable("WORKFLOW_CONVERSATIONS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("WORKFLOW_ID", false), uuid("VERSION_ID", false), uuid("OWNER_ID", false), version("REVISION"), col("TURN_COUNT", "INTEGER", 0, false, "0"), applicationsStamp("CREATED_AT", false, true), applicationsStamp("UPDATED_AT", false, true), applicationsStamp("DELETED_AT", true, false),
	}, pk("WORKFLOW_CONVERSATIONS_PK", "ID"), unique("WORKFLOW_CONVERSATIONS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "ID"), unique("WORKFLOW_CONVERSATIONS_RUN_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "ID", "OWNER_ID", "VERSION_ID"), fk("WORKFLOW_CONVERSATIONS_OWNER_FK", "USERS", "USERS_PK", "OWNER_ID"), fk("WORKFLOW_CONVERSATIONS_VERSION_FK", "WORKFLOW_VERSIONS", "WORKFLOW_VERSIONS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "VERSION_ID"), bounded("WORKFLOW_CONVERSATIONS_REVISION", "REVISION"), check("WORKFLOW_CONVERSATIONS_TURNS", `"TURN_COUNT" BETWEEN 0 AND 10000`))
}

func applicationsRunTable() table {
	active := uuid("ACTIVE_CONVERSATION_ID", true)
	active.Virtual = `CASE WHEN "CONVERSATION_ID" IS NOT NULL AND "STATE" IN ('pending','running') THEN "CONVERSATION_ID" ELSE NULL END`
	resultKind := text("RESULT_KIND", 16, false)
	resultKind.Default = "'answer'"
	return nativeTable("WORKFLOW_RUNS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("WORKFLOW_ID", false), uuid("OWNER_ID", false), uuid("CONVERSATION_ID", true), col("TURN_NUMBER", "INTEGER", 0, true, ""), uuid("VERSION_ID", true), col("DRAFT_REVISION", "BIGINT", 0, true, ""), uuid("EXECUTION_AUTHORITY", false), uuid("MEMBERSHIP_AUTHORITY", false), text("REQUESTED_AUDIENCE", 6, false), col("GRAPH_SNAPSHOT", "CLOB", 0, false, ""), text("GRAPH_SHA256", 64, false), text("STATE", 16, false), col("QUESTION", "CLOB", 0, false, ""), col("ANSWER", "CLOB", 0, true, ""), col("EVIDENCE", "CLOB", 0, true, ""), col("ALIAS_SNAPSHOT", "CLOB", 0, true, ""), col("USAGE", "CLOB", 0, true, ""), text("ERROR_CODE", 100, true), uuid("PROCESS_OWNER", true), uuid("LEASE_ID", true), applicationsStamp("LEASE_EXPIRES_AT", true, false), applicationsStamp("DEADLINE_AT", false, false), applicationsStamp("CANCELLATION_REQUESTED_AT", true, false), applicationsStamp("CREATED_AT", false, true), applicationsStamp("STARTED_AT", true, false), applicationsStamp("FINISHED_AT", true, false), active, resultKind, col("TOOL_RESULT", "CLOB", 0, true, ""), col("TOOL_PROOF", "CLOB", 0, true, ""),
	}, pk("WORKFLOW_RUNS_PK", "ID"), unique("WORKFLOW_RUNS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "ID"), unique("WORKFLOW_RUNS_RECIPIENT_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "ID", "OWNER_ID"), unique("WORKFLOW_RUNS_CONVERSATION_TURN_UK", "CONVERSATION_ID", "TURN_NUMBER"),
		fk("WORKFLOW_RUNS_OWNER_FK", "USERS", "USERS_PK", "OWNER_ID"), fk("WORKFLOW_RUNS_WORKFLOW_FK", "WORKFLOWS", "WORKFLOWS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID"), fk("WORKFLOW_RUNS_VERSION_FK", "WORKFLOW_VERSIONS", "WORKFLOW_VERSIONS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "VERSION_ID"), fk("WORKFLOW_RUNS_CONVERSATION_FK", "WORKFLOW_CONVERSATIONS", "WORKFLOW_CONVERSATIONS_RUN_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "CONVERSATION_ID", "OWNER_ID", "VERSION_ID"),
		check("WORKFLOW_RUNS_TURN_NUMBER", `"TURN_NUMBER" IS NULL OR "TURN_NUMBER" BETWEEN 1 AND 10000`), check("WORKFLOW_RUNS_DRAFT_REVISION", `"DRAFT_REVISION" IS NULL OR "DRAFT_REVISION" BETWEEN 1 AND 9007199254740991`), check("WORKFLOW_RUNS_AUDIENCE", `"REQUESTED_AUDIENCE" IN ('portal','admin')`), applicationsJSON("WORKFLOW_RUNS_GRAPH", "GRAPH_SNAPSHOT", false, "1048576"), applicationsHash("WORKFLOW_RUNS_GRAPH_HASH", "GRAPH_SHA256"),
		check("WORKFLOW_RUNS_STATE", `"STATE" IN ('pending','running','succeeded','failed','cancelled','interrupted')`), check("WORKFLOW_RUNS_QUESTION_BYTES", `OCTET_LENGTH("QUESTION") BETWEEN 1 AND 32768`), check("WORKFLOW_ANSWER_BYTES", `"ANSWER" IS NULL OR OCTET_LENGTH("ANSWER")<=1048576`), applicationsJSON("WORKFLOW_RUNS_EVIDENCE", "EVIDENCE", true, ""), applicationsJSON("WORKFLOW_RUNS_ALIAS_SNAPSHOT", "ALIAS_SNAPSHOT", true, ""), applicationsJSON("WORKFLOW_RUNS_USAGE", "USAGE", true, ""),
		check("WORKFLOW_RUNS_SOURCE", `("VERSION_ID" IS NOT NULL AND "DRAFT_REVISION" IS NULL AND "CONVERSATION_ID" IS NOT NULL AND "TURN_NUMBER" IS NOT NULL) OR ("VERSION_ID" IS NULL AND "DRAFT_REVISION" IS NOT NULL AND "CONVERSATION_ID" IS NULL AND "TURN_NUMBER" IS NULL)`), check("WORKFLOW_RUNS_FINISHED", `("STATE" IN ('pending','running') AND "FINISHED_AT" IS NULL) OR ("STATE" NOT IN ('pending','running') AND "FINISHED_AT" IS NOT NULL)`),
		check("WORKFLOW_RUNS_LEASE", `("STATE"='running' AND "PROCESS_OWNER" IS NOT NULL AND "LEASE_ID" IS NOT NULL AND "LEASE_EXPIRES_AT" IS NOT NULL AND "STARTED_AT" IS NOT NULL) OR ("STATE"<>'running' AND "PROCESS_OWNER" IS NULL AND "LEASE_ID" IS NULL AND "LEASE_EXPIRES_AT" IS NULL)`),
		check("WORKFLOW_RUNS_RESULT_KIND", `"RESULT_KIND" IN ('answer','model_list')`), applicationsJSON("WORKFLOW_RUNS_TOOL_RESULT", "TOOL_RESULT", true, "1048576"), applicationsJSON("WORKFLOW_RUNS_TOOL_PROOF", "TOOL_PROOF", true, "65536"),
		check("WORKFLOW_NON_SUCCESS_RESULT_ABSENT", `"STATE"='succeeded' OR ("ANSWER" IS NULL AND "TOOL_RESULT" IS NULL AND "TOOL_PROOF" IS NULL)`),
		check("WORKFLOW_SUCCESS_RESULT_REQUIRED", `"STATE"<>'succeeded' OR ("ERROR_CODE" IS NULL AND (("RESULT_KIND"='answer' AND "ANSWER" IS NOT NULL AND "EVIDENCE" IS NOT NULL AND "ALIAS_SNAPSHOT" IS NOT NULL AND "USAGE" IS NOT NULL AND "TOOL_RESULT" IS NULL AND "TOOL_PROOF" IS NULL) OR ("RESULT_KIND"='model_list' AND "ANSWER" IS NULL AND "EVIDENCE" IS NULL AND "ALIAS_SNAPSHOT" IS NULL AND "USAGE" IS NULL AND "TOOL_RESULT" IS NOT NULL AND "TOOL_PROOF" IS NOT NULL)))`),
		check("WORKFLOW_RESULT_GRAPH_KIND", `JSON_VALUE("GRAPH_SNAPSHOT", '$.schema_version' ERROR ON EMPTY ERROR ON ERROR) IS NOT NULL AND (("RESULT_KIND"='answer' AND JSON_VALUE("GRAPH_SNAPSHOT", '$.schema_version' ERROR ON EMPTY ERROR ON ERROR)='1') OR ("RESULT_KIND"='model_list' AND JSON_VALUE("GRAPH_SNAPSHOT", '$.schema_version' ERROR ON EMPTY ERROR ON ERROR)='2'))`))
}

func applicationsTraceTable() table {
	metadata := col("RESULT_METADATA", "CLOB", 0, false, "'{}'")
	return nativeTable("WORKFLOW_NODE_TRACES", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("WORKFLOW_ID", false), uuid("RUN_ID", false), uuid("NODE_ID", false), text("KIND", 16, false), text("STATE", 16, false), metadata, text("ERROR_CODE", 100, true), applicationsStamp("STARTED_AT", true, false), applicationsStamp("FINISHED_AT", true, false),
	}, pk("WORKFLOW_NODE_TRACES_PK", "RUN_ID", "NODE_ID"), unique("WORKFLOW_NODE_TRACES_KIND_UK", "RUN_ID", "KIND"), fk("WORKFLOW_NODE_TRACES_RUN_FK", "WORKFLOW_RUNS", "WORKFLOW_RUNS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "RUN_ID"),
		check("WORKFLOW_NODE_TRACES_KIND", `"KIND" IN ('start','retrieval','answer','end','tool')`), check("WORKFLOW_NODE_TRACES_STATE", `"STATE" IN ('not_started','running','succeeded','failed','cancelled','interrupted')`), applicationsJSON("WORKFLOW_NODE_TRACES_METADATA", "RESULT_METADATA", false, "65536"), check("WORKFLOW_NODE_TRACES_TIMING", `("STATE"='not_started' AND "STARTED_AT" IS NULL AND "FINISHED_AT" IS NULL) OR ("STATE"='running' AND "STARTED_AT" IS NOT NULL AND "FINISHED_AT" IS NULL) OR ("STATE" IN ('succeeded','failed','cancelled','interrupted') AND "FINISHED_AT" IS NOT NULL)`))
}

func applicationsIntentTable() table {
	return nativeTable("WORKFLOW_MUTATION_INTENTS", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("ACTOR_ID", false), uuid("IDEMPOTENCY_KEY", false), text("PURPOSE", 64, false), col("REQUEST_HASH", "VARBINARY", 32, false, ""), uuid("ENTITY_ID", false), applicationsStamp("CREATED_AT", false, true),
	}, pk("WORKFLOW_MUTATION_INTENTS_PK", "TENANT_ID", "WORKSPACE_ID", "ACTOR_ID", "IDEMPOTENCY_KEY"), fk("WORKFLOW_MUTATION_INTENTS_ACTOR_FK", "USERS", "USERS_PK", "ACTOR_ID"), fk("WORKFLOW_MUTATION_INTENTS_WORKSPACE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), check("WORKFLOW_MUTATION_INTENTS_HASH", `OCTET_LENGTH("REQUEST_HASH")=32`))
}

func applicationsPluginVersionTable() table {
	return nativeTable("PLUGIN_VERSIONS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), text("PLUGIN_ID", 100, false), text("RELEASE_VERSION", 32, false), col("VERSION_MAJOR", "INTEGER", 0, false, ""), col("VERSION_MINOR", "INTEGER", 0, false, ""), col("VERSION_PATCH", "INTEGER", 0, false, ""), text("ABI", 100, false), text("EXECUTOR", 100, false), col("CAPSULE", "BLOB", 0, false, ""), text("CAPSULE_SHA256", 64, false), col("MANIFEST", "CLOB", 0, false, ""), text("MANIFEST_SHA256", 64, false), text("KEY_ID", 64, false), text("STATE", 16, false), version("REVISION"), uuid("IMPORTED_BY", false), applicationsStamp("CREATED_AT", false, true), applicationsStamp("INSTALLED_AT", true, false), applicationsStamp("UNINSTALLED_AT", true, false),
	}, pk("PLUGIN_VERSIONS_PK", "ID"), unique("PLUGIN_VERSIONS_TENANT_UK", "TENANT_ID", "ID"), unique("PLUGIN_VERSIONS_RELEASE_UK", "TENANT_ID", "PLUGIN_ID", "RELEASE_VERSION"), unique("PLUGIN_VERSIONS_CAPSULE_UK", "TENANT_ID", "CAPSULE_SHA256"), fk("PLUGIN_VERSIONS_TENANT_FK", "TENANTS", "TENANTS_PK", "TENANT_ID"), fk("PLUGIN_VERSIONS_IMPORTER_FK", "USERS", "USERS_PK", "IMPORTED_BY"),
		check("PLUGIN_VERSIONS_VERSION", `"VERSION_MAJOR"=1 AND "VERSION_MINOR" BETWEEN 0 AND 9999 AND "VERSION_PATCH" BETWEEN 0 AND 9999`), check("PLUGIN_VERSIONS_RELEASE", `"RELEASE_VERSION"=CAST("VERSION_MAJOR" AS VARCHAR(12))||'.'||CAST("VERSION_MINOR" AS VARCHAR(12))||'.'||CAST("VERSION_PATCH" AS VARCHAR(12))`), check("PLUGIN_VERSIONS_CAPSULE_BYTES", `OCTET_LENGTH("CAPSULE") BETWEEN 1 AND 1048576`), applicationsHash("PLUGIN_VERSIONS_CAPSULE_HASH", "CAPSULE_SHA256"), applicationsJSON("PLUGIN_VERSIONS_MANIFEST", "MANIFEST", false, "16384"), applicationsHash("PLUGIN_VERSIONS_MANIFEST_HASH", "MANIFEST_SHA256"), applicationsHash("PLUGIN_VERSIONS_KEY_ID", "KEY_ID"), check("PLUGIN_VERSIONS_STATE", `"STATE" IN ('imported','installed','uninstalled')`), bounded("PLUGIN_VERSIONS_REVISION", "REVISION"), check("PLUGIN_VERSIONS_INSTALLED", `"STATE"<>'installed' OR "INSTALLED_AT" IS NOT NULL`))
}

func applicationsPluginGrantTable() table {
	return nativeTable("PLUGIN_GRANTS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("PLUGIN_VERSION_ID", false), col("ENABLED", "BIT", 0, false, "1"), uuid("AUTHORITY_ID", false), version("REVISION"), uuid("GRANTED_BY", false), applicationsStamp("CREATED_AT", false, true), applicationsStamp("UPDATED_AT", false, true),
	}, pk("PLUGIN_GRANTS_PK", "ID"), unique("PLUGIN_GRANTS_VERSION_UK", "TENANT_ID", "WORKSPACE_ID", "PLUGIN_VERSION_ID"), unique("PLUGIN_GRANTS_REFERENCE_UK", "TENANT_ID", "WORKSPACE_ID", "ID", "PLUGIN_VERSION_ID"), fk("PLUGIN_GRANTS_WORKSPACE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), fk("PLUGIN_GRANTS_VERSION_FK", "PLUGIN_VERSIONS", "PLUGIN_VERSIONS_TENANT_UK", "TENANT_ID", "PLUGIN_VERSION_ID"), fk("PLUGIN_GRANTS_ACTOR_FK", "USERS", "USERS_PK", "GRANTED_BY"), bounded("PLUGIN_GRANTS_REVISION", "REVISION"))
}

func applicationsPluginIntentTable() table {
	return nativeTable("PLUGIN_MUTATION_INTENTS", []column{
		uuid("TENANT_ID", false), uuid("ACTOR_ID", false), uuid("IDEMPOTENCY_KEY", false), text("PURPOSE", 100, false), col("REQUEST_HASH", "VARBINARY", 32, false, ""), uuid("ENTITY_ID", false), applicationsStamp("CREATED_AT", false, true),
	}, pk("PLUGIN_MUTATION_INTENTS_PK", "TENANT_ID", "ACTOR_ID", "IDEMPOTENCY_KEY"), fk("PLUGIN_MUTATION_INTENTS_TENANT_FK", "TENANTS", "TENANTS_PK", "TENANT_ID"), fk("PLUGIN_MUTATION_INTENTS_ACTOR_FK", "USERS", "USERS_PK", "ACTOR_ID"), check("PLUGIN_MUTATION_INTENTS_HASH", `OCTET_LENGTH("REQUEST_HASH")=32`))
}

func applicationsPluginReferenceTable() table {
	return nativeTable("WORKFLOW_PLUGIN_REFERENCES", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("WORKFLOW_ID", false), uuid("WORKFLOW_VERSION_ID", false), uuid("PLUGIN_VERSION_ID", false), uuid("GRANT_ID", false), uuid("PUBLICATION_GRANT_AUTHORITY", false),
	}, pk("WORKFLOW_PLUGIN_REFERENCES_PK", "WORKFLOW_VERSION_ID", "PLUGIN_VERSION_ID"), fk("WORKFLOW_PLUGIN_REFERENCES_WORKFLOW_FK", "WORKFLOW_VERSIONS", "WORKFLOW_VERSIONS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "WORKFLOW_VERSION_ID"), fk("WORKFLOW_PLUGIN_REFERENCES_GRANT_FK", "PLUGIN_GRANTS", "PLUGIN_GRANTS_REFERENCE_UK", "TENANT_ID", "WORKSPACE_ID", "GRANT_ID", "PLUGIN_VERSION_ID"))
}

func applicationsNotificationTable() table {
	return nativeTable("TERMINAL_NOTIFICATIONS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("WORKFLOW_ID", false), uuid("RUN_ID", false), uuid("RECIPIENT_ID", false), text("OUTCOME", 16, false), text("ERROR_CODE", 100, true), applicationsStamp("OCCURRED_AT", false, true), applicationsStamp("READ_AT", true, false),
	}, pk("TERMINAL_NOTIFICATIONS_PK", "ID"), unique("TERMINAL_NOTIFICATIONS_RUN_UK", "RUN_ID"), fk("TERMINAL_NOTIFICATIONS_RUN_FK", "WORKFLOW_RUNS", "WORKFLOW_RUNS_RECIPIENT_UK", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "RUN_ID", "RECIPIENT_ID"),
		check("TERMINAL_NOTIFICATIONS_OUTCOME", `"OUTCOME" IN ('succeeded','failed','cancelled','interrupted')`), check("TERMINAL_NOTIFICATIONS_ERROR_CODE", `"ERROR_CODE" IS NULL OR REGEXP_LIKE("ERROR_CODE", '^[A-Z][A-Z0-9_]{0,99}$', 'c')`), check("TERMINAL_NOTIFICATIONS_ERROR_STATE", `("OUTCOME"='succeeded' AND "ERROR_CODE" IS NULL) OR ("OUTCOME"<>'succeeded' AND "ERROR_CODE" IS NOT NULL)`), check("TERMINAL_NOTIFICATIONS_READ_TIME", `"READ_AT" IS NULL OR "READ_AT">="OCCURRED_AT"`))
}

// Fixed schema column names only. Explicit null branches avoid SQL UNKNOWN
// accepting a change between a null and nonnull immutable field.
func applicationsChanged(fields ...string) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		n, o := ":NEW."+quoted(field), ":OLD."+quoted(field)
		parts = append(parts, "("+n+"<>"+o+" OR ("+n+" IS NULL AND "+o+" IS NOT NULL) OR ("+n+" IS NOT NULL AND "+o+" IS NULL))")
	}
	return strings.Join(parts, " OR ")
}

func applicationsRunIdentityBody() string {
	changed := applicationsChanged("ID", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "OWNER_ID", "CONVERSATION_ID", "TURN_NUMBER", "VERSION_ID", "DRAFT_REVISION", "EXECUTION_AUTHORITY", "MEMBERSHIP_AUTHORITY", "REQUESTED_AUDIENCE", "GRAPH_SHA256", "DEADLINE_AT", "CREATED_AT", "RESULT_KIND")
	// Both CLOBs are mandatory. Compare the entire content, never an abbreviated
	// VARCHAR conversion or a prefix; NULL comparison results fail closed.
	for _, field := range []string{"GRAPH_SNAPSHOT", "QUESTION"} {
		compare := "DBMS_LOB.COMPARE(:NEW." + quoted(field) + ",:OLD." + quoted(field) + ")"
		changed += " OR " + compare + " IS NULL OR " + compare + "<>0"
	}
	return "BEGIN IF " + changed + " THEN RAISE_APPLICATION_ERROR(-20042,'WORKFLOW_RUN_IDENTITY_IMMUTABLE'); END IF; END;"
}

func applicationsPluginIdentityBody() string {
	changed := applicationsChanged("ID", "TENANT_ID", "PLUGIN_ID", "RELEASE_VERSION", "VERSION_MAJOR", "VERSION_MINOR", "VERSION_PATCH", "ABI", "EXECUTOR", "CAPSULE_SHA256", "MANIFEST_SHA256", "KEY_ID", "IMPORTED_BY", "CREATED_AT")
	for _, field := range []string{"CAPSULE", "MANIFEST"} {
		compare := "DBMS_LOB.COMPARE(:NEW." + quoted(field) + ",:OLD." + quoted(field) + ")"
		changed += " OR " + compare + " IS NULL OR " + compare + "<>0"
	}
	return "BEGIN IF " + changed + " THEN RAISE_APPLICATION_ERROR(-20046,'PLUGIN_PROVENANCE_IMMUTABLE'); END IF; END;"
}

func applicationsNoticeProvenanceBody(update bool) string {
	body := "DECLARE matched_count INTEGER; BEGIN "
	if update {
		body += "IF " + applicationsChanged("ID", "TENANT_ID", "WORKSPACE_ID", "WORKFLOW_ID", "RUN_ID", "RECIPIENT_ID", "OUTCOME", "ERROR_CODE", "OCCURRED_AT") + " THEN RAISE_APPLICATION_ERROR(-20043,'NOTIFICATION_PROVENANCE_IMMUTABLE'); END IF; "
		body += "IF :OLD.\"READ_AT\" IS NOT NULL AND (" + applicationsChanged("READ_AT") + ") THEN RAISE_APPLICATION_ERROR(-20044,'NOTIFICATION_READ_TIME_IMMUTABLE'); END IF; "
	}
	body += `SELECT COUNT(*) INTO matched_count FROM "WORKFLOW_RUNS" r WHERE r."TENANT_ID"=:NEW."TENANT_ID" AND r."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND r."WORKFLOW_ID"=:NEW."WORKFLOW_ID" AND r."ID"=:NEW."RUN_ID" AND r."OWNER_ID"=:NEW."RECIPIENT_ID" AND r."STATE"=:NEW."OUTCOME" AND ((r."ERROR_CODE"=:NEW."ERROR_CODE") OR (r."ERROR_CODE" IS NULL AND :NEW."ERROR_CODE" IS NULL)) AND r."FINISHED_AT" IS NOT NULL; IF matched_count<>1 THEN RAISE_APPLICATION_ERROR(-20045,'NOTIFICATION_RUN_PROVENANCE_REQUIRED'); END IF; END;`
	return body
}

func applicationsNoticeProducerBody() string {
	// A run already has a canonical application-generated UUID and at most one
	// notice. Reusing that UUID for the notice avoids an assumed native UUID
	// function, has no new data input and preserves stable retry/readback identity.
	return `DECLARE existing_count INTEGER; matched_count INTEGER; BEGIN
 IF :OLD."STATE" IN ('pending','running') AND :NEW."STATE" IN ('succeeded','failed','cancelled','interrupted') THEN
  SELECT COUNT(*) INTO existing_count FROM "TERMINAL_NOTIFICATIONS" WHERE "RUN_ID"=:NEW."ID";
  IF existing_count=0 THEN
   INSERT INTO "TERMINAL_NOTIFICATIONS" ("ID","TENANT_ID","WORKSPACE_ID","WORKFLOW_ID","RUN_ID","RECIPIENT_ID","OUTCOME","ERROR_CODE") VALUES (:NEW."ID",:NEW."TENANT_ID",:NEW."WORKSPACE_ID",:NEW."WORKFLOW_ID",:NEW."ID",:NEW."OWNER_ID",:NEW."STATE",:NEW."ERROR_CODE");
  ELSE
   SELECT COUNT(*) INTO matched_count FROM "TERMINAL_NOTIFICATIONS" n WHERE n."TENANT_ID"=:NEW."TENANT_ID" AND n."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND n."WORKFLOW_ID"=:NEW."WORKFLOW_ID" AND n."RUN_ID"=:NEW."ID" AND n."RECIPIENT_ID"=:NEW."OWNER_ID" AND n."OUTCOME"=:NEW."STATE" AND ((n."ERROR_CODE"=:NEW."ERROR_CODE") OR (n."ERROR_CODE" IS NULL AND :NEW."ERROR_CODE" IS NULL));
   IF matched_count<>1 THEN RAISE_APPLICATION_ERROR(-20045,'NOTIFICATION_RUN_PROVENANCE_REQUIRED'); END IF;
  END IF;
 END IF;
END;`
}
