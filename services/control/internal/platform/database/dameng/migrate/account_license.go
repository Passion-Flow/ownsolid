package migrate

import "strings"

// All statements are native DM definitions. No runtime caller, environment key
// or vendor-SQL translator can add a migration to this ordered catalog.
func accountLicenseFragment(c catalog) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "account_license_v2", Requires: []string{"foundation_v1"}}
	known := c.project(len(c.steps)).tables
	add := func(x schemaChange) {
		f.Changes = append(f.Changes, x)
		if x.After != nil {
			known[x.After.Name] = cloneTable(*x.After)
		}
	}
	alter := func(id, sql string, after table) {
		before := cloneTable(known[after.Name])
		add(schemaChange{ID: id, Kind: "alter", SQL: sql, Before: &before, After: &after})
	}
	tableStep := func(t table, rules map[string]string) {
		add(schemaChange{ID: t.Name, Kind: "table", SQL: extensionTableSQL(t, known, rules), After: &t, DeleteRules: rules})
	}
	constraintStep := func(name string, v constraint) {
		t := cloneTable(known[name])
		t.Constraints = append(t.Constraints, v)
		body := "CHECK (" + v.Check + ")"
		switch v.Kind {
		case "U":
			body = "UNIQUE (" + names(v.Columns) + ")"
		case "R":
			var keys []string
			for _, key := range known[v.Reference].Constraints {
				if key.Name == v.ReferenceKey {
					keys = key.Columns
				}
			}
			body = "FOREIGN KEY (" + names(v.Columns) + ") REFERENCES " + quoted(v.Reference) + " (" + names(keys) + ")"
		}
		alter(v.Name, "ALTER TABLE "+quoted(name)+" ADD CONSTRAINT "+quoted(v.Name)+" "+body, t)
	}
	triggerStep := func(name, table, event, timing, body string) {
		t := trigger{name, table, event, timing + " EACH ROW", body}
		add(schemaChange{ID: name, Kind: "trigger", SQL: "CREATE TRIGGER " + quoted(name) + " " + timing + " " + event + " ON " + quoted(table) + " FOR EACH ROW " + body, Trigger: &t})
	}
	indexStep := func(name, table string, columns []string, descending []bool) {
		if descending == nil {
			descending = make([]bool, len(columns))
		}
		i := index{Name: name, Table: table, Columns: columns, Descending: descending}
		add(schemaChange{ID: name, Kind: "index", SQL: i.ddl(), Index: &i})
	}

	// Both checks coexist during step32. The old bound is removed only after the
	// new bound is durably applied and completely read back, before reaching256.
	j := cloneTable(known[journalName])
	j.Constraints = append(j.Constraints, check("PO_SCHEMA_MIGRATIONS_STEP_V2", `"STEP_NO" BETWEEN 1 AND 4096`))
	alter("JOURNAL_CAPACITY_V2_ADD", `ALTER TABLE "PO_SCHEMA_MIGRATIONS" ADD CONSTRAINT "PO_SCHEMA_MIGRATIONS_STEP_V2" CHECK ("STEP_NO" BETWEEN 1 AND 4096)`, j)
	j = cloneTable(j)
	for i, v := range j.Constraints {
		if v.Name == "PO_SCHEMA_MIGRATIONS_STEP" {
			j.Constraints = append(j.Constraints[:i], j.Constraints[i+1:]...)
			break
		}
	}
	alter("JOURNAL_CAPACITY_V1_DROP", `ALTER TABLE "PO_SCHEMA_MIGRATIONS" DROP CONSTRAINT "PO_SCHEMA_MIGRATIONS_STEP"`, j)

	tableStep(nativeTable("PLATFORM_LICENSE_STATE", []column{col("ID", "SMALLINT", 0, false, ""), uuid("DEPLOYMENT_ID", false), stamp("OBSERVED_AT", false, true)}, pk("PLATFORM_LICENSE_STATE_PK", "ID"), unique("PLATFORM_LICENSE_DEPLOYMENT_UK", "DEPLOYMENT_ID"), check("PLATFORM_LICENSE_SINGLETON", `"ID"=1`)), nil)
	add(schemaChange{ID: "PLATFORM_LICENSE_SEED", Kind: "seed", SQL: `INSERT INTO "PLATFORM_LICENSE_STATE" ("ID","DEPLOYMENT_ID") VALUES (1,?)`, Probe: `SELECT COUNT(*) FROM "PLATFORM_LICENSE_STATE" WHERE "ID"=1`, ProbeTables: []string{"PLATFORM_LICENSE_STATE"}, AfterCount: 1, UUIDParameters: 1})
	tableStep(nativeTable("PLATFORM_LICENSE_ARTIFACTS", []column{uuid("LICENSE_ID", false), col("SEQUENCE", "INTEGER", 0, false, ""), uuid("DEPLOYMENT_ID", false), text("ARTIFACT_SHA256", 64, false), col("ARTIFACT", "BLOB", 0, false, ""), uuid("PREDECESSOR_ID", true), uuid("IMPORTED_BY", false), stamp("IMPORTED_AT", false, true), text("REASON", 500, false)}, pk("PLATFORM_LICENSE_ARTIFACTS_PK", "LICENSE_ID"), unique("PLATFORM_LICENSE_SEQUENCE_UK", "SEQUENCE"), unique("PLATFORM_LICENSE_ARTIFACT_HASH_UK", "ARTIFACT_SHA256"), fk("PLATFORM_LICENSE_DEPLOYMENT_FK", "PLATFORM_LICENSE_STATE", "PLATFORM_LICENSE_DEPLOYMENT_UK", "DEPLOYMENT_ID"), fk("PLATFORM_LICENSE_PREDECESSOR_FK", "PLATFORM_LICENSE_ARTIFACTS", "PLATFORM_LICENSE_ARTIFACTS_PK", "PREDECESSOR_ID"), fk("PLATFORM_LICENSE_IMPORTER_FK", "USERS", "USERS_PK", "IMPORTED_BY"), check("PLATFORM_LICENSE_SEQUENCE", `"SEQUENCE" BETWEEN 1 AND 1024`), check("PLATFORM_LICENSE_HASH", `REGEXP_LIKE("ARTIFACT_SHA256", '^[a-f0-9]{64}$', 'c')`), check("PLATFORM_LICENSE_BYTES", `OCTET_LENGTH("ARTIFACT") BETWEEN 1 AND 65536`), check("PLATFORM_LICENSE_REASON", `LENGTH("REASON")>0`), check("PLATFORM_LICENSE_PREDECESSOR", `"PREDECESSOR_ID" IS NULL OR "PREDECESSOR_ID"<>"LICENSE_ID"`)), nil)
	theme := text("THEME", 8, false)
	theme.Default = "'light'"
	tableStep(nativeTable("USER_PREFERENCES", []column{uuid("USER_ID", false), theme, version("REVISION"), stamp("UPDATED_AT", false, true)}, pk("USER_PREFERENCES_PK", "USER_ID"), fk("USER_PREFERENCES_USER_FK", "USERS", "USERS_PK", "USER_ID"), check("USER_PREFERENCES_THEME", `"THEME" IN ('light','dark')`), bounded("USER_PREFERENCES_REVISION", "REVISION")), map[string]string{"USER_PREFERENCES_USER_FK": "CASCADE"})
	add(schemaChange{ID: "USER_PREFERENCES_SEED", Kind: "backfill", SQL: `INSERT INTO "USER_PREFERENCES" ("USER_ID") SELECT u."ID" FROM "USERS" u WHERE NOT EXISTS (SELECT 1 FROM "USER_PREFERENCES" p WHERE p."USER_ID"=u."ID")`, Probe: `SELECT COUNT(*) FROM "USERS" u WHERE NOT EXISTS (SELECT 1 FROM "USER_PREFERENCES" p WHERE p."USER_ID"=u."ID")`, ProbeTables: []string{"USERS", "USER_PREFERENCES"}})
	triggerStep("USER_PREFERENCES_INITIALIZE", "USERS", "INSERT", "AFTER", `BEGIN INSERT INTO "USER_PREFERENCES" ("USER_ID") VALUES (:NEW."ID"); END;`)
	triggerStep("USER_PREFERENCES_REVISION_GUARD", "USER_PREFERENCES", "UPDATE", "BEFORE", `BEGIN IF :NEW."USER_ID"<>:OLD."USER_ID" THEN RAISE_APPLICATION_ERROR(-20001,'PREFERENCE_OWNER_IMMUTABLE'); END IF; IF :NEW."REVISION"<>:OLD."REVISION"+1 THEN RAISE_APPLICATION_ERROR(-20002,'PREFERENCE_REVISION_MUST_ADVANCE'); END IF; END;`)

	for _, t := range ssoTables() {
		tableStep(t, nil)
	}
	before := known["SESSIONS"]
	after := cloneTable(before)
	added := []column{uuid("SSO_TENANT_ID", true), uuid("SSO_CONNECTOR_ID", true), uuid("SSO_BINDING_ID", true), col("SSO_AUTH_VERSION", "BIGINT", 0, true, ""), uuid("SSO_FLOW_ID", true)}
	parts := []string{}
	for _, v := range added {
		parts = append(parts, v.declaration())
	}
	after.Columns = append(after.Columns, added...)
	alter("SESSIONS_SSO_COLUMNS", `ALTER TABLE "SESSIONS" ADD (`+strings.Join(parts, ",")+")", after)
	for _, v := range nativeTable("SESSIONS", added).Constraints {
		constraintStep("SESSIONS", v)
	}
	constraintStep("SESSIONS", check("SESSIONS_SSO_SOURCE_COMPLETE", `("SSO_TENANT_ID" IS NULL AND "SSO_CONNECTOR_ID" IS NULL AND "SSO_BINDING_ID" IS NULL AND "SSO_AUTH_VERSION" IS NULL AND "SSO_FLOW_ID" IS NULL) OR ("SSO_TENANT_ID" IS NOT NULL AND "SSO_CONNECTOR_ID" IS NOT NULL AND "SSO_BINDING_ID" IS NOT NULL AND "SSO_AUTH_VERSION" IS NOT NULL AND "SSO_AUTH_VERSION" BETWEEN 1 AND 9007199254740991 AND "SSO_FLOW_ID" IS NOT NULL)`))
	constraintStep("SESSIONS", fk("SESSIONS_SSO_BINDING_FK", "SSO_BINDINGS", "SSO_BINDINGS_SCOPE_USER_UK", "SSO_TENANT_ID", "SSO_CONNECTOR_ID", "SSO_BINDING_ID", "USER_ID"))
	constraintStep("SESSIONS", fk("SESSIONS_SSO_FLOW_FK", "SSO_FLOWS", "SSO_FLOWS_SCOPE_USER_UK", "SSO_TENANT_ID", "SSO_CONNECTOR_ID", "SSO_FLOW_ID", "USER_ID"))
	constraintStep("SESSIONS", unique("SESSIONS_SSO_FLOW_UK", "SSO_FLOW_ID"))
	indexStep("SSO_CONNECTORS_PAGE_IDX", "SSO_CONNECTORS", []string{"TENANT_ID", "CREATED_AT", "ID"}, []bool{false, true, true})
	indexStep("SSO_BINDINGS_USER_IDX", "SSO_BINDINGS", []string{"TENANT_ID", "USER_ID", "CREATED_AT", "ID"}, nil)
	indexStep("SSO_FLOWS_EXPIRY_IDX", "SSO_FLOWS", []string{"STATE", "EXPIRES_AT", "ID"}, nil)
	indexStep("SSO_FLOWS_CONNECTOR_IDX", "SSO_FLOWS", []string{"TENANT_ID", "CONNECTOR_ID", "STATE"}, nil)
	indexStep("SESSIONS_SSO_CONNECTOR_LIVE_IDX", "SESSIONS", []string{"SSO_TENANT_ID", "SSO_CONNECTOR_ID", "REVOKED_AT"}, nil)
	indexStep("SESSIONS_SSO_BINDING_LIVE_IDX", "SESSIONS", []string{"SSO_BINDING_ID", "REVOKED_AT"}, nil)
	for _, t := range ssoTriggers(known["SSO_FLOWS"]) {
		triggerStep(t.Name, t.Table, t.Event, strings.TrimSuffix(t.Timing, " EACH ROW"), t.Body)
	}
	return f
}

