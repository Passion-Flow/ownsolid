package migrate

import "strings"

// datasetsFragment is the complete native upload/inspection/publication schema.
// It references only foundation USERS/WORKSPACES. Artifact ingestion references
// datasets from its own contribution; datasets does not depend on that owner.
// Registration, numbering, privileges and execution remain with the coordinator.
func datasetsFragment(current map[string]table) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "datasets_v2", Requires: []string{"foundation_v1"}}
	known := make(map[string]table, len(current)+7)
	for name, t := range current {
		known[name] = cloneTable(t)
	}
	for _, t := range []table{datasetsTable(), datasetsDraftTable(), datasetsRowTable(), datasetsHistoryTable(), datasetsJobTable(), datasetsAttemptTable(), datasetsVersionTable()} {
		after := cloneTable(t)
		f.Changes = append(f.Changes, schemaChange{ID: t.Name, Kind: "table", SQL: extensionTableSQL(t, known, nil), After: &after})
		known[t.Name] = cloneTable(t)
	}
	// The three cycles are internal: create all seven real tables first, then
	// add each FK in one separately journaled ALTER with its exact intermediate
	// Before/After shape. No constraint is silently omitted for cycle ordering.
	for _, key := range []constraint{
		fk("DATASET_DRAFT_CURRENT_JOB_FK", "DATASET_JOBS", "DATASET_JOB_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "ID", "CURRENT_JOB_ID"),
		fk("DATASET_DRAFT_PUBLISHED_FK", "DATASET_VERSIONS", "DATASET_VERSION_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "ID", "PUBLISHED_VERSION_ID"),
		fk("DATASET_DRAFT_PARENT_FK", "DATASET_VERSIONS", "DATASET_VERSION_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "PARENT_VERSION_ID"),
	} {
		before := cloneTable(known["DATASET_DRAFTS"])
		after := cloneTable(before)
		after.Constraints = append(after.Constraints, key)
		var columns []string
		for _, target := range known[key.Reference].Constraints {
			if target.Name == key.ReferenceKey {
				columns = target.Columns
			}
		}
		sql := "ALTER TABLE " + quoted(after.Name) + " ADD CONSTRAINT " + quoted(key.Name) + " FOREIGN KEY (" + names(key.Columns) + ") REFERENCES " + quoted(key.Reference) + " (" + names(columns) + ")"
		f.Changes = append(f.Changes, schemaChange{ID: key.Name, Kind: "alter", SQL: sql, Before: &before, After: &after})
		known[after.Name] = cloneTable(after)
	}
	for _, i := range []index{
		{Name: "DATASET_ACTIVE_NAME_IDX", Table: "DATASETS", Unique: true, Columns: []string{"ACTIVE_TENANT_ID", "ACTIVE_WORKSPACE_ID", "ACTIVE_NAME_KEY"}, Descending: []bool{false, false, false}},
		{Name: "DATASET_WORKSPACE_PAGE_IDX", Table: "DATASETS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, Descending: []bool{false, false, true, true}},
		{Name: "DATASET_DRAFT_PAGE_IDX", Table: "DATASET_DRAFTS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "CREATED_AT", "ID"}, Descending: []bool{false, false, false, true, true}},
		{Name: "DATASET_ROW_PAGE_IDX", Table: "DATASET_ROWS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "DRAFT_ID", "DELETED", "LINE_NUMBER", "ID"}, Descending: []bool{false, false, false, false, false, false}},
		{Name: "DATASET_JOB_CLAIM_IDX", Table: "DATASET_JOBS", Columns: []string{"STATE", "NOT_BEFORE", "CREATED_AT", "ID"}, Descending: []bool{false, false, false, false}},
		{Name: "DATASET_JOB_EXPIRED_IDX", Table: "DATASET_JOBS", Columns: []string{"STATE", "LEASE_EXPIRES_AT", "CREATED_AT", "ID"}, Descending: []bool{false, false, false, false}},
		{Name: "DATASET_VERSION_PAGE_IDX", Table: "DATASET_VERSIONS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "PUBLISHED_AT", "ID"}, Descending: []bool{false, false, false, true, true}},
	} {
		value := i
		f.Changes = append(f.Changes, schemaChange{ID: i.Name, Kind: "index", SQL: i.ddl(), Index: &value})
	}
	for _, guard := range datasetsGuards() {
		value := guard
		sql := "CREATE TRIGGER " + quoted(guard.Name) + " BEFORE " + guard.Event + " ON " + quoted(guard.Table) + " FOR EACH ROW " + guard.Body
		f.Changes = append(f.Changes, schemaChange{ID: guard.Name, Kind: "trigger", SQL: sql, Trigger: &value})
	}
	return f
}

func datasetsDigest(name, field string, width int) constraint {
	pattern := "^[a-f0-9]{64}$"
	if width == 32 {
		pattern = "^[a-f0-9]{32}$"
	}
	return check(name, quoted(field)+" IS NULL OR REGEXP_LIKE("+quoted(field)+",'"+pattern+"','c')")
}
func datasetsJSON(name, field, kind string, maximum string) constraint {
	return check(name, "JSON_TYPE("+quoted(field)+") IS NOT NULL AND JSON_TYPE("+quoted(field)+")='"+kind+"' AND OCTET_LENGTH("+quoted(field)+")<="+maximum)
}
func datasetsTable() table {
	// Native DM exempts a composite unique key only when every key component
	// is NULL. Keep the complete retired scope out of the active-name index.
	activeTenant := uuid("ACTIVE_TENANT_ID", true)
	activeTenant.Virtual = `CASE WHEN "RETIRED_AT" IS NULL THEN "TENANT_ID" ELSE NULL END`
	activeWorkspace := uuid("ACTIVE_WORKSPACE_ID", true)
	activeWorkspace.Virtual = `CASE WHEN "RETIRED_AT" IS NULL THEN "WORKSPACE_ID" ELSE NULL END`
	active := text("ACTIVE_NAME_KEY", 200, true)
	active.Virtual = `CASE WHEN "RETIRED_AT" IS NULL THEN "NAME_KEY" ELSE NULL END`
	return nativeTable("DATASETS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false),
		text("NAME", 100, false), text("NAME_KEY", 200, false), text("ORIGIN", 2000, false), text("LICENSE", 500, false),
		col("RIGHTS_ASSERTED", "BIT", 0, false, ""), uuid("CREATED_BY", false), version("REVISION"), version("NEXT_VERSION"),
		stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true), stamp("RETIRED_AT", true, false), text("RETIRE_REASON", 500, true),
		uuid("CREATION_KEY", false), col("CREATION_FINGERPRINT", "VARBINARY", 32, false, ""), activeTenant, activeWorkspace, active,
	}, pk("DATASETS_PK", "ID"), unique("DATASET_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"),
		unique("DATASET_CREATION_UK", "TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "CREATION_KEY"),
		fk("DATASET_WORKSPACE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), fk("DATASET_CREATOR_FK", "USERS", "USERS_PK", "CREATED_BY"),
		bounded("DATASET_REVISION", "REVISION"), bounded("DATASET_NEXT_VERSION", "NEXT_VERSION"),
		check("DATASET_TEXT", `LENGTH("NAME")>0 AND LENGTH("NAME_KEY")>0 AND LENGTH("ORIGIN")>0 AND LENGTH("LICENSE")>0`),
		check("DATASET_RIGHTS", `"RIGHTS_ASSERTED"=1`), check("DATASET_FINGERPRINT", `OCTET_LENGTH("CREATION_FINGERPRINT")=32`),
		check("DATASET_RETIREMENT", `("RETIRED_AT" IS NULL AND "RETIRE_REASON" IS NULL) OR ("RETIRED_AT" IS NOT NULL AND "RETIRE_REASON" IS NOT NULL AND LENGTH("RETIRE_REASON")>0)`))
}
func datasetsDraftTable() table {
	return nativeTable("DATASET_DRAFTS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("DATASET_ID", false), uuid("CREATED_BY", false),
		text("REQUESTED_AUDIENCE", 8, false), col("REQUESTED_EPOCH", "BIGINT", 0, false, ""), uuid("MEMBERSHIP_AUTHORITY", true), uuid("PARENT_VERSION_ID", true),
		uuid("SOURCE_OBJECT_ID", false), text("SOURCE_NAME", 255, false), text("SOURCE_SHA256", 64, false), col("EXPECTED_BYTES", "BIGINT", 0, false, ""),
		stamp("SOURCE_RECEIVED_AT", true, false), uuid("UPLOAD_ATTEMPT_ID", true), stamp("UPLOAD_EXPIRES_AT", true, false), text("STATE", 16, false), version("REVISION"),
		col("TOTAL_ROWS", "INTEGER", 0, false, "0"), col("INVALID_ROWS", "INTEGER", 0, false, "0"), stamp("VALIDATION_COMPLETED_AT", true, false),
		col("FATAL_SOURCE_ERROR", "BIT", 0, false, "0"), col("VALIDATION_ERRORS", "CLOB", 0, false, "'[]'"), col("ERRORS_TRUNCATED", "BIT", 0, false, "0"),
		text("ERROR_CODE", 80, true), col("ERROR_LINE_NUMBER", "INTEGER", 0, true, ""), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true),
		uuid("CREATION_KEY", false), col("CREATION_FINGERPRINT", "VARBINARY", 32, false, ""), uuid("CURRENT_JOB_ID", true), uuid("PUBLISHED_VERSION_ID", true),
	}, pk("DATASET_DRAFTS_PK", "ID"), unique("DATASET_DRAFT_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "ID"),
		unique("DATASET_DRAFT_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("DATASET_DRAFT_CREATION_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "CREATED_BY", "CREATION_KEY"),
		fk("DATASET_DRAFT_DATASET_FK", "DATASETS", "DATASET_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID"), fk("DATASET_DRAFT_CREATOR_FK", "USERS", "USERS_PK", "CREATED_BY"),
		bounded("DATASET_DRAFT_REVISION", "REVISION"), check("DATASET_DRAFT_SOURCE_NAME", `LENGTH("SOURCE_NAME")>0`), datasetsDigest("DATASET_DRAFT_SOURCE_SHA", "SOURCE_SHA256", 64),
		check("DATASET_DRAFT_SOURCE_BYTES", `"EXPECTED_BYTES" BETWEEN 1 AND 67108864`), check("DATASET_DRAFT_AUDIENCE", `"REQUESTED_AUDIENCE" IN ('portal','admin') AND "REQUESTED_EPOCH">=0 AND ("REQUESTED_AUDIENCE"='admin' OR "MEMBERSHIP_AUTHORITY" IS NOT NULL)`),
		check("DATASET_DRAFT_STATE", `"STATE" IN ('uploading','validating','editing','publishing','published','failed','cancelled')`),
		check("DATASET_DRAFT_COUNTS", `"TOTAL_ROWS" BETWEEN 0 AND 10000 AND "INVALID_ROWS" BETWEEN 0 AND 10001 AND ("ERROR_LINE_NUMBER" IS NULL OR "ERROR_LINE_NUMBER" BETWEEN 0 AND 10001)`),
		datasetsJSON("DATASET_DRAFT_ERRORS", "VALIDATION_ERRORS", "ARRAY", "8388608"), check("DATASET_DRAFT_FINGERPRINT", `OCTET_LENGTH("CREATION_FINGERPRINT")=32`),
		check("DATASET_DRAFT_UPLOAD_LEASE", `("UPLOAD_ATTEMPT_ID" IS NULL AND "UPLOAD_EXPIRES_AT" IS NULL) OR ("UPLOAD_ATTEMPT_ID" IS NOT NULL AND "UPLOAD_EXPIRES_AT" IS NOT NULL)`),
		check("DATASET_DRAFT_RECEIVED", `"SOURCE_RECEIVED_AT" IS NOT NULL OR "STATE" IN ('uploading','failed','cancelled')`))
}
func datasetsRowTable() table {
	preview := text("RAW_PREVIEW", 4096, false)
	preview.Default = "''"
	return nativeTable("DATASET_ROWS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("DATASET_ID", false), uuid("DRAFT_ID", false),
		col("LINE_NUMBER", "INTEGER", 0, false, ""), version("REVISION"), text("SOURCE_ID", 512, true), col("PROMPT", "CLOB", 0, true, ""), col("COMPLETION", "CLOB", 0, true, ""),
		text("ERROR_CODE", 80, true), preview, col("PREVIEW_REPLACED", "BIT", 0, false, "0"), col("DELETED", "BIT", 0, false, "0"),
		stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true),
	}, pk("DATASET_ROWS_PK", "ID"), unique("DATASET_ROW_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DRAFT_ID", "ID"), unique("DATASET_ROW_LINE_UK", "DRAFT_ID", "LINE_NUMBER"),
		fk("DATASET_ROW_DRAFT_FK", "DATASET_DRAFTS", "DATASET_DRAFT_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DRAFT_ID"),
		bounded("DATASET_ROW_REVISION", "REVISION"), check("DATASET_ROW_LINE", `"LINE_NUMBER" BETWEEN 1 AND 10000`),
		check("DATASET_ROW_SOURCE", `"SOURCE_ID" IS NULL OR OCTET_LENGTH("SOURCE_ID") BETWEEN 1 AND 512`),
		check("DATASET_ROW_PROMPT", `"PROMPT" IS NULL OR OCTET_LENGTH("PROMPT") BETWEEN 1 AND 32768`),
		check("DATASET_ROW_COMPLETION", `"COMPLETION" IS NULL OR OCTET_LENGTH("COMPLETION") BETWEEN 1 AND 32768`), check("DATASET_ROW_PREVIEW", `OCTET_LENGTH("RAW_PREVIEW")<=4096`),
		check("DATASET_ROW_VALIDITY", `("ERROR_CODE" IS NULL AND "SOURCE_ID" IS NOT NULL AND "PROMPT" IS NOT NULL AND "COMPLETION" IS NOT NULL) OR ("ERROR_CODE" IS NOT NULL AND "SOURCE_ID" IS NULL AND "PROMPT" IS NULL AND "COMPLETION" IS NULL)`))
}
func datasetsHistoryTable() table {
	return nativeTable("DATASET_ROW_HISTORY", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("DATASET_ID", false), uuid("DRAFT_ID", false), uuid("ROW_ID", false),
		col("REVISION", "BIGINT", 0, false, ""), uuid("ACTOR_USER_ID", true), text("ACTION", 16, false), text("CONTENT_SHA256", 64, true), text("REASON", 500, false), stamp("OCCURRED_AT", false, true),
	}, pk("DATASET_ROW_HISTORY_PK", "TENANT_ID", "WORKSPACE_ID", "DRAFT_ID", "ROW_ID", "REVISION"),
		fk("DATASET_HISTORY_ROW_FK", "DATASET_ROWS", "DATASET_ROW_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DRAFT_ID", "ROW_ID"), fk("DATASET_HISTORY_ACTOR_FK", "USERS", "USERS_PK", "ACTOR_USER_ID"),
		bounded("DATASET_HISTORY_REVISION", "REVISION"), check("DATASET_HISTORY_ACTION", `"ACTION" IN ('imported','appended','replaced','deleted')`),
		datasetsDigest("DATASET_HISTORY_DIGEST", "CONTENT_SHA256", 64), check("DATASET_HISTORY_REASON", `LENGTH("REASON")>0`))
}
func datasetsJobTable() table {
	return nativeTable("DATASET_JOBS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("DATASET_ID", false), uuid("DRAFT_ID", false),
		text("KIND", 8, false), uuid("REQUESTED_BY", false), text("REQUESTED_AUDIENCE", 8, false), col("REQUESTED_EPOCH", "BIGINT", 0, false, ""), uuid("MEMBERSHIP_AUTHORITY", true),
		text("REASON", 500, false), col("EXPECTED_REVISION", "BIGINT", 0, false, ""), text("STATE", 12, false), uuid("VERSION_ID", true), text("INPUT_SHA256", 64, true),
		col("INPUT_POLICY", "CLOB", 0, false, ""), text("SPLIT_SEED", 32, true), col("VALIDATION_BASIS_POINTS", "INTEGER", 0, true, ""), col("ATTEMPT_COUNT", "INTEGER", 0, false, "0"),
		uuid("ATTEMPT_ID", true), stamp("LEASE_EXPIRES_AT", true, false), stamp("CANCEL_REQUESTED_AT", true, false), uuid("CANCEL_REQUESTED_BY", true),
		text("ERROR_CODE", 80, true), col("ERROR_LINE_NUMBER", "INTEGER", 0, true, ""), col("RETRYABLE", "BIT", 0, false, "0"), uuid("RETRY_OF", true),
		stamp("NOT_BEFORE", false, true), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true), stamp("COMPLETED_AT", true, false), uuid("CREATION_KEY", false), col("CREATION_FINGERPRINT", "VARBINARY", 32, false, ""),
	}, pk("DATASET_JOBS_PK", "ID"), unique("DATASET_JOB_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DRAFT_ID", "ID"), unique("DATASET_JOB_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"),
		unique("DATASET_JOB_CREATION_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DRAFT_ID", "REQUESTED_BY", "CREATION_KEY"),
		fk("DATASET_JOB_DRAFT_FK", "DATASET_DRAFTS", "DATASET_DRAFT_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DRAFT_ID"), fk("DATASET_JOB_RETRY_FK", "DATASET_JOBS", "DATASET_JOB_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "RETRY_OF"),
		fk("DATASET_JOB_REQUESTER_FK", "USERS", "USERS_PK", "REQUESTED_BY"), fk("DATASET_JOB_CANCEL_ACTOR_FK", "USERS", "USERS_PK", "CANCEL_REQUESTED_BY"),
		bounded("DATASET_JOB_REVISION", "EXPECTED_REVISION"), check("DATASET_JOB_REASON", `LENGTH("REASON")>0`),
		check("DATASET_JOB_KIND", `"KIND" IN ('import','publish')`), check("DATASET_JOB_STATE", `"STATE" IN ('pending','running','succeeded','failed','cancelled')`),
		check("DATASET_JOB_AUDIENCE", `"REQUESTED_AUDIENCE" IN ('portal','admin') AND "REQUESTED_EPOCH">=0 AND ("REQUESTED_AUDIENCE"='admin' OR "MEMBERSHIP_AUTHORITY" IS NOT NULL)`),
		datasetsDigest("DATASET_JOB_INPUT_SHA", "INPUT_SHA256", 64), datasetsDigest("DATASET_JOB_SEED", "SPLIT_SEED", 32), datasetsJSON("DATASET_JOB_POLICY", "INPUT_POLICY", "OBJECT", "16384"),
		check("DATASET_JOB_ATTEMPTS", `"ATTEMPT_COUNT" BETWEEN 0 AND 5`), check("DATASET_JOB_ERROR_LINE", `"ERROR_LINE_NUMBER" IS NULL OR "ERROR_LINE_NUMBER" BETWEEN 0 AND 10001`),
		check("DATASET_JOB_SPLIT", `"VALIDATION_BASIS_POINTS" IS NULL OR "VALIDATION_BASIS_POINTS" BETWEEN 100 AND 5000`), check("DATASET_JOB_FINGERPRINT", `OCTET_LENGTH("CREATION_FINGERPRINT")=32`),
		check("DATASET_JOB_VERSION_KIND", `("KIND"='publish' AND "VERSION_ID" IS NOT NULL AND "SPLIT_SEED" IS NOT NULL AND "VALIDATION_BASIS_POINTS" IS NOT NULL) OR ("KIND"='import' AND "VERSION_ID" IS NULL AND "SPLIT_SEED" IS NULL AND "VALIDATION_BASIS_POINTS" IS NULL)`),
		check("DATASET_JOB_LEASE", `("STATE"='running' AND "ATTEMPT_ID" IS NOT NULL AND "LEASE_EXPIRES_AT" IS NOT NULL AND "COMPLETED_AT" IS NULL) OR ("STATE"='pending' AND "LEASE_EXPIRES_AT" IS NULL AND "COMPLETED_AT" IS NULL) OR ("STATE" IN ('succeeded','failed','cancelled') AND "LEASE_EXPIRES_AT" IS NULL AND "COMPLETED_AT" IS NOT NULL)`),
		check("DATASET_JOB_CANCEL_PAIR", `("CANCEL_REQUESTED_AT" IS NULL AND "CANCEL_REQUESTED_BY" IS NULL) OR ("CANCEL_REQUESTED_AT" IS NOT NULL AND "CANCEL_REQUESTED_BY" IS NOT NULL)`))
}
func datasetsAttemptTable() table {
	return nativeTable("DATASET_JOB_ATTEMPTS", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("DATASET_ID", false), uuid("DRAFT_ID", false), uuid("JOB_ID", false), uuid("ID", false),
		col("ATTEMPT_NUMBER", "INTEGER", 0, false, ""), text("STATE", 12, false), stamp("STARTED_AT", false, true), stamp("FINISHED_AT", true, false), text("ERROR_CODE", 80, true), text("MEMORY_ENFORCEMENT", 100, true),
	}, pk("DATASET_ATTEMPTS_PK", "TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"), unique("DATASET_ATTEMPT_NUMBER_UK", "JOB_ID", "ATTEMPT_NUMBER"),
		fk("DATASET_ATTEMPT_JOB_FK", "DATASET_JOBS", "DATASET_JOB_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DRAFT_ID", "JOB_ID"),
		check("DATASET_ATTEMPT_NUMBER", `"ATTEMPT_NUMBER" BETWEEN 1 AND 5`), check("DATASET_ATTEMPT_STATE", `"STATE" IN ('running','succeeded','failed','cancelled','expired')`),
		check("DATASET_ATTEMPT_FINISHED", `("STATE"='running' AND "FINISHED_AT" IS NULL) OR ("STATE"<>'running' AND "FINISHED_AT" IS NOT NULL)`))
}
func datasetsVersionTable() table {
	return nativeTable("DATASET_VERSIONS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("DATASET_ID", false), uuid("DRAFT_ID", false), uuid("JOB_ID", false), uuid("ATTEMPT_ID", false),
		col("ORDINAL", "BIGINT", 0, false, ""), uuid("PARENT_VERSION_ID", true), uuid("PUBLISHED_BY", false), stamp("PUBLISHED_AT", false, true),
		text("RAW_SOURCE_SHA256", 64, false), text("EFFECTIVE_SOURCE_SHA256", 64, false), text("MANIFEST_SHA256", 64, false), col("MANIFEST", "CLOB", 0, false, ""), text("ROW_MAPPING_SHA256", 64, false),
		stamp("RETIRED_AT", true, false), text("RETIRE_REASON", 500, true),
	}, pk("DATASET_VERSIONS_PK", "ID"), unique("DATASET_VERSION_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "ID"), unique("DATASET_VERSION_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DRAFT_ID", "ID"),
		unique("DATASET_VERSION_ORDINAL_UK", "DATASET_ID", "ORDINAL"), unique("DATASET_VERSION_JOB_UK", "JOB_ID"),
		fk("DATASET_VERSION_JOB_FK", "DATASET_JOBS", "DATASET_JOB_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DRAFT_ID", "JOB_ID"),
		fk("DATASET_VERSION_ATTEMPT_FK", "DATASET_JOB_ATTEMPTS", "DATASET_ATTEMPTS_PK", "TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ATTEMPT_ID"),
		fk("DATASET_VERSION_PARENT_FK", "DATASET_VERSIONS", "DATASET_VERSION_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "PARENT_VERSION_ID"), fk("DATASET_VERSION_PUBLISHER_FK", "USERS", "USERS_PK", "PUBLISHED_BY"),
		bounded("DATASET_VERSION_ORDINAL", "ORDINAL"), datasetsDigest("DATASET_VERSION_RAW_SHA", "RAW_SOURCE_SHA256", 64), datasetsDigest("DATASET_VERSION_EFFECTIVE_SHA", "EFFECTIVE_SOURCE_SHA256", 64),
		datasetsDigest("DATASET_VERSION_MANIFEST_SHA", "MANIFEST_SHA256", 64), datasetsDigest("DATASET_VERSION_MAPPING_SHA", "ROW_MAPPING_SHA256", 64), datasetsJSON("DATASET_VERSION_MANIFEST", "MANIFEST", "OBJECT", "8388608"),
		check("DATASET_VERSION_RETIREMENT", `("RETIRED_AT" IS NULL AND "RETIRE_REASON" IS NULL) OR ("RETIRED_AT" IS NOT NULL AND "RETIRE_REASON" IS NOT NULL AND LENGTH("RETIRE_REASON")>0)`))
}

func datasetsChanged(fields ...string) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		n, o := ":NEW."+quoted(field), ":OLD."+quoted(field)
		parts = append(parts, "(("+n+" IS NULL AND "+o+" IS NOT NULL) OR ("+n+" IS NOT NULL AND "+o+" IS NULL) OR ("+n+" IS NOT NULL AND "+o+" IS NOT NULL AND "+n+"<>"+o+"))")
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}
func datasetsGuards() []trigger {
	identity := datasetsChanged("ID", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DRAFT_ID", "JOB_ID", "ATTEMPT_ID", "ORDINAL", "PARENT_VERSION_ID", "PUBLISHED_BY", "PUBLISHED_AT", "RAW_SOURCE_SHA256", "EFFECTIVE_SOURCE_SHA256", "MANIFEST_SHA256", "ROW_MAPPING_SHA256")
	retired := datasetsChanged("RETIRED_AT", "RETIRE_REASON")
	body := "BEGIN IF " + identity + ` OR :NEW."MANIFEST" IS NULL OR :OLD."MANIFEST" IS NULL OR COALESCE(DBMS_LOB.COMPARE(:NEW."MANIFEST",:OLD."MANIFEST"),1)<>0 OR (:OLD."RETIRED_AT" IS NOT NULL AND ` + retired + `) THEN RAISE_APPLICATION_ERROR(-20008,'DATASET_VERSION_IMMUTABLE'); END IF; END;`
	return []trigger{
		{Name: "DATASET_VERSION_UPDATE_GUARD", Table: "DATASET_VERSIONS", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: body},
		{Name: "DATASET_VERSION_DELETE_GUARD", Table: "DATASET_VERSIONS", Event: "DELETE", Timing: "BEFORE EACH ROW", Body: `BEGIN RAISE_APPLICATION_ERROR(-20008,'DATASET_VERSION_IMMUTABLE'); END;`},
		{Name: "DATASET_HISTORY_UPDATE_GUARD", Table: "DATASET_ROW_HISTORY", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: `BEGIN RAISE_APPLICATION_ERROR(-20009,'DATASET_HISTORY_IMMUTABLE'); END;`},
		{Name: "DATASET_HISTORY_DELETE_GUARD", Table: "DATASET_ROW_HISTORY", Event: "DELETE", Timing: "BEFORE EACH ROW", Body: `BEGIN RAISE_APPLICATION_ERROR(-20009,'DATASET_HISTORY_IMMUTABLE'); END;`},
	}
}
