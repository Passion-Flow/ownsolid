package migrate

import "strings"

// These native DM fragments are source contributions. The catalog owner must
// register them after their dependencies; they do not activate the server engine.
func registryFragment(current map[string]table) schemaFragment {
	b := newInfrastructureFragment("registry_v2", current, "foundation_v1", "account_license_v2", "nodes_components_v2", "models_v2")
	b.create(nativeTable("IMAGE_REGISTRY_PINS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("ASSET_ID", 160, false),
		col("SOURCE_ARCHIVE_SHA256", "CHAR", 64, false, ""), col("SOURCE_MANIFEST_SHA256", "CHAR", 64, false, ""), text("BACKEND_IDENTITY", 71, false), text("STATE", 16, false),
		text("MANIFEST_DIGEST", 71, true), text("CONFIG_DIGEST", 71, true), col("ARCHIVE_SHA256", "CHAR", 64, true, ""), col("ARCHIVE_BYTES", "BIGINT", 0, true, ""), col("INVENTORY", "CLOB", 0, true, ""), col("INVENTORY_SHA256", "CHAR", 64, true, ""),
		uuid("CREATED_BY", false), col("ACTOR_EPOCH", "BIGINT", 0, false, ""), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true), version("REVISION"),
	}, pk("REGISTRY_PINS_PK", "ID"), unique("REGISTRY_PINS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), b.scope("REGISTRY_PINS_WORKSPACE_FK"), b.user("REGISTRY_PINS_ACTOR_FK", "CREATED_BY"), bounded("REGISTRY_PINS_REVISION", "REVISION"),
		check("REGISTRY_PINS_ASSET", `LENGTH("ASSET_ID")>0`), check("REGISTRY_PINS_EPOCH", `"ACTOR_EPOCH">=1`), check("REGISTRY_PINS_STATE", `"STATE" IN ('importing','ready','unknown','deleting','deleted','failed')`),
		infrastructureHash("REGISTRY_SOURCE_ARCHIVE", "SOURCE_ARCHIVE_SHA256", false), infrastructureHash("REGISTRY_SOURCE_MANIFEST", "SOURCE_MANIFEST_SHA256", false), infrastructureHash("REGISTRY_BACKEND", "BACKEND_IDENTITY", true), infrastructureHash("REGISTRY_MANIFEST", "MANIFEST_DIGEST", true), infrastructureHash("REGISTRY_CONFIG", "CONFIG_DIGEST", true), infrastructureHash("REGISTRY_ARCHIVE", "ARCHIVE_SHA256", false), infrastructureHash("REGISTRY_INVENTORY_HASH", "INVENTORY_SHA256", false),
		check("REGISTRY_INVENTORY_CLOSURE", `("INVENTORY" IS NULL AND "MANIFEST_DIGEST" IS NULL AND "CONFIG_DIGEST" IS NULL AND "ARCHIVE_SHA256" IS NULL AND "ARCHIVE_BYTES" IS NULL AND "INVENTORY_SHA256" IS NULL) OR ("INVENTORY" IS NOT NULL AND "MANIFEST_DIGEST" IS NOT NULL AND "CONFIG_DIGEST" IS NOT NULL AND "ARCHIVE_SHA256" IS NOT NULL AND "ARCHIVE_BYTES" IS NOT NULL AND "INVENTORY_SHA256" IS NOT NULL)`),
		check("REGISTRY_ARCHIVE_BYTES", `"ARCHIVE_BYTES" IS NULL OR "ARCHIVE_BYTES">0`), infrastructureObject("REGISTRY_INVENTORY_OBJECT", "INVENTORY"), check("REGISTRY_READY_INVENTORY", `"STATE"<>'ready' OR "INVENTORY" IS NOT NULL`)))
	b.index("REGISTRY_SCOPE_IDX", "IMAGE_REGISTRY_PINS", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, []bool{false, false, true, true}, false)
	b.create(nativeTable("IMAGE_REGISTRY_BLOBS", []column{uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("PIN_ID", false), col("ORDINAL", "INTEGER", 0, false, ""), text("DIGEST", 71, false), col("SIZE", "BIGINT", 0, false, ""), text("MEDIA_TYPE", 160, false), text("KIND", 16, false)},
		pk("REGISTRY_BLOBS_PK", "TENANT_ID", "WORKSPACE_ID", "PIN_ID", "ORDINAL"), b.pin("REGISTRY_BLOBS_PIN_FK"), infrastructureHash("REGISTRY_BLOB_DIGEST", "DIGEST", true), check("REGISTRY_BLOB_VALUES", `"ORDINAL">=0 AND "SIZE">0 AND LENGTH("MEDIA_TYPE")>0 AND "KIND" IN ('manifest','config','layer')`)))
	b.create(nativeTable("IMAGE_REGISTRY_IMPORTS", []column{uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("PIN_ID", false), uuid("OWNER_TOKEN", false), text("PHASE", 16, false)}, pk("REGISTRY_IMPORTS_PK", "TENANT_ID", "WORKSPACE_ID", "PIN_ID"), b.pin("REGISTRY_IMPORTS_PIN_FK"), check("REGISTRY_IMPORT_PHASE", `"PHASE" IN ('prepared','remote','completed','unknown')`)))
	b.create(nativeTable("IMAGE_REGISTRY_DEPLOYMENT_REFS", []column{uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("PIN_ID", false), uuid("NODE_ID", false), uuid("DEPLOYMENT_ID", false), col("CREATED_REVISION", "BIGINT", 0, false, ""), stamp("RELEASED_AT", true, false), stamp("CREATED_AT", false, true)},
		pk("REGISTRY_DEPLOYMENT_REFS_PK", "TENANT_ID", "WORKSPACE_ID", "DEPLOYMENT_ID"), unique("REGISTRY_DEPLOYMENT_BINDING_UK", "TENANT_ID", "WORKSPACE_ID", "PIN_ID", "NODE_ID", "DEPLOYMENT_ID"), b.pin("REGISTRY_DEPLOYMENT_PIN_FK"), b.reference("REGISTRY_DEPLOYMENT_SCOPE_FK", "DEPLOYMENTS", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "DEPLOYMENT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"}), check("REGISTRY_DEPLOYMENT_REVISION", `"CREATED_REVISION">=1`)))
	grants := []column{uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("PIN_ID", false), uuid("NODE_ID", false), uuid("DEPLOYMENT_ID", false), uuid("TASK_ID", false), col("REVISION", "BIGINT", 0, false, ""), uuid("CREATED_BY", false), col("ACTOR_EPOCH", "BIGINT", 0, false, ""), uuid("CREATED_SESSION_ID", false), col("CREATED_TOKEN_HASH", "VARBINARY", 32, false, ""), stamp("CREATED_AT", false, true), stamp("EXPIRES_AT", false, false), stamp("REVOKED_AT", true, false)}
	// Every unique-key component becomes NULL outside the live predicate. A mix
	// of ordinary scope columns and one nullable marker would reject history.
	for _, name := range []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID"} {
		c := uuid("LIVE_"+name, true)
		c.Virtual = `CASE WHEN "REVOKED_AT" IS NULL THEN ` + quoted(name) + ` ELSE NULL END`
		grants = append(grants, c)
	}
	b.create(nativeTable("IMAGE_REGISTRY_GRANTS", grants, pk("REGISTRY_GRANTS_PK", "ID"), b.user("REGISTRY_GRANT_ACTOR_FK", "CREATED_BY"), b.reference("REGISTRY_GRANT_REFERENCE_FK", "IMAGE_REGISTRY_DEPLOYMENT_REFS", []string{"TENANT_ID", "WORKSPACE_ID", "PIN_ID", "NODE_ID", "DEPLOYMENT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "PIN_ID", "NODE_ID", "DEPLOYMENT_ID"}), b.reference("REGISTRY_GRANT_TASK_FK", "NODE_TASKS", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"}), check("REGISTRY_GRANT_VALUES", `"REVISION">=1 AND "ACTOR_EPOCH">=1 AND OCTET_LENGTH("CREATED_TOKEN_HASH")=32 AND "EXPIRES_AT">"CREATED_AT"`)))
	b.index("REGISTRY_CURRENT_GRANT_UK", "IMAGE_REGISTRY_GRANTS", []string{"LIVE_TENANT_ID", "LIVE_WORKSPACE_ID", "LIVE_NODE_ID", "LIVE_TASK_ID"}, nil, true)
	b.create(nativeTable("IMAGE_REGISTRY_OFFLINE_REFS", []column{uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("PIN_ID", false), uuid("CREATED_BY", false), stamp("CREATED_AT", false, true), stamp("RELEASED_AT", true, false)}, pk("REGISTRY_OFFLINE_REFS_PK", "ID"), b.pin("REGISTRY_OFFLINE_PIN_FK"), b.user("REGISTRY_OFFLINE_ACTOR_FK", "CREATED_BY")))
	b.create(nativeTable("IMAGE_REGISTRY_MUTATIONS", []column{uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("ACTOR_ID", false), text("OPERATION", 32, false), uuid("IDEMPOTENCY_KEY", false), col("FINGERPRINT", "VARBINARY", 32, false, ""), uuid("TARGET_ID", false), stamp("CREATED_AT", false, true)}, pk("REGISTRY_MUTATIONS_PK", "TENANT_ID", "WORKSPACE_ID", "ACTOR_ID", "OPERATION", "IDEMPOTENCY_KEY"), b.scope("REGISTRY_MUTATION_SCOPE_FK"), b.user("REGISTRY_MUTATION_ACTOR_FK", "ACTOR_ID"), check("REGISTRY_MUTATION_OPERATION", `"OPERATION" IN ('import','reconcile','remove','revoke','offline.hold','offline.release','reference.release','abandon','discard','grant')`), check("REGISTRY_MUTATION_HASH", `OCTET_LENGTH("FINGERPRINT")=32`)))
	pinIdentity := infrastructureChanged("ID", "TENANT_ID", "WORKSPACE_ID", "ASSET_ID", "SOURCE_ARCHIVE_SHA256", "SOURCE_MANIFEST_SHA256", "BACKEND_IDENTITY", "CREATED_BY", "ACTOR_EPOCH", "CREATED_AT")
	inventory := infrastructureChanged("MANIFEST_DIGEST", "CONFIG_DIGEST", "ARCHIVE_SHA256", "ARCHIVE_BYTES", "INVENTORY_SHA256") + ` OR ` + infrastructureLOBChanged("INVENTORY")
	b.guard("REGISTRY_PIN_IDENTITY", "IMAGE_REGISTRY_PINS", "UPDATE", pinIdentity+` OR (:OLD."INVENTORY" IS NOT NULL AND (`+inventory+`)) OR :NEW."REVISION"<>:OLD."REVISION"+1 OR :OLD."STATE"='deleted'`, "REGISTRY_IMMUTABLE_IDENTITY")
	b.guard("REGISTRY_GRANT_IDENTITY", "IMAGE_REGISTRY_GRANTS", "UPDATE", infrastructureChanged("ID", "TENANT_ID", "WORKSPACE_ID", "PIN_ID", "NODE_ID", "DEPLOYMENT_ID", "TASK_ID", "REVISION", "CREATED_BY", "ACTOR_EPOCH", "CREATED_SESSION_ID", "CREATED_TOKEN_HASH", "CREATED_AT", "EXPIRES_AT")+` OR (:OLD."REVOKED_AT" IS NOT NULL AND (`+infrastructureChanged("REVOKED_AT")+`))`, "REGISTRY_IMMUTABLE_GRANT")
	b.guard("REGISTRY_DEPLOYMENT_REF_IDENTITY", "IMAGE_REGISTRY_DEPLOYMENT_REFS", "UPDATE", infrastructureChanged("TENANT_ID", "WORKSPACE_ID", "PIN_ID", "NODE_ID", "DEPLOYMENT_ID", "CREATED_REVISION", "CREATED_AT")+` OR (:OLD."RELEASED_AT" IS NOT NULL AND (`+infrastructureChanged("RELEASED_AT")+`))`, "REGISTRY_IMMUTABLE_REFERENCE")
	b.guard("REGISTRY_OFFLINE_REF_IDENTITY", "IMAGE_REGISTRY_OFFLINE_REFS", "UPDATE", infrastructureChanged("ID", "TENANT_ID", "WORKSPACE_ID", "PIN_ID", "CREATED_BY", "CREATED_AT")+` OR (:OLD."RELEASED_AT" IS NOT NULL AND (`+infrastructureChanged("RELEASED_AT")+`))`, "REGISTRY_IMMUTABLE_OFFLINE_REFERENCE")
	return b.fragment
}