func ssoTables() []table {
	return []table{
		nativeTable("SSO_CONNECTORS", []column{uuid("ID", false), uuid("TENANT_ID", false), text("NAME", 100, false), text("PROVIDER", 16, false), text("ORIGIN", 2048, false), uuid("CLIENT_ID", false), text("SECRET_KEY_ID", 64, false), col("SECRET_NONCE", "VARBINARY", 12, false, ""), col("SECRET_CIPHERTEXT", "VARBINARY", 4112, false, ""), col("ENABLED", "BIT", 0, false, ""), version("REVISION"), version("AUTH_VERSION"), uuid("CREATED_BY", false), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true)}, pk("SSO_CONNECTORS_PK", "ID"), unique("SSO_CONNECTORS_SCOPE_UK", "TENANT_ID", "ID"), fk("SSO_CONNECTORS_TENANT_FK", "TENANTS", "TENANTS_PK", "TENANT_ID"), fk("SSO_CONNECTORS_CREATOR_FK", "USERS", "USERS_PK", "CREATED_BY"), check("SSO_CONNECTORS_NAME", `LENGTH(TRIM("NAME"))>0`), check("SSO_CONNECTORS_PROVIDER", `"PROVIDER"='gitea'`), check("SSO_CONNECTORS_NONCE", `OCTET_LENGTH("SECRET_NONCE")=12`), check("SSO_CONNECTORS_CIPHER", `OCTET_LENGTH("SECRET_CIPHERTEXT") BETWEEN 17 AND 4112`), bounded("SSO_CONNECTORS_REVISION", "REVISION"), bounded("SSO_CONNECTORS_AUTH_VERSION", "AUTH_VERSION")),
		nativeTable("SSO_BINDINGS", []column{uuid("ID", false), uuid("TENANT_ID", false), uuid("CONNECTOR_ID", false), text("SUBJECT", 19, false), uuid("USER_ID", false), col("ACTIVE", "BIT", 0, false, "1"), version("REVISION"), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true)}, pk("SSO_BINDINGS_PK", "ID"), unique("SSO_BINDINGS_SUBJECT_UK", "TENANT_ID", "CONNECTOR_ID", "SUBJECT"), unique("SSO_BINDINGS_SCOPE_USER_UK", "TENANT_ID", "CONNECTOR_ID", "ID", "USER_ID"), fk("SSO_BINDINGS_CONNECTOR_FK", "SSO_CONNECTORS", "SSO_CONNECTORS_SCOPE_UK", "TENANT_ID", "CONNECTOR_ID"), fk("SSO_BINDINGS_USER_FK", "USERS", "USERS_PK", "USER_ID"), bounded("SSO_BINDINGS_REVISION", "REVISION"), check("SSO_BINDINGS_SUBJECT", `REGEXP_LIKE("SUBJECT",'^[1-9][0-9]{0,18}$','c') AND (LENGTH("SUBJECT")<19 OR "SUBJECT"<='9223372036854775807')`)),
		ssoFlowTable(),
		nativeTable("SSO_MUTATION_INTENTS", []column{uuid("TENANT_ID", false), uuid("ACTOR_USER_ID", false), uuid("IDEMPOTENCY_KEY", false), text("OPERATION", 24, false), uuid("TARGET_ID", false), col("FINGERPRINT", "VARBINARY", 32, false, ""), col("RESULT", "CLOB", 0, false, ""), stamp("CREATED_AT", false, true)}, pk("SSO_MUTATION_INTENTS_PK", "TENANT_ID", "ACTOR_USER_ID", "IDEMPOTENCY_KEY"), fk("SSO_MUTATION_INTENTS_TENANT_FK", "TENANTS", "TENANTS_PK", "TENANT_ID"), fk("SSO_MUTATION_INTENTS_ACTOR_FK", "USERS", "USERS_PK", "ACTOR_USER_ID"), check("SSO_MUTATION_INTENTS_OPERATION", `"OPERATION" IN ('create_connector','update_connector','unlink_binding')`), check("SSO_MUTATION_INTENTS_HASH", `OCTET_LENGTH("FINGERPRINT")=32`), check("SSO_MUTATION_INTENTS_RESULT", `JSON_TYPE("RESULT") IS NOT NULL AND JSON_TYPE("RESULT")='OBJECT'`)),
	}
}
func ssoFlowTable() table {
	state := text("STATE", 16, false)
	state.Default = "'pending'"
	return nativeTable("SSO_FLOWS", []column{uuid("ID", false), uuid("TENANT_ID", false), uuid("CONNECTOR_ID", false), col("CONNECTOR_REVISION", "BIGINT", 0, false, ""), col("AUTH_VERSION", "BIGINT", 0, false, ""), text("AUDIENCE", 16, false), text("MODE", 16, false), state, col("STATE_HASH", "VARBINARY", 32, false, ""), col("BROWSER_HASH", "VARBINARY", 32, false, ""), col("PREVIOUS_COOKIE_HASH", "VARBINARY", 32, false, ""), uuid("PREVIOUS_SESSION_ID", true), uuid("INITIATING_USER_ID", true), uuid("INITIATING_SESSION_ID", true), col("INITIATING_EPOCH", "BIGINT", 0, true, ""), col("CREDENTIAL_VERSION", "BIGINT", 0, true, ""), text("REASON", 500, true), text("VERIFIER_KEY_ID", 64, true), col("VERIFIER_NONCE", "VARBINARY", 12, true, ""), col("VERIFIER_CIPHERTEXT", "VARBINARY", 59, true, ""), uuid("CLAIM_ID", true), uuid("AUTHENTICATED_USER_ID", true), uuid("ISSUED_SESSION_ID", true), text("ERROR_CODE", 80, true), stamp("CREATED_AT", false, true), stamp("EXPIRES_AT", false, false), stamp("FINISHED_AT", true, false)}, pk("SSO_FLOWS_PK", "ID"), unique("SSO_FLOWS_STATE_HASH_UK", "STATE_HASH"), unique("SSO_FLOWS_SCOPE_UK", "TENANT_ID", "CONNECTOR_ID", "ID"), unique("SSO_FLOWS_SCOPE_USER_UK", "TENANT_ID", "CONNECTOR_ID", "ID", "AUTHENTICATED_USER_ID"), fk("SSO_FLOWS_CONNECTOR_FK", "SSO_CONNECTORS", "SSO_CONNECTORS_SCOPE_UK", "TENANT_ID", "CONNECTOR_ID"), fk("SSO_FLOWS_INITIATOR_FK", "USERS", "USERS_PK", "INITIATING_USER_ID"), fk("SSO_FLOWS_AUTHENTICATED_FK", "USERS", "USERS_PK", "AUTHENTICATED_USER_ID"), bounded("SSO_FLOWS_REVISION", "CONNECTOR_REVISION"), bounded("SSO_FLOWS_AUTH_VERSION", "AUTH_VERSION"), check("SSO_FLOWS_AUDIENCE", `"AUDIENCE" IN ('portal','admin')`), check("SSO_FLOWS_MODE", `"MODE" IN ('login','link')`), check("SSO_FLOWS_STATE", `"STATE" IN ('pending','claimed','succeeded','failed','expired')`), check("SSO_FLOWS_HASHES", `OCTET_LENGTH("STATE_HASH")=32 AND OCTET_LENGTH("BROWSER_HASH")=32 AND OCTET_LENGTH("PREVIOUS_COOKIE_HASH")=32`), check("SSO_FLOWS_EXPIRY", `"EXPIRES_AT">"CREATED_AT"`),
		check("SSO_FLOWS_LINK_CONTEXT", `("MODE"='login' AND "INITIATING_USER_ID" IS NULL AND "INITIATING_SESSION_ID" IS NULL AND "INITIATING_EPOCH" IS NULL AND "CREDENTIAL_VERSION" IS NULL AND "REASON" IS NULL) OR ("MODE"='link' AND "PREVIOUS_SESSION_ID" IS NOT NULL AND "PREVIOUS_SESSION_ID"="INITIATING_SESSION_ID" AND "INITIATING_USER_ID" IS NOT NULL AND "INITIATING_SESSION_ID" IS NOT NULL AND "INITIATING_EPOCH" IS NOT NULL AND "CREDENTIAL_VERSION" IS NOT NULL AND "REASON" IS NOT NULL AND "INITIATING_EPOCH" BETWEEN 1 AND 9007199254740991 AND "CREDENTIAL_VERSION" BETWEEN 1 AND 9007199254740991 AND LENGTH(TRIM("REASON")) BETWEEN 1 AND 500)`),
		check("SSO_FLOWS_VERIFIER", `("STATE"='pending' AND "VERIFIER_KEY_ID" IS NOT NULL AND "VERIFIER_NONCE" IS NOT NULL AND OCTET_LENGTH("VERIFIER_NONCE")=12 AND "VERIFIER_CIPHERTEXT" IS NOT NULL AND OCTET_LENGTH("VERIFIER_CIPHERTEXT")=59 AND "CLAIM_ID" IS NULL) OR ("STATE"<>'pending' AND "VERIFIER_KEY_ID" IS NULL AND "VERIFIER_NONCE" IS NULL AND "VERIFIER_CIPHERTEXT" IS NULL)`), check("SSO_FLOWS_CLAIM", `"STATE" NOT IN ('claimed','succeeded') OR "CLAIM_ID" IS NOT NULL`), check("SSO_FLOWS_AUTHENTICATED", `("STATE"='succeeded' AND "AUTHENTICATED_USER_ID" IS NOT NULL) OR ("STATE"<>'succeeded' AND "AUTHENTICATED_USER_ID" IS NULL)`), check("SSO_FLOWS_ISSUED", `"STATE"='succeeded' OR "ISSUED_SESSION_ID" IS NULL`), check("SSO_FLOWS_FINISHED", `("STATE" IN ('succeeded','failed','expired') AND "FINISHED_AT" IS NOT NULL) OR ("STATE" IN ('pending','claimed') AND "FINISHED_AT" IS NULL)`), check("SSO_FLOWS_ERROR", `("STATE" IN ('failed','expired') AND "ERROR_CODE" IS NOT NULL AND REGEXP_LIKE("ERROR_CODE",'^[A-Z][A-Z0-9_]{1,79}$','c')) OR ("STATE" NOT IN ('failed','expired') AND "ERROR_CODE" IS NULL)`))
}

