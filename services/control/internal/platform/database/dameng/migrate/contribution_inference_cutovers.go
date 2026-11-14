package migrate

// Frozen PG000010 approved-base identity. Keep this migration literal independent
// of future runtime catalog revisions so applied journal checksums cannot drift.
const inferenceCutoverBaseDigest = "4d32aa5cd4b2d86f12beff791f0428f0813c2d3479041080d13b0be6bc6842bc"

// inferenceCutoversFragment completes PG000006 inference observations and the
// PG000010 post-base deployment/alias projection. The catalog owner registers it
// after the retained prefix; none of the original 454 operations is rewritten.
func inferenceCutoversFragment(current map[string]table) schemaFragment {
	b := lifecycleStart(current, "inference_cutovers_v2", "foundation_v1", "nodes_components_v2", "models_v2", "model_preparations_v2")
	b.create(nativeTable("INFERENCE_NODES", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("NODE_ID", false),
		col("OBSERVATION", "CLOB", 0, false, ""), stamp("RECEIVED_AT", false, true),
	}, pk("INFERENCE_NODES_PK", "NODE_ID"),
		modelsReference(b.known, "INFERENCE_NODES_NODE_FK", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"}),
		modelsJSON("INFERENCE_NODES_OBSERVATION", "OBSERVATION", 32768)))

	// Install guards before widening the historical first-path checks. Existing
	// history/target UPDATE and DELETE guards remain part of the retained prefix.
	b.trigger("IC_ALIAS_IDENTITY", "MODEL_ALIASES", "UPDATE", "BEFORE", "BEGIN IF "+modelsChanged("ID", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "GATEWAY_ID", "NAME", "CREATED_BY", "IDEMPOTENCY_KEY", "FINGERPRINT", "CREATED_AT")+" THEN RAISE_APPLICATION_ERROR(-20063,'MODEL_ALIAS_IDENTITY_IMMUTABLE'); END IF; END;")
	b.trigger("IC_ALIAS_TARGET_SNAPSHOT", "MODEL_ALIAS_TARGET_VERSIONS", "INSERT", "BEFORE", `DECLARE MATCH_COUNT INTEGER; BEGIN
 SELECT COUNT(*) INTO MATCH_COUNT FROM "MODEL_ALIASES" A
 JOIN "DEPLOYMENTS" D ON D."TENANT_ID"=A."TENANT_ID" AND D."WORKSPACE_ID"=A."WORKSPACE_ID" AND D."NODE_ID"=A."NODE_ID" AND D."ID"=A."DEPLOYMENT_ID"
 WHERE A."TENANT_ID"=:NEW."TENANT_ID" AND A."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND A."ID"=:NEW."ALIAS_ID"
 AND A."REVISION"=:NEW."REVISION" AND A."DEPLOYMENT_REVISION"=:NEW."DEPLOYMENT_REVISION"
 AND D."ID"=:NEW."DEPLOYMENT_ID" AND D."MODEL_ID"=:NEW."MODEL_ID" AND D."MODEL_KIND"=:NEW."MODEL_KIND" AND D."CATALOG_DIGEST"=:NEW."CATALOG_DIGEST"
 AND (D."SERVING_RECIPE_SHA256"=:NEW."SERVING_RECIPE_SHA256" OR (D."SERVING_RECIPE_SHA256" IS NULL AND :NEW."SERVING_RECIPE_SHA256" IS NULL));
 IF MATCH_COUNT<>1 THEN RAISE_APPLICATION_ERROR(-20063,'ALIAS_CURRENT_TARGET_SNAPSHOT_REQUIRED'); END IF; END;`)

	before := cloneTable(b.known["DEPLOYMENTS"])
	after := cloneTable(before)
	for i := range after.Columns {
		if after.Columns[i].Name == "CATALOG_DIGEST" {
			after.Columns[i].Default = "'" + inferenceCutoverBaseDigest + "'"
			b.alter("IC_DEPLOYMENT_CATALOG_DEFAULT", before, after, "MODIFY "+after.Columns[i].declaration())
			break
		}
	}
	b.constraint("DEPLOYMENTS", check("IC_DEPLOYMENTS_VARIANT", `("MODEL_KIND"='approved_base' AND "CATALOG_DIGEST"='`+inferenceCutoverBaseDigest+`' AND "SERVING_RECIPE_SHA256" IS NULL)
 OR ("MODEL_KIND"='merged_lora' AND REGEXP_LIKE("CATALOG_DIGEST",'^[a-f0-9]{64}$','c') AND "CATALOG_DIGEST"<>'`+inferenceCutoverBaseDigest+`' AND "SERVING_RECIPE_SHA256" IS NOT NULL AND REGEXP_LIKE("SERVING_RECIPE_SHA256",'^[a-f0-9]{64}$','c'))`))
	b.drop("IC_DEPLOYMENTS_DROP_BASE_ONLY", "DEPLOYMENTS", "DEPLOYMENTS_VARIANT")
	b.constraint("MODEL_ALIAS_HISTORY", check("IC_ALIAS_HISTORY_ACTION", `"ACTION" IN ('published','disabled','paused','resumed','retargeted','rolled_back') AND CHAR_LENGTH("REASON")>0`))
	b.drop("IC_ALIAS_HISTORY_DROP_FIRST_PATH", "MODEL_ALIAS_HISTORY", "ALIAS_HISTORY_ACTION")
	b.constraint("MODEL_ALIAS_TARGET_VERSIONS", check("IC_ALIAS_TARGET_ACTION", `"ACTION" IN ('migration_current','published','disabled','paused','resumed','retargeted','rolled_back')`))
	b.constraint("MODEL_ALIAS_TARGET_VERSIONS", check("IC_ALIAS_TARGET_SOURCE", `("ACTION"='rolled_back' AND "SOURCE_REVISION" IS NOT NULL AND "SOURCE_REVISION">=1 AND "SOURCE_REVISION"<"REVISION") OR ("ACTION"<>'rolled_back' AND "SOURCE_REVISION" IS NULL)`))
	b.constraint("MODEL_ALIAS_TARGET_VERSIONS", check("IC_ALIAS_TARGET_VARIANT", `("MODEL_KIND"='approved_base' AND "SERVING_RECIPE_SHA256" IS NULL) OR ("MODEL_KIND"='merged_lora' AND "SERVING_RECIPE_SHA256" IS NOT NULL AND REGEXP_LIKE("SERVING_RECIPE_SHA256",'^[a-f0-9]{64}$','c'))`))
	b.foreign("MODEL_ALIAS_TARGET_VERSIONS", "IC_ALIAS_TARGET_SOURCE_FK", "MODEL_ALIAS_TARGET_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "SOURCE_REVISION"}, []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "REVISION"})
	b.drop("IC_ALIAS_TARGET_DROP_FIRST_PATH", "MODEL_ALIAS_TARGET_VERSIONS", "ALIAS_TARGET_FIRST_PATH")

	// A pre-cutover alias supplies its already canonical application UUID for the
	// one migration-current record. No server UUID function or new bind is needed.
	// Occupied IDs fail rather than replace retained history. Existing mismatching
	// current snapshots also fail the postcondition instead of being overwritten.
	b.add(schemaChange{ID: "IC_ALIAS_CURRENT_BACKFILL", Kind: "backfill", SQL: `INSERT INTO "MODEL_ALIAS_TARGET_VERSIONS" ("ID","TENANT_ID","WORKSPACE_ID","ALIAS_ID","REVISION","ACTION","DEPLOYMENT_ID","DEPLOYMENT_REVISION","MODEL_ID","MODEL_KIND","CATALOG_DIGEST","SERVING_RECIPE_SHA256")
 SELECT A."ID",A."TENANT_ID",A."WORKSPACE_ID",A."ID",A."REVISION",'migration_current',D."ID",A."DEPLOYMENT_REVISION",D."MODEL_ID",D."MODEL_KIND",D."CATALOG_DIGEST",D."SERVING_RECIPE_SHA256"
 FROM "MODEL_ALIASES" A JOIN "DEPLOYMENTS" D ON D."TENANT_ID"=A."TENANT_ID" AND D."WORKSPACE_ID"=A."WORKSPACE_ID" AND D."NODE_ID"=A."NODE_ID" AND D."ID"=A."DEPLOYMENT_ID"
 WHERE NOT EXISTS (SELECT 1 FROM "MODEL_ALIAS_TARGET_VERSIONS" T WHERE T."TENANT_ID"=A."TENANT_ID" AND T."WORKSPACE_ID"=A."WORKSPACE_ID" AND T."ALIAS_ID"=A."ID" AND T."REVISION"=A."REVISION")`,
		Probe: `SELECT COUNT(*) FROM "MODEL_ALIASES" A WHERE NOT EXISTS (
 SELECT 1 FROM "MODEL_ALIAS_TARGET_VERSIONS" T JOIN "DEPLOYMENTS" D ON D."TENANT_ID"=A."TENANT_ID" AND D."WORKSPACE_ID"=A."WORKSPACE_ID" AND D."NODE_ID"=A."NODE_ID" AND D."ID"=A."DEPLOYMENT_ID"
 WHERE T."TENANT_ID"=A."TENANT_ID" AND T."WORKSPACE_ID"=A."WORKSPACE_ID" AND T."ALIAS_ID"=A."ID" AND T."REVISION"=A."REVISION"
 AND T."DEPLOYMENT_ID"=D."ID" AND T."DEPLOYMENT_REVISION"=A."DEPLOYMENT_REVISION" AND T."MODEL_ID"=D."MODEL_ID" AND T."MODEL_KIND"=D."MODEL_KIND" AND T."CATALOG_DIGEST"=D."CATALOG_DIGEST"
 AND (T."SERVING_RECIPE_SHA256"=D."SERVING_RECIPE_SHA256" OR (T."SERVING_RECIPE_SHA256" IS NULL AND D."SERVING_RECIPE_SHA256" IS NULL)))`,
		ProbeTables: []string{"MODEL_ALIASES", "MODEL_ALIAS_TARGET_VERSIONS", "DEPLOYMENTS"}})

	b.create(nativeTable("MODEL_ALIAS_CUTOVER_INTENTS", []column{
		uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("ACTOR_USER_ID", false), uuid("IDEMPOTENCY_KEY", false), uuid("ALIAS_ID", false),
		col("RESULT_REVISION", "BIGINT", 0, false, ""), col("FINGERPRINT", "VARBINARY", 32, false, ""), stamp("CREATED_AT", false, true),
	}, pk("IC_ALIAS_INTENT_PK", "TENANT_ID", "WORKSPACE_ID", "ACTOR_USER_ID", "IDEMPOTENCY_KEY"),
		modelsReference(b.known, "IC_ALIAS_INTENT_ACTOR_FK", "USERS", []string{"ACTOR_USER_ID"}, []string{"ID"}),
		modelsReference(b.known, "IC_ALIAS_INTENT_TARGET_FK", "MODEL_ALIAS_TARGET_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "RESULT_REVISION"}, []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "REVISION"}),
		check("IC_ALIAS_INTENT_FINGERPRINT", `OCTET_LENGTH("FINGERPRINT")=32`)))
	for _, event := range []string{"UPDATE", "DELETE"} {
		b.trigger("IC_ALIAS_INTENT_"+event, "MODEL_ALIAS_CUTOVER_INTENTS", event, "BEFORE", "BEGIN RAISE_APPLICATION_ERROR(-20063,'ALIAS_TARGET_HISTORY_IMMUTABLE'); END;")
	}
	return b.fragment
}