func privateclustersFragment(current map[string]table) schemaFragment {
	b := newInfrastructureFragment("privateclusters_v2", current, "foundation_v1", "account_license_v2")
	b.create(nativeTable("PRIVATE_CLUSTER_CONNECTIONS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("PROFILE_ID", 63, false), uuid("CLUSTER_UID", false), uuid("NAMESPACE_UID", false), text("NAMESPACE_NAME", 63, false), uuid("INSTALLATION_ID", false), text("PROFILE", 64, false), col("CONFIG_SHA256", "CHAR", 64, false, ""), col("CREDENTIAL_SHA256", "CHAR", 64, false, ""), col("MANIFEST_SHA256", "CHAR", 64, false, ""), text("IMAGE_DIGEST", 71, false), text("CONFIG_DIGEST", 71, false), text("STATE", 16, false), col("REVISION", "BIGINT", 0, false, ""), uuid("ACTIVE_RUN_ID", true), col("OBSERVATION", "CLOB", 0, true, ""), uuid("CREATED_BY", false), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true),
	}, pk("PRIVATE_CONNECTIONS_PK", "ID"), unique("PRIVATE_CONNECTIONS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("PRIVATE_NAMESPACE_UK", "CLUSTER_UID", "NAMESPACE_UID"), unique("PRIVATE_PROFILE_UK", "TENANT_ID", "WORKSPACE_ID", "PROFILE_ID"), b.scope("PRIVATE_CONNECTION_SCOPE_FK"), b.user("PRIVATE_CONNECTION_ACTOR_FK", "CREATED_BY"), bounded("PRIVATE_CONNECTION_REVISION", "REVISION"), check("PRIVATE_CONNECTION_STATE", `"STATE" IN ('active','disabled','restore_fenced')`), check("PRIVATE_CONNECTION_NAMESPACE", `"CLUSTER_UID"<>"NAMESPACE_UID" AND "NAMESPACE_NAME" LIKE 'projectone-test-%'`), check("PRIVATE_CONNECTION_PROFILE", `"PROFILE" IN ('kubernetes1.36.4-linux-amd64','local-kind1.36.4-linux-arm64')`), infrastructureHash("PRIVATE_CONFIG_HASH", "CONFIG_SHA256", false), infrastructureHash("PRIVATE_CREDENTIAL_HASH", "CREDENTIAL_SHA256", false), infrastructureHash("PRIVATE_MANIFEST_HASH", "MANIFEST_SHA256", false), infrastructureHash("PRIVATE_IMAGE_DIGEST", "IMAGE_DIGEST", true), infrastructureHash("PRIVATE_CONFIG_DIGEST", "CONFIG_DIGEST", true), infrastructureObject("PRIVATE_OBSERVATION_OBJECT", "OBSERVATION")))
	b.create(nativeTable("PRIVATE_CLUSTER_RUNS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("CONNECTION_ID", false), col("CONNECTION_REVISION", "BIGINT", 0, false, ""), uuid("CREATED_BY", false), col("ACTOR_EPOCH", "BIGINT", 0, false, ""), uuid("MEMBERSHIP_AUTHORITY", true), uuid("CREATED_SESSION_ID", false), col("CREATED_TOKEN_HASH", "VARBINARY", 32, false, ""), col("CONFIG_SHA256", "CHAR", 64, false, ""), col("REQUEST", "CLOB", 0, false, ""), col("SPEC_SHA256", "CHAR", 64, false, ""), text("PHASE", 24, false), text("RESULT", 16, true), uuid("LEASE_UID", true), uuid("JOB_UID", true), uuid("POD_UID", true), col("REMOTE_STATE", "CLOB", 0, true, ""), col("REVISION", "BIGINT", 0, false, ""), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true),
	}, pk("PRIVATE_RUNS_PK", "ID"), unique("PRIVATE_RUN_CONNECTION_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "CONNECTION_ID", "ID"), unique("PRIVATE_RUN_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), b.reference("PRIVATE_RUN_CONNECTION_FK", "PRIVATE_CLUSTER_CONNECTIONS", []string{"TENANT_ID", "WORKSPACE_ID", "CONNECTION_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"}), b.user("PRIVATE_RUN_ACTOR_FK", "CREATED_BY"), bounded("PRIVATE_RUN_REVISION", "REVISION"), bounded("PRIVATE_RUN_CONNECTION_REVISION", "CONNECTION_REVISION"), check("PRIVATE_RUN_AUTHORITY", `"ACTOR_EPOCH">=1 AND OCTET_LENGTH("CREATED_TOKEN_HASH")=32`), infrastructureHash("PRIVATE_RUN_CONFIG_HASH", "CONFIG_SHA256", false), infrastructureHash("PRIVATE_RUN_SPEC_HASH", "SPEC_SHA256", false), infrastructureObject("PRIVATE_RUN_REQUEST_OBJECT", "REQUEST"), infrastructureObject("PRIVATE_RUN_REMOTE_OBJECT", "REMOTE_STATE"), check("PRIVATE_RUN_PHASE", `"PHASE" IN ('prepared','lease_pending','lease_acquired','job_pending','observing','job_delete_pending','job_absent','lease_delete_pending','cleaned')`), check("PRIVATE_RUN_RESULT", `"RESULT" IS NULL OR "RESULT" IN ('succeeded','failed','cancelled')`), check("PRIVATE_RUN_REMOTE_UIDS", `("JOB_UID" IS NULL OR "LEASE_UID" IS NOT NULL) AND ("POD_UID" IS NULL OR "JOB_UID" IS NOT NULL)`), check("PRIVATE_RUN_LEASE_REQUIRED", `"PHASE" NOT IN ('lease_acquired','job_pending','observing','job_delete_pending','job_absent','lease_delete_pending') OR "LEASE_UID" IS NOT NULL`), check("PRIVATE_RUN_JOB_REQUIRED", `"PHASE" NOT IN ('observing','job_delete_pending') OR "JOB_UID" IS NOT NULL`), check("PRIVATE_RUN_CLEAN_RESULT", `"PHASE"<>'cleaned' OR "RESULT" IS NOT NULL`)))
	// The cyclic active pointer is added only after both scoped keys exist.
	before := cloneTable(b.known["PRIVATE_CLUSTER_CONNECTIONS"])
	after := cloneTable(before)
	active := b.reference("PRIVATE_ACTIVE_RUN_FK", "PRIVATE_CLUSTER_RUNS", []string{"TENANT_ID", "WORKSPACE_ID", "ID", "ACTIVE_RUN_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "CONNECTION_ID", "ID"})
	after.Constraints = append(after.Constraints, active)
	b.add(schemaChange{ID: active.Name, Kind: "alter", Before: &before, After: &after, SQL: `ALTER TABLE "PRIVATE_CLUSTER_CONNECTIONS" ADD CONSTRAINT "PRIVATE_ACTIVE_RUN_FK" FOREIGN KEY ("TENANT_ID","WORKSPACE_ID","ID","ACTIVE_RUN_ID") REFERENCES "PRIVATE_CLUSTER_RUNS" ("TENANT_ID","WORKSPACE_ID","CONNECTION_ID","ID")`})
	b.index("PRIVATE_CLUSTER_HISTORY_IDX", "PRIVATE_CLUSTER_RUNS", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, []bool{false, false, true, true}, false)
	b.create(nativeTable("PRIVATE_CLUSTER_INTENTS", []column{uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("ACTOR_ID", false), uuid("IDEMPOTENCY_KEY", false), text("OPERATION", 24, false), col("FINGERPRINT", "CHAR", 64, false, ""), uuid("TARGET_ID", false), col("RESULT_REVISION", "BIGINT", 0, false, ""), stamp("CREATED_AT", false, true)}, pk("PRIVATE_INTENTS_PK", "TENANT_ID", "WORKSPACE_ID", "ACTOR_ID", "IDEMPOTENCY_KEY"), b.scope("PRIVATE_INTENT_SCOPE_FK"), b.user("PRIVATE_INTENT_ACTOR_FK", "ACTOR_ID"), bounded("PRIVATE_INTENT_REVISION", "RESULT_REVISION"), infrastructureHash("PRIVATE_INTENT_HASH", "FINGERPRINT", false), check("PRIVATE_INTENT_OPERATION", `"OPERATION" IN ('bind','rebind','disable','observe','start','continue','reconcile','cancel','cleanup')`)))
	b.guard("PRIVATE_CONNECTION_IDENTITY", "PRIVATE_CLUSTER_CONNECTIONS", "UPDATE", infrastructureChanged("ID", "TENANT_ID", "WORKSPACE_ID", "PROFILE_ID", "CLUSTER_UID", "NAMESPACE_UID", "NAMESPACE_NAME", "INSTALLATION_ID", "PROFILE", "CREATED_BY", "CREATED_AT")+` OR :NEW."REVISION"<>:OLD."REVISION"+1 OR (:OLD."ACTIVE_RUN_ID" IS NOT NULL AND (`+infrastructureChanged("CONFIG_SHA256", "MANIFEST_SHA256", "IMAGE_DIGEST", "CONFIG_DIGEST")+`))`, "PRIVATE_CLUSTER_IMMUTABLE_CONNECTION")
	// Credential rotation during explicit same-scope recovery remains supported;
	// the request/config identity, historical session and token are immutable.
	transitions := `:OLD."PHASE"=:NEW."PHASE" OR :OLD."PHASE"='prepared' AND :NEW."PHASE" IN ('lease_pending','cleaned') OR :OLD."PHASE"='lease_pending' AND :NEW."PHASE" IN ('prepared','lease_acquired') OR :OLD."PHASE"='lease_acquired' AND :NEW."PHASE" IN ('job_pending','job_absent') OR :OLD."PHASE"='job_pending' AND :NEW."PHASE" IN ('lease_acquired','observing') OR :OLD."PHASE"='observing' AND :NEW."PHASE"='job_delete_pending' OR :OLD."PHASE"='job_delete_pending' AND :NEW."PHASE" IN ('observing','job_absent') OR :OLD."PHASE"='job_absent' AND :NEW."PHASE"='lease_delete_pending' OR :OLD."PHASE"='lease_delete_pending' AND :NEW."PHASE" IN ('job_absent','cleaned')`
	run := infrastructureChanged("ID", "TENANT_ID", "WORKSPACE_ID", "CONNECTION_ID", "CONNECTION_REVISION", "CREATED_BY", "ACTOR_EPOCH", "MEMBERSHIP_AUTHORITY", "CREATED_SESSION_ID", "CREATED_TOKEN_HASH", "CONFIG_SHA256", "SPEC_SHA256", "CREATED_AT") + ` OR ` + infrastructureLOBChanged("REQUEST") + ` OR :NEW."REVISION"<>:OLD."REVISION"+1 OR :OLD."PHASE"='cleaned' OR NOT (` + transitions + `)`
	for _, name := range []string{"RESULT", "LEASE_UID", "JOB_UID", "POD_UID"} {
		run += ` OR (:OLD.` + quoted(name) + ` IS NOT NULL AND (` + infrastructureChanged(name) + `))`
	}
	b.guard("PRIVATE_RUN_IDENTITY", "PRIVATE_CLUSTER_RUNS", "UPDATE", run, "PRIVATE_CLUSTER_IMMUTABLE_RUN")
	b.guard("PRIVATE_INTENT_IMMUTABLE", "PRIVATE_CLUSTER_INTENTS", "UPDATE OR DELETE", "1=1", "PRIVATE_CLUSTER_IMMUTABLE_INTENT")
	b.guard("PRIVATE_CONNECTION_RETAINED", "PRIVATE_CLUSTER_CONNECTIONS", "DELETE", "1=1", "PRIVATE_CLUSTER_IMMUTABLE_HISTORY")
	b.guard("PRIVATE_RUN_RETAINED", "PRIVATE_CLUSTER_RUNS", "DELETE", "1=1", "PRIVATE_CLUSTER_IMMUTABLE_HISTORY")
	return b.fragment
}

type infrastructureFragmentBuilder struct {
	fragment schemaFragment
	known    map[string]table
}

func newInfrastructureFragment(id string, current map[string]table, requires ...string) *infrastructureFragmentBuilder {
	b := &infrastructureFragmentBuilder{fragment: schemaFragment{FormatVersion: 1, ID: id, Requires: requires}, known: map[string]table{}}
	for name, t := range current {
		b.known[name] = cloneTable(t)
	}
	return b
}
func (b *infrastructureFragmentBuilder) add(c schemaChange) {
	b.fragment.Changes = append(b.fragment.Changes, c)
	if c.After != nil {
		b.known[c.After.Name] = cloneTable(*c.After)
	}
}
func (b *infrastructureFragmentBuilder) create(t table) {
	b.add(schemaChange{ID: t.Name, Kind: "table", SQL: extensionTableSQL(t, b.known, nil), After: &t})
}
func (b *infrastructureFragmentBuilder) reference(name, target string, local, remote []string) constraint {
	// Find the exact scoped key by columns, never silently reduce it to ID.
	for _, c := range b.known[target].Constraints {
		if (c.Kind == "P" || c.Kind == "U") && strings.Join(c.Columns, ",") == strings.Join(remote, ",") {
			return fk(name, target, c.Name, local...)
		}
	}
	// appendFragment must refuse a missing dependency before any DDL is admitted.
	return fk(name, target, "REQUIRED_SCOPED_KEY_MISSING", local...)
}
func (b *infrastructureFragmentBuilder) scope(name string) constraint {
	return b.reference(name, "WORKSPACES", []string{"TENANT_ID", "WORKSPACE_ID"}, []string{"TENANT_ID", "ID"})
}
func (b *infrastructureFragmentBuilder) user(name, field string) constraint {
	return b.reference(name, "USERS", []string{field}, []string{"ID"})
}
func (b *infrastructureFragmentBuilder) pin(name string) constraint {
	return b.reference(name, "IMAGE_REGISTRY_PINS", []string{"TENANT_ID", "WORKSPACE_ID", "PIN_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
}
func (b *infrastructureFragmentBuilder) index(name, target string, columns []string, descending []bool, unique bool) {
	if descending == nil {
		descending = make([]bool, len(columns))
	}
	i := index{Name: name, Table: target, Columns: columns, Descending: descending, Unique: unique}
	b.add(schemaChange{ID: name, Kind: "index", SQL: i.ddl(), Index: &i})
}
func (b *infrastructureFragmentBuilder) guard(name, target, event, condition, code string) {
	body := `BEGIN IF ` + condition + ` THEN RAISE_APPLICATION_ERROR(-20001,'` + code + `'); END IF; END;`
	t := trigger{Name: name, Table: target, Event: event, Timing: "BEFORE EACH ROW", Body: body}
	b.add(schemaChange{ID: name, Kind: "trigger", SQL: `CREATE TRIGGER ` + quoted(name) + ` BEFORE ` + event + ` ON ` + quoted(target) + ` FOR EACH ROW ` + body, Trigger: &t})
}
func infrastructureHash(name, field string, digest bool) constraint {
	pattern := "^[a-f0-9]{64}$"
	if digest {
		pattern = "^sha256:[a-f0-9]{64}$"
	}
	return check(name, quoted(field)+` IS NULL OR REGEXP_LIKE(`+quoted(field)+`, '`+pattern+`', 'c')`)
}
func infrastructureObject(name, field string) constraint {
	return check(name, quoted(field)+` IS NULL OR (JSON_TYPE(`+quoted(field)+`) IS NOT NULL AND JSON_TYPE(`+quoted(field)+`)='OBJECT')`)
}
func infrastructureChanged(fields ...string) string {
	out := []string{}
	for _, f := range fields {
		old, new := `:OLD.`+quoted(f), `:NEW.`+quoted(f)
		out = append(out, `(`+old+`<>`+new+` OR (`+old+` IS NULL AND `+new+` IS NOT NULL) OR (`+old+` IS NOT NULL AND `+new+` IS NULL))`)
	}
	return strings.Join(out, " OR ")
}
func infrastructureLOBChanged(field string) string {
	old, new := `:OLD.`+quoted(field), `:NEW.`+quoted(field)
	// COALESCE makes an unexpected native comparison NULL fail closed.
	return `((` + old + ` IS NULL AND ` + new + ` IS NOT NULL) OR (` + old + ` IS NOT NULL AND ` + new + ` IS NULL) OR (` + old + ` IS NOT NULL AND ` + new + ` IS NOT NULL AND COALESCE(DBMS_LOB.COMPARE(` + old + `,` + new + `),1)<>0))`
}