func changed(columns ...string) string {
	parts := []string{}
	for _, col := range columns {
		n, o := ":NEW."+quoted(col), ":OLD."+quoted(col)
		parts = append(parts, "(("+n+" IS NOT NULL AND "+o+" IS NOT NULL AND "+n+"<>"+o+") OR ("+n+" IS NULL AND "+o+" IS NOT NULL) OR ("+n+" IS NOT NULL AND "+o+" IS NULL))")
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}
func ssoTriggers(flow table) []trigger {
	guard := func(condition, code string) string {
		return "IF " + condition + " THEN RAISE_APPLICATION_ERROR(-20003,'" + code + "'); END IF; "
	}
	identity := []string{"ID", "TENANT_ID", "CONNECTOR_ID", "CONNECTOR_REVISION", "AUTH_VERSION", "AUDIENCE", "MODE", "STATE_HASH", "BROWSER_HASH", "PREVIOUS_COOKIE_HASH", "PREVIOUS_SESSION_ID", "INITIATING_USER_ID", "INITIATING_SESSION_ID", "INITIATING_EPOCH", "CREDENTIAL_VERSION", "REASON", "CREATED_AT", "EXPIRES_AT"}
	allExceptIssued := []string{}
	for _, col := range flow.Columns {
		if col.Name != "ISSUED_SESSION_ID" {
			allExceptIssued = append(allExceptIssued, col.Name)
		}
	}
	return []trigger{
		{"SSO_CONNECTOR_IDENTITY", "SSO_CONNECTORS", "UPDATE", "BEFORE EACH ROW", "BEGIN " + guard(changed("ID", "TENANT_ID", "PROVIDER", "ORIGIN", "CREATED_BY", "CREATED_AT"), "SSO_CONNECTOR_IDENTITY_IMMUTABLE") + guard(`:NEW."REVISION"<>:OLD."REVISION"+1 OR :NEW."AUTH_VERSION" NOT IN (:OLD."AUTH_VERSION",:OLD."AUTH_VERSION"+1) OR (`+changed("ENABLED", "CLIENT_ID", "SECRET_KEY_ID", "SECRET_NONCE", "SECRET_CIPHERTEXT")+` AND :NEW."AUTH_VERSION"<>:OLD."AUTH_VERSION"+1)`, "SSO_CONNECTOR_REVISION_INVALID") + "END;"},
		{"SSO_BINDING_IDENTITY", "SSO_BINDINGS", "UPDATE", "BEFORE EACH ROW", "BEGIN " + guard(changed("ID", "TENANT_ID", "CONNECTOR_ID", "SUBJECT", "USER_ID", "CREATED_AT")+` OR :NEW."REVISION"<>:OLD."REVISION"+1`, "SSO_BINDING_IDENTITY_IMMUTABLE") + "END;"},
		{"SSO_FLOW_BEGIN", "SSO_FLOWS", "INSERT", "BEFORE EACH ROW", "BEGIN " + guard(`:NEW."STATE"<>'pending'`, "SSO_FLOW_MUST_BEGIN_PENDING") + "END;"},
		{"SSO_FLOW_TRANSITION", "SSO_FLOWS", "UPDATE", "BEFORE EACH ROW", `BEGIN IF NOT (:OLD."STATE"='succeeded' AND :NEW."STATE"='succeeded' AND :OLD."ISSUED_SESSION_ID" IS NULL AND :NEW."ISSUED_SESSION_ID" IS NOT NULL AND NOT ` + changed(allExceptIssued...) + ") THEN " + guard(changed(identity...)+" OR "+changed("ISSUED_SESSION_ID")+` OR NOT ((:OLD."STATE"='pending' AND :NEW."STATE" IN ('claimed','failed','expired')) OR (:OLD."STATE"='claimed' AND :NEW."STATE" IN ('succeeded','failed','expired'))) OR (:OLD."CLAIM_ID" IS NOT NULL AND `+changed("CLAIM_ID")+")", "SSO_FLOW_TRANSITION_INVALID") + "END IF; END;"},
		{"SESSIONS_SSO_SINGLE_ISSUANCE", "SESSIONS", "INSERT", "BEFORE EACH ROW", `BEGIN IF :NEW."SSO_FLOW_ID" IS NOT NULL THEN UPDATE "SSO_FLOWS" SET "ISSUED_SESSION_ID"=:NEW."ID" WHERE "TENANT_ID"=:NEW."SSO_TENANT_ID" AND "CONNECTOR_ID"=:NEW."SSO_CONNECTOR_ID" AND "ID"=:NEW."SSO_FLOW_ID" AND "AUTHENTICATED_USER_ID"=:NEW."USER_ID" AND "AUTH_VERSION"=:NEW."SSO_AUTH_VERSION" AND "STATE"='succeeded' AND "ISSUED_SESSION_ID" IS NULL; IF SQL%ROWCOUNT<>1 THEN RAISE_APPLICATION_ERROR(-20004,'SSO_FLOW_ALREADY_ISSUED'); END IF; END IF; END;`},
		{"SSO_INTENT_UPDATE", "SSO_MUTATION_INTENTS", "UPDATE", "BEFORE EACH ROW", `BEGIN RAISE_APPLICATION_ERROR(-20005,'SSO_INTENT_IMMUTABLE'); END;`},
		{"SSO_INTENT_DELETE", "SSO_MUTATION_INTENTS", "DELETE", "BEFORE EACH ROW", `BEGIN RAISE_APPLICATION_ERROR(-20005,'SSO_INTENT_IMMUTABLE'); END;`},
		{"SSO_CONNECTOR_RETAINED", "SSO_CONNECTORS", "DELETE", "BEFORE EACH ROW", `BEGIN RAISE_APPLICATION_ERROR(-20005,'SSO_INTENT_IMMUTABLE'); END;`},
		{"SSO_BINDING_RETAINED", "SSO_BINDINGS", "DELETE", "BEFORE EACH ROW", `BEGIN RAISE_APPLICATION_ERROR(-20005,'SSO_INTENT_IMMUTABLE'); END;`},
	}
}
