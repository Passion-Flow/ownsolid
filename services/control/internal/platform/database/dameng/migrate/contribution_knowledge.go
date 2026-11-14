package migrate

import "strings"

// knowledgeFragment owns M12's native relational/LOB/vector representation.
// It neither registers itself nor changes foundation numbering or privileges.
func knowledgeFragment(current map[string]table) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "knowledge_v2", Requires: []string{"foundation_v1"}}
	known := make(map[string]table, len(current)+7)
	for name, t := range current {
		known[name] = cloneTable(t)
	}
	for _, t := range []table{knowledgeBaseTable(), knowledgeGenerationTable(), knowledgeDocumentTable(), knowledgeSourceTable(), knowledgeAttemptTable(), knowledgeChunkTable(), knowledgeIntentTable()} {
		after := cloneTable(t)
		f.Changes = append(f.Changes, schemaChange{ID: t.Name, Kind: "table", SQL: extensionTableSQL(t, known, nil), After: &after})
		known[t.Name] = cloneTable(t)
	}
	for _, item := range []struct {
		table string
		key   constraint
	}{
		{"KNOWLEDGE_BASES", fk("KNOWLEDGE_BASE_ACTIVE_FK", "KNOWLEDGE_GENERATIONS", "KNOWLEDGE_GENERATION_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID", "ACTIVE_GENERATION_ID")},
		{"KNOWLEDGE_BASES", fk("KNOWLEDGE_BASE_BUILDING_FK", "KNOWLEDGE_GENERATIONS", "KNOWLEDGE_GENERATION_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID", "BUILDING_GENERATION_ID")},
		{"KNOWLEDGE_DOCUMENTS", fk("KNOWLEDGE_DOCUMENT_VISIBLE_FK", "KNOWLEDGE_SOURCE_VERSIONS", "KNOWLEDGE_SOURCE_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "ID", "VISIBLE_SOURCE_ID")},
		{"KNOWLEDGE_DOCUMENTS", fk("KNOWLEDGE_DOCUMENT_PENDING_FK", "KNOWLEDGE_SOURCE_VERSIONS", "KNOWLEDGE_SOURCE_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "ID", "PENDING_SOURCE_ID")},
		{"KNOWLEDGE_DOCUMENTS", fk("KNOWLEDGE_DOCUMENT_ATTEMPT_FK", "KNOWLEDGE_ATTEMPTS", "KNOWLEDGE_ATTEMPT_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "ID", "CURRENT_ATTEMPT_ID")},
	} {
		before := cloneTable(known[item.table])
		after := cloneTable(before)
		after.Constraints = append(after.Constraints, item.key)
		var target []string
		for _, c := range known[item.key.Reference].Constraints {
			if c.Name == item.key.ReferenceKey {
				target = c.Columns
			}
		}
		sql := "ALTER TABLE " + quoted(item.table) + " ADD CONSTRAINT " + quoted(item.key.Name) + " FOREIGN KEY (" + names(item.key.Columns) + ") REFERENCES " + quoted(item.key.Reference) + " (" + names(target) + ")"
		f.Changes = append(f.Changes, schemaChange{ID: item.key.Name, Kind: "alter", SQL: sql, Before: &before, After: &after})
		known[item.table] = cloneTable(after)
	}
	for _, i := range []index{
		{Name: "KNOWLEDGE_BASE_NAME_IDX", Table: "KNOWLEDGE_BASES", Unique: true, Columns: []string{"TENANT_ID", "WORKSPACE_ID", "NAME_KEY"}, Descending: []bool{false, false, false}},
		{Name: "KNOWLEDGE_BASE_PAGE_IDX", Table: "KNOWLEDGE_BASES", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, Descending: []bool{false, false, true, true}},
		{Name: "KNOWLEDGE_ACTIVE_GENERATION_IDX", Table: "KNOWLEDGE_GENERATIONS", Unique: true, Columns: []string{"ACTIVE_BASE_ID"}, Descending: []bool{false}},
		{Name: "KNOWLEDGE_BUILDING_GENERATION_IDX", Table: "KNOWLEDGE_GENERATIONS", Unique: true, Columns: []string{"BUILDING_BASE_ID"}, Descending: []bool{false}},
		{Name: "KNOWLEDGE_DOCUMENT_NAME_IDX", Table: "KNOWLEDGE_DOCUMENTS", Unique: true, Columns: []string{"LIVE_BASE_ID", "LIVE_NAME_KEY"}, Descending: []bool{false, false}},
		{Name: "KNOWLEDGE_DOCUMENT_PAGE_IDX", Table: "KNOWLEDGE_DOCUMENTS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "BASE_ID", "CREATED_AT", "ID"}, Descending: []bool{false, false, false, true, true}},
		{Name: "KNOWLEDGE_PENDING_ATTEMPT_IDX", Table: "KNOWLEDGE_ATTEMPTS", Columns: []string{"STATE", "CREATED_AT", "ID"}, Descending: []bool{false, false, false}},
		{Name: "KNOWLEDGE_READY_ATTEMPT_IDX", Table: "KNOWLEDGE_ATTEMPTS", Unique: true, Columns: []string{"READY_GENERATION_ID", "READY_DOCUMENT_ID", "READY_SOURCE_ID"}, Descending: []bool{false, false, false}},
		{Name: "KNOWLEDGE_LIVE_ATTEMPT_IDX", Table: "KNOWLEDGE_ATTEMPTS", Unique: true, Columns: []string{"LIVE_GENERATION_ID", "LIVE_DOCUMENT_ID", "LIVE_SOURCE_ID"}, Descending: []bool{false, false, false}},
		{Name: "KNOWLEDGE_EXACT_SEARCH_IDX", Table: "KNOWLEDGE_CHUNKS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "BASE_ID", "GENERATION_ID", "DOCUMENT_ID", "ORDINAL", "SOURCE_ID"}, Descending: []bool{false, false, false, false, false, false, false}},
	} {
		v := i
		f.Changes = append(f.Changes, schemaChange{ID: i.Name, Kind: "index", SQL: i.ddl(), Index: &v})
	}
	for _, t := range knowledgeGuards() {
		v := t
		f.Changes = append(f.Changes, schemaChange{ID: t.Name, Kind: "trigger", SQL: "CREATE TRIGGER " + quoted(t.Name) + " BEFORE " + t.Event + " ON " + quoted(t.Table) + " FOR EACH ROW " + t.Body, Trigger: &v})
	}
	return f
}

func knowledgeConditionalID(name, source, condition string) column {
	c := uuid(name, true)
	c.Virtual = "CASE WHEN " + condition + " THEN " + quoted(source) + " ELSE NULL END"
	return c
}
func knowledgeDigest(name, field string) constraint {
	return check(name, quoted(field)+" IS NULL OR REGEXP_LIKE("+quoted(field)+",'^[a-f0-9]{64}$','c')")
}
func knowledgeErrorCode(name string) constraint {
	return check(name, `"ERROR_CODE" IS NULL OR REGEXP_LIKE("ERROR_CODE",'^[A-Z][A-Z0-9_]{1,79}$','c')`)
}
func knowledgeAudience(name string) constraint {
	return check(name, `"REQUESTED_AUDIENCE" IN ('portal','admin') AND "REQUESTED_EPOCH">=0 AND ("REQUESTED_AUDIENCE"='admin' OR "MEMBERSHIP_AUTHORITY" IS NOT NULL)`)
}

func knowledgeBaseTable() table {
	return nativeTable("KNOWLEDGE_BASES", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("NAME", 100, false), text("NAME_KEY", 200, false), col("ENABLED", "BIT", 0, false, "1"), version("REVISION"), uuid("ACTIVE_GENERATION_ID", true), uuid("BUILDING_GENERATION_ID", true), uuid("CREATED_BY", false), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true), uuid("CREATION_KEY", false), col("CREATION_FINGERPRINT", "VARBINARY", 32, false, ""), col("INITIALIZATION_STATE", "VARCHAR", 12, false, "'initializing'"),
	}, pk("KNOWLEDGE_BASES_PK", "ID"), unique("KNOWLEDGE_BASE_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("KNOWLEDGE_BASE_CREATION_UK", "TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "CREATION_KEY"), fk("KNOWLEDGE_BASE_WORKSPACE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), fk("KNOWLEDGE_BASE_CREATOR_FK", "USERS", "USERS_PK", "CREATED_BY"), bounded("KNOWLEDGE_BASE_REVISION", "REVISION"), check("KNOWLEDGE_BASE_TEXT", `LENGTH("NAME")>0 AND LENGTH("NAME_KEY")>0`), check("KNOWLEDGE_BASE_FINGERPRINT", `LENGTHB("CREATION_FINGERPRINT")=32`), check("KNOWLEDGE_BASE_GENERATIONS", `"BUILDING_GENERATION_ID" IS NULL OR "BUILDING_GENERATION_ID"<>"ACTIVE_GENERATION_ID"`), check("KNOWLEDGE_BASE_INITIALIZATION", `("INITIALIZATION_STATE"='initializing' AND "ACTIVE_GENERATION_ID" IS NULL AND "BUILDING_GENERATION_ID" IS NULL) OR ("INITIALIZATION_STATE"='ready' AND "ACTIVE_GENERATION_ID" IS NOT NULL)`))
}
func knowledgeGenerationTable() table {
	return nativeTable("KNOWLEDGE_GENERATIONS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("BASE_ID", false), text("CATALOG_SHA256", 64, false), col("DIMENSION", "INTEGER", 0, false, ""), col("CHUNK_TOKENS", "INTEGER", 0, false, ""), col("OVERLAP_TOKENS", "INTEGER", 0, false, ""), text("STATE", 12, false), uuid("REQUESTED_BY", false), text("REQUESTED_AUDIENCE", 8, false), col("REQUESTED_EPOCH", "BIGINT", 0, false, ""), uuid("MEMBERSHIP_AUTHORITY", true), col("EXPECTED_DOCUMENTS", "INTEGER", 0, false, ""), text("ERROR_CODE", 80, true), stamp("CREATED_AT", false, true), stamp("ACTIVATED_AT", true, false), stamp("FINISHED_AT", true, false), knowledgeConditionalID("ACTIVE_BASE_ID", "BASE_ID", `"STATE"='active'`), knowledgeConditionalID("BUILDING_BASE_ID", "BASE_ID", `"STATE"='building'`),
	}, pk("KNOWLEDGE_GENERATIONS_PK", "ID"), unique("KNOWLEDGE_GENERATION_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "ID"), fk("KNOWLEDGE_GENERATION_BASE_FK", "KNOWLEDGE_BASES", "KNOWLEDGE_BASE_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID"), fk("KNOWLEDGE_GENERATION_ACTOR_FK", "USERS", "USERS_PK", "REQUESTED_BY"), knowledgeDigest("KNOWLEDGE_GENERATION_CATALOG", "CATALOG_SHA256"), knowledgeAudience("KNOWLEDGE_GENERATION_AUDIENCE"), knowledgeErrorCode("KNOWLEDGE_GENERATION_ERROR"), check("KNOWLEDGE_GENERATION_ENCODER", `"DIMENSION"=384 AND "CHUNK_TOKENS" BETWEEN 8 AND 254 AND "OVERLAP_TOKENS">=0 AND "OVERLAP_TOKENS"<"CHUNK_TOKENS"`), check("KNOWLEDGE_GENERATION_STATE", `"STATE" IN ('building','active','failed','retired') AND ("STATE"<>'active' OR "ACTIVATED_AT" IS NOT NULL)`), check("KNOWLEDGE_GENERATION_COUNT", `"EXPECTED_DOCUMENTS" BETWEEN 0 AND 1000`))
}
func knowledgeDocumentTable() table {
	liveName := text("LIVE_NAME_KEY", 600, true)
	liveName.Virtual = `CASE WHEN "DELETED_AT" IS NULL THEN "NAME_KEY" ELSE NULL END`
	return nativeTable("KNOWLEDGE_DOCUMENTS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("BASE_ID", false), text("NAME", 255, false), text("NAME_KEY", 600, false), version("REVISION"), text("STATE", 12, false), uuid("VISIBLE_SOURCE_ID", true), uuid("PENDING_SOURCE_ID", true), uuid("CURRENT_ATTEMPT_ID", true), uuid("CREATED_BY", false), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true), stamp("DELETED_AT", true, false), text("DELETE_REASON", 500, true), knowledgeConditionalID("LIVE_BASE_ID", "BASE_ID", `"DELETED_AT" IS NULL`), liveName,
	}, pk("KNOWLEDGE_DOCUMENTS_PK", "ID"), unique("KNOWLEDGE_DOCUMENT_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "ID"), fk("KNOWLEDGE_DOCUMENT_BASE_FK", "KNOWLEDGE_BASES", "KNOWLEDGE_BASE_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID"), fk("KNOWLEDGE_DOCUMENT_CREATOR_FK", "USERS", "USERS_PK", "CREATED_BY"), bounded("KNOWLEDGE_DOCUMENT_REVISION", "REVISION"), check("KNOWLEDGE_DOCUMENT_TEXT", `LENGTH("NAME")>0 AND LENGTH("NAME_KEY")>0`), check("KNOWLEDGE_DOCUMENT_STATE", `"STATE" IN ('pending','running','ready','failed','deleted') AND ("STATE"<>'ready' OR "VISIBLE_SOURCE_ID" IS NOT NULL)`), check("KNOWLEDGE_DOCUMENT_DELETION", `("STATE"='deleted' AND "DELETED_AT" IS NOT NULL AND "DELETE_REASON" IS NOT NULL AND LENGTH("DELETE_REASON")>0) OR ("STATE"<>'deleted' AND "DELETED_AT" IS NULL AND "DELETE_REASON" IS NULL)`))
}
func knowledgeSourceTable() table {
	return nativeTable("KNOWLEDGE_SOURCE_VERSIONS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("BASE_ID", false), uuid("DOCUMENT_ID", false), version("SOURCE_VERSION"), col("CONTENT", "BLOB", 0, false, ""), col("SIZE_BYTES", "INTEGER", 0, false, ""), text("SHA256", 64, false), uuid("CREATED_BY", false), stamp("CREATED_AT", false, true), stamp("SUPERSEDED_AT", true, false),
	}, pk("KNOWLEDGE_SOURCES_PK", "ID"), unique("KNOWLEDGE_SOURCE_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "DOCUMENT_ID", "ID"), unique("KNOWLEDGE_SOURCE_VERSION_UK", "DOCUMENT_ID", "SOURCE_VERSION"), fk("KNOWLEDGE_SOURCE_DOCUMENT_FK", "KNOWLEDGE_DOCUMENTS", "KNOWLEDGE_DOCUMENT_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "DOCUMENT_ID"), fk("KNOWLEDGE_SOURCE_CREATOR_FK", "USERS", "USERS_PK", "CREATED_BY"), bounded("KNOWLEDGE_SOURCE_VERSION", "SOURCE_VERSION"), knowledgeDigest("KNOWLEDGE_SOURCE_DIGEST", "SHA256"), check("KNOWLEDGE_SOURCE_BYTES", `"SIZE_BYTES" BETWEEN 1 AND 2097152 AND "SIZE_BYTES"=LENGTHB("CONTENT")`), check("KNOWLEDGE_SOURCE_RETIRE_ORDER", `"SUPERSEDED_AT" IS NULL OR "SUPERSEDED_AT">="CREATED_AT"`))
}
func knowledgeAttemptTable() table {
	return nativeTable("KNOWLEDGE_ATTEMPTS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("BASE_ID", false), uuid("DOCUMENT_ID", false), uuid("SOURCE_ID", false), uuid("GENERATION_ID", false), text("KIND", 8, false), col("ATTEMPT_NUMBER", "INTEGER", 0, false, ""), version("EXPECTED_DOCUMENT_REVISION"), uuid("REQUESTED_BY", false), text("REQUESTED_AUDIENCE", 8, false), col("REQUESTED_EPOCH", "BIGINT", 0, false, ""), uuid("MEMBERSHIP_AUTHORITY", true), text("STATE", 12, false), col("LEASE_HASH", "VARBINARY", 32, true, ""), stamp("LEASE_EXPIRES_AT", true, false), col("CHUNK_COUNT", "INTEGER", 0, false, "0"), text("ERROR_CODE", 80, true), stamp("CREATED_AT", false, true), stamp("STARTED_AT", true, false), stamp("FINISHED_AT", true, false),
		knowledgeConditionalID("READY_GENERATION_ID", "GENERATION_ID", `"STATE"='ready'`), knowledgeConditionalID("READY_DOCUMENT_ID", "DOCUMENT_ID", `"STATE"='ready'`), knowledgeConditionalID("READY_SOURCE_ID", "SOURCE_ID", `"STATE"='ready'`), knowledgeConditionalID("LIVE_GENERATION_ID", "GENERATION_ID", `"STATE" IN ('pending','running')`), knowledgeConditionalID("LIVE_DOCUMENT_ID", "DOCUMENT_ID", `"STATE" IN ('pending','running')`), knowledgeConditionalID("LIVE_SOURCE_ID", "SOURCE_ID", `"STATE" IN ('pending','running')`),
	}, pk("KNOWLEDGE_ATTEMPTS_PK", "ID"), unique("KNOWLEDGE_ATTEMPT_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "DOCUMENT_ID", "GENERATION_ID", "SOURCE_ID", "ID"), unique("KNOWLEDGE_ATTEMPT_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "DOCUMENT_ID", "ID"), unique("KNOWLEDGE_ATTEMPT_NUMBER_UK", "GENERATION_ID", "DOCUMENT_ID", "SOURCE_ID", "ATTEMPT_NUMBER"), fk("KNOWLEDGE_ATTEMPT_SOURCE_FK", "KNOWLEDGE_SOURCE_VERSIONS", "KNOWLEDGE_SOURCE_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "DOCUMENT_ID", "SOURCE_ID"), fk("KNOWLEDGE_ATTEMPT_GENERATION_FK", "KNOWLEDGE_GENERATIONS", "KNOWLEDGE_GENERATION_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "GENERATION_ID"), fk("KNOWLEDGE_ATTEMPT_ACTOR_FK", "USERS", "USERS_PK", "REQUESTED_BY"), knowledgeAudience("KNOWLEDGE_ATTEMPT_AUDIENCE"), knowledgeErrorCode("KNOWLEDGE_ATTEMPT_ERROR"), bounded("KNOWLEDGE_ATTEMPT_REVISION", "EXPECTED_DOCUMENT_REVISION"), check("KNOWLEDGE_ATTEMPT_KIND", `"KIND" IN ('ingest','rebuild')`), check("KNOWLEDGE_ATTEMPT_BOUNDS", `"ATTEMPT_NUMBER" BETWEEN 1 AND 10 AND "CHUNK_COUNT" BETWEEN 0 AND 4096`), check("KNOWLEDGE_ATTEMPT_STATE", `"STATE" IN ('pending','running','ready','failed','cancelled')`), check("KNOWLEDGE_ATTEMPT_LEASE", `("STATE"='running' AND "LEASE_HASH" IS NOT NULL AND LENGTHB("LEASE_HASH")=32 AND "LEASE_EXPIRES_AT" IS NOT NULL) OR ("STATE"<>'running' AND "LEASE_HASH" IS NULL AND "LEASE_EXPIRES_AT" IS NULL)`), check("KNOWLEDGE_ATTEMPT_FAILURE", `("STATE" IN ('failed','cancelled') AND "ERROR_CODE" IS NOT NULL) OR ("STATE" NOT IN ('failed','cancelled') AND "ERROR_CODE" IS NULL)`), check("KNOWLEDGE_ATTEMPT_FINISHED", `("STATE" IN ('ready','failed','cancelled') AND "FINISHED_AT" IS NOT NULL) OR ("STATE" IN ('pending','running') AND "FINISHED_AT" IS NULL)`))
}
func knowledgeChunkTable() table {
	return nativeTable("KNOWLEDGE_CHUNKS", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("BASE_ID", false), uuid("DOCUMENT_ID", false), uuid("GENERATION_ID", false), uuid("SOURCE_ID", false), uuid("ATTEMPT_ID", false), col("ORDINAL", "INTEGER", 0, false, ""), col("START_BYTE", "INTEGER", 0, false, ""), col("END_BYTE", "INTEGER", 0, false, ""), col("TOKEN_COUNT", "INTEGER", 0, false, ""), col("EMBEDDING", "CLOB", 0, false, ""),
	}, pk("KNOWLEDGE_CHUNKS_PK", "ATTEMPT_ID", "ORDINAL"), fk("KNOWLEDGE_CHUNK_ATTEMPT_FK", "KNOWLEDGE_ATTEMPTS", "KNOWLEDGE_ATTEMPT_FULL_UK", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "DOCUMENT_ID", "GENERATION_ID", "SOURCE_ID", "ATTEMPT_ID"), check("KNOWLEDGE_CHUNK_ORDINAL", `"ORDINAL" BETWEEN 0 AND 4095`), check("KNOWLEDGE_CHUNK_BYTES", `"START_BYTE" BETWEEN 0 AND 2097151 AND "END_BYTE">"START_BYTE" AND "END_BYTE"<=2097152 AND "END_BYTE"-"START_BYTE"<=65536`), check("KNOWLEDGE_CHUNK_TOKENS", `"TOKEN_COUNT" BETWEEN 1 AND 254`), check("KNOWLEDGE_CHUNK_JSON", `JSON_TYPE("EMBEDDING") IS NOT NULL AND JSON_TYPE("EMBEDDING")='ARRAY' AND LENGTHB("EMBEDDING")<=32768`))
}
func knowledgeIntentTable() table {
	return nativeTable("KNOWLEDGE_MUTATION_INTENTS", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("ACTOR_ID", false), uuid("IDEMPOTENCY_KEY", false), text("PURPOSE", 24, false), col("REQUEST_HASH", "VARBINARY", 32, false, ""), uuid("ENTITY_ID", false), stamp("CREATED_AT", false, true),
	}, pk("KNOWLEDGE_INTENTS_PK", "TENANT_ID", "WORKSPACE_ID", "ACTOR_ID", "IDEMPOTENCY_KEY"), fk("KNOWLEDGE_INTENT_ACTOR_FK", "USERS", "USERS_PK", "ACTOR_ID"), fk("KNOWLEDGE_INTENT_WORKSPACE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), check("KNOWLEDGE_INTENT_PURPOSE", `"PURPOSE" IN ('create_base','upload_source','replace_source','retry_document','rebuild')`), check("KNOWLEDGE_INTENT_HASH", `LENGTHB("REQUEST_HASH")=32`))
}

func knowledgeChanged(fields ...string) string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		n, o := ":NEW."+quoted(f), ":OLD."+quoted(f)
		out = append(out, "("+n+"<>"+o+" OR ("+n+" IS NULL AND "+o+" IS NOT NULL) OR ("+n+" IS NOT NULL AND "+o+" IS NULL))")
	}
	return strings.Join(out, " OR ")
}
func knowledgeGuards() []trigger {
	sourceIdentity := knowledgeChanged("ID", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "DOCUMENT_ID", "SOURCE_VERSION", "SIZE_BYTES", "SHA256", "CREATED_BY", "CREATED_AT")
	sourceBody := `DECLARE DIFFERENCE INTEGER; BEGIN DIFFERENCE:=DBMS_LOB.COMPARE(:NEW."CONTENT",:OLD."CONTENT"); IF :OLD."SUPERSEDED_AT" IS NOT NULL OR :NEW."SUPERSEDED_AT" IS NULL OR DIFFERENCE IS NULL OR DIFFERENCE<>0 OR ` + sourceIdentity + ` THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_SOURCE_IMMUTABLE'); END IF; END;`
	generation := knowledgeChanged("ID", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "CATALOG_SHA256", "DIMENSION", "CHUNK_TOKENS", "OVERLAP_TOKENS", "REQUESTED_BY", "REQUESTED_AUDIENCE", "REQUESTED_EPOCH", "MEMBERSHIP_AUTHORITY", "EXPECTED_DOCUMENTS", "CREATED_AT")
	attempt := knowledgeChanged("ID", "TENANT_ID", "WORKSPACE_ID", "BASE_ID", "DOCUMENT_ID", "SOURCE_ID", "GENERATION_ID", "KIND", "ATTEMPT_NUMBER", "EXPECTED_DOCUMENT_REVISION", "REQUESTED_BY", "REQUESTED_AUDIENCE", "REQUESTED_EPOCH", "MEMBERSHIP_AUTHORITY", "CREATED_AT")
	return []trigger{
		{Name: "KNOWLEDGE_SOURCE_VALIDATE", Table: "KNOWLEDGE_SOURCE_VERSIONS", Event: "INSERT", Timing: "BEFORE EACH ROW", Body: knowledgeSourceValidation},
		{Name: "KNOWLEDGE_SOURCE_IMMUTABLE", Table: "KNOWLEDGE_SOURCE_VERSIONS", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: sourceBody},
		{Name: "KNOWLEDGE_CHUNK_IMMUTABLE", Table: "KNOWLEDGE_CHUNKS", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: `BEGIN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_CHUNK_IMMUTABLE'); END;`},
		{Name: "KNOWLEDGE_GENERATION_IMMUTABLE", Table: "KNOWLEDGE_GENERATIONS", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: `BEGIN IF ` + generation + ` THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_GENERATION_IMMUTABLE'); END IF; END;`},
		{Name: "KNOWLEDGE_ATTEMPT_IMMUTABLE", Table: "KNOWLEDGE_ATTEMPTS", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: `BEGIN IF ` + attempt + ` THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_ATTEMPT_IMMUTABLE'); END IF; END;`},
		{Name: "KNOWLEDGE_EMBEDDING_VALIDATE", Table: "KNOWLEDGE_CHUNKS", Event: "INSERT", Timing: "BEFORE EACH ROW", Body: knowledgeEmbeddingValidation},
		{Name: "KNOWLEDGE_BASE_INITIALIZATION_GUARD", Table: "KNOWLEDGE_BASES", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: `BEGIN IF :OLD."INITIALIZATION_STATE"='ready' AND :NEW."INITIALIZATION_STATE"<>'ready' THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_BASE_INITIALIZATION_IMMUTABLE'); END IF; END;`},
	}
}

// Full BLOB SHA256 plus a bounded RFC3629 byte-state validator. Hex windows
// preserve UTF8 continuation state at window boundaries, reject overlong forms,
// surrogates, values above U+10FFFF and NUL without relying on conversion mode.
const knowledgeSourceValidation = `DECLARE
 OFFSET_BYTES INTEGER:=1; SOURCE_LENGTH INTEGER; WINDOW_HEX VARCHAR(8192); HEX_INDEX INTEGER; BYTE_VALUE INTEGER;
 REMAINING INTEGER:=0; LOWER_BYTE INTEGER:=128; UPPER_BYTE INTEGER:=191; DIGEST VARCHAR(64);
BEGIN
 SOURCE_LENGTH:=LENGTHB(:NEW."CONTENT");
 IF SOURCE_LENGTH IS NULL OR SOURCE_LENGTH<1 OR SOURCE_LENGTH>2097152 OR SOURCE_LENGTH<>:NEW."SIZE_BYTES" THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_INVALID_SOURCE'); END IF;
 DIGEST:=LOWER(RAWTOHEX(DBMS_CRYPTO.HASH(:NEW."CONTENT",DBMS_CRYPTO.HASH_SH256)));
 IF DIGEST IS NULL OR DIGEST<>:NEW."SHA256" THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_INVALID_SOURCE'); END IF;
 WHILE OFFSET_BYTES<=SOURCE_LENGTH LOOP
  WINDOW_HEX:=RAWTOHEX(DBMS_LOB.SUBSTR(:NEW."CONTENT",LEAST(4096,SOURCE_LENGTH-OFFSET_BYTES+1),OFFSET_BYTES));
  IF WINDOW_HEX IS NULL OR LENGTH(WINDOW_HEX)<>2*LEAST(4096,SOURCE_LENGTH-OFFSET_BYTES+1) THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_INVALID_SOURCE'); END IF;
  HEX_INDEX:=1;
  WHILE HEX_INDEX<=LENGTH(WINDOW_HEX) LOOP
   BYTE_VALUE:=(INSTR('0123456789ABCDEF',SUBSTR(WINDOW_HEX,HEX_INDEX,1))-1)*16+INSTR('0123456789ABCDEF',SUBSTR(WINDOW_HEX,HEX_INDEX+1,1))-1;
   IF REMAINING>0 THEN
    IF BYTE_VALUE<LOWER_BYTE OR BYTE_VALUE>UPPER_BYTE THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_INVALID_SOURCE'); END IF;
    REMAINING:=REMAINING-1; LOWER_BYTE:=128; UPPER_BYTE:=191;
   ELSIF BYTE_VALUE BETWEEN 1 AND 127 THEN NULL;
   ELSIF BYTE_VALUE BETWEEN 194 AND 223 THEN REMAINING:=1;
   ELSIF BYTE_VALUE=224 THEN REMAINING:=2; LOWER_BYTE:=160;
   ELSIF BYTE_VALUE BETWEEN 225 AND 236 OR BYTE_VALUE BETWEEN 238 AND 239 THEN REMAINING:=2;
   ELSIF BYTE_VALUE=237 THEN REMAINING:=2; UPPER_BYTE:=159;
   ELSIF BYTE_VALUE=240 THEN REMAINING:=3; LOWER_BYTE:=144;
   ELSIF BYTE_VALUE BETWEEN 241 AND 243 THEN REMAINING:=3;
   ELSIF BYTE_VALUE=244 THEN REMAINING:=3; UPPER_BYTE:=143;
   ELSE RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_INVALID_SOURCE'); END IF;
   HEX_INDEX:=HEX_INDEX+2;
  END LOOP;
  OFFSET_BYTES:=OFFSET_BYTES+4096;
 END LOOP;
 IF REMAINING<>0 THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_INVALID_SOURCE'); END IF;
END;`

const knowledgeEmbeddingValidation = `DECLARE
 DOCUMENT_JSON JDOM_T; COMPONENT_JSON JDOM_T; COMPONENT_INDEX INTEGER:=0; COMPONENT_VALUE DOUBLE; NORM_SQUARED DOUBLE:=0;
BEGIN
 DOCUMENT_JSON:=JDOM_T.PARSE(:NEW."EMBEDDING");
 IF NOT DOCUMENT_JSON.IS_ARRAY() OR DOCUMENT_JSON.GET_SIZE()<>384 THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_VECTOR_INVALID'); END IF;
 WHILE COMPONENT_INDEX<384 LOOP
  COMPONENT_JSON:=DOCUMENT_JSON."GET"(COMPONENT_INDEX);
  IF NOT COMPONENT_JSON.IS_NUMBER() THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_VECTOR_INVALID'); END IF;
  COMPONENT_VALUE:=DOCUMENT_JSON.GET_NUMBER(COMPONENT_INDEX);
  IF COMPONENT_VALUE IS NULL OR ABS(COMPONENT_VALUE)>1.001 THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_VECTOR_INVALID'); END IF;
  NORM_SQUARED:=NORM_SQUARED+COMPONENT_VALUE*COMPONENT_VALUE; COMPONENT_INDEX:=COMPONENT_INDEX+1;
 END LOOP;
 IF NOT (ABS(SQRT(NORM_SQUARED)-1)<0.001) THEN RAISE_APPLICATION_ERROR(-20012,'KNOWLEDGE_VECTOR_INVALID'); END IF;
END;`
