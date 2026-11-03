package migrate

import "strings"

// Register both fragments. The second is deliberately after datasets_v2 and
// models_v2: those owners may reference ARTIFACT_OBJECTS in their first phase.
// Neither this file nor the first fragment alone activates a serving catalog.
func artifactStoresFragment(current map[string]table) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "artifactstores_v2", Requires: []string{"foundation_v1"}}
	known := artifactStoresProjection(current)
	for _, t := range artifactStoresTables() {
		v := t
		f.Changes = append(f.Changes, schemaChange{ID: v.Name, Kind: "table", SQL: extensionTableSQL(v, known, nil), After: &v})
		known[v.Name] = cloneTable(v)
	}
	for _, v := range []index{
		{Name: "ARTIFACT_OBJECTS_SCOPE_PAGE", Table: "ARTIFACT_OBJECTS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, Descending: []bool{false, false, true, true}},
		{Name: "ARTIFACT_GRANT_RECIPIENT", Table: "ARTIFACT_OBJECT_GRANTS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "RECIPIENT_USER_ID", "OBJECT_ID"}, Descending: []bool{false, false, false, false}},
	} {
		i := v
		f.Changes = append(f.Changes, schemaChange{ID: i.Name, Kind: "index", SQL: i.ddl(), Index: &i})
	}
	for _, tr := range artifactStoresCoreTriggers() {
		f.Changes = append(f.Changes, artifactStoresTriggerChange(tr))
	}
	return f
}

// This mandatory later fragment closes the three cross-lane FKs with exact
// intermediate table shapes. Missing scoped unique keys fail catalog validation;
// they are never substituted with a global ID-only reference.
func artifactStoresTargetsFragment(current map[string]table) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "artifactstores_targets_v2", Requires: []string{"artifactstores_v2", "datasets_v2", "models_v2"}}
	known := artifactStoresProjection(current)
	for _, relation := range []struct {
		name, target  string
		local, remote []string
	}{
		{"ARTIFACT_INGESTIONS_DATASET_FK", "DATASETS", []string{"TENANT_ID", "WORKSPACE_ID", "TARGET_DATASET_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"}},
		{"ARTIFACT_INGESTIONS_MODEL_FK", "MODEL_IMPORTS", []string{"TENANT_ID", "WORKSPACE_ID", "MODEL_IMPORT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"}},
		{"ARTIFACT_INGESTIONS_DRAFT_FK", "DATASET_DRAFTS", []string{"TENANT_ID", "WORKSPACE_ID", "TARGET_DATASET_ID", "DATASET_DRAFT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "ID"}},
	} {
		before := cloneTable(known["ARTIFACT_OBJECT_INGESTIONS"])
		after := cloneTable(before)
		key := artifactStoresReferenceKey(known[relation.target], relation.remote)
		after.Constraints = append(after.Constraints, fk(relation.name, relation.target, key, relation.local...))
		sql := "ALTER TABLE \"ARTIFACT_OBJECT_INGESTIONS\" ADD CONSTRAINT " + quoted(relation.name) + " FOREIGN KEY (" + names(relation.local) + ") REFERENCES " + quoted(relation.target) + " (" + names(relation.remote) + ")"
		f.Changes = append(f.Changes, schemaChange{ID: relation.name, Kind: "alter", SQL: sql, Before: &before, After: &after})
		known[after.Name] = cloneTable(after)
	}
	for _, tr := range artifactStoresIngestionTriggers(known["ARTIFACT_OBJECT_INGESTIONS"]) {
		f.Changes = append(f.Changes, artifactStoresTriggerChange(tr))
	}
	return f
}

func artifactStoresProjection(current map[string]table) map[string]table {
	out := make(map[string]table, len(current))
	for name, v := range current {
		out[name] = cloneTable(v)
	}
	return out
}

func artifactStoresReferenceKey(t table, columns []string) string {
	for _, c := range t.Constraints {
		if (c.Kind == "P" || c.Kind == "U") && strings.Join(c.Columns, ",") == strings.Join(columns, ",") {
			return c.Name
		}
	}
	return "ARTIFACT_REQUIRED_SCOPED_KEY_MISSING"
}

func artifactStoresStamp(name string, nullable, defaultNow bool) column {
	c := stamp(name, nullable, false)
	if defaultNow {
		c.Default = "SYS_EXTRACT_UTC(SYSTIMESTAMP(6))"
	}
	return c
}

func artifactStoresTable(name string, columns []column, constraints ...constraint) table {
	t := nativeTable(name, columns, constraints...)
	for _, c := range columns {
		if c.Type == "CHAR" && c.Width == 36 {
			t.Constraints = append(t.Constraints, check(name+"_"+c.Name+"_NONZERO", quoted(c.Name)+` IS NULL OR `+quoted(c.Name)+`<>'00000000-0000-0000-0000-000000000000'`))
		}
		if c.Type == "TIMESTAMP" {
			t.Constraints = append(t.Constraints, check(name+"_"+c.Name+"_RANGE", quoted(c.Name)+` IS NULL OR (`+quoted(c.Name)+`>=TIMESTAMP '1970-01-01 00:00:00' AND `+quoted(c.Name)+`<=TIMESTAMP '9999-12-31 23:59:59.999999')`))
		}
	}
	return t
}

func artifactStoresTables() []table {
	return []table{
		artifactStoresTable("ARTIFACT_OBJECTS", []column{
			uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("CREATED_BY", false), text("REQUESTED_AUDIENCE", 8, false),
			col("REQUESTED_EPOCH", "BIGINT", 0, false, ""), uuid("MEMBERSHIP_AUTHORITY", true), col("EXECUTION_EPOCH", "BIGINT", 0, false, ""), uuid("EXECUTION_MEMBERSHIP", true),
			uuid("CREATION_KEY", false), col("FINGERPRINT", "VARBINARY", 32, false, ""), uuid("STORE_ID", false), text("NAME", 200, false), text("PURPOSE", 24, false),
			col("EXPECTED_BYTES", "BIGINT", 0, false, ""), col("SHA256", "CHAR", 64, false, ""), col("PART_BYTES", "BIGINT", 0, false, ""), text("STATE", 16, false),
			text("REMOTE_UPLOAD_ID", 4096, true), text("REMOTE_ETAG", 1024, true), uuid("OPERATION_ID", true), text("OPERATION_KIND", 16, true), text("OPERATION_PHASE", 16, true),
			artifactStoresStamp("OPERATION_EXPIRES_AT", true, false), col("OPERATION_AUTHORITY", "CLOB", 0, true, ""), text("ERROR_CODE", 64, true), version("REVISION"),
			artifactStoresStamp("UPLOAD_EXPIRES_AT", false, false), artifactStoresStamp("EXPIRES_AT", false, false), artifactStoresStamp("CREATED_AT", false, true), artifactStoresStamp("UPDATED_AT", false, true),
		}, pk("ARTIFACT_OBJECTS_PK", "ID"), unique("ARTIFACT_OBJECTS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("ARTIFACT_OBJECTS_CREATION_UK", "TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "CREATION_KEY"),
			fk("ARTIFACT_OBJECTS_CREATOR_FK", "USERS", "USERS_PK", "CREATED_BY"), fk("ARTIFACT_OBJECTS_SCOPE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"),
			check("ARTIFACT_OBJECTS_AUTHORITY", `"REQUESTED_AUDIENCE" IN ('admin','portal') AND "REQUESTED_EPOCH">0 AND "EXECUTION_EPOCH">0`),
			check("ARTIFACT_OBJECTS_FINGERPRINT", `OCTET_LENGTH("FINGERPRINT")=32`), check("ARTIFACT_OBJECTS_PURPOSE", `"PURPOSE" IN ('model_bundle','dataset_source')`),
			check("ARTIFACT_OBJECTS_CONTENT", `"EXPECTED_BYTES" BETWEEN 1 AND 5497558138880 AND "PART_BYTES" BETWEEN 5242880 AND 5368709120 AND "EXPECTED_BYTES"<="PART_BYTES"*10000 AND REGEXP_LIKE("SHA256",'^[a-f0-9]{64}$','c')`),
			check("ARTIFACT_OBJECTS_STATE", `"STATE" IN ('creating','uploading','completing','ready','deleting','deleted','aborting','aborted') AND ("STATE"<>'ready' OR "REMOTE_ETAG" IS NOT NULL)`),
			check("ARTIFACT_OBJECTS_OPERATION", `("OPERATION_ID" IS NULL AND "OPERATION_KIND" IS NULL AND "OPERATION_PHASE" IS NULL AND "OPERATION_EXPIRES_AT" IS NULL AND "OPERATION_AUTHORITY" IS NULL) OR ("OPERATION_ID" IS NOT NULL AND "OPERATION_KIND" IS NOT NULL AND "OPERATION_KIND" IN ('create','part','complete','delete','abort','ingest') AND "OPERATION_PHASE" IS NOT NULL AND "OPERATION_PHASE" IN ('prepared','sent') AND "OPERATION_EXPIRES_AT" IS NOT NULL AND "OPERATION_AUTHORITY" IS NOT NULL)`),
			check("ARTIFACT_OBJECTS_ACTOR_JSON", `"OPERATION_AUTHORITY" IS NULL OR (JSON_TYPE("OPERATION_AUTHORITY") IS NOT NULL AND JSON_TYPE("OPERATION_AUTHORITY")='OBJECT')`),
			check("ARTIFACT_OBJECTS_LIFETIME", `"REVISION">0 AND "EXPIRES_AT">="UPLOAD_EXPIRES_AT"`)),
		artifactStoresTable("ARTIFACT_OBJECT_PARTS", []column{
			uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("OBJECT_ID", false), col("NUMBER", "INTEGER", 0, false, ""), col("BYTES", "BIGINT", 0, false, ""),
			col("SHA256", "CHAR", 64, false, ""), text("ETAG", 1024, true), text("STATE", 16, false), uuid("STAGE_ID", false),
		}, pk("ARTIFACT_OBJECT_PARTS_PK", "OBJECT_ID", "NUMBER"), fk("ARTIFACT_OBJECT_PARTS_SCOPE_FK", "ARTIFACT_OBJECTS", "ARTIFACT_OBJECTS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "OBJECT_ID"),
			check("ARTIFACT_OBJECT_PARTS_CONTENT", `"NUMBER" BETWEEN 1 AND 10000 AND "BYTES" BETWEEN 1 AND 5368709120 AND REGEXP_LIKE("SHA256",'^[a-f0-9]{64}$','c')`),
			check("ARTIFACT_OBJECT_PARTS_STATE", `("STATE"='intent' AND "ETAG" IS NULL) OR ("STATE"='uploaded' AND "ETAG" IS NOT NULL)`)),
		artifactStoresTable("ARTIFACT_OBJECT_GRANTS", []column{
			uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("OBJECT_ID", false), uuid("RECIPIENT_USER_ID", false), uuid("CREATED_BY", false),
			col("CREATOR_EPOCH", "BIGINT", 0, false, ""), uuid("CREATOR_MEMBERSHIP", true), col("RECIPIENT_EPOCH", "BIGINT", 0, false, ""), uuid("RECIPIENT_MEMBERSHIP", false),
			text("ACTION", 24, false), uuid("CREATION_KEY", false), col("FINGERPRINT", "VARBINARY", 32, false, ""),
			artifactStoresStamp("NOT_BEFORE", false, false), artifactStoresStamp("EXPIRES_AT", false, false), artifactStoresStamp("REVOKED_AT", true, false), artifactStoresStamp("CREATED_AT", false, true),
		}, pk("ARTIFACT_OBJECT_GRANTS_PK", "ID"), unique("ARTIFACT_OBJECT_GRANTS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "OBJECT_ID", "ID"), unique("ARTIFACT_OBJECT_GRANTS_CREATION_UK", "TENANT_ID", "WORKSPACE_ID", "OBJECT_ID", "CREATED_BY", "CREATION_KEY"),
			fk("ARTIFACT_OBJECT_GRANTS_SCOPE_FK", "ARTIFACT_OBJECTS", "ARTIFACT_OBJECTS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "OBJECT_ID"), fk("ARTIFACT_OBJECT_GRANTS_CREATOR_FK", "USERS", "USERS_PK", "CREATED_BY"), fk("ARTIFACT_OBJECT_GRANTS_RECIPIENT_FK", "USERS", "USERS_PK", "RECIPIENT_USER_ID"),
			check("ARTIFACT_OBJECT_GRANTS_ACTION", `"ACTION" IN ('read','ingest_model','ingest_dataset')`), check("ARTIFACT_OBJECT_GRANTS_FINGERPRINT", `OCTET_LENGTH("FINGERPRINT")=32`), check("ARTIFACT_OBJECT_GRANTS_LIFETIME", `"EXPIRES_AT">"NOT_BEFORE"`)),
		artifactStoresTable("ARTIFACT_OBJECT_INGESTIONS", []column{
			uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("OBJECT_ID", false), uuid("CREATED_BY", false), uuid("CREATION_KEY", false),
			col("FINGERPRINT", "VARBINARY", 32, false, ""), text("KIND", 16, false), uuid("MODEL_IMPORT_ID", true), uuid("DATASET_DRAFT_ID", true), uuid("TARGET_DATASET_ID", true), text("STATE", 16, false),
			col("SOURCE_VERIFIED_SHA256", "CHAR", 64, true, ""), artifactStoresStamp("SOURCE_VERIFIED_AT", true, false), col("INPUT", "CLOB", 0, false, ""), col("RECEIPT", "CLOB", 0, true, ""), text("ERROR_CODE", 64, true),
			artifactStoresStamp("CREATED_AT", false, true), artifactStoresStamp("UPDATED_AT", false, true),
		}, pk("ARTIFACT_OBJECT_INGESTIONS_PK", "ID"), unique("ARTIFACT_INGESTIONS_CREATION_UK", "TENANT_ID", "WORKSPACE_ID", "OBJECT_ID", "CREATED_BY", "CREATION_KEY"),
			fk("ARTIFACT_INGESTIONS_SCOPE_FK", "ARTIFACT_OBJECTS", "ARTIFACT_OBJECTS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "OBJECT_ID"), fk("ARTIFACT_INGESTIONS_CREATOR_FK", "USERS", "USERS_PK", "CREATED_BY"),
			check("ARTIFACT_INGESTIONS_FINGERPRINT", `OCTET_LENGTH("FINGERPRINT")=32`),
			check("ARTIFACT_INGESTIONS_KIND", `("KIND"='model' AND "DATASET_DRAFT_ID" IS NULL AND "TARGET_DATASET_ID" IS NULL) OR ("KIND"='dataset' AND "MODEL_IMPORT_ID" IS NULL AND "TARGET_DATASET_ID" IS NOT NULL)`),
			check("ARTIFACT_INGESTIONS_STATE", `"STATE" IN ('intent','running','submitted','failed') AND ("STATE"<>'submitted' OR ("KIND"='model' AND "MODEL_IMPORT_ID" IS NOT NULL) OR ("KIND"='dataset' AND "DATASET_DRAFT_ID" IS NOT NULL))`),
			check("ARTIFACT_INGESTIONS_INPUT", `JSON_TYPE("INPUT") IS NOT NULL AND JSON_TYPE("INPUT")='OBJECT' AND OCTET_LENGTH("INPUT")<=16384`),
			check("ARTIFACT_INGESTIONS_RECEIPT", `"RECEIPT" IS NULL OR (JSON_TYPE("RECEIPT") IS NOT NULL AND JSON_TYPE("RECEIPT")='OBJECT' AND OCTET_LENGTH("RECEIPT")<=16384)`),
			check("ARTIFACT_INGESTIONS_PROOF", `("SOURCE_VERIFIED_SHA256" IS NULL AND "SOURCE_VERIFIED_AT" IS NULL) OR ("SOURCE_VERIFIED_SHA256" IS NOT NULL AND "SOURCE_VERIFIED_AT" IS NOT NULL AND REGEXP_LIKE("SOURCE_VERIFIED_SHA256",'^[a-f0-9]{64}$','c'))`),
			check("ARTIFACT_INGESTIONS_SUBMITTED", `"STATE"<>'submitted' OR ("RECEIPT" IS NOT NULL AND "SOURCE_VERIFIED_SHA256" IS NOT NULL AND "SOURCE_VERIFIED_AT" IS NOT NULL)`)),
	}
}

func artifactStoresChanged(columns ...string) string {
	parts := []string{}
	for _, name := range columns {
		n, o := ":NEW."+quoted(name), ":OLD."+quoted(name)
		parts = append(parts, "(("+n+" IS NOT NULL AND "+o+" IS NOT NULL AND "+n+"<>"+o+") OR ("+n+" IS NULL AND "+o+" IS NOT NULL) OR ("+n+" IS NOT NULL AND "+o+" IS NULL))")
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func artifactStoresLOBChanged(name string) string {
	n, o := ":NEW."+quoted(name), ":OLD."+quoted(name)
	return "((" + n + " IS NOT NULL AND " + o + " IS NOT NULL AND NVL(DBMS_LOB.COMPARE(" + n + "," + o + "),1)<>0) OR (" + n + " IS NULL AND " + o + " IS NOT NULL) OR (" + n + " IS NOT NULL AND " + o + " IS NULL))"
}

func artifactStoresGuard(condition string) string {
	return "IF " + condition + " THEN RAISE_APPLICATION_ERROR(-20022,'OBJECT_STORED_PROVENANCE_INVALID'); END IF; "
}

func artifactStoresTriggerChange(v trigger) schemaChange {
	return schemaChange{ID: v.Name, Kind: "trigger", SQL: "CREATE TRIGGER " + quoted(v.Name) + " BEFORE " + v.Event + " ON " + quoted(v.Table) + " FOR EACH ROW " + v.Body, Trigger: &v}
}

func artifactStoresCoreTriggers() []trigger {
	objectIdentity := artifactStoresChanged("ID", "TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "REQUESTED_AUDIENCE", "REQUESTED_EPOCH", "MEMBERSHIP_AUTHORITY", "CREATION_KEY", "FINGERPRINT", "STORE_ID", "NAME", "PURPOSE", "EXPECTED_BYTES", "SHA256", "PART_BYTES", "CREATED_AT")
	objectTransition := `NOT (:NEW."STATE"=:OLD."STATE" OR (:OLD."STATE"='creating' AND :NEW."STATE" IN ('uploading','aborting')) OR (:OLD."STATE"='uploading' AND :NEW."STATE" IN ('completing','aborting')) OR (:OLD."STATE"='completing' AND :NEW."STATE" IN ('ready','aborting')) OR (:OLD."STATE"='ready' AND :NEW."STATE"='deleting') OR (:OLD."STATE"='deleting' AND :NEW."STATE" IN ('deleted','aborting')) OR (:OLD."STATE"='aborting' AND :NEW."STATE"='aborted'))`
	memberMismatch := `((:NEW."EXECUTION_MEMBERSHIP" IS NULL AND :NEW."MEMBERSHIP_AUTHORITY" IS NOT NULL) OR (:NEW."EXECUTION_MEMBERSHIP" IS NOT NULL AND :NEW."MEMBERSHIP_AUTHORITY" IS NULL) OR :NEW."EXECUTION_MEMBERSHIP"<>:NEW."MEMBERSHIP_AUTHORITY")`
	return []trigger{
		{"ARTIFACT_OBJECTS_INSERT_GUARD", "ARTIFACT_OBJECTS", "INSERT", "BEFORE EACH ROW", "BEGIN " + artifactStoresGuard(`:NEW."STATE"<>'creating' OR :NEW."REMOTE_UPLOAD_ID" IS NOT NULL OR :NEW."REMOTE_ETAG" IS NOT NULL OR :NEW."OPERATION_ID" IS NULL OR :NEW."OPERATION_KIND"<>'create' OR :NEW."OPERATION_PHASE"<>'prepared' OR :NEW."OPERATION_AUTHORITY" IS NULL OR :NEW."EXECUTION_EPOCH"<>:NEW."REQUESTED_EPOCH" OR `+memberMismatch) + "END;"},
		{"ARTIFACT_OBJECTS_UPDATE_GUARD", "ARTIFACT_OBJECTS", "UPDATE", "BEFORE EACH ROW", "BEGIN " + artifactStoresGuard(objectIdentity+` OR (:OLD."REMOTE_UPLOAD_ID" IS NOT NULL AND `+artifactStoresChanged("REMOTE_UPLOAD_ID")+`) OR (:OLD."REMOTE_ETAG" IS NOT NULL AND `+artifactStoresChanged("REMOTE_ETAG")+`) OR (:OLD."STATE" IN ('deleted','aborted') AND :NEW."OPERATION_ID" IS NOT NULL) OR :NEW."UPLOAD_EXPIRES_AT">:OLD."UPLOAD_EXPIRES_AT" OR :NEW."EXPIRES_AT">:OLD."EXPIRES_AT" OR :NEW."REVISION"<:OLD."REVISION" OR `+objectTransition) + "END;"},
		{"ARTIFACT_PARTS_INSERT_GUARD", "ARTIFACT_OBJECT_PARTS", "INSERT", "BEFORE EACH ROW", `DECLARE MATCHED INTEGER; BEGIN ` + artifactStoresGuard(`:NEW."STATE"<>'intent' OR :NEW."ETAG" IS NOT NULL`) + `SELECT COUNT(*) INTO MATCHED FROM "ARTIFACT_OBJECTS" O WHERE O."TENANT_ID"=:NEW."TENANT_ID" AND O."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND O."ID"=:NEW."OBJECT_ID" AND O."STATE"='uploading' AND O."REMOTE_UPLOAD_ID" IS NOT NULL AND O."OPERATION_ID" IS NULL AND (:NEW."NUMBER"-1)*O."PART_BYTES"<O."EXPECTED_BYTES" AND :NEW."BYTES"=LEAST(O."PART_BYTES",O."EXPECTED_BYTES"-(:NEW."NUMBER"-1)*O."PART_BYTES"); ` + artifactStoresGuard("MATCHED<>1") + "END;"},
		{"ARTIFACT_PARTS_UPDATE_GUARD", "ARTIFACT_OBJECT_PARTS", "UPDATE", "BEFORE EACH ROW", "BEGIN " + artifactStoresGuard(artifactStoresChanged("TENANT_ID", "WORKSPACE_ID", "OBJECT_ID", "NUMBER", "BYTES", "SHA256", "STAGE_ID")+` OR (:OLD."STATE"='uploaded' AND (:NEW."STATE"<>'uploaded' OR `+artifactStoresChanged("ETAG")+"))") + "END;"},
		{"ARTIFACT_GRANTS_INSERT_GUARD", "ARTIFACT_OBJECT_GRANTS", "INSERT", "BEFORE EACH ROW", `DECLARE MATCHED INTEGER; BEGIN ` + artifactStoresGuard(`:NEW."REVOKED_AT" IS NOT NULL`) + `SELECT COUNT(*) INTO MATCHED FROM "ARTIFACT_OBJECTS" O WHERE O."TENANT_ID"=:NEW."TENANT_ID" AND O."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND O."ID"=:NEW."OBJECT_ID" AND O."STATE"='ready' AND :NEW."EXPIRES_AT"<=O."EXPIRES_AT"; ` + artifactStoresGuard("MATCHED<>1") + "END;"},
		{"ARTIFACT_GRANTS_UPDATE_GUARD", "ARTIFACT_OBJECT_GRANTS", "UPDATE", "BEFORE EACH ROW", "BEGIN " + artifactStoresGuard(artifactStoresChanged("ID", "TENANT_ID", "WORKSPACE_ID", "OBJECT_ID", "RECIPIENT_USER_ID", "CREATED_BY", "CREATOR_EPOCH", "CREATOR_MEMBERSHIP", "RECIPIENT_EPOCH", "RECIPIENT_MEMBERSHIP", "ACTION", "CREATION_KEY", "FINGERPRINT", "NOT_BEFORE", "EXPIRES_AT", "CREATED_AT")+` OR (:OLD."REVOKED_AT" IS NOT NULL AND `+artifactStoresChanged("REVOKED_AT")+")") + "END;"},
	}
}

func artifactStoresIngestionTriggers(t table) []trigger {
	common := `IF :NEW."MODEL_IMPORT_ID" IS NOT NULL THEN
SELECT COUNT(*) INTO MATCHED FROM "MODEL_IMPORTS" M JOIN "ARTIFACT_OBJECTS" O ON O."TENANT_ID"=M."TENANT_ID" AND O."WORKSPACE_ID"=M."WORKSPACE_ID"
WHERE O."ID"=:NEW."OBJECT_ID" AND M."ID"=:NEW."MODEL_IMPORT_ID" AND M."TENANT_ID"=:NEW."TENANT_ID" AND M."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND M."REQUESTED_BY"=:NEW."CREATED_BY" AND M."EXPECTED_BYTES"=O."EXPECTED_BYTES";
` + artifactStoresGuard("MATCHED<>1") + `END IF;
IF :NEW."DATASET_DRAFT_ID" IS NOT NULL THEN
SELECT COUNT(*) INTO MATCHED FROM "DATASET_DRAFTS" D JOIN "ARTIFACT_OBJECTS" O ON O."TENANT_ID"=D."TENANT_ID" AND O."WORKSPACE_ID"=D."WORKSPACE_ID"
WHERE O."ID"=:NEW."OBJECT_ID" AND D."ID"=:NEW."DATASET_DRAFT_ID" AND D."TENANT_ID"=:NEW."TENANT_ID" AND D."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND D."DATASET_ID"=:NEW."TARGET_DATASET_ID" AND D."CREATED_BY"=:NEW."CREATED_BY" AND D."EXPECTED_BYTES"=O."EXPECTED_BYTES" AND D."SOURCE_SHA256"=O."SHA256";
` + artifactStoresGuard("MATCHED<>1") + `END IF;
IF :NEW."SOURCE_VERIFIED_SHA256" IS NOT NULL THEN
SELECT COUNT(*) INTO MATCHED FROM "ARTIFACT_OBJECTS" O WHERE O."TENANT_ID"=:NEW."TENANT_ID" AND O."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND O."ID"=:NEW."OBJECT_ID" AND O."SHA256"=:NEW."SOURCE_VERIFIED_SHA256";
` + artifactStoresGuard("MATCHED<>1") + "END IF; "
	all := []string{}
	for _, c := range t.Columns {
		if c.Type != "CLOB" {
			all = append(all, c.Name)
		}
	}
	terminalChanged := artifactStoresChanged(all...) + " OR " + artifactStoresLOBChanged("INPUT") + " OR " + artifactStoresLOBChanged("RECEIPT")
	identity := artifactStoresChanged("ID", "TENANT_ID", "WORKSPACE_ID", "OBJECT_ID", "CREATED_BY", "CREATION_KEY", "FINGERPRINT", "KIND", "TARGET_DATASET_ID", "CREATED_AT") + " OR " + artifactStoresLOBChanged("INPUT")
	update := identity + ` OR (:OLD."MODEL_IMPORT_ID" IS NOT NULL AND ` + artifactStoresChanged("MODEL_IMPORT_ID") + `) OR (:OLD."DATASET_DRAFT_ID" IS NOT NULL AND ` + artifactStoresChanged("DATASET_DRAFT_ID") + `) OR (:OLD."SOURCE_VERIFIED_SHA256" IS NOT NULL AND ` + artifactStoresChanged("SOURCE_VERIFIED_SHA256", "SOURCE_VERIFIED_AT") + `) OR (:OLD."STATE" IN ('submitted','failed') AND (` + terminalChanged + `)) OR NOT (:NEW."STATE"=:OLD."STATE" OR (:OLD."STATE"='intent' AND :NEW."STATE" IN ('running','failed')) OR (:OLD."STATE"='running' AND :NEW."STATE" IN ('submitted','failed')))`
	return []trigger{
		{"ARTIFACT_INGESTIONS_INSERT_GUARD", "ARTIFACT_OBJECT_INGESTIONS", "INSERT", "BEFORE EACH ROW", "DECLARE MATCHED INTEGER; BEGIN " + artifactStoresGuard(`:NEW."STATE"<>'intent' OR :NEW."MODEL_IMPORT_ID" IS NOT NULL OR :NEW."DATASET_DRAFT_ID" IS NOT NULL OR :NEW."SOURCE_VERIFIED_SHA256" IS NOT NULL OR :NEW."SOURCE_VERIFIED_AT" IS NOT NULL OR :NEW."RECEIPT" IS NOT NULL OR :NEW."ERROR_CODE" IS NOT NULL`) + common + "END;"},
		{"ARTIFACT_INGESTIONS_UPDATE_GUARD", "ARTIFACT_OBJECT_INGESTIONS", "UPDATE", "BEFORE EACH ROW", "DECLARE MATCHED INTEGER; BEGIN " + artifactStoresGuard(update) + common + "END;"},
	}
}
