package migrate

import "strings"

// Register only after the real datasets_v2 scoped keys exist. This contribution
// does not activate a catalog, run DDL, or grant runtime DDL authority.
func mongoDatasetsFragment(current map[string]table) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "mongodatasets_v2", Requires: []string{"foundation_v1", "datasets_v2"}}
	t := mongoDatasetsTable(current)
	f.Changes = append(f.Changes, schemaChange{ID: t.Name, Kind: "table", SQL: extensionTableSQL(t, current, nil), After: &t})
	for _, v := range []index{
		{Name: "MONGO_IMPORTS_SCOPE_PAGE", Table: t.Name, Columns: []string{"TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "REQUESTED_BY", "CREATED_AT", "ID"}, Descending: []bool{false, false, false, false, true, true}},
		{Name: "MONGO_IMPORTS_LEASE", Table: t.Name, Columns: []string{"TENANT_ID", "LEASE_EXPIRES_AT"}, Descending: []bool{false, false}},
	} {
		i := v
		f.Changes = append(f.Changes, schemaChange{ID: i.Name, Kind: "index", SQL: i.ddl(), Index: &i})
	}
	for _, v := range mongoDatasetsTriggers(t) {
		tr := v
		f.Changes = append(f.Changes, schemaChange{ID: tr.Name, Kind: "trigger", SQL: "CREATE TRIGGER " + quoted(tr.Name) + " BEFORE " + tr.Event + " ON " + quoted(tr.Table) + " FOR EACH ROW " + tr.Body, Trigger: &tr})
	}
	return f
}

func mongoDatasetsReference(t table, columns ...string) string {
	for _, c := range t.Constraints {
		if (c.Kind == "P" || c.Kind == "U") && strings.Join(c.Columns, ",") == strings.Join(columns, ",") {
			return c.Name
		}
	}
	// The shared catalog validator rejects this unresolved dependency.
	return "MONGO_REQUIRED_SCOPED_KEY_MISSING"
}

func mongoDatasetsStamp(name string, nullable, now bool) column {
	c := stamp(name, nullable, false)
	if now {
		c.Default = "SYS_EXTRACT_UTC(SYSTIMESTAMP(6))"
	}
	return c
}

func mongoDatasetsTable(current map[string]table) table {
	t := nativeTable("MONGODB_DATASET_IMPORTS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("DATASET_ID", false), uuid("REQUESTED_BY", false), uuid("CREATION_KEY", false),
		col("INTENT", "VARBINARY", 32, false, ""), col("REQUEST", "CLOB", 0, false, ""), col("SOURCE_GRANT", "CLOB", 0, false, ""), text("SOURCE_ID", 63, false),
		text("STATE", 16, false), uuid("ATTEMPT_ID", false), col("ATTEMPT_COUNT", "INTEGER", 0, false, ""), mongoDatasetsStamp("LEASE_EXPIRES_AT", true, false),
		uuid("OBJECT_ID", false), col("RETAINED_OBJECTS", "CLOB", 0, false, ""), col("SOURCE_SHA256", "CHAR", 64, true, ""), col("SOURCE_BYTES", "BIGINT", 0, true, ""),
		col("SOURCE_ROWS", "BIGINT", 0, true, ""), col("FIRST_ID", "CHAR", 24, true, ""), col("LAST_ID", "CHAR", 24, true, ""), uuid("DRAFT_ID", true), text("ERROR_CODE", 64, true),
		mongoDatasetsStamp("CREATED_AT", false, true), mongoDatasetsStamp("UPDATED_AT", false, true),
	}, pk("MONGO_IMPORTS_PK", "ID"),
		unique("MONGO_IMPORTS_CREATION_UK", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "REQUESTED_BY", "CREATION_KEY"),
		fk("MONGO_IMPORTS_ACTOR_FK", "USERS", mongoDatasetsReference(current["USERS"], "ID"), "REQUESTED_BY"),
		fk("MONGO_IMPORTS_DATASET_FK", "DATASETS", mongoDatasetsReference(current["DATASETS"], "TENANT_ID", "WORKSPACE_ID", "ID"), "TENANT_ID", "WORKSPACE_ID", "DATASET_ID"),
		fk("MONGO_IMPORTS_DRAFT_FK", "DATASET_DRAFTS", mongoDatasetsReference(current["DATASET_DRAFTS"], "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "ID"), "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DRAFT_ID"),
		check("MONGO_IMPORTS_INTENT", `OCTET_LENGTH("INTENT")=32`),
		check("MONGO_IMPORTS_REQUEST", `JSON_TYPE("REQUEST") IS NOT NULL AND JSON_TYPE("REQUEST")='OBJECT' AND OCTET_LENGTH("REQUEST")<=16384`),
		check("MONGO_IMPORTS_GRANT", `JSON_TYPE("SOURCE_GRANT") IS NOT NULL AND JSON_TYPE("SOURCE_GRANT")='OBJECT' AND OCTET_LENGTH("SOURCE_GRANT")<=65536`),
		check("MONGO_IMPORTS_SOURCE", `REGEXP_LIKE("SOURCE_ID",'^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$','c')`),
		check("MONGO_IMPORTS_ATTEMPTS", `"ATTEMPT_COUNT" BETWEEN 1 AND 20 AND JSON_TYPE("RETAINED_OBJECTS") IS NOT NULL AND JSON_TYPE("RETAINED_OBJECTS")='ARRAY' AND OCTET_LENGTH("RETAINED_OBJECTS")=39*"ATTEMPT_COUNT"+1`),
		check("MONGO_IMPORTS_PROOF", `("SOURCE_SHA256" IS NULL AND "SOURCE_BYTES" IS NULL AND "SOURCE_ROWS" IS NULL AND "FIRST_ID" IS NULL AND "LAST_ID" IS NULL) OR ("SOURCE_SHA256" IS NOT NULL AND "SOURCE_BYTES" IS NOT NULL AND "SOURCE_ROWS" IS NOT NULL AND "FIRST_ID" IS NOT NULL AND "LAST_ID" IS NOT NULL AND REGEXP_LIKE("SOURCE_SHA256",'^[a-f0-9]{64}$','c') AND "SOURCE_BYTES">0 AND "SOURCE_ROWS">0 AND REGEXP_LIKE("FIRST_ID",'^[a-f0-9]{24}$','c') AND REGEXP_LIKE("LAST_ID",'^[a-f0-9]{24}$','c') AND "FIRST_ID"<="LAST_ID")`),
		check("MONGO_IMPORTS_STATE", `("STATE"='reading' AND "SOURCE_SHA256" IS NULL AND "DRAFT_ID" IS NULL AND "ERROR_CODE" IS NULL AND "LEASE_EXPIRES_AT" IS NOT NULL) OR ("STATE"='exported' AND "SOURCE_SHA256" IS NOT NULL AND "ERROR_CODE" IS NULL AND "LEASE_EXPIRES_AT" IS NOT NULL) OR ("STATE"='imported' AND "SOURCE_SHA256" IS NOT NULL AND "DRAFT_ID" IS NOT NULL AND "ERROR_CODE" IS NULL AND "LEASE_EXPIRES_AT" IS NULL) OR ("STATE"='failed' AND "ERROR_CODE" IS NOT NULL AND "LEASE_EXPIRES_AT" IS NULL)`),
		check("MONGO_IMPORTS_DRAFT_PROOF", `"DRAFT_ID" IS NULL OR "SOURCE_SHA256" IS NOT NULL`),
		check("MONGO_IMPORTS_TIME_ORDER", `"UPDATED_AT">="CREATED_AT"`))
	for _, c := range t.Columns {
		if c.Type == "CHAR" && c.Width == 36 {
			t.Constraints = append(t.Constraints, check("MONGO_"+c.Name+"_NONZERO", quoted(c.Name)+` IS NULL OR `+quoted(c.Name)+`<>'00000000-0000-0000-0000-000000000000'`))
		}
		if c.Type == "TIMESTAMP" {
			t.Constraints = append(t.Constraints, check("MONGO_"+c.Name+"_RANGE", quoted(c.Name)+` IS NULL OR (`+quoted(c.Name)+`>=TIMESTAMP '1970-01-01 00:00:00' AND `+quoted(c.Name)+`<=TIMESTAMP '9999-12-31 23:59:59.999999')`))
		}
	}
	return t
}

func mongoDatasetsChanged(columns ...string) string {
	parts := []string{}
	for _, name := range columns {
		n, o := ":NEW."+quoted(name), ":OLD."+quoted(name)
		parts = append(parts, "(("+n+" IS NOT NULL AND "+o+" IS NOT NULL AND "+n+"<>"+o+") OR ("+n+" IS NULL AND "+o+" IS NOT NULL) OR ("+n+" IS NOT NULL AND "+o+" IS NULL))")
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}
func mongoDatasetsLOBChanged(name string) string {
	return "NVL(DBMS_LOB.COMPARE(:NEW." + quoted(name) + ",:OLD." + quoted(name) + "),1)<>0"
}
func mongoDatasetsGuard(condition string) string {
	return "IF " + condition + " THEN RAISE_APPLICATION_ERROR(-20021,'MONGODB_STORED_PROVENANCE_INVALID'); END IF; "
}
func mongoDatasetsTriggers(t table) []trigger {
	identity := mongoDatasetsChanged("ID", "TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "REQUESTED_BY", "CREATION_KEY", "INTENT", "SOURCE_ID", "CREATED_AT") + " OR " + mongoDatasetsLOBChanged("REQUEST") + " OR " + mongoDatasetsLOBChanged("SOURCE_GRANT")
	proof := mongoDatasetsChanged("SOURCE_SHA256", "SOURCE_BYTES", "SOURCE_ROWS", "FIRST_ID", "LAST_ID")
	all := []string{}
	for _, c := range t.Columns {
		if c.Type != "CLOB" {
			all = append(all, c.Name)
		}
	}
	terminal := mongoDatasetsChanged(all...) + " OR " + mongoDatasetsLOBChanged("REQUEST") + " OR " + mongoDatasetsLOBChanged("SOURCE_GRANT") + " OR " + mongoDatasetsLOBChanged("RETAINED_OBJECTS")
	// The append-only canonical array is at most 781 ASCII bytes. No JSON path
	// or SQL text is built from source/user data, and no vendor SQL is rewritten.
	appendObjects := `DBMS_LOB.SUBSTR(:NEW."RETAINED_OBJECTS",781,1)<>DBMS_LOB.SUBSTR(:OLD."RETAINED_OBJECTS",DBMS_LOB.GETLENGTH(:OLD."RETAINED_OBJECTS")-1,1)||',"'||:NEW."OBJECT_ID"||'"]'`
	newAttempt := `:NEW."ATTEMPT_ID"<>:OLD."ATTEMPT_ID"`
	retryInvalid := `:NEW."ATTEMPT_COUNT"<>:OLD."ATTEMPT_COUNT"+1 OR :OLD."STATE"='imported' OR (:OLD."LEASE_EXPIRES_AT" IS NOT NULL AND :OLD."LEASE_EXPIRES_AT">SYS_EXTRACT_UTC(SYSTIMESTAMP(6))) OR :NEW."LEASE_EXPIRES_AT" IS NULL OR :NEW."LEASE_EXPIRES_AT"<=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) OR :NEW."ERROR_CODE" IS NOT NULL OR ` + proof + ` OR ` + mongoDatasetsChanged("DRAFT_ID") + ` OR ` + appendObjects + ` OR (:OLD."SOURCE_SHA256" IS NULL AND (:NEW."STATE"<>'reading' OR :NEW."OBJECT_ID"=:OLD."OBJECT_ID")) OR (:OLD."SOURCE_SHA256" IS NOT NULL AND (:NEW."STATE"<>'exported' OR :NEW."OBJECT_ID"<>:OLD."OBJECT_ID"))`
	sameAttemptInvalid := mongoDatasetsChanged("ATTEMPT_COUNT", "OBJECT_ID") + ` OR ` + mongoDatasetsLOBChanged("RETAINED_OBJECTS") + ` OR NOT (:NEW."STATE"=:OLD."STATE" OR (:OLD."STATE"='reading' AND :NEW."STATE" IN ('exported','failed')) OR (:OLD."STATE"='exported' AND :NEW."STATE" IN ('imported','failed'))) OR (:OLD."STATE" IN ('reading','exported') AND :NEW."STATE" IN ('reading','exported') AND ` + mongoDatasetsChanged("LEASE_EXPIRES_AT") + `) OR ((` + proof + `) AND NOT (:OLD."STATE"='reading' AND :NEW."STATE"='exported')) OR (:NEW."STATE" IN ('exported','imported') AND (:OLD."LEASE_EXPIRES_AT" IS NULL OR :OLD."LEASE_EXPIRES_AT"<=SYS_EXTRACT_UTC(SYSTIMESTAMP(6))))`
	draft := `IF :NEW."DRAFT_ID" IS NOT NULL THEN SELECT COUNT(*) INTO MATCHED FROM "DATASET_DRAFTS" D WHERE D."TENANT_ID"=:NEW."TENANT_ID" AND D."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND D."DATASET_ID"=:NEW."DATASET_ID" AND D."ID"=:NEW."DRAFT_ID" AND D."CREATED_BY"=:NEW."REQUESTED_BY" AND D."CREATION_KEY"=:NEW."ID" AND D."SOURCE_SHA256"=:NEW."SOURCE_SHA256" AND D."EXPECTED_BYTES"=:NEW."SOURCE_BYTES" AND (:NEW."STATE"<>'imported' OR D."SOURCE_RECEIVED_AT" IS NOT NULL); ` + mongoDatasetsGuard("MATCHED<>1") + `END IF; `
	update := mongoDatasetsGuard(identity+` OR (:OLD."SOURCE_SHA256" IS NOT NULL AND `+proof+`) OR (:OLD."DRAFT_ID" IS NOT NULL AND `+mongoDatasetsChanged("DRAFT_ID")+`) OR (:OLD."STATE"='imported' AND (`+terminal+`))`) + `IF ` + newAttempt + ` THEN ` + mongoDatasetsGuard(retryInvalid) + ` ELSE ` + mongoDatasetsGuard(sameAttemptInvalid) + ` END IF; `
	return []trigger{
		{Name: "MONGO_IMPORTS_INSERT_GUARD", Table: t.Name, Event: "INSERT", Timing: "BEFORE EACH ROW", Body: `BEGIN ` + mongoDatasetsGuard(`:NEW."STATE"<>'reading' OR :NEW."ATTEMPT_COUNT"<>1 OR :NEW."SOURCE_SHA256" IS NOT NULL OR :NEW."DRAFT_ID" IS NOT NULL OR :NEW."ERROR_CODE" IS NOT NULL OR :NEW."LEASE_EXPIRES_AT" IS NULL OR :NEW."LEASE_EXPIRES_AT"<=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) OR DBMS_LOB.SUBSTR(:NEW."RETAINED_OBJECTS",781,1)<>'["'||:NEW."OBJECT_ID"||'"]'`) + `END;`},
		{Name: "MONGO_IMPORTS_UPDATE_GUARD", Table: t.Name, Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: `DECLARE MATCHED INTEGER; BEGIN ` + update + draft + `END;`},
	}
}
