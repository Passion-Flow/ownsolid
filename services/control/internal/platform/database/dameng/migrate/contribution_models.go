package migrate

import (
	"strconv"
	"strings"
)

// modelsFragment is the approved-base import / deployment identity / M07 native
// contribution. Central registration owns append-only numbering and execution.
func modelsFragment(current map[string]table) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "models_v2", Requires: []string{"foundation_v1", "nodes_components_v2"}}
	known := map[string]table{}
	for name, t := range current {
		known[name] = cloneTable(t)
	}
	add := func(c schemaChange) {
		f.Changes = append(f.Changes, c)
		if c.After != nil {
			known[c.After.Name] = cloneTable(*c.After)
		}
	}
	create := func(t table) {
		add(schemaChange{ID: t.Name, Kind: "table", SQL: extensionTableSQL(t, known, nil), After: &t})
	}
	reference := func(name, target string, local, targetColumns []string) constraint {
		return modelsReference(known, name, target, local, targetColumns)
	}
	user := func(name, field string) constraint { return reference(name, "USERS", []string{field}, []string{"ID"}) }
	scope := func(name string) constraint {
		return reference(name, "WORKSPACES", []string{"TENANT_ID", "WORKSPACE_ID"}, []string{"TENANT_ID", "ID"})
	}
	node := func(name string) constraint {
		return reference(name, "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	}
	model := func(name string) constraint {
		return reference(name, "MODEL_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "MODEL_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	}
	deployment := func(name string) constraint {
		return reference(name, "DEPLOYMENTS", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "DEPLOYMENT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"})
	}
	triggerStep := func(name, owner, event, body string) {
		v := trigger{Name: name, Table: owner, Event: event, Timing: "BEFORE EACH ROW", Body: body}
		add(schemaChange{ID: name, Kind: "trigger", SQL: "CREATE TRIGGER " + quoted(name) + " BEFORE " + event + " ON " + quoted(owner) + " FOR EACH ROW " + body, Trigger: &v})
	}
	indexStep := func(name, owner string, columns []string, descending []bool) {
		if descending == nil {
			descending = make([]bool, len(columns))
		}
		v := index{Name: name, Table: owner, Columns: columns, Descending: descending}
		add(schemaChange{ID: name, Kind: "index", SQL: v.ddl(), Index: &v})
	}

	// DM exempts a composite unique key only when every key column is NULL.
	// Apply the same lifecycle predicate to every component of conditional keys.
	create(nativeTable("MODEL_VERSIONS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("NAME", 100, false), text("MODEL_KEY", 64, false), text("CATALOG_DIGEST", 64, false), col("MANIFEST", "CLOB", 0, false, ""), text("STATE", 16, false), uuid("CREATED_BY", false), stamp("CREATED_AT", false, true), stamp("DELETED_AT", true, false), col("KIND", "VARCHAR", 24, false, "'approved_base'"), modelsVirtual("READY_TENANT_ID", "CHAR", 36, `CASE WHEN "STATE"='ready' THEN "TENANT_ID" ELSE NULL END`), modelsVirtual("READY_WORKSPACE_ID", "CHAR", 36, `CASE WHEN "STATE"='ready' THEN "WORKSPACE_ID" ELSE NULL END`), modelsVirtual("READY_CATALOG_DIGEST", "VARCHAR", 64, `CASE WHEN "STATE"='ready' THEN "CATALOG_DIGEST" ELSE NULL END`),
	}, pk("MODEL_VERSIONS_PK", "ID"), unique("MODELS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("MODELS_CATALOG_ID_UK", "TENANT_ID", "WORKSPACE_ID", "ID", "CATALOG_DIGEST"), unique("MODELS_KIND_CATALOG_UK", "TENANT_ID", "WORKSPACE_ID", "ID", "KIND", "CATALOG_DIGEST"), unique("MODELS_READY_CATALOG_UK", "READY_TENANT_ID", "READY_WORKSPACE_ID", "READY_CATALOG_DIGEST"), scope("MODELS_WORKSPACE_FK"), user("MODELS_CREATOR_FK", "CREATED_BY"), modelsDigest("MODELS_DIGEST", "CATALOG_DIGEST"), modelsJSON("MODELS_MANIFEST", "MANIFEST", 16384), check("MODELS_APPROVED_KIND", `"KIND"='approved_base' AND "MODEL_KEY"='qwen3-0.6b' AND "STATE" IN ('ready','deleted')`)))
	// No merged_lora row is accepted before its native preparation/publication
	// guards exist. Later support requires a separate reviewed ALTER contribution.
	create(nativeTable("MODEL_IMPORTS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("NAME", 100, false), text("CATALOG_DIGEST", 64, false), col("EXPECTED_BYTES", "BIGINT", 0, false, ""), col("UPLOADED_BYTES", "BIGINT", 0, false, "0"), text("STATE", 16, false), text("ERROR_CODE", 64, true), col("ATTEMPTS", "INTEGER", 0, false, "0"), uuid("REQUESTED_BY", false), col("REQUESTED_EPOCH", "BIGINT", 0, false, ""), uuid("IDEMPOTENCY_KEY", false), col("FINGERPRINT", "VARBINARY", 32, false, ""), text("REASON", 500, false), uuid("MODEL_ID", true), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true), modelsVirtual("ACTIVE_TENANT_ID", "CHAR", 36, `CASE WHEN "STATE" NOT IN ('ready','cancelled') THEN "TENANT_ID" ELSE NULL END`), modelsVirtual("ACTIVE_WORKSPACE_ID", "CHAR", 36, `CASE WHEN "STATE" NOT IN ('ready','cancelled') THEN "WORKSPACE_ID" ELSE NULL END`),
	}, pk("MODEL_IMPORTS_PK", "ID"), unique("MODEL_IMPORT_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("MODEL_IMPORT_INTENT_UK", "TENANT_ID", "WORKSPACE_ID", "IDEMPOTENCY_KEY"), unique("MODEL_IMPORT_ACTIVE_UK", "ACTIVE_TENANT_ID", "ACTIVE_WORKSPACE_ID"), scope("MODEL_IMPORT_SCOPE_FK"), user("MODEL_IMPORT_ACTOR_FK", "REQUESTED_BY"), model("MODEL_IMPORT_VERSION_FK"), modelsDigest("MODEL_IMPORT_DIGEST", "CATALOG_DIGEST"), check("MODEL_IMPORT_BOUNDS", `"EXPECTED_BYTES" BETWEEN 1 AND 2147483648 AND "UPLOADED_BYTES" BETWEEN 0 AND "EXPECTED_BYTES" AND "ATTEMPTS" BETWEEN 0 AND 3 AND "REQUESTED_EPOCH">=0 AND OCTET_LENGTH("FINGERPRINT")=32`), check("MODEL_IMPORT_STATE", `"STATE" IN ('uploading','queued','verifying','ready','failed','cancelled')`)))
	create(nativeTable("MODEL_IMPORT_CHUNKS", []column{uuid("IMPORT_ID", false), col("BYTE_OFFSET", "BIGINT", 0, false, ""), col("SIZE_BYTES", "INTEGER", 0, false, ""), text("SHA256", 64, false)}, pk("MODEL_CHUNKS_PK", "IMPORT_ID", "BYTE_OFFSET"), reference("MODEL_CHUNKS_IMPORT_FK", "MODEL_IMPORTS", []string{"IMPORT_ID"}, []string{"ID"}), modelsDigest("MODEL_CHUNKS_DIGEST", "SHA256"), check("MODEL_CHUNKS_BOUNDS", `"BYTE_OFFSET">=0 AND MOD("BYTE_OFFSET",8388608)=0 AND "SIZE_BYTES" BETWEEN 1 AND 8388608`)))
	indexStep("MODEL_IMPORT_QUEUE_IDX", "MODEL_IMPORTS", []string{"STATE", "CREATED_AT", "ID"}, nil)
	indexStep("MODEL_DIRECTORY_IDX", "MODEL_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "STATE", "CREATED_AT", "ID"}, []bool{false, false, false, true, true})

	create(nativeTable("DEPLOYMENTS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("NODE_ID", false), uuid("MODEL_ID", false), text("GPU_UUID", 64, false), text("DESIRED_STATE", 16, false), col("OBSERVED_STATE", "VARCHAR", 16, false, "'unknown'"), col("REVISION", "BIGINT", 0, false, ""), col("OBSERVATION", "CLOB", 0, true, ""), stamp("OBSERVED_AT", true, false), uuid("LAST_TASK_ID", true), stamp("REMOVED_AT", true, false), uuid("CREATED_BY", false), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true), text("RECOVERY_RECEIPT_SHA256", 64, true), col("MODEL_KIND", "VARCHAR", 20, false, "'approved_base'"), text("CATALOG_DIGEST", 64, false), text("SERVING_RECIPE_SHA256", 64, true), modelsVirtual("RETAINED_NODE_ID", "CHAR", 36, `CASE WHEN "REMOVED_AT" IS NULL THEN "NODE_ID" ELSE NULL END`),
	}, pk("DEPLOYMENTS_PK", "ID"), unique("DEPLOYMENTS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("DEPLOYMENTS_NODE_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"), unique("DEPLOYMENTS_RETAINED_UK", "RETAINED_NODE_ID"), node("DEPLOYMENTS_NODE_FK"), model("DEPLOYMENTS_MODEL_FK"), user("DEPLOYMENTS_CREATOR_FK", "CREATED_BY"), reference("DEPLOYMENTS_TASK_FK", "NODE_TASKS", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "LAST_TASK_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"}), reference("DEPLOYMENTS_MODEL_IDENTITY_FK", "MODEL_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "MODEL_ID", "MODEL_KIND", "CATALOG_DIGEST"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID", "KIND", "CATALOG_DIGEST"}), bounded("DEPLOYMENTS_REVISION", "REVISION"), modelsJSON("DEPLOYMENTS_OBSERVATION", "OBSERVATION", 8192), modelsDigest("DEPLOYMENTS_RECEIPT", "RECOVERY_RECEIPT_SHA256"), modelsDigest("DEPLOYMENTS_CATALOG", "CATALOG_DIGEST"), check("DEPLOYMENTS_GPU", `REGEXP_LIKE("GPU_UUID",'^GPU-[a-f0-9-]{36}$','c')`), check("DEPLOYMENTS_DESIRED", `"DESIRED_STATE" IN ('running','stopped','removed')`), check("DEPLOYMENTS_OBSERVED", `"OBSERVED_STATE" IN ('running','loading','stopped','not_installed','unknown','failed')`), check("DEPLOYMENTS_VARIANT", `"MODEL_KIND"='approved_base' AND "SERVING_RECIPE_SHA256" IS NULL`)))
	create(nativeTable("GPU_LEASES", []column{uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("NODE_ID", false), text("GPU_UUID", 64, false), uuid("DEPLOYMENT_ID", false), col("REVISION", "BIGINT", 0, false, ""), text("STATE", 24, false), stamp("UPDATED_AT", false, true), modelsVirtual("RESERVED_NODE_ID", "CHAR", 36, `CASE WHEN "STATE" IN ('held','recovery_required') THEN "NODE_ID" ELSE NULL END`), modelsVirtual("RESERVED_GPU_UUID", "VARCHAR", 64, `CASE WHEN "STATE" IN ('held','recovery_required') THEN "GPU_UUID" ELSE NULL END`)}, pk("GPU_LEASES_PK", "DEPLOYMENT_ID"), unique("GPU_NODE_EXCLUSIVE_UK", "RESERVED_NODE_ID"), unique("GPU_UUID_EXCLUSIVE_UK", "RESERVED_GPU_UUID"), deployment("GPU_DEPLOYMENT_FK"), bounded("GPU_REVISION", "REVISION"), check("GPU_STATE", `"STATE" IN ('held','released','recovery_required')`)))
	indexStep("DEPLOYMENTS_SCOPE_IDX", "DEPLOYMENTS", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, []bool{false, false, true, true})

	create(nativeTable("GATEWAY_INSTANCES", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("NODE_ID", false), text("NAME", 100, false), text("CLIENT_ORIGIN", 2048, false), text("CONTROL_ORIGIN_SHA256", 64, false), col("CREDENTIAL_HASH", "VARBINARY", 32, true, ""), uuid("CREATED_BY", false), col("REQUESTED_EPOCH", "BIGINT", 0, false, ""), uuid("IDEMPOTENCY_KEY", false), col("FINGERPRINT", "VARBINARY", 32, false, ""), stamp("CREATED_AT", false, true), stamp("REVOKED_AT", true, false), text("REVOKE_REASON", 500, true), stamp("AUTHORITY_INVALIDATED_AT", true, false), modelsVirtual("ACTIVE_NODE_ID", "CHAR", 36, `CASE WHEN "REVOKED_AT" IS NULL AND "AUTHORITY_INVALIDATED_AT" IS NULL THEN "NODE_ID" ELSE NULL END`),
	}, pk("GATEWAY_INSTANCES_PK", "ID"), unique("GATEWAY_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("GATEWAY_NODE_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"), unique("GATEWAY_INTENT_UK", "TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "IDEMPOTENCY_KEY"), unique("GATEWAY_ACTIVE_NODE_UK", "ACTIVE_NODE_ID"), node("GATEWAY_NODE_FK"), user("GATEWAY_CREATOR_FK", "CREATED_BY"), modelsDigest("GATEWAY_ORIGIN_DIGEST", "CONTROL_ORIGIN_SHA256"), check("GATEWAY_LIMITS", `CHAR_LENGTH("CLIENT_ORIGIN") BETWEEN 9 AND 2048 AND "REQUESTED_EPOCH">=0 AND OCTET_LENGTH("FINGERPRINT")=32 AND ("CREDENTIAL_HASH" IS NULL OR OCTET_LENGTH("CREDENTIAL_HASH")=32)`), check("GATEWAY_REVOCATION", `("REVOKED_AT" IS NULL OR ("REVOKE_REASON" IS NOT NULL AND CHAR_LENGTH("REVOKE_REASON")>0)) AND ("CREDENTIAL_HASH" IS NOT NULL OR "AUTHORITY_INVALIDATED_AT" IS NOT NULL)`)))
	create(nativeTable("MODEL_ALIASES", []column{uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("NODE_ID", false), uuid("GATEWAY_ID", false), text("NAME", 64, false), uuid("DEPLOYMENT_ID", false), col("DEPLOYMENT_REVISION", "BIGINT", 0, false, ""), version("REVISION"), uuid("CREATED_BY", false), uuid("IDEMPOTENCY_KEY", false), col("FINGERPRINT", "VARBINARY", 32, false, ""), stamp("CREATED_AT", false, true), stamp("DISABLED_AT", true, false)}, pk("MODEL_ALIASES_PK", "ID"), unique("ALIAS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("ALIAS_NAME_UK", "TENANT_ID", "WORKSPACE_ID", "NAME"), unique("ALIAS_INTENT_UK", "TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "IDEMPOTENCY_KEY"), reference("ALIAS_GATEWAY_FK", "GATEWAY_INSTANCES", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "GATEWAY_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"}), deployment("ALIAS_DEPLOYMENT_FK"), user("ALIAS_CREATOR_FK", "CREATED_BY"), bounded("ALIAS_REVISION", "REVISION"), bounded("ALIAS_DEPLOYMENT_REVISION", "DEPLOYMENT_REVISION"), check("ALIAS_NAME", `REGEXP_LIKE("NAME",'^[a-z][a-z0-9.-]{0,63}$','c') AND OCTET_LENGTH("FINGERPRINT")=32`)))
	create(nativeTable("MODEL_ALIAS_HISTORY", []column{uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("ALIAS_ID", false), col("REVISION", "BIGINT", 0, false, ""), text("ACTION", 20, false), uuid("ACTOR_USER_ID", false), text("REASON", 500, false), stamp("OCCURRED_AT", false, true)}, pk("ALIAS_HISTORY_PK", "TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "REVISION"), reference("ALIAS_HISTORY_ALIAS_FK", "MODEL_ALIASES", []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"}), user("ALIAS_HISTORY_ACTOR_FK", "ACTOR_USER_ID"), bounded("ALIAS_HISTORY_REVISION", "REVISION"), check("ALIAS_HISTORY_ACTION", `"ACTION" IN ('published','disabled') AND CHAR_LENGTH("REASON")>0`)))
	create(nativeTable("MODEL_ALIAS_TARGET_VERSIONS", []column{uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("ALIAS_ID", false), col("REVISION", "BIGINT", 0, false, ""), text("ACTION", 20, false), uuid("DEPLOYMENT_ID", false), col("DEPLOYMENT_REVISION", "BIGINT", 0, false, ""), uuid("MODEL_ID", false), text("MODEL_KIND", 20, false), text("CATALOG_DIGEST", 64, false), text("SERVING_RECIPE_SHA256", 64, true), col("SOURCE_REVISION", "BIGINT", 0, true, ""), stamp("CREATED_AT", false, true)}, pk("ALIAS_TARGET_PK", "ID"), unique("ALIAS_TARGET_REVISION_UK", "TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "REVISION"), reference("ALIAS_TARGET_HISTORY_FK", "MODEL_ALIAS_HISTORY", []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "REVISION"}, []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "REVISION"}), reference("ALIAS_TARGET_DEPLOYMENT_FK", "DEPLOYMENTS", []string{"TENANT_ID", "WORKSPACE_ID", "DEPLOYMENT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"}), reference("ALIAS_TARGET_MODEL_FK", "MODEL_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "MODEL_ID", "MODEL_KIND", "CATALOG_DIGEST"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID", "KIND", "CATALOG_DIGEST"}), bounded("ALIAS_TARGET_REVISION", "REVISION"), bounded("ALIAS_TARGET_DEPLOYMENT_REVISION", "DEPLOYMENT_REVISION"), modelsDigest("ALIAS_TARGET_DIGEST", "CATALOG_DIGEST"), check("ALIAS_TARGET_FIRST_PATH", `"ACTION" IN ('published','disabled') AND "SOURCE_REVISION" IS NULL AND "MODEL_KIND"='approved_base' AND "SERVING_RECIPE_SHA256" IS NULL`)))
	create(nativeTable("API_KEYS", []column{uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("ALIAS_ID", false), text("LABEL", 100, false), col("SECRET_HASH", "VARBINARY", 32, false, ""), uuid("CREATED_BY", false), col("REQUESTED_EPOCH", "BIGINT", 0, false, ""), uuid("MEMBERSHIP_AUTHORITY", false), uuid("IDEMPOTENCY_KEY", false), col("FINGERPRINT", "VARBINARY", 32, false, ""), stamp("CREATED_AT", false, true), stamp("EXPIRES_AT", false, false), stamp("LAST_USED_AT", true, false), stamp("REVOKED_AT", true, false), text("REVOKE_REASON", 500, true)}, pk("API_KEYS_PK", "ID"), unique("API_KEYS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("API_KEYS_INTENT_UK", "TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "IDEMPOTENCY_KEY"), user("API_KEYS_CREATOR_FK", "CREATED_BY"), reference("API_KEYS_ALIAS_FK", "MODEL_ALIASES", []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"}), check("API_KEYS_BOUNDS", `"REQUESTED_EPOCH">=0 AND OCTET_LENGTH("SECRET_HASH")=32 AND OCTET_LENGTH("FINGERPRINT")=32 AND "EXPIRES_AT">"CREATED_AT"`), check("API_KEYS_REVOKE_REASON", `"REVOKED_AT" IS NULL OR ("REVOKE_REASON" IS NOT NULL AND CHAR_LENGTH("REVOKE_REASON")>0)`)))
	create(modelsRequestsTable(known))
	create(modelsRecoveriesTable(known))
	// The only FK cycle is local: requests -> active recovery -> request. Both
	// tables exist before this one exact ALTER, with complete Before/After shapes.
	before := cloneTable(known["GATEWAY_REQUESTS"])
	after := cloneTable(before)
	active := reference("GATEWAY_REQUEST_ACTIVE_RECOVERY_FK", "GATEWAY_RECOVERIES", []string{"TENANT_ID", "WORKSPACE_ID", "ID", "ACTIVE_RECOVERY_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "REQUEST_ID", "ID"})
	after.Constraints = append(after.Constraints, active)
	add(schemaChange{ID: active.Name, Kind: "alter", Before: &before, After: &after, SQL: `ALTER TABLE "GATEWAY_REQUESTS" ADD CONSTRAINT "GATEWAY_REQUEST_ACTIVE_RECOVERY_FK" FOREIGN KEY ("TENANT_ID","WORKSPACE_ID","ID","ACTIVE_RECOVERY_ID") REFERENCES "GATEWAY_RECOVERIES" ("TENANT_ID","WORKSPACE_ID","REQUEST_ID","ID")`})
	for _, v := range []struct {
		name, owner string
		cols        []string
		desc        []bool
	}{
		{"GATEWAY_SCOPE_IDX", "GATEWAY_INSTANCES", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, []bool{false, false, true, true}},
		{"ALIAS_SCOPE_IDX", "MODEL_ALIASES", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, []bool{false, false, true, true}},
		{"ALIAS_TARGET_PAGE_IDX", "MODEL_ALIAS_TARGET_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "CREATED_AT", "ID"}, []bool{false, false, false, true, true}},
		{"API_KEY_OWNER_IDX", "API_KEYS", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "CREATED_AT", "ID"}, []bool{false, false, false, true, true}},
		{"GATEWAY_REQUEST_HELD_IDX", "GATEWAY_REQUESTS", []string{"DEPLOYMENT_ID", "CAPACITY_HELD"}, nil},
		{"GATEWAY_REQUEST_OWNER_IDX", "GATEWAY_REQUESTS", []string{"TENANT_ID", "WORKSPACE_ID", "ACTOR_USER_ID", "ADMITTED_AT", "ID"}, []bool{false, false, false, true, true}},
		{"GATEWAY_REQUEST_INSTANCE_IDX", "GATEWAY_REQUESTS", []string{"TENANT_ID", "WORKSPACE_ID", "GATEWAY_ID", "ADMITTED_AT", "ID"}, []bool{false, false, false, true, true}},
		{"GATEWAY_RECOVERY_REQUEST_IDX", "GATEWAY_RECOVERIES", []string{"TENANT_ID", "WORKSPACE_ID", "REQUEST_ID", "CREATED_AT", "ID"}, []bool{false, false, false, true, true}},
	} {
		indexStep(v.name, v.owner, v.cols, v.desc)
	}
	triggerStep("MODEL_VERSION_IDENTITY", "MODEL_VERSIONS", "UPDATE", "BEGIN IF "+modelsChanged("ID", "TENANT_ID", "WORKSPACE_ID", "NAME", "MODEL_KEY", "CATALOG_DIGEST", "KIND", "CREATED_BY", "CREATED_AT")+` OR DBMS_LOB.COMPARE(:NEW."MANIFEST",:OLD."MANIFEST")<>0 OR (:OLD."STATE"='deleted' AND :NEW."STATE"<>'deleted') THEN RAISE_APPLICATION_ERROR(-20051,'MODEL_IDENTITY_IMMUTABLE'); END IF; END;`)
	triggerStep("DEPLOYMENT_MODEL_IDENTITY", "DEPLOYMENTS", "UPDATE", "BEGIN IF "+modelsChanged("ID", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "MODEL_ID", "GPU_UUID", "MODEL_KIND", "CATALOG_DIGEST", "SERVING_RECIPE_SHA256")+" THEN RAISE_APPLICATION_ERROR(-20051,'DEPLOYMENT_MODEL_IDENTITY_IMMUTABLE'); END IF; END;")
	for _, owner := range []string{"MODEL_ALIAS_HISTORY", "MODEL_ALIAS_TARGET_VERSIONS", "MODEL_IMPORT_CHUNKS"} {
		for _, event := range []string{"UPDATE", "DELETE"} {
			triggerStep(owner+"_"+event, owner, event, "BEGIN RAISE_APPLICATION_ERROR(-20051,'MODEL_RETAINED_RECORD_IMMUTABLE'); END;")
		}
	}
	triggerStep("MODEL_VERSION_RETAINED", "MODEL_VERSIONS", "DELETE", "BEGIN RAISE_APPLICATION_ERROR(-20051,'MODEL_VERSION_RETAINED'); END;")
	// DM has JSON_OBJECT_KEYS as a table function, not MySQL JSON_LENGTH. Require
	// the exact six top-level outcome members without weakening the JSON contract.
	for _, event := range []string{"INSERT", "UPDATE"} {
		triggerStep("GATEWAY_OUTCOME_KEYS_"+event, "GATEWAY_REQUESTS", event, `DECLARE MEMBER_COUNT INTEGER; BEGIN IF :NEW."OUTCOME" IS NOT NULL THEN SELECT COUNT(*) INTO MEMBER_COUNT FROM JSON_OBJECT_KEYS(:NEW."OUTCOME"); IF MEMBER_COUNT<>6 THEN RAISE_APPLICATION_ERROR(-20052,'GATEWAY_OUTCOME_SHAPE_INVALID'); END IF; END IF; END;`)
	}
	return f
}

func modelsVirtual(name, kind string, width int, expr string) column {
	v := col(name, kind, width, true, "")
	v.Virtual = expr
	return v
}
func modelsDigest(name, field string) constraint {
	return check(name, quoted(field)+" IS NULL OR REGEXP_LIKE("+quoted(field)+",'^[a-f0-9]{64}$','c')")
}
func modelsJSON(name, field string, maximum int) constraint {
	bound := strconv.Itoa(maximum)
	q := quoted(field)
	return check(name, q+" IS NULL OR (JSON_TYPE("+q+") IS NOT NULL AND JSON_TYPE("+q+")='OBJECT' AND OCTET_LENGTH("+q+")<="+bound+")")
}
func modelsReference(current map[string]table, name, target string, local, targetColumns []string) constraint {
	key := ""
	for _, c := range current[target].Constraints {
		if (c.Kind == "P" || c.Kind == "U") && strings.Join(c.Columns, "\x00") == strings.Join(targetColumns, "\x00") {
			key = c.Name
			break
		}
	}
	// Missing exact keys intentionally produce an invalid fragment; no fallback
	// drops scope columns or invents a placeholder dependency.
	return fk(name, target, key, local...)
}
func modelsChanged(fields ...string) string {
	parts := make([]string, 0, len(fields))
	for _, name := range fields {
		n, o := `:NEW.`+quoted(name), `:OLD.`+quoted(name)
		parts = append(parts, "("+n+"<>"+o+" OR ("+n+" IS NULL AND "+o+" IS NOT NULL) OR ("+n+" IS NOT NULL AND "+o+" IS NULL))")
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func modelsRequestsTable(current map[string]table) table {
	r := func(name, target string, local, remote []string) constraint {
		return modelsReference(current, name, target, local, remote)
	}
	return nativeTable("GATEWAY_REQUESTS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("NODE_ID", false), uuid("GATEWAY_ID", false), uuid("API_KEY_ID", false), uuid("ACTOR_USER_ID", false), col("ACTOR_EPOCH", "BIGINT", 0, false, ""), uuid("ALIAS_ID", false), col("ALIAS_REVISION", "BIGINT", 0, false, ""), uuid("DEPLOYMENT_ID", false), col("DEPLOYMENT_REVISION", "BIGINT", 0, false, ""), col("MAXIMUM_OUTPUT_TOKENS", "INTEGER", 0, false, ""), col("MAXIMUM_CONTEXT_TOKENS", "INTEGER", 0, false, ""), col("MAXIMUM_REQUEST_SECONDS", "INTEGER", 0, false, ""), col("STREAM", "BIT", 0, false, ""), text("STATE", 16, false), col("CAPACITY_HELD", "BIT", 0, false, "1"), stamp("ADMITTED_AT", false, true), stamp("DEADLINE_AT", false, false), stamp("RESOLVED_AT", true, false), col("OUTCOME", "CLOB", 0, true, ""), text("OUTCOME_SHA256", 64, true), stamp("REPORTED_AT", true, false), uuid("RECONCILED_BY", true), stamp("RECONCILED_AT", true, false), text("RECONCILIATION_REASON", 500, true), uuid("ACTIVE_RECOVERY_ID", true),
	}, pk("GATEWAY_REQUESTS_PK", "ID"), unique("GATEWAY_REQUEST_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"),
		r("GATEWAY_REQUEST_ACTOR_FK", "USERS", []string{"ACTOR_USER_ID"}, []string{"ID"}), r("GATEWAY_REQUEST_RECONCILER_FK", "USERS", []string{"RECONCILED_BY"}, []string{"ID"}), r("GATEWAY_REQUEST_INSTANCE_FK", "GATEWAY_INSTANCES", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "GATEWAY_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"}), r("GATEWAY_REQUEST_KEY_FK", "API_KEYS", []string{"TENANT_ID", "WORKSPACE_ID", "API_KEY_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"}), r("GATEWAY_REQUEST_ALIAS_REVISION_FK", "MODEL_ALIAS_HISTORY", []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "ALIAS_REVISION"}, []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "REVISION"}), r("GATEWAY_REQUEST_DEPLOYMENT_FK", "DEPLOYMENTS", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "DEPLOYMENT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"}),
		bounded("GATEWAY_REQUEST_DEPLOYMENT_REVISION", "DEPLOYMENT_REVISION"), bounded("GATEWAY_REQUEST_ALIAS_REVISION", "ALIAS_REVISION"), modelsJSON("GATEWAY_REQUEST_OUTCOME_JSON", "OUTCOME", 4096), modelsDigest("GATEWAY_REQUEST_OUTCOME_DIGEST", "OUTCOME_SHA256"),
		check("GATEWAY_REQUEST_BOUNDS", `"ACTOR_EPOCH">=0 AND "MAXIMUM_OUTPUT_TOKENS" BETWEEN 1 AND 512 AND "MAXIMUM_CONTEXT_TOKENS" BETWEEN 1 AND 65536 AND "MAXIMUM_REQUEST_SECONDS" BETWEEN 1 AND 300 AND "STREAM" IN (0,1) AND "CAPACITY_HELD" IN (0,1) AND "DEADLINE_AT">"ADMITTED_AT"`),
		check("GATEWAY_REQUEST_STATE", `"STATE" IN ('admitted','reported','reconciled') AND ("STATE"<>'admitted' OR "CAPACITY_HELD"=1) AND ("STATE"<>'reconciled' OR "CAPACITY_HELD"=0)`),
		check("GATEWAY_REQUEST_OUTCOME_SHAPE", `"OUTCOME" IS NULL OR (JSON_CONTAINS_PATH("OUTCOME",'ALL','$.request_id','$.status','$.error_code','$.usage','$.elapsed_milliseconds','$.output_started')=1 AND JSON_VALUE("OUTCOME",'$.request_id') IS NOT NULL AND JSON_VALUE("OUTCOME",'$.request_id')="ID")`),
		check("GATEWAY_REQUEST_OUTCOME_RELEASE", `"OUTCOME" IS NULL OR "RECONCILED_AT" IS NOT NULL OR (JSON_VALUE("OUTCOME",'$.status') IN ('succeeded','client_cancelled','upstream_timeout','upstream_error','invalid_response','gateway_shutdown') AND (JSON_VALUE("OUTCOME",'$.error_code') IS NULL OR JSON_VALUE("OUTCOME",'$.error_code')<>'gateway_interrupted')) OR "CAPACITY_HELD"=1`),
		check("GATEWAY_REQUEST_RESOLUTION", `("STATE"='admitted' AND "RESOLVED_AT" IS NULL AND "OUTCOME" IS NULL AND "OUTCOME_SHA256" IS NULL AND "REPORTED_AT" IS NULL AND "RECONCILED_AT" IS NULL) OR ("STATE"='reported' AND "RESOLVED_AT" IS NOT NULL AND "OUTCOME" IS NOT NULL AND "OUTCOME_SHA256" IS NOT NULL AND "REPORTED_AT" IS NOT NULL) OR ("STATE"='reconciled' AND "RESOLVED_AT" IS NOT NULL AND "OUTCOME" IS NULL AND "OUTCOME_SHA256" IS NULL AND "REPORTED_AT" IS NULL AND "RECONCILED_AT" IS NOT NULL)`),
		check("GATEWAY_REQUEST_RECONCILIATION", `("RECONCILED_AT" IS NULL AND "RECONCILED_BY" IS NULL AND "RECONCILIATION_REASON" IS NULL) OR ("RECONCILED_AT" IS NOT NULL AND "RECONCILED_BY" IS NOT NULL AND "RECONCILIATION_REASON" IS NOT NULL AND CHAR_LENGTH("RECONCILIATION_REASON")>0)`))
}
func modelsRecoveriesTable(current map[string]table) table {
	r := func(name, target string, local, remote []string) constraint {
		return modelsReference(current, name, target, local, remote)
	}
	return nativeTable("GATEWAY_RECOVERIES", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("REQUEST_ID", false), uuid("REQUESTED_BY", false), text("REASON", 500, false), uuid("IDEMPOTENCY_KEY", false), col("FINGERPRINT", "VARBINARY", 32, false, ""), stamp("CREATED_AT", false, true), stamp("EXPIRES_AT", false, false), stamp("SUPERSEDED_AT", true, false), stamp("INVALIDATED_AT", true, false), col("FENCE_RECEIPT", "CLOB", 0, true, ""), text("FENCE_RECEIPT_SHA256", 64, true), stamp("FENCE_ACCEPTED_AT", true, false), uuid("FENCE_ACCEPTED_BY", true), stamp("CONFIRMED_AT", true, false), col("CONFIRMATION_FINGERPRINT", "VARBINARY", 32, true, ""), col("CONFIRMED_DEPLOYMENT_REVISION", "BIGINT", 0, true, ""),
	}, pk("GATEWAY_RECOVERIES_PK", "ID"), unique("GATEWAY_RECOVERY_REQUEST_UK", "TENANT_ID", "WORKSPACE_ID", "REQUEST_ID", "ID"), unique("GATEWAY_RECOVERY_INTENT_UK", "TENANT_ID", "WORKSPACE_ID", "REQUEST_ID", "REQUESTED_BY", "IDEMPOTENCY_KEY"), r("GATEWAY_RECOVERY_REQUEST_FK", "GATEWAY_REQUESTS", []string{"TENANT_ID", "WORKSPACE_ID", "REQUEST_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"}), r("GATEWAY_RECOVERY_REQUESTER_FK", "USERS", []string{"REQUESTED_BY"}, []string{"ID"}), r("GATEWAY_RECOVERY_ACCEPTOR_FK", "USERS", []string{"FENCE_ACCEPTED_BY"}, []string{"ID"}), modelsJSON("GATEWAY_RECOVERY_FENCE_JSON", "FENCE_RECEIPT", 4096), modelsDigest("GATEWAY_RECOVERY_FENCE_DIGEST", "FENCE_RECEIPT_SHA256"), check("GATEWAY_RECOVERY_BOUNDS", `CHAR_LENGTH("REASON")>0 AND OCTET_LENGTH("FINGERPRINT")=32 AND "EXPIRES_AT">"CREATED_AT" AND ("CONFIRMATION_FINGERPRINT" IS NULL OR OCTET_LENGTH("CONFIRMATION_FINGERPRINT")=32) AND ("CONFIRMED_DEPLOYMENT_REVISION" IS NULL OR "CONFIRMED_DEPLOYMENT_REVISION" BETWEEN 1 AND 9007199254740991)`),
		check("GATEWAY_RECOVERY_FENCE_GROUP", `("FENCE_RECEIPT" IS NULL AND "FENCE_RECEIPT_SHA256" IS NULL AND "FENCE_ACCEPTED_AT" IS NULL AND "FENCE_ACCEPTED_BY" IS NULL) OR ("FENCE_RECEIPT" IS NOT NULL AND "FENCE_RECEIPT_SHA256" IS NOT NULL AND "FENCE_ACCEPTED_AT" IS NOT NULL AND "FENCE_ACCEPTED_BY" IS NOT NULL)`), check("GATEWAY_RECOVERY_CONFIRM_GROUP", `("CONFIRMED_AT" IS NULL AND "CONFIRMATION_FINGERPRINT" IS NULL AND "CONFIRMED_DEPLOYMENT_REVISION" IS NULL) OR ("CONFIRMED_AT" IS NOT NULL AND "FENCE_ACCEPTED_AT" IS NOT NULL AND "CONFIRMATION_FINGERPRINT" IS NOT NULL AND "CONFIRMED_DEPLOYMENT_REVISION" IS NOT NULL)`))
}
