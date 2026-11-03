package migrate

import "strings"

// Each fragment is independently registered by the central catalog owner.
// The current projection is copied; no old fragment or step is rewritten.
type lifecycleBuilder struct {
	fragment schemaFragment
	known    map[string]table
}

func lifecycleStart(current map[string]table, id string, requires ...string) *lifecycleBuilder {
	b := &lifecycleBuilder{fragment: schemaFragment{FormatVersion: 1, ID: id, Requires: requires}, known: map[string]table{}}
	for name, value := range current {
		b.known[name] = cloneTable(value)
	}
	return b
}
func (b *lifecycleBuilder) add(change schemaChange) {
	b.fragment.Changes = append(b.fragment.Changes, change)
	if change.After != nil {
		b.known[change.After.Name] = cloneTable(*change.After)
	}
}
func (b *lifecycleBuilder) create(t table) {
	b.add(schemaChange{ID: t.Name, Kind: "table", SQL: extensionTableSQL(t, b.known, nil), After: &t})
}
func (b *lifecycleBuilder) alter(id string, before, after table, action string) {
	b.add(schemaChange{ID: id, Kind: "alter", Before: &before, After: &after, SQL: "ALTER TABLE " + quoted(before.Name) + " " + action})
}
func (b *lifecycleBuilder) constraint(owner string, c constraint) {
	before := cloneTable(b.known[owner])
	after := cloneTable(before)
	after.Constraints = append(after.Constraints, c)
	body := ""
	switch c.Kind {
	case "P":
		body = "PRIMARY KEY (" + names(c.Columns) + ")"
	case "U":
		body = "UNIQUE (" + names(c.Columns) + ")"
	case "C":
		body = "CHECK (" + c.Check + ")"
	case "R":
		var keys []string
		for _, key := range b.known[c.Reference].Constraints {
			if key.Name == c.ReferenceKey {
				keys = key.Columns
			}
		}
		body = "FOREIGN KEY (" + names(c.Columns) + ") REFERENCES " + quoted(c.Reference) + " (" + names(keys) + ")"
	}
	b.alter(c.Name, before, after, "ADD CONSTRAINT "+quoted(c.Name)+" "+body)
}
func (b *lifecycleBuilder) foreign(owner, name, target string, fields, keys []string) {
	keyName := ""
	for _, key := range b.known[target].Constraints {
		if (key.Kind == "P" || key.Kind == "U") && strings.Join(key.Columns, "\x00") == strings.Join(keys, "\x00") {
			keyName = key.Name
			break
		}
	}
	b.constraint(owner, constraint{Name: name, Kind: "R", Columns: fields, Reference: target, ReferenceKey: keyName})
}
func (b *lifecycleBuilder) column(id, owner string, c column) {
	before := cloneTable(b.known[owner])
	after := cloneTable(before)
	after.Columns = append(after.Columns, c)
	b.alter(id, before, after, "ADD "+c.declaration())
}
func (b *lifecycleBuilder) nullable(id, owner, field string, nullable bool) {
	before := cloneTable(b.known[owner])
	after := cloneTable(before)
	declaration := ""
	for i := range after.Columns {
		if after.Columns[i].Name == field {
			after.Columns[i].Nullable = nullable
			declaration = after.Columns[i].declaration()
		}
	}
	b.alter(id, before, after, "MODIFY "+declaration)
}
func (b *lifecycleBuilder) drop(id, owner, name string) {
	before := cloneTable(b.known[owner])
	after := cloneTable(before)
	after.Constraints = nil
	for _, c := range before.Constraints {
		if c.Name != name {
			after.Constraints = append(after.Constraints, c)
		}
	}
	b.alter(id, before, after, "DROP CONSTRAINT "+quoted(name))
}
func (b *lifecycleBuilder) index(name, owner string, fields []string, descending []bool) {
	v := index{Name: name, Table: owner, Columns: fields, Descending: descending}
	b.add(schemaChange{ID: name, Kind: "index", SQL: v.ddl(), Index: &v})
}
func (b *lifecycleBuilder) trigger(name, owner, event, timing, body string) {
	v := trigger{Name: name, Table: owner, Event: event, Timing: timing + " EACH ROW", Body: body}
	b.add(schemaChange{ID: name, Kind: "trigger", SQL: "CREATE TRIGGER " + quoted(name) + " " + timing + " " + event + " ON " + quoted(owner) + " FOR EACH ROW " + body, Trigger: &v})
}
func lifecycleVirtual(name, kind string, width int, expression string) column {
	c := col(name, kind, width, true, "")
	c.Virtual = expression
	return c
}
func lifecycleUUID(name, field string) constraint {
	q := quoted(field)
	return check(name, q+" IS NULL OR REGEXP_LIKE("+q+",'^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$','c')")
}

// SQL NULL is not equality. LOB identity uses the native byte-aware comparator.
func lifecycleChanged(field string, lob bool) string {
	n, o := ":NEW."+quoted(field), ":OLD."+quoted(field)
	differs := n + "<>" + o
	if lob {
		differs = "DBMS_LOB.COMPARE(" + n + "," + o + ")<>0"
	}
	return "((" + n + " IS NULL AND " + o + " IS NOT NULL) OR (" + n + " IS NOT NULL AND " + o + " IS NULL) OR (" + n + " IS NOT NULL AND " + o + " IS NOT NULL AND " + differs + "))"
}
func lifecycleGPUTraining(b *lifecycleBuilder) {
	b.column("LC_GPU_ADD_ID", "GPU_LEASES", uuid("ID", true))
	b.add(schemaChange{ID: "LC_GPU_BACKFILL_ID", Kind: "backfill",
		SQL:   "UPDATE \"GPU_LEASES\" SET \"ID\"=\"DEPLOYMENT_ID\" WHERE \"ID\" IS NULL",
		Probe: "SELECT COUNT(*) FROM \"GPU_LEASES\" WHERE \"ID\" IS NULL", ProbeTables: []string{"GPU_LEASES"}})
	b.nullable("LC_GPU_ID_REQUIRED", "GPU_LEASES", "ID", false)
	b.constraint("GPU_LEASES", unique("LC_GPU_DEPLOYMENT_OWNER_UK", "DEPLOYMENT_ID"))
	b.drop("LC_GPU_DROP_DEPLOYMENT_PK", "GPU_LEASES", "GPU_LEASES_PK")
	b.constraint("GPU_LEASES", pk("LC_GPU_PK", "ID"))
	b.nullable("LC_GPU_DEPLOYMENT_OPTIONAL", "GPU_LEASES", "DEPLOYMENT_ID", true)
	b.constraint("GPU_LEASES", lifecycleUUID("LC_GPU_ID_UUID", "ID"))
	b.column("LC_GPU_ADD_TRAINING", "GPU_LEASES", uuid("TRAINING_JOB_ID", true))
	b.constraint("GPU_LEASES", lifecycleUUID("LC_GPU_TRAINING_UUID", "TRAINING_JOB_ID"))
	b.constraint("GPU_LEASES", unique("LC_GPU_TRAINING_OWNER_UK", "TRAINING_JOB_ID"))
	b.constraint("GPU_LEASES", check("LC_GPU_EXACT_TWO_OWNERS", "(CASE WHEN \"DEPLOYMENT_ID\" IS NULL THEN 0 ELSE 1 END)+(CASE WHEN \"TRAINING_JOB_ID\" IS NULL THEN 0 ELSE 1 END)=1"))
	b.foreign("GPU_LEASES", "LC_GPU_TRAINING_SCOPE_FK", "TRAINING_JOBS", []string{"TENANT_ID", "WORKSPACE_ID", "TRAINING_JOB_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("GPU_LEASES", "LC_GPU_NODE_SCOPE_FK", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
}
func lifecycleGPUPreparation(b *lifecycleBuilder) {
	b.column("LC_GPU_ADD_PREPARATION", "GPU_LEASES", uuid("MODEL_PREPARATION_ID", true))
	b.constraint("GPU_LEASES", lifecycleUUID("LC_GPU_PREPARATION_UUID", "MODEL_PREPARATION_ID"))
	b.constraint("GPU_LEASES", unique("LC_GPU_PREPARATION_OWNER_UK", "MODEL_PREPARATION_ID"))
	b.drop("LC_GPU_DROP_TWO_OWNERS", "GPU_LEASES", "LC_GPU_EXACT_TWO_OWNERS")
	b.constraint("GPU_LEASES", check("LC_GPU_EXACT_THREE_OWNERS", "(CASE WHEN \"DEPLOYMENT_ID\" IS NULL THEN 0 ELSE 1 END)+(CASE WHEN \"TRAINING_JOB_ID\" IS NULL THEN 0 ELSE 1 END)+(CASE WHEN \"MODEL_PREPARATION_ID\" IS NULL THEN 0 ELSE 1 END)=1"))
	b.foreign("GPU_LEASES", "LC_GPU_PREPARATION_SCOPE_FK", "MODEL_PREPARATIONS", []string{"TENANT_ID", "WORKSPACE_ID", "MODEL_PREPARATION_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
}

func trainingFragment(current map[string]table) schemaFragment {
	b := lifecycleStart(current, "training_v2", "foundation_v1", "datasets_v2", "models_v2", "nodes_components_v2")
	// 000060_training_jobs.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("TRAINING_JOBS", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "NODE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "CURRENT_NODE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "MODEL_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "DATASET_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "DATASET_VERSION_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "GPU_UUID", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "CONFIGURATION", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "CONFIGURATION_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "EXECUTION_RECIPE", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "DATASET_MANIFEST_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "MODEL_CATALOG_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATED_BY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "REQUESTED_BY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "REQUESTED_AUDIENCE", Type: "VARCHAR", Width: 8, Characters: true, Nullable: false, Default: ""},
		column{Name: "REQUESTED_EPOCH", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "MEMBERSHIP_AUTHORITY", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "REASON", Type: "VARCHAR", Width: 500, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATION_KEY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "CREATION_FINGERPRINT", Type: "VARBINARY", Width: 32, Characters: false, Nullable: false, Default: ""},
		column{Name: "STATE", Type: "VARCHAR", Width: 24, Characters: true, Nullable: false, Default: ""},
		column{Name: "WAIT_CODE", Type: "VARCHAR", Width: 80, Characters: true, Nullable: true, Default: ""},
		column{Name: "NEXT_DISPATCH_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		column{Name: "REVISION", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: "1"},
		column{Name: "ATTEMPT_NUMBER", Type: "INTEGER", Width: 0, Characters: false, Nullable: false, Default: "0"},
		column{Name: "CURRENT_ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "ACCEPTED_ARTIFACT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "PENDING_RECOVERY_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "CANCEL_REQUESTED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "ERROR_CODE", Type: "VARCHAR", Width: 80, Characters: true, Nullable: true, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		column{Name: "UPDATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		column{Name: "FINISHED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""}},
		pk("LC_TRAINING_JOBS_PK", "ID"),
		check("LC_TRAINING_JOBS_C01", `REGEXP_LIKE("GPU_UUID",'^GPU-[a-f0-9-]{36}$','c')`),
		check("LC_TRAINING_JOBS_C02", `JSON_TYPEOF("CONFIGURATION") IS NOT NULL AND JSON_TYPEOF("CONFIGURATION")='object' AND OCTET_LENGTH("CONFIGURATION")<=16384`),
		check("LC_TRAINING_JOBS_C03", `REGEXP_LIKE("CONFIGURATION_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_TRAINING_JOBS_C04", `JSON_TYPEOF("EXECUTION_RECIPE") IS NOT NULL AND JSON_TYPEOF("EXECUTION_RECIPE")='object' AND OCTET_LENGTH("EXECUTION_RECIPE")<=32768`),
		check("LC_TRAINING_JOBS_C05", `REGEXP_LIKE("DATASET_MANIFEST_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_TRAINING_JOBS_C06", `REGEXP_LIKE("MODEL_CATALOG_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_TRAINING_JOBS_C07", `"REQUESTED_AUDIENCE" IN ('portal','admin')`),
		check("LC_TRAINING_JOBS_C08", `"REQUESTED_EPOCH">=0`),
		check("LC_TRAINING_JOBS_C09", `CHAR_LENGTH(TRIM("REASON"))>0`),
		check("LC_TRAINING_JOBS_C10", `OCTET_LENGTH("CREATION_FINGERPRINT")=32`),
		check("LC_TRAINING_JOBS_C11", `"STATE" IN ('queued','preparing','running','cancelling','succeeded','cancelled','failed','recovery_required')`),
		check("LC_TRAINING_JOBS_C12", `"REVISION" BETWEEN 1 AND 9007199254740991`),
		check("LC_TRAINING_JOBS_C13", `"ATTEMPT_NUMBER" BETWEEN 0 AND 1000`),
		unique("LC_TRAINING_JOBS_U01", "TENANT_ID", "WORKSPACE_ID", "ID"),
		unique("LC_TRAINING_JOBS_U02", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"),
		unique("LC_TRAINING_JOBS_U03", "TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "CREATION_KEY"),
		check("LC_TRAINING_JOBS_C14", `"REQUESTED_AUDIENCE"='admin' OR "MEMBERSHIP_AUTHORITY" IS NOT NULL`)))
	// 000061_training_attempts.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("TRAINING_ATTEMPTS", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "JOB_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "NODE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TASK_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "ATTEMPT_NUMBER", Type: "INTEGER", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "STATE", Type: "VARCHAR", Width: 24, Characters: true, Nullable: false, Default: ""},
		column{Name: "REQUESTED_BY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "REQUESTED_AUDIENCE", Type: "VARCHAR", Width: 8, Characters: true, Nullable: false, Default: ""},
		column{Name: "REQUESTED_EPOCH", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "MEMBERSHIP_AUTHORITY", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "INPUT_INVENTORY", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "RESUME_CHECKPOINT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "LAST_EVENT_SEQUENCE", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: "0"},
		column{Name: "LAST_GLOBAL_STEP", Type: "INTEGER", Width: 0, Characters: false, Nullable: false, Default: "0"},
		column{Name: "STOP_TOKEN_HASH", Type: "VARBINARY", Width: 32, Characters: false, Nullable: true, Default: ""},
		column{Name: "OUTCOME_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: true, Default: ""},
		column{Name: "OUTCOME", Type: "CLOB", Width: 0, Characters: false, Nullable: true, Default: ""},
		column{Name: "PHYSICAL_STOP_RECEIPT", Type: "CLOB", Width: 0, Characters: false, Nullable: true, Default: ""},
		column{Name: "PHYSICAL_STOP_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: true, Default: ""},
		column{Name: "PHYSICAL_STOPPED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "ERROR_CODE", Type: "VARCHAR", Width: 80, Characters: true, Nullable: true, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		column{Name: "STARTED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "FINISHED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "UPDATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		lifecycleVirtual("UNCLEARED_JOB_ID", "CHAR", 36, `CASE WHEN "STATE" IN('pending','preparing','running','stopping','recovery_required') AND "PHYSICAL_STOPPED_AT" IS NULL THEN "JOB_ID" ELSE NULL END`)},
		pk("LC_TRAINING_ATTEMPTS_PK", "ID"),
		check("LC_TRAINING_ATTEMPTS_C01", `"ATTEMPT_NUMBER" BETWEEN 1 AND 1000`),
		check("LC_TRAINING_ATTEMPTS_C02", `"STATE" IN ('pending','preparing','running','stopping','succeeded','cancelled','failed','recovery_required','stopped')`),
		check("LC_TRAINING_ATTEMPTS_C03", `"REQUESTED_AUDIENCE" IN ('portal','admin')`),
		check("LC_TRAINING_ATTEMPTS_C04", `"REQUESTED_EPOCH">=0`),
		check("LC_TRAINING_ATTEMPTS_C05", `JSON_TYPEOF("INPUT_INVENTORY") IS NOT NULL AND JSON_TYPEOF("INPUT_INVENTORY")='object' AND OCTET_LENGTH("INPUT_INVENTORY")<=65536`),
		check("LC_TRAINING_ATTEMPTS_C06", `"LAST_EVENT_SEQUENCE" BETWEEN 0 AND 1000000`),
		check("LC_TRAINING_ATTEMPTS_C07", `"LAST_GLOBAL_STEP" BETWEEN 0 AND 1000`),
		check("LC_TRAINING_ATTEMPTS_C08", `"STOP_TOKEN_HASH" IS NULL OR OCTET_LENGTH("STOP_TOKEN_HASH")=32`),
		check("LC_TRAINING_ATTEMPTS_C09", `"OUTCOME_SHA256" IS NULL OR REGEXP_LIKE("OUTCOME_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_TRAINING_ATTEMPTS_C10", `"OUTCOME" IS NULL OR JSON_TYPEOF("OUTCOME") IS NOT NULL AND JSON_TYPEOF("OUTCOME")='object' AND OCTET_LENGTH("OUTCOME")<=16777216`),
		check("LC_TRAINING_ATTEMPTS_C11", `"PHYSICAL_STOP_RECEIPT" IS NULL OR JSON_TYPEOF("PHYSICAL_STOP_RECEIPT") IS NOT NULL AND JSON_TYPEOF("PHYSICAL_STOP_RECEIPT")='object' AND OCTET_LENGTH("PHYSICAL_STOP_RECEIPT")<=32768`),
		check("LC_TRAINING_ATTEMPTS_C12", `"PHYSICAL_STOP_SHA256" IS NULL OR REGEXP_LIKE("PHYSICAL_STOP_SHA256",'^[a-f0-9]{64}$','c')`),
		unique("LC_TRAINING_ATTEMPTS_U01", "TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"),
		unique("LC_TRAINING_ATTEMPTS_U02", "JOB_ID", "ATTEMPT_NUMBER"),
		unique("LC_TRAINING_ATTEMPTS_U03", "TASK_ID"),
		check("LC_TRAINING_ATTEMPTS_C13", `"REQUESTED_AUDIENCE"='admin' OR "MEMBERSHIP_AUTHORITY" IS NOT NULL`),
		check("LC_TRAINING_ATTEMPTS_C14", `((CASE WHEN "PHYSICAL_STOP_RECEIPT" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "PHYSICAL_STOP_SHA256" IS NULL THEN 1 ELSE 0 END))`),
		check("LC_TRAINING_ATTEMPTS_C15", `((CASE WHEN "PHYSICAL_STOP_RECEIPT" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "PHYSICAL_STOPPED_AT" IS NULL THEN 1 ELSE 0 END))`),
		check("LC_TRAINING_ATTEMPTS_C16", `((CASE WHEN "OUTCOME" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "OUTCOME_SHA256" IS NULL THEN 1 ELSE 0 END))`),
		unique("LC_TRAINING_ATTEMPTS_U04", "UNCLEARED_JOB_ID")))
	// 000062_training_events.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("TRAINING_EVENTS", []column{column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "JOB_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "SEQUENCE", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "KIND", Type: "VARCHAR", Width: 16, Characters: true, Nullable: false, Default: ""},
		column{Name: "GLOBAL_STEP", Type: "INTEGER", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "PAYLOAD", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "CANONICAL_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "RECORDED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		lifecycleVirtual("CHECKPOINT_ATTEMPT_ID", "CHAR", 36, `CASE WHEN "KIND"='checkpoint' THEN "ATTEMPT_ID" ELSE NULL END`),
		lifecycleVirtual("CHECKPOINT_STEP", "INTEGER", 0, `CASE WHEN "KIND"='checkpoint' THEN "GLOBAL_STEP" ELSE NULL END`)},
		check("LC_TRAINING_EVENTS_C01", `"SEQUENCE" BETWEEN 1 AND 1000000`),
		check("LC_TRAINING_EVENTS_C02", `"KIND" IN ('phase','metric','checkpoint')`),
		check("LC_TRAINING_EVENTS_C03", `"GLOBAL_STEP" BETWEEN 0 AND 1000`),
		check("LC_TRAINING_EVENTS_C04", `JSON_TYPEOF("PAYLOAD") IS NOT NULL AND JSON_TYPEOF("PAYLOAD")='object' AND OCTET_LENGTH("PAYLOAD")<=16384`),
		check("LC_TRAINING_EVENTS_C05", `REGEXP_LIKE("CANONICAL_SHA256",'^[a-f0-9]{64}$','c')`),
		pk("LC_TRAINING_EVENTS_PK", "ATTEMPT_ID", "SEQUENCE"),
		unique("LC_TRAINING_EVENTS_U01", "CHECKPOINT_ATTEMPT_ID", "CHECKPOINT_STEP")))
	// 000063_training_checkpoints.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("TRAINING_CHECKPOINTS", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "JOB_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "GLOBAL_STEP", Type: "INTEGER", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "RECEIPT_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "RECEIPT", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "FILE_INVENTORY", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "STORAGE_KEY", Type: "VARCHAR", Width: 160, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		pk("LC_TRAINING_CHECKPOINTS_PK", "ID"),
		check("LC_TRAINING_CHECKPOINTS_C01", `"GLOBAL_STEP" BETWEEN 1 AND 1000`),
		check("LC_TRAINING_CHECKPOINTS_C02", `REGEXP_LIKE("RECEIPT_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_TRAINING_CHECKPOINTS_C03", `JSON_TYPEOF("RECEIPT") IS NOT NULL AND JSON_TYPEOF("RECEIPT")='object' AND OCTET_LENGTH("RECEIPT")<=1048576`),
		check("LC_TRAINING_CHECKPOINTS_C04", `JSON_TYPEOF("FILE_INVENTORY") IS NOT NULL AND JSON_TYPEOF("FILE_INVENTORY")='object' AND OCTET_LENGTH("FILE_INVENTORY")<=1048576`),
		check("LC_TRAINING_CHECKPOINTS_C05", `REGEXP_LIKE("STORAGE_KEY",'^checkpoints/[a-f0-9-]{36}/[a-f0-9-]{36}/[a-f0-9-]{36}$','c')`),
		unique("LC_TRAINING_CHECKPOINTS_U01", "TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"),
		unique("LC_TRAINING_CHECKPOINTS_U02", "ATTEMPT_ID", "GLOBAL_STEP")))
	// 000064_training_artifacts.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("TRAINING_ARTIFACTS", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "JOB_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "MANIFEST_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "MANIFEST", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "FILE_INVENTORY", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "STORAGE_KEY", Type: "VARCHAR", Width: 160, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		pk("LC_TRAINING_ARTIFACTS_PK", "ID"),
		check("LC_TRAINING_ARTIFACTS_C01", `REGEXP_LIKE("MANIFEST_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_TRAINING_ARTIFACTS_C02", `JSON_TYPEOF("MANIFEST") IS NOT NULL AND JSON_TYPEOF("MANIFEST")='object' AND OCTET_LENGTH("MANIFEST")<=1048576`),
		check("LC_TRAINING_ARTIFACTS_C03", `JSON_TYPEOF("FILE_INVENTORY") IS NOT NULL AND JSON_TYPEOF("FILE_INVENTORY")='object' AND OCTET_LENGTH("FILE_INVENTORY")<=1048576`),
		check("LC_TRAINING_ARTIFACTS_C04", `REGEXP_LIKE("STORAGE_KEY",'^artifacts/[a-f0-9-]{36}/[a-f0-9-]{36}/[a-f0-9-]{36}$','c')`),
		unique("LC_TRAINING_ARTIFACTS_U01", "TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"),
		unique("LC_TRAINING_ARTIFACTS_U02", "ATTEMPT_ID")))
	// 000065_training_recoveries.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("TRAINING_RECOVERIES", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "JOB_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "PREVIOUS_ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "NEXT_ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "CHECKPOINT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "PREVIOUS_NODE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "NEXT_NODE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "MODE", Type: "VARCHAR", Width: 16, Characters: true, Nullable: false, Default: ""},
		column{Name: "REQUESTED_BY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "REASON", Type: "VARCHAR", Width: 500, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATION_KEY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "CREATION_FINGERPRINT", Type: "VARBINARY", Width: 32, Characters: false, Nullable: false, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		pk("LC_TRAINING_RECOVERIES_PK", "ID"),
		check("LC_TRAINING_RECOVERIES_C01", `"MODE" IN ('checkpoint','restart')`),
		check("LC_TRAINING_RECOVERIES_C02", `CHAR_LENGTH(TRIM("REASON"))>0`),
		check("LC_TRAINING_RECOVERIES_C03", `OCTET_LENGTH("CREATION_FINGERPRINT")=32`),
		unique("LC_TRAINING_RECOVERIES_U01", "TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"),
		unique("LC_TRAINING_RECOVERIES_U02", "JOB_ID", "CREATION_KEY"),
		check("LC_TRAINING_RECOVERIES_C04", `((CASE WHEN "MODE"='checkpoint' THEN 1 ELSE 0 END)=(CASE WHEN "CHECKPOINT_ID" IS NOT NULL THEN 1 ELSE 0 END))`)))
	// 000066_training_uploads.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("TRAINING_UPLOADS", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "JOB_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "KIND", Type: "VARCHAR", Width: 16, Characters: true, Nullable: false, Default: ""},
		column{Name: "GLOBAL_STEP", Type: "INTEGER", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "MANIFEST_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "MANIFEST", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "STATE", Type: "VARCHAR", Width: 16, Characters: true, Nullable: false, Default: ""},
		column{Name: "MANIFEST_BYTES", Type: "BLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "ACTIVE_CHUNK_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "ACTIVE_ASSET", Type: "VARCHAR", Width: 100, Characters: true, Nullable: true, Default: ""},
		column{Name: "ACTIVE_OFFSET", Type: "BIGINT", Width: 0, Characters: false, Nullable: true, Default: ""},
		column{Name: "ACTIVE_EXPIRES_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		column{Name: "UPDATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		pk("LC_TRAINING_UPLOADS_PK", "ID"),
		check("LC_TRAINING_UPLOADS_C01", `"KIND" IN ('checkpoint','artifact')`),
		check("LC_TRAINING_UPLOADS_C02", `"GLOBAL_STEP" BETWEEN 0 AND 1000`),
		check("LC_TRAINING_UPLOADS_C03", `REGEXP_LIKE("MANIFEST_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_TRAINING_UPLOADS_C04", `JSON_TYPEOF("MANIFEST") IS NOT NULL AND JSON_TYPEOF("MANIFEST")='object' AND OCTET_LENGTH("MANIFEST")<=1048576`),
		check("LC_TRAINING_UPLOADS_C05", `"STATE" IN ('uploading','accepted','failed')`),
		check("LC_TRAINING_UPLOADS_C06", `OCTET_LENGTH("MANIFEST_BYTES") BETWEEN 2 AND 1048576`),
		check("LC_TRAINING_UPLOADS_C07", `"ACTIVE_ASSET" IS NULL OR REGEXP_LIKE("ACTIVE_ASSET",'^[A-Za-z0-9_.-]{1,100}$','c')`),
		check("LC_TRAINING_UPLOADS_C08", `"ACTIVE_OFFSET" IS NULL OR "ACTIVE_OFFSET">=0 AND MOD("ACTIVE_OFFSET",8388608)=0`),
		unique("LC_TRAINING_UPLOADS_U01", "TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ATTEMPT_ID", "ID"),
		unique("LC_TRAINING_UPLOADS_U02", "ATTEMPT_ID", "KIND", "GLOBAL_STEP"),
		check("LC_TRAINING_UPLOADS_C09", `((CASE WHEN "ACTIVE_CHUNK_ID" IS NOT NULL THEN 1 ELSE 0 END)+(CASE WHEN "ACTIVE_ASSET" IS NOT NULL THEN 1 ELSE 0 END)+(CASE WHEN "ACTIVE_OFFSET" IS NOT NULL THEN 1 ELSE 0 END)+(CASE WHEN "ACTIVE_EXPIRES_AT" IS NOT NULL THEN 1 ELSE 0 END)) IN (0,4)`)))
	// 000067_training_upload_chunks.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("TRAINING_UPLOAD_CHUNKS", []column{column{Name: "UPLOAD_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ASSET", Type: "VARCHAR", Width: 100, Characters: true, Nullable: false, Default: ""},
		column{Name: "BYTE_OFFSET", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "SIZE_BYTES", Type: "INTEGER", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""}},
		check("LC_TRAINING_UPLOAD_CHUNKS_C01", `REGEXP_LIKE("ASSET",'^[A-Za-z0-9_.-]{1,100}$','c')`),
		check("LC_TRAINING_UPLOAD_CHUNKS_C02", `"BYTE_OFFSET">=0 AND MOD("BYTE_OFFSET",8388608)=0`),
		check("LC_TRAINING_UPLOAD_CHUNKS_C03", `"SIZE_BYTES" BETWEEN 1 AND 8388608`),
		check("LC_TRAINING_UPLOAD_CHUNKS_C04", `REGEXP_LIKE("SHA256",'^[a-f0-9]{64}$','c')`),
		pk("LC_TRAINING_UPLOAD_CHUNKS_PK", "UPLOAD_ID", "ASSET", "BYTE_OFFSET")))
	b.foreign("TRAINING_JOBS", "LC_TRAINING_JOBS_F01", "WORKSPACES", []string{"TENANT_ID", "WORKSPACE_ID"}, []string{"TENANT_ID", "ID"})
	b.foreign("TRAINING_JOBS", "LC_TRAINING_JOBS_F02", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("TRAINING_JOBS", "LC_TRAINING_JOBS_F03", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "CURRENT_NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("TRAINING_JOBS", "LC_TRAINING_JOBS_F04", "MODEL_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "MODEL_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("TRAINING_JOBS", "LC_TRAINING_JOBS_F05", "DATASET_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DATASET_VERSION_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "ID"})
	b.foreign("TRAINING_JOBS", "LC_TRAINING_JOBS_F06", "USERS", []string{"CREATED_BY"}, []string{"ID"})
	b.foreign("TRAINING_JOBS", "LC_TRAINING_JOBS_F07", "USERS", []string{"REQUESTED_BY"}, []string{"ID"})
	b.foreign("TRAINING_JOBS", "TRAINING_JOB_CURRENT_ATTEMPT_FK", "TRAINING_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "ID", "CURRENT_ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.foreign("TRAINING_JOBS", "TRAINING_JOB_ACCEPTED_ARTIFACT_FK", "TRAINING_ARTIFACTS", []string{"TENANT_ID", "WORKSPACE_ID", "ID", "ACCEPTED_ARTIFACT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.foreign("TRAINING_JOBS", "TRAINING_JOB_PENDING_RECOVERY_FK", "TRAINING_RECOVERIES", []string{"TENANT_ID", "WORKSPACE_ID", "ID", "PENDING_RECOVERY_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.index("TRAINING_JOB_SCOPE_PAGE_IDX", "TRAINING_JOBS", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, []bool{false, false, true, true})
	b.index("TRAINING_JOB_QUEUE_IDX", "TRAINING_JOBS", []string{"STATE", "NEXT_DISPATCH_AT", "CREATED_AT", "ID"}, []bool{false, false, false, false})
	b.foreign("TRAINING_ATTEMPTS", "LC_TRAINING_ATTEMPTS_F01", "TRAINING_JOBS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("TRAINING_ATTEMPTS", "LC_TRAINING_ATTEMPTS_F02", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("TRAINING_ATTEMPTS", "LC_TRAINING_ATTEMPTS_F03", "NODE_TASKS", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"})
	b.foreign("TRAINING_ATTEMPTS", "LC_TRAINING_ATTEMPTS_F04", "USERS", []string{"REQUESTED_BY"}, []string{"ID"})
	b.foreign("TRAINING_ATTEMPTS", "TRAINING_ATTEMPT_RESUME_CHECKPOINT_FK", "TRAINING_CHECKPOINTS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "RESUME_CHECKPOINT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.index("TRAINING_ATTEMPT_SCOPE_PAGE_IDX", "TRAINING_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "CREATED_AT", "ID"}, []bool{false, false, false, true, true})
	b.foreign("TRAINING_EVENTS", "LC_TRAINING_EVENTS_F01", "TRAINING_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.index("TRAINING_EVENT_JOB_PAGE_IDX", "TRAINING_EVENTS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ATTEMPT_ID", "SEQUENCE"}, []bool{false, false, false, false, false})
	b.foreign("TRAINING_CHECKPOINTS", "LC_TRAINING_CHECKPOINTS_F01", "TRAINING_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.foreign("TRAINING_CHECKPOINTS", "TRAINING_CHECKPOINT_UPLOAD_FK", "TRAINING_UPLOADS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ATTEMPT_ID", "ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ATTEMPT_ID", "ID"})
	b.index("TRAINING_CHECKPOINT_SCOPE_PAGE_IDX", "TRAINING_CHECKPOINTS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "CREATED_AT", "ID"}, []bool{false, false, false, true, true})
	b.foreign("TRAINING_ARTIFACTS", "LC_TRAINING_ARTIFACTS_F01", "TRAINING_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.foreign("TRAINING_ARTIFACTS", "TRAINING_ARTIFACT_UPLOAD_FK", "TRAINING_UPLOADS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ATTEMPT_ID", "ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ATTEMPT_ID", "ID"})
	b.foreign("TRAINING_RECOVERIES", "LC_TRAINING_RECOVERIES_F01", "TRAINING_JOBS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("TRAINING_RECOVERIES", "LC_TRAINING_RECOVERIES_F02", "TRAINING_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "PREVIOUS_ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.foreign("TRAINING_RECOVERIES", "LC_TRAINING_RECOVERIES_F03", "TRAINING_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "NEXT_ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.foreign("TRAINING_RECOVERIES", "LC_TRAINING_RECOVERIES_F04", "TRAINING_CHECKPOINTS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "CHECKPOINT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.foreign("TRAINING_RECOVERIES", "LC_TRAINING_RECOVERIES_F05", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "PREVIOUS_NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("TRAINING_RECOVERIES", "LC_TRAINING_RECOVERIES_F06", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "NEXT_NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("TRAINING_RECOVERIES", "LC_TRAINING_RECOVERIES_F07", "USERS", []string{"REQUESTED_BY"}, []string{"ID"})
	b.foreign("TRAINING_UPLOADS", "LC_TRAINING_UPLOADS_F01", "TRAINING_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.foreign("TRAINING_UPLOAD_CHUNKS", "LC_TRAINING_UPLOAD_CHUNKS_F01", "TRAINING_UPLOADS", []string{"UPLOAD_ID"}, []string{"ID"})
	lifecycleGPUTraining(b)
	b.trigger("LC_TRAINING_JOB_INPUTS_IMMUTABLE", "TRAINING_JOBS", "UPDATE", "BEFORE", `BEGIN
    IF ((:NEW."ID" IS NULL AND :OLD."ID" IS NOT NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NOT NULL AND :NEW."ID"<>:OLD."ID")) OR
       ((:NEW."TENANT_ID" IS NULL AND :OLD."TENANT_ID" IS NOT NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NOT NULL AND :NEW."TENANT_ID"<>:OLD."TENANT_ID")) OR
       ((:NEW."WORKSPACE_ID" IS NULL AND :OLD."WORKSPACE_ID" IS NOT NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NOT NULL AND :NEW."WORKSPACE_ID"<>:OLD."WORKSPACE_ID")) OR
       ((:NEW."NODE_ID" IS NULL AND :OLD."NODE_ID" IS NOT NULL) OR (:NEW."NODE_ID" IS NOT NULL AND :OLD."NODE_ID" IS NULL) OR (:NEW."NODE_ID" IS NOT NULL AND :OLD."NODE_ID" IS NOT NULL AND :NEW."NODE_ID"<>:OLD."NODE_ID")) OR
       ((:NEW."MODEL_ID" IS NULL AND :OLD."MODEL_ID" IS NOT NULL) OR (:NEW."MODEL_ID" IS NOT NULL AND :OLD."MODEL_ID" IS NULL) OR (:NEW."MODEL_ID" IS NOT NULL AND :OLD."MODEL_ID" IS NOT NULL AND :NEW."MODEL_ID"<>:OLD."MODEL_ID")) OR
       ((:NEW."DATASET_ID" IS NULL AND :OLD."DATASET_ID" IS NOT NULL) OR (:NEW."DATASET_ID" IS NOT NULL AND :OLD."DATASET_ID" IS NULL) OR (:NEW."DATASET_ID" IS NOT NULL AND :OLD."DATASET_ID" IS NOT NULL AND :NEW."DATASET_ID"<>:OLD."DATASET_ID")) OR
       ((:NEW."DATASET_VERSION_ID" IS NULL AND :OLD."DATASET_VERSION_ID" IS NOT NULL) OR (:NEW."DATASET_VERSION_ID" IS NOT NULL AND :OLD."DATASET_VERSION_ID" IS NULL) OR (:NEW."DATASET_VERSION_ID" IS NOT NULL AND :OLD."DATASET_VERSION_ID" IS NOT NULL AND :NEW."DATASET_VERSION_ID"<>:OLD."DATASET_VERSION_ID")) OR
       ((:NEW."GPU_UUID" IS NULL AND :OLD."GPU_UUID" IS NOT NULL) OR (:NEW."GPU_UUID" IS NOT NULL AND :OLD."GPU_UUID" IS NULL) OR (:NEW."GPU_UUID" IS NOT NULL AND :OLD."GPU_UUID" IS NOT NULL AND :NEW."GPU_UUID"<>:OLD."GPU_UUID")) OR
       ((:NEW."CONFIGURATION" IS NULL AND :OLD."CONFIGURATION" IS NOT NULL) OR (:NEW."CONFIGURATION" IS NOT NULL AND :OLD."CONFIGURATION" IS NULL) OR (:NEW."CONFIGURATION" IS NOT NULL AND :OLD."CONFIGURATION" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."CONFIGURATION",:OLD."CONFIGURATION")<>0)) OR
       ((:NEW."CONFIGURATION_SHA256" IS NULL AND :OLD."CONFIGURATION_SHA256" IS NOT NULL) OR (:NEW."CONFIGURATION_SHA256" IS NOT NULL AND :OLD."CONFIGURATION_SHA256" IS NULL) OR (:NEW."CONFIGURATION_SHA256" IS NOT NULL AND :OLD."CONFIGURATION_SHA256" IS NOT NULL AND :NEW."CONFIGURATION_SHA256"<>:OLD."CONFIGURATION_SHA256")) OR
       ((:NEW."EXECUTION_RECIPE" IS NULL AND :OLD."EXECUTION_RECIPE" IS NOT NULL) OR (:NEW."EXECUTION_RECIPE" IS NOT NULL AND :OLD."EXECUTION_RECIPE" IS NULL) OR (:NEW."EXECUTION_RECIPE" IS NOT NULL AND :OLD."EXECUTION_RECIPE" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."EXECUTION_RECIPE",:OLD."EXECUTION_RECIPE")<>0)) OR
       ((:NEW."DATASET_MANIFEST_SHA256" IS NULL AND :OLD."DATASET_MANIFEST_SHA256" IS NOT NULL) OR (:NEW."DATASET_MANIFEST_SHA256" IS NOT NULL AND :OLD."DATASET_MANIFEST_SHA256" IS NULL) OR (:NEW."DATASET_MANIFEST_SHA256" IS NOT NULL AND :OLD."DATASET_MANIFEST_SHA256" IS NOT NULL AND :NEW."DATASET_MANIFEST_SHA256"<>:OLD."DATASET_MANIFEST_SHA256")) OR
       ((:NEW."MODEL_CATALOG_SHA256" IS NULL AND :OLD."MODEL_CATALOG_SHA256" IS NOT NULL) OR (:NEW."MODEL_CATALOG_SHA256" IS NOT NULL AND :OLD."MODEL_CATALOG_SHA256" IS NULL) OR (:NEW."MODEL_CATALOG_SHA256" IS NOT NULL AND :OLD."MODEL_CATALOG_SHA256" IS NOT NULL AND :NEW."MODEL_CATALOG_SHA256"<>:OLD."MODEL_CATALOG_SHA256")) OR
       ((:NEW."CREATED_BY" IS NULL AND :OLD."CREATED_BY" IS NOT NULL) OR (:NEW."CREATED_BY" IS NOT NULL AND :OLD."CREATED_BY" IS NULL) OR (:NEW."CREATED_BY" IS NOT NULL AND :OLD."CREATED_BY" IS NOT NULL AND :NEW."CREATED_BY"<>:OLD."CREATED_BY")) OR
       ((:NEW."REASON" IS NULL AND :OLD."REASON" IS NOT NULL) OR (:NEW."REASON" IS NOT NULL AND :OLD."REASON" IS NULL) OR (:NEW."REASON" IS NOT NULL AND :OLD."REASON" IS NOT NULL AND :NEW."REASON"<>:OLD."REASON")) OR
       ((:NEW."CREATION_KEY" IS NULL AND :OLD."CREATION_KEY" IS NOT NULL) OR (:NEW."CREATION_KEY" IS NOT NULL AND :OLD."CREATION_KEY" IS NULL) OR (:NEW."CREATION_KEY" IS NOT NULL AND :OLD."CREATION_KEY" IS NOT NULL AND :NEW."CREATION_KEY"<>:OLD."CREATION_KEY")) OR
       ((:NEW."CREATION_FINGERPRINT" IS NULL AND :OLD."CREATION_FINGERPRINT" IS NOT NULL) OR (:NEW."CREATION_FINGERPRINT" IS NOT NULL AND :OLD."CREATION_FINGERPRINT" IS NULL) OR (:NEW."CREATION_FINGERPRINT" IS NOT NULL AND :OLD."CREATION_FINGERPRINT" IS NOT NULL AND :NEW."CREATION_FINGERPRINT"<>:OLD."CREATION_FINGERPRINT")) OR
       ((:NEW."CREATED_AT" IS NULL AND :OLD."CREATED_AT" IS NOT NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NOT NULL AND :NEW."CREATED_AT"<>:OLD."CREATED_AT")) THEN
        RAISE_APPLICATION_ERROR(-20061,'TRAINING_INPUTS_IMMUTABLE');
    END IF;
END;`)
	b.trigger("LC_TRAINING_ATTEMPT_INPUTS_IMMUTABLE", "TRAINING_ATTEMPTS", "UPDATE", "BEFORE", `BEGIN
    IF ((:NEW."ID" IS NULL AND :OLD."ID" IS NOT NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NOT NULL AND :NEW."ID"<>:OLD."ID")) OR
       ((:NEW."TENANT_ID" IS NULL AND :OLD."TENANT_ID" IS NOT NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NOT NULL AND :NEW."TENANT_ID"<>:OLD."TENANT_ID")) OR
       ((:NEW."WORKSPACE_ID" IS NULL AND :OLD."WORKSPACE_ID" IS NOT NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NOT NULL AND :NEW."WORKSPACE_ID"<>:OLD."WORKSPACE_ID")) OR
       ((:NEW."JOB_ID" IS NULL AND :OLD."JOB_ID" IS NOT NULL) OR (:NEW."JOB_ID" IS NOT NULL AND :OLD."JOB_ID" IS NULL) OR (:NEW."JOB_ID" IS NOT NULL AND :OLD."JOB_ID" IS NOT NULL AND :NEW."JOB_ID"<>:OLD."JOB_ID")) OR
       ((:NEW."NODE_ID" IS NULL AND :OLD."NODE_ID" IS NOT NULL) OR (:NEW."NODE_ID" IS NOT NULL AND :OLD."NODE_ID" IS NULL) OR (:NEW."NODE_ID" IS NOT NULL AND :OLD."NODE_ID" IS NOT NULL AND :NEW."NODE_ID"<>:OLD."NODE_ID")) OR
       ((:NEW."TASK_ID" IS NULL AND :OLD."TASK_ID" IS NOT NULL) OR (:NEW."TASK_ID" IS NOT NULL AND :OLD."TASK_ID" IS NULL) OR (:NEW."TASK_ID" IS NOT NULL AND :OLD."TASK_ID" IS NOT NULL AND :NEW."TASK_ID"<>:OLD."TASK_ID")) OR
       ((:NEW."ATTEMPT_NUMBER" IS NULL AND :OLD."ATTEMPT_NUMBER" IS NOT NULL) OR (:NEW."ATTEMPT_NUMBER" IS NOT NULL AND :OLD."ATTEMPT_NUMBER" IS NULL) OR (:NEW."ATTEMPT_NUMBER" IS NOT NULL AND :OLD."ATTEMPT_NUMBER" IS NOT NULL AND :NEW."ATTEMPT_NUMBER"<>:OLD."ATTEMPT_NUMBER")) OR
       ((:NEW."REQUESTED_BY" IS NULL AND :OLD."REQUESTED_BY" IS NOT NULL) OR (:NEW."REQUESTED_BY" IS NOT NULL AND :OLD."REQUESTED_BY" IS NULL) OR (:NEW."REQUESTED_BY" IS NOT NULL AND :OLD."REQUESTED_BY" IS NOT NULL AND :NEW."REQUESTED_BY"<>:OLD."REQUESTED_BY")) OR
       ((:NEW."REQUESTED_AUDIENCE" IS NULL AND :OLD."REQUESTED_AUDIENCE" IS NOT NULL) OR (:NEW."REQUESTED_AUDIENCE" IS NOT NULL AND :OLD."REQUESTED_AUDIENCE" IS NULL) OR (:NEW."REQUESTED_AUDIENCE" IS NOT NULL AND :OLD."REQUESTED_AUDIENCE" IS NOT NULL AND :NEW."REQUESTED_AUDIENCE"<>:OLD."REQUESTED_AUDIENCE")) OR
       ((:NEW."REQUESTED_EPOCH" IS NULL AND :OLD."REQUESTED_EPOCH" IS NOT NULL) OR (:NEW."REQUESTED_EPOCH" IS NOT NULL AND :OLD."REQUESTED_EPOCH" IS NULL) OR (:NEW."REQUESTED_EPOCH" IS NOT NULL AND :OLD."REQUESTED_EPOCH" IS NOT NULL AND :NEW."REQUESTED_EPOCH"<>:OLD."REQUESTED_EPOCH")) OR
       ((:NEW."MEMBERSHIP_AUTHORITY" IS NULL AND :OLD."MEMBERSHIP_AUTHORITY" IS NOT NULL) OR (:NEW."MEMBERSHIP_AUTHORITY" IS NOT NULL AND :OLD."MEMBERSHIP_AUTHORITY" IS NULL) OR (:NEW."MEMBERSHIP_AUTHORITY" IS NOT NULL AND :OLD."MEMBERSHIP_AUTHORITY" IS NOT NULL AND :NEW."MEMBERSHIP_AUTHORITY"<>:OLD."MEMBERSHIP_AUTHORITY")) OR
       ((:NEW."INPUT_INVENTORY" IS NULL AND :OLD."INPUT_INVENTORY" IS NOT NULL) OR (:NEW."INPUT_INVENTORY" IS NOT NULL AND :OLD."INPUT_INVENTORY" IS NULL) OR (:NEW."INPUT_INVENTORY" IS NOT NULL AND :OLD."INPUT_INVENTORY" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."INPUT_INVENTORY",:OLD."INPUT_INVENTORY")<>0)) OR
       ((:NEW."RESUME_CHECKPOINT_ID" IS NULL AND :OLD."RESUME_CHECKPOINT_ID" IS NOT NULL) OR (:NEW."RESUME_CHECKPOINT_ID" IS NOT NULL AND :OLD."RESUME_CHECKPOINT_ID" IS NULL) OR (:NEW."RESUME_CHECKPOINT_ID" IS NOT NULL AND :OLD."RESUME_CHECKPOINT_ID" IS NOT NULL AND :NEW."RESUME_CHECKPOINT_ID"<>:OLD."RESUME_CHECKPOINT_ID")) OR
       ((:NEW."CREATED_AT" IS NULL AND :OLD."CREATED_AT" IS NOT NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NOT NULL AND :NEW."CREATED_AT"<>:OLD."CREATED_AT")) OR
       (:OLD."OUTCOME" IS NOT NULL AND (((:NEW."OUTCOME" IS NULL AND :OLD."OUTCOME" IS NOT NULL) OR (:NEW."OUTCOME" IS NOT NULL AND :OLD."OUTCOME" IS NULL) OR (:NEW."OUTCOME" IS NOT NULL AND :OLD."OUTCOME" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."OUTCOME",:OLD."OUTCOME")<>0)) OR ((:NEW."OUTCOME_SHA256" IS NULL AND :OLD."OUTCOME_SHA256" IS NOT NULL) OR (:NEW."OUTCOME_SHA256" IS NOT NULL AND :OLD."OUTCOME_SHA256" IS NULL) OR (:NEW."OUTCOME_SHA256" IS NOT NULL AND :OLD."OUTCOME_SHA256" IS NOT NULL AND :NEW."OUTCOME_SHA256"<>:OLD."OUTCOME_SHA256")))) OR
       (:OLD."STOP_TOKEN_HASH" IS NOT NULL AND (((:NEW."STOP_TOKEN_HASH" IS NULL AND :OLD."STOP_TOKEN_HASH" IS NOT NULL) OR (:NEW."STOP_TOKEN_HASH" IS NOT NULL AND :OLD."STOP_TOKEN_HASH" IS NULL) OR (:NEW."STOP_TOKEN_HASH" IS NOT NULL AND :OLD."STOP_TOKEN_HASH" IS NOT NULL AND :NEW."STOP_TOKEN_HASH"<>:OLD."STOP_TOKEN_HASH")))) OR
       (:OLD."PHYSICAL_STOP_RECEIPT" IS NOT NULL AND (((:NEW."PHYSICAL_STOP_RECEIPT" IS NULL AND :OLD."PHYSICAL_STOP_RECEIPT" IS NOT NULL) OR (:NEW."PHYSICAL_STOP_RECEIPT" IS NOT NULL AND :OLD."PHYSICAL_STOP_RECEIPT" IS NULL) OR (:NEW."PHYSICAL_STOP_RECEIPT" IS NOT NULL AND :OLD."PHYSICAL_STOP_RECEIPT" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."PHYSICAL_STOP_RECEIPT",:OLD."PHYSICAL_STOP_RECEIPT")<>0)) OR ((:NEW."PHYSICAL_STOP_SHA256" IS NULL AND :OLD."PHYSICAL_STOP_SHA256" IS NOT NULL) OR (:NEW."PHYSICAL_STOP_SHA256" IS NOT NULL AND :OLD."PHYSICAL_STOP_SHA256" IS NULL) OR (:NEW."PHYSICAL_STOP_SHA256" IS NOT NULL AND :OLD."PHYSICAL_STOP_SHA256" IS NOT NULL AND :NEW."PHYSICAL_STOP_SHA256"<>:OLD."PHYSICAL_STOP_SHA256")) OR ((:NEW."PHYSICAL_STOPPED_AT" IS NULL AND :OLD."PHYSICAL_STOPPED_AT" IS NOT NULL) OR (:NEW."PHYSICAL_STOPPED_AT" IS NOT NULL AND :OLD."PHYSICAL_STOPPED_AT" IS NULL) OR (:NEW."PHYSICAL_STOPPED_AT" IS NOT NULL AND :OLD."PHYSICAL_STOPPED_AT" IS NOT NULL AND :NEW."PHYSICAL_STOPPED_AT"<>:OLD."PHYSICAL_STOPPED_AT")))) THEN
        RAISE_APPLICATION_ERROR(-20061,'TRAINING_ATTEMPT_INPUTS_IMMUTABLE');
    END IF;
END;`)
	b.trigger("LC_TRAINING_CHECKPOINT_IMMUTABLE_UPDATE", "TRAINING_CHECKPOINTS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'TRAINING_ACCEPTED_METADATA_IMMUTABLE');
END;`)
	b.trigger("LC_TRAINING_CHECKPOINT_IMMUTABLE_DELETE", "TRAINING_CHECKPOINTS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'TRAINING_ACCEPTED_METADATA_IMMUTABLE');
END;`)
	b.trigger("LC_TRAINING_ARTIFACT_IMMUTABLE_UPDATE", "TRAINING_ARTIFACTS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'TRAINING_ACCEPTED_METADATA_IMMUTABLE');
END;`)
	b.trigger("LC_TRAINING_ARTIFACT_IMMUTABLE_DELETE", "TRAINING_ARTIFACTS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'TRAINING_ACCEPTED_METADATA_IMMUTABLE');
END;`)
	b.trigger("LC_TRAINING_EVENT_IMMUTABLE_UPDATE", "TRAINING_EVENTS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'TRAINING_ACCEPTED_METADATA_IMMUTABLE');
END;`)
	b.trigger("LC_TRAINING_EVENT_IMMUTABLE_DELETE", "TRAINING_EVENTS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'TRAINING_ACCEPTED_METADATA_IMMUTABLE');
END;`)
	b.trigger("LC_TRAINING_CHUNK_IMMUTABLE_UPDATE", "TRAINING_UPLOAD_CHUNKS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'TRAINING_ACCEPTED_METADATA_IMMUTABLE');
END;`)
	b.trigger("LC_TRAINING_CHUNK_IMMUTABLE_DELETE", "TRAINING_UPLOAD_CHUNKS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'TRAINING_ACCEPTED_METADATA_IMMUTABLE');
END;`)
	b.trigger("LC_TRAINING_UPLOAD_INPUTS_IMMUTABLE", "TRAINING_UPLOADS", "UPDATE", "BEFORE", `BEGIN
    IF ((:NEW."ID" IS NULL AND :OLD."ID" IS NOT NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NOT NULL AND :NEW."ID"<>:OLD."ID")) OR
       ((:NEW."TENANT_ID" IS NULL AND :OLD."TENANT_ID" IS NOT NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NOT NULL AND :NEW."TENANT_ID"<>:OLD."TENANT_ID")) OR
       ((:NEW."WORKSPACE_ID" IS NULL AND :OLD."WORKSPACE_ID" IS NOT NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NOT NULL AND :NEW."WORKSPACE_ID"<>:OLD."WORKSPACE_ID")) OR
       ((:NEW."JOB_ID" IS NULL AND :OLD."JOB_ID" IS NOT NULL) OR (:NEW."JOB_ID" IS NOT NULL AND :OLD."JOB_ID" IS NULL) OR (:NEW."JOB_ID" IS NOT NULL AND :OLD."JOB_ID" IS NOT NULL AND :NEW."JOB_ID"<>:OLD."JOB_ID")) OR
       ((:NEW."ATTEMPT_ID" IS NULL AND :OLD."ATTEMPT_ID" IS NOT NULL) OR (:NEW."ATTEMPT_ID" IS NOT NULL AND :OLD."ATTEMPT_ID" IS NULL) OR (:NEW."ATTEMPT_ID" IS NOT NULL AND :OLD."ATTEMPT_ID" IS NOT NULL AND :NEW."ATTEMPT_ID"<>:OLD."ATTEMPT_ID")) OR
       ((:NEW."KIND" IS NULL AND :OLD."KIND" IS NOT NULL) OR (:NEW."KIND" IS NOT NULL AND :OLD."KIND" IS NULL) OR (:NEW."KIND" IS NOT NULL AND :OLD."KIND" IS NOT NULL AND :NEW."KIND"<>:OLD."KIND")) OR
       ((:NEW."GLOBAL_STEP" IS NULL AND :OLD."GLOBAL_STEP" IS NOT NULL) OR (:NEW."GLOBAL_STEP" IS NOT NULL AND :OLD."GLOBAL_STEP" IS NULL) OR (:NEW."GLOBAL_STEP" IS NOT NULL AND :OLD."GLOBAL_STEP" IS NOT NULL AND :NEW."GLOBAL_STEP"<>:OLD."GLOBAL_STEP")) OR
       ((:NEW."MANIFEST_SHA256" IS NULL AND :OLD."MANIFEST_SHA256" IS NOT NULL) OR (:NEW."MANIFEST_SHA256" IS NOT NULL AND :OLD."MANIFEST_SHA256" IS NULL) OR (:NEW."MANIFEST_SHA256" IS NOT NULL AND :OLD."MANIFEST_SHA256" IS NOT NULL AND :NEW."MANIFEST_SHA256"<>:OLD."MANIFEST_SHA256")) OR
       ((:NEW."MANIFEST" IS NULL AND :OLD."MANIFEST" IS NOT NULL) OR (:NEW."MANIFEST" IS NOT NULL AND :OLD."MANIFEST" IS NULL) OR (:NEW."MANIFEST" IS NOT NULL AND :OLD."MANIFEST" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."MANIFEST",:OLD."MANIFEST")<>0)) OR
       ((:NEW."MANIFEST_BYTES" IS NULL AND :OLD."MANIFEST_BYTES" IS NOT NULL) OR (:NEW."MANIFEST_BYTES" IS NOT NULL AND :OLD."MANIFEST_BYTES" IS NULL) OR (:NEW."MANIFEST_BYTES" IS NOT NULL AND :OLD."MANIFEST_BYTES" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."MANIFEST_BYTES",:OLD."MANIFEST_BYTES")<>0)) OR
       ((:NEW."CREATED_AT" IS NULL AND :OLD."CREATED_AT" IS NOT NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NOT NULL AND :NEW."CREATED_AT"<>:OLD."CREATED_AT")) OR
       (:OLD."STATE"='accepted' AND ((:NEW."STATE" IS NULL AND :OLD."STATE" IS NOT NULL) OR (:NEW."STATE" IS NOT NULL AND :OLD."STATE" IS NULL) OR (:NEW."STATE" IS NOT NULL AND :OLD."STATE" IS NOT NULL AND :NEW."STATE"<>:OLD."STATE"))) THEN
        RAISE_APPLICATION_ERROR(-20061,'TRAINING_UPLOAD_INPUTS_IMMUTABLE');
    END IF;
END;`)
	b.trigger("LC_TRAINING_RECOVERY_INPUTS_IMMUTABLE", "TRAINING_RECOVERIES", "UPDATE", "BEFORE", `BEGIN
    IF ((:NEW."ID" IS NULL AND :OLD."ID" IS NOT NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NOT NULL AND :NEW."ID"<>:OLD."ID")) OR
       ((:NEW."TENANT_ID" IS NULL AND :OLD."TENANT_ID" IS NOT NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NOT NULL AND :NEW."TENANT_ID"<>:OLD."TENANT_ID")) OR
       ((:NEW."WORKSPACE_ID" IS NULL AND :OLD."WORKSPACE_ID" IS NOT NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NOT NULL AND :NEW."WORKSPACE_ID"<>:OLD."WORKSPACE_ID")) OR
       ((:NEW."JOB_ID" IS NULL AND :OLD."JOB_ID" IS NOT NULL) OR (:NEW."JOB_ID" IS NOT NULL AND :OLD."JOB_ID" IS NULL) OR (:NEW."JOB_ID" IS NOT NULL AND :OLD."JOB_ID" IS NOT NULL AND :NEW."JOB_ID"<>:OLD."JOB_ID")) OR
       ((:NEW."PREVIOUS_ATTEMPT_ID" IS NULL AND :OLD."PREVIOUS_ATTEMPT_ID" IS NOT NULL) OR (:NEW."PREVIOUS_ATTEMPT_ID" IS NOT NULL AND :OLD."PREVIOUS_ATTEMPT_ID" IS NULL) OR (:NEW."PREVIOUS_ATTEMPT_ID" IS NOT NULL AND :OLD."PREVIOUS_ATTEMPT_ID" IS NOT NULL AND :NEW."PREVIOUS_ATTEMPT_ID"<>:OLD."PREVIOUS_ATTEMPT_ID")) OR
       ((:NEW."CHECKPOINT_ID" IS NULL AND :OLD."CHECKPOINT_ID" IS NOT NULL) OR (:NEW."CHECKPOINT_ID" IS NOT NULL AND :OLD."CHECKPOINT_ID" IS NULL) OR (:NEW."CHECKPOINT_ID" IS NOT NULL AND :OLD."CHECKPOINT_ID" IS NOT NULL AND :NEW."CHECKPOINT_ID"<>:OLD."CHECKPOINT_ID")) OR
       ((:NEW."PREVIOUS_NODE_ID" IS NULL AND :OLD."PREVIOUS_NODE_ID" IS NOT NULL) OR (:NEW."PREVIOUS_NODE_ID" IS NOT NULL AND :OLD."PREVIOUS_NODE_ID" IS NULL) OR (:NEW."PREVIOUS_NODE_ID" IS NOT NULL AND :OLD."PREVIOUS_NODE_ID" IS NOT NULL AND :NEW."PREVIOUS_NODE_ID"<>:OLD."PREVIOUS_NODE_ID")) OR
       ((:NEW."NEXT_NODE_ID" IS NULL AND :OLD."NEXT_NODE_ID" IS NOT NULL) OR (:NEW."NEXT_NODE_ID" IS NOT NULL AND :OLD."NEXT_NODE_ID" IS NULL) OR (:NEW."NEXT_NODE_ID" IS NOT NULL AND :OLD."NEXT_NODE_ID" IS NOT NULL AND :NEW."NEXT_NODE_ID"<>:OLD."NEXT_NODE_ID")) OR
       ((:NEW."MODE" IS NULL AND :OLD."MODE" IS NOT NULL) OR (:NEW."MODE" IS NOT NULL AND :OLD."MODE" IS NULL) OR (:NEW."MODE" IS NOT NULL AND :OLD."MODE" IS NOT NULL AND :NEW."MODE"<>:OLD."MODE")) OR
       ((:NEW."REQUESTED_BY" IS NULL AND :OLD."REQUESTED_BY" IS NOT NULL) OR (:NEW."REQUESTED_BY" IS NOT NULL AND :OLD."REQUESTED_BY" IS NULL) OR (:NEW."REQUESTED_BY" IS NOT NULL AND :OLD."REQUESTED_BY" IS NOT NULL AND :NEW."REQUESTED_BY"<>:OLD."REQUESTED_BY")) OR
       ((:NEW."REASON" IS NULL AND :OLD."REASON" IS NOT NULL) OR (:NEW."REASON" IS NOT NULL AND :OLD."REASON" IS NULL) OR (:NEW."REASON" IS NOT NULL AND :OLD."REASON" IS NOT NULL AND :NEW."REASON"<>:OLD."REASON")) OR
       ((:NEW."CREATION_KEY" IS NULL AND :OLD."CREATION_KEY" IS NOT NULL) OR (:NEW."CREATION_KEY" IS NOT NULL AND :OLD."CREATION_KEY" IS NULL) OR (:NEW."CREATION_KEY" IS NOT NULL AND :OLD."CREATION_KEY" IS NOT NULL AND :NEW."CREATION_KEY"<>:OLD."CREATION_KEY")) OR
       ((:NEW."CREATION_FINGERPRINT" IS NULL AND :OLD."CREATION_FINGERPRINT" IS NOT NULL) OR (:NEW."CREATION_FINGERPRINT" IS NOT NULL AND :OLD."CREATION_FINGERPRINT" IS NULL) OR (:NEW."CREATION_FINGERPRINT" IS NOT NULL AND :OLD."CREATION_FINGERPRINT" IS NOT NULL AND :NEW."CREATION_FINGERPRINT"<>:OLD."CREATION_FINGERPRINT")) OR
       ((:NEW."CREATED_AT" IS NULL AND :OLD."CREATED_AT" IS NOT NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NOT NULL AND :NEW."CREATED_AT"<>:OLD."CREATED_AT")) OR
       (:OLD."NEXT_ATTEMPT_ID" IS NOT NULL AND ((:NEW."NEXT_ATTEMPT_ID" IS NULL AND :OLD."NEXT_ATTEMPT_ID" IS NOT NULL) OR (:NEW."NEXT_ATTEMPT_ID" IS NOT NULL AND :OLD."NEXT_ATTEMPT_ID" IS NULL) OR (:NEW."NEXT_ATTEMPT_ID" IS NOT NULL AND :OLD."NEXT_ATTEMPT_ID" IS NOT NULL AND :NEW."NEXT_ATTEMPT_ID"<>:OLD."NEXT_ATTEMPT_ID"))) THEN
        RAISE_APPLICATION_ERROR(-20061,'TRAINING_RECOVERY_INPUTS_IMMUTABLE');
    END IF;
END;`)
	b.trigger("LC_TRAINING_RECOVERY_IMMUTABLE_DELETE", "TRAINING_RECOVERIES", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'TRAINING_RECOVERY_INPUTS_IMMUTABLE');
END;`)
	b.trigger("LC_TRAINING_JOBS_RETAINED", "TRAINING_JOBS", "DELETE", "BEFORE", `BEGIN RAISE_APPLICATION_ERROR(-20061,'MODEL_LIFECYCLE_RETAINED'); END;`)
	b.trigger("LC_TRAINING_ATTEMPTS_RETAINED", "TRAINING_ATTEMPTS", "DELETE", "BEFORE", `BEGIN RAISE_APPLICATION_ERROR(-20061,'MODEL_LIFECYCLE_RETAINED'); END;`)
	b.trigger("LC_TRAINING_UPLOADS_RETAINED", "TRAINING_UPLOADS", "DELETE", "BEFORE", `BEGIN RAISE_APPLICATION_ERROR(-20061,'MODEL_LIFECYCLE_RETAINED'); END;`)
	return b.fragment
}

func modelPreparationsFragment(current map[string]table) schemaFragment {
	b := lifecycleStart(current, "model_preparations_v2", "foundation_v1", "training_v2", "datasets_v2", "models_v2", "nodes_components_v2")
	// 000086_model_holdouts.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_HOLDOUTS", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "DATASET_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "DATASET_VERSION_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "NAME", Type: "VARCHAR", Width: 100, Characters: true, Nullable: false, Default: ""},
		column{Name: "MANIFEST_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "EFFECTIVE_SOURCE_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "ROW_MAPPING_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "SOURCE_BYTES", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "ROW_COUNT", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "LABEL_COUNTS", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "RECIPE_ID", Type: "VARCHAR", Width: 80, Characters: true, Nullable: false, Default: ""},
		column{Name: "RECIPE_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "INSTRUCTION_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "FINGERPRINT_ALGORITHM", Type: "VARCHAR", Width: 100, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATED_BY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "AUDIENCE", Type: "VARCHAR", Width: 8, Characters: true, Nullable: false, Default: ""},
		column{Name: "REASON", Type: "VARCHAR", Width: 500, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATION_KEY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "CREATION_FINGERPRINT", Type: "VARBINARY", Width: 32, Characters: false, Nullable: false, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		pk("LC_MODEL_HOLDOUTS_PK", "ID"),
		check("LC_MODEL_HOLDOUTS_C01", `CHAR_LENGTH(TRIM("NAME"))>0`),
		check("LC_MODEL_HOLDOUTS_C02", `REGEXP_LIKE("MANIFEST_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_HOLDOUTS_C03", `REGEXP_LIKE("EFFECTIVE_SOURCE_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_HOLDOUTS_C04", `REGEXP_LIKE("ROW_MAPPING_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_HOLDOUTS_C05", `"SOURCE_BYTES" BETWEEN 1 AND 67108864`),
		check("LC_MODEL_HOLDOUTS_C06", `"ROW_COUNT" BETWEEN 3 AND 256`),
		check("LC_MODEL_HOLDOUTS_C07", `JSON_TYPEOF("LABEL_COUNTS") IS NOT NULL AND JSON_TYPEOF("LABEL_COUNTS")='object' AND OCTET_LENGTH("LABEL_COUNTS")<=1024`),
		check("LC_MODEL_HOLDOUTS_C08", `CHAR_LENGTH(TRIM("RECIPE_ID"))>0`),
		check("LC_MODEL_HOLDOUTS_C09", `REGEXP_LIKE("RECIPE_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_HOLDOUTS_C10", `REGEXP_LIKE("INSTRUCTION_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_HOLDOUTS_C11", `CHAR_LENGTH(TRIM("FINGERPRINT_ALGORITHM"))>0`),
		check("LC_MODEL_HOLDOUTS_C12", `"AUDIENCE" IN ('portal','admin')`),
		check("LC_MODEL_HOLDOUTS_C13", `CHAR_LENGTH(TRIM("REASON"))>0`),
		check("LC_MODEL_HOLDOUTS_C14", `OCTET_LENGTH("CREATION_FINGERPRINT")=32`),
		unique("LC_MODEL_HOLDOUTS_U01", "TENANT_ID", "WORKSPACE_ID", "ID"),
		unique("MODEL_HOLDOUT_VERSION_UNIQUE", "TENANT_ID", "WORKSPACE_ID", "DATASET_VERSION_ID", "RECIPE_ID"),
		unique("MODEL_HOLDOUT_CREATION_UNIQUE", "TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "AUDIENCE", "CREATION_KEY"),
		check("MODEL_HOLDOUT_LABEL_SHAPE", `CASE WHEN
 JSON_TYPE(JSON_EXTRACT("LABEL_COUNTS",'$.account'))='INTEGER'
 AND JSON_TYPE(JSON_EXTRACT("LABEL_COUNTS",'$.billing'))='INTEGER'
 AND JSON_TYPE(JSON_EXTRACT("LABEL_COUNTS",'$.technical'))='INTEGER'
 AND JSON_VALUE("LABEL_COUNTS",'$.account' RETURNING NUMBER) BETWEEN 1 AND 256
 AND JSON_VALUE("LABEL_COUNTS",'$.billing' RETURNING NUMBER) BETWEEN 1 AND 256
 AND JSON_VALUE("LABEL_COUNTS",'$.technical' RETURNING NUMBER) BETWEEN 1 AND 256
 AND CAST("LABEL_COUNTS" AS JSON)=JSON_BUILD_OBJECT(
   'account',JSON_VALUE("LABEL_COUNTS",'$.account' RETURNING NUMBER),
   'billing',JSON_VALUE("LABEL_COUNTS",'$.billing' RETURNING NUMBER),
   'technical',JSON_VALUE("LABEL_COUNTS",'$.technical' RETURNING NUMBER))
 AND JSON_VALUE("LABEL_COUNTS",'$.account' RETURNING NUMBER)
   + JSON_VALUE("LABEL_COUNTS",'$.billing' RETURNING NUMBER)
   + JSON_VALUE("LABEL_COUNTS",'$.technical' RETURNING NUMBER)="ROW_COUNT"
 THEN 1 ELSE 0 END=1`)))
	// 000087_model_holdout_sources.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_HOLDOUT_SOURCES", []column{column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "HOLDOUT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "SOURCE_SHA256", Type: "VARBINARY", Width: 32, Characters: false, Nullable: false, Default: ""}},
		check("LC_MODEL_HOLDOUT_SOURCES_C01", `OCTET_LENGTH("SOURCE_SHA256")=32`),
		pk("LC_MODEL_HOLDOUT_SOURCES_PK", "TENANT_ID", "WORKSPACE_ID", "HOLDOUT_ID", "SOURCE_SHA256")))
	// 000088_model_holdout_prompts.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_HOLDOUT_PROMPTS", []column{column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "HOLDOUT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "PROMPT_SHA256", Type: "VARBINARY", Width: 32, Characters: false, Nullable: false, Default: ""}},
		check("LC_MODEL_HOLDOUT_PROMPTS_C01", `OCTET_LENGTH("PROMPT_SHA256")=32`),
		pk("LC_MODEL_HOLDOUT_PROMPTS_PK", "TENANT_ID", "WORKSPACE_ID", "HOLDOUT_ID", "PROMPT_SHA256")))
	// 000095_model_preparations.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_PREPARATIONS", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "NODE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "CURRENT_NODE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "MODEL_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TRAINING_JOB_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TRAINING_ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TRAINING_ARTIFACT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "HELDOUT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "GPU_UUID", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "INPUT_SNAPSHOT", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "INPUT_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "CONFIGURATION", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "CONFIGURATION_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "EXECUTION_RECIPE", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "CREATED_BY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "REQUESTED_BY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "CREATION_AUDIENCE", Type: "VARCHAR", Width: 8, Characters: true, Nullable: false, Default: ""},
		column{Name: "REQUESTED_AUDIENCE", Type: "VARCHAR", Width: 8, Characters: true, Nullable: false, Default: ""},
		column{Name: "REQUESTED_EPOCH", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "MEMBERSHIP_AUTHORITY", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "REASON", Type: "VARCHAR", Width: 500, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATION_KEY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "CREATION_FINGERPRINT", Type: "VARBINARY", Width: 32, Characters: false, Nullable: false, Default: ""},
		column{Name: "STATE", Type: "VARCHAR", Width: 24, Characters: true, Nullable: false, Default: ""},
		column{Name: "REVISION", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: "1"},
		column{Name: "WAIT_CODE", Type: "VARCHAR", Width: 80, Characters: true, Nullable: true, Default: ""},
		column{Name: "ERROR_CODE", Type: "VARCHAR", Width: 80, Characters: true, Nullable: true, Default: ""},
		column{Name: "CANCEL_REQUESTED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "CANCEL_REQUESTED_BY", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "NEXT_DISPATCH_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		column{Name: "UPDATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		column{Name: "FINISHED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "ATTEMPT_NUMBER", Type: "INTEGER", Width: 0, Characters: false, Nullable: false, Default: "0"},
		column{Name: "CURRENT_ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "PENDING_RECOVERY_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		uuid("REVIEW_ID", true),
		uuid("DERIVED_MODEL_ID", true)},
		pk("LC_MODEL_PREPARATIONS_PK", "ID"),
		check("LC_MODEL_PREPARATIONS_C01", `REGEXP_LIKE("GPU_UUID",'^GPU-[a-f0-9-]{36}$','c')`),
		check("LC_MODEL_PREPARATIONS_C02", `JSON_TYPEOF("INPUT_SNAPSHOT") IS NOT NULL AND JSON_TYPEOF("INPUT_SNAPSHOT")='object' AND OCTET_LENGTH("INPUT_SNAPSHOT")<=65536`),
		check("LC_MODEL_PREPARATIONS_C03", `REGEXP_LIKE("INPUT_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_PREPARATIONS_C04", `JSON_TYPEOF("CONFIGURATION") IS NOT NULL AND JSON_TYPEOF("CONFIGURATION")='object' AND OCTET_LENGTH("CONFIGURATION")<=16384`),
		check("LC_MODEL_PREPARATIONS_C05", `REGEXP_LIKE("CONFIGURATION_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_PREPARATIONS_C06", `JSON_TYPEOF("EXECUTION_RECIPE") IS NOT NULL AND JSON_TYPEOF("EXECUTION_RECIPE")='object' AND OCTET_LENGTH("EXECUTION_RECIPE")<=32768`),
		check("LC_MODEL_PREPARATIONS_C07", `"CREATION_AUDIENCE" IN ('portal','admin')`),
		check("LC_MODEL_PREPARATIONS_C08", `"REQUESTED_AUDIENCE" IN ('portal','admin')`),
		check("LC_MODEL_PREPARATIONS_C09", `"REQUESTED_EPOCH">=0`),
		check("LC_MODEL_PREPARATIONS_C10", `CHAR_LENGTH(TRIM("REASON"))>0`),
		check("LC_MODEL_PREPARATIONS_C11", `OCTET_LENGTH("CREATION_FINGERPRINT")=32`),
		check("LC_MODEL_PREPARATIONS_C12", `"STATE" IN ('queued','preparing','running','cancelling','recovery_required','quality_failed','awaiting_review','approved','rejected','cancelled','failed')`),
		check("LC_MODEL_PREPARATIONS_C13", `"REVISION" BETWEEN 1 AND 9007199254740991`),
		check("LC_MODEL_PREPARATIONS_C14", `"WAIT_CODE" IS NULL OR REGEXP_LIKE("WAIT_CODE",'^[A-Z][A-Z0-9_]{1,79}$','c')`),
		check("LC_MODEL_PREPARATIONS_C15", `"ERROR_CODE" IS NULL OR REGEXP_LIKE("ERROR_CODE",'^[A-Z][A-Z0-9_]{1,79}$','c')`),
		unique("LC_MODEL_PREPARATIONS_U01", "TENANT_ID", "WORKSPACE_ID", "ID"),
		unique("MODEL_PREPARATION_CREATION_UNIQUE", "TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "CREATION_AUDIENCE", "CREATION_KEY"),
		check("LC_MODEL_PREPARATIONS_C16", `"REQUESTED_AUDIENCE"='admin' OR "MEMBERSHIP_AUTHORITY" IS NOT NULL`),
		check("LC_MODEL_PREPARATIONS_C17", `((CASE WHEN "CANCEL_REQUESTED_AT" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "CANCEL_REQUESTED_BY" IS NULL THEN 1 ELSE 0 END))`),
		check("LC_MODEL_PREPARATIONS_C18", `"ATTEMPT_NUMBER" BETWEEN 0 AND 1000`),
		check("LC_MODEL_PREPARATIONS_C19", `((CASE WHEN "ATTEMPT_NUMBER"=0 THEN 1 ELSE 0 END)=(CASE WHEN "CURRENT_ATTEMPT_ID" IS NULL THEN 1 ELSE 0 END))`),
		check("LC_MODEL_PREPARATIONS_C20", `"PENDING_RECOVERY_ID" IS NULL OR "ATTEMPT_NUMBER">0`),
		check("LC_PREPARATION_REVIEW_PRESENCE", `("STATE"='approved' AND "REVIEW_ID" IS NOT NULL AND "DERIVED_MODEL_ID" IS NOT NULL) OR ("STATE"='rejected' AND "REVIEW_ID" IS NOT NULL AND "DERIVED_MODEL_ID" IS NULL) OR ("STATE" NOT IN('approved','rejected') AND "REVIEW_ID" IS NULL AND "DERIVED_MODEL_ID" IS NULL)`)))
	// 000096_model_preparation_attempts.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_PREPARATION_ATTEMPTS", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "PREPARATION_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "NODE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TASK_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ATTEMPT_NUMBER", Type: "INTEGER", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "STATE", Type: "VARCHAR", Width: 24, Characters: true, Nullable: false, Default: ""},
		column{Name: "REQUESTED_BY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "REQUESTED_AUDIENCE", Type: "VARCHAR", Width: 8, Characters: true, Nullable: false, Default: ""},
		column{Name: "REQUESTED_EPOCH", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "MEMBERSHIP_AUTHORITY", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "COMMAND_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "LAST_EVENT_SEQUENCE", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: "0"},
		column{Name: "STOP_TOKEN_HASH", Type: "VARBINARY", Width: 32, Characters: false, Nullable: true, Default: ""},
		column{Name: "OUTCOME_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: true, Default: ""},
		column{Name: "OUTCOME", Type: "CLOB", Width: 0, Characters: false, Nullable: true, Default: ""},
		column{Name: "PHYSICAL_STOP_RECEIPT", Type: "CLOB", Width: 0, Characters: false, Nullable: true, Default: ""},
		column{Name: "PHYSICAL_STOP_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: true, Default: ""},
		column{Name: "PHYSICAL_STOPPED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "ERROR_CODE", Type: "VARCHAR", Width: 80, Characters: true, Nullable: true, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		column{Name: "STARTED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "FINISHED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "UPDATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		column{Name: "EVENT_BYTES", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: "0"},
		column{Name: "OUTCOME_BYTES", Type: "BLOB", Width: 0, Characters: false, Nullable: true, Default: ""},
		lifecycleVirtual("UNCLEARED_PREPARATION_ID", "CHAR", 36, `CASE WHEN "STATE" IN('pending','preparing','running','stopping','recovery_required') AND "PHYSICAL_STOPPED_AT" IS NULL THEN "PREPARATION_ID" ELSE NULL END`)},
		pk("LC_MODEL_PREPARATION_ATTEMPTS_PK", "ID"),
		unique("LC_MODEL_PREPARATION_ATTEMPTS_U01", "TASK_ID"),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C01", `"ATTEMPT_NUMBER" BETWEEN 1 AND 1000`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C02", `"STATE" IN ('pending','preparing','running','stopping','recovery_required','stopped','prepared','cancelled','failed')`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C03", `"REQUESTED_AUDIENCE" IN ('portal','admin')`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C04", `"REQUESTED_EPOCH">=0`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C05", `REGEXP_LIKE("COMMAND_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C06", `"LAST_EVENT_SEQUENCE" BETWEEN 0 AND 1024`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C07", `"STOP_TOKEN_HASH" IS NULL OR OCTET_LENGTH("STOP_TOKEN_HASH")=32`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C08", `"OUTCOME_SHA256" IS NULL OR REGEXP_LIKE("OUTCOME_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C09", `"OUTCOME" IS NULL OR JSON_TYPEOF("OUTCOME") IS NOT NULL AND JSON_TYPEOF("OUTCOME")='object' AND OCTET_LENGTH("OUTCOME")<=8388608`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C10", `"PHYSICAL_STOP_RECEIPT" IS NULL OR JSON_TYPEOF("PHYSICAL_STOP_RECEIPT") IS NOT NULL AND JSON_TYPEOF("PHYSICAL_STOP_RECEIPT")='object' AND OCTET_LENGTH("PHYSICAL_STOP_RECEIPT")<=32768`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C11", `"PHYSICAL_STOP_SHA256" IS NULL OR REGEXP_LIKE("PHYSICAL_STOP_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C12", `"ERROR_CODE" IS NULL OR REGEXP_LIKE("ERROR_CODE",'^[A-Z][A-Z0-9_]{1,79}$','c')`),
		unique("LC_MODEL_PREPARATION_ATTEMPTS_U02", "TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ID"),
		unique("LC_MODEL_PREPARATION_ATTEMPTS_U03", "PREPARATION_ID", "ATTEMPT_NUMBER"),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C13", `"REQUESTED_AUDIENCE"='admin' OR "MEMBERSHIP_AUTHORITY" IS NOT NULL`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C14", `((CASE WHEN "OUTCOME" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "OUTCOME_SHA256" IS NULL THEN 1 ELSE 0 END))`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C15", `((CASE WHEN "PHYSICAL_STOP_RECEIPT" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "PHYSICAL_STOP_SHA256" IS NULL THEN 1 ELSE 0 END))`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C16", `((CASE WHEN "PHYSICAL_STOP_RECEIPT" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "PHYSICAL_STOPPED_AT" IS NULL THEN 1 ELSE 0 END))`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C17", `"EVENT_BYTES" BETWEEN 0 AND 16777216`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C18", `"OUTCOME_BYTES" IS NULL OR OCTET_LENGTH("OUTCOME_BYTES") BETWEEN 2 AND 8388608`),
		check("LC_MODEL_PREPARATION_ATTEMPTS_C19", `((CASE WHEN "OUTCOME" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "OUTCOME_BYTES" IS NULL THEN 1 ELSE 0 END))`),
		unique("LC_MODEL_PREPARATION_ATTEMPTS_U04", "UNCLEARED_PREPARATION_ID")))
	// 000097_model_preparation_events.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_PREPARATION_EVENTS", []column{column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "PREPARATION_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "SEQUENCE", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "KIND", Type: "VARCHAR", Width: 24, Characters: true, Nullable: false, Default: ""},
		column{Name: "PAYLOAD", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "CANONICAL_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		check("LC_MODEL_PREPARATION_EVENTS_C01", `"SEQUENCE" BETWEEN 1 AND 1024`),
		check("LC_MODEL_PREPARATION_EVENTS_C02", `"KIND" IN ('phase','evaluation_progress')`),
		check("LC_MODEL_PREPARATION_EVENTS_C03", `JSON_TYPEOF("PAYLOAD") IS NOT NULL AND JSON_TYPEOF("PAYLOAD")='object' AND OCTET_LENGTH("PAYLOAD")<=16384`),
		check("LC_MODEL_PREPARATION_EVENTS_C04", `REGEXP_LIKE("CANONICAL_SHA256",'^[a-f0-9]{64}$','c')`),
		pk("LC_MODEL_PREPARATION_EVENTS_PK", "ATTEMPT_ID", "SEQUENCE")))
	// 000098_model_preparation_recoveries.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_PREPARATION_RECOVERIES", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "PREPARATION_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "PREVIOUS_ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "PREVIOUS_NODE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "NEXT_NODE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "NEXT_ATTEMPT_NUMBER", Type: "INTEGER", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "REQUESTED_BY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "REQUESTED_AUDIENCE", Type: "VARCHAR", Width: 8, Characters: true, Nullable: false, Default: ""},
		column{Name: "REQUESTED_EPOCH", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "MEMBERSHIP_AUTHORITY", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "REASON", Type: "VARCHAR", Width: 500, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATION_KEY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "CREATION_FINGERPRINT", Type: "VARBINARY", Width: 32, Characters: false, Nullable: false, Default: ""},
		column{Name: "DISPATCHED_ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "APPLIED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		pk("LC_MODEL_PREPARATION_RECOVERIES_PK", "ID"),
		check("LC_MODEL_PREPARATION_RECOVERIES_C01", `"NEXT_ATTEMPT_NUMBER" BETWEEN 2 AND 1000`),
		check("LC_MODEL_PREPARATION_RECOVERIES_C02", `"REQUESTED_AUDIENCE" IN ('portal','admin')`),
		check("LC_MODEL_PREPARATION_RECOVERIES_C03", `"REQUESTED_EPOCH">=0`),
		check("LC_MODEL_PREPARATION_RECOVERIES_C04", `CHAR_LENGTH(TRIM("REASON"))>0`),
		check("LC_MODEL_PREPARATION_RECOVERIES_C05", `OCTET_LENGTH("CREATION_FINGERPRINT")=32`),
		unique("LC_MODEL_PREPARATION_RECOVERIES_U01", "TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ID"),
		unique("LC_MODEL_PREPARATION_RECOVERIES_U02", "PREPARATION_ID", "REQUESTED_BY", "REQUESTED_AUDIENCE", "CREATION_KEY"),
		check("LC_MODEL_PREPARATION_RECOVERIES_C06", `"REQUESTED_AUDIENCE"='admin' OR "MEMBERSHIP_AUTHORITY" IS NOT NULL`),
		check("LC_MODEL_PREPARATION_RECOVERIES_C07", `((CASE WHEN "DISPATCHED_ATTEMPT_ID" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "APPLIED_AT" IS NULL THEN 1 ELSE 0 END))`)))
	// 000099_model_preparation_uploads.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_PREPARATION_UPLOADS", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "PREPARATION_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "MANIFEST_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "MANIFEST", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "MANIFEST_BYTES", Type: "BLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "DESCRIPTOR", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "STATE", Type: "VARCHAR", Width: 16, Characters: true, Nullable: false, Default: "'uploading'"},
		column{Name: "ACTIVE_CHUNK_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "ACTIVE_ASSET", Type: "VARCHAR", Width: 100, Characters: true, Nullable: true, Default: ""},
		column{Name: "ACTIVE_OFFSET", Type: "BIGINT", Width: 0, Characters: false, Nullable: true, Default: ""},
		column{Name: "ACTIVE_EXPIRES_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "ERROR_CODE", Type: "VARCHAR", Width: 80, Characters: true, Nullable: true, Default: ""},
		column{Name: "ACCEPTED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: true, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"},
		column{Name: "UPDATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		pk("LC_MODEL_PREPARATION_UPLOADS_PK", "ID"),
		unique("LC_MODEL_PREPARATION_UPLOADS_U01", "ATTEMPT_ID"),
		check("LC_MODEL_PREPARATION_UPLOADS_C01", `REGEXP_LIKE("MANIFEST_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_PREPARATION_UPLOADS_C02", `JSON_TYPEOF("MANIFEST") IS NOT NULL AND JSON_TYPEOF("MANIFEST")='object' AND OCTET_LENGTH("MANIFEST")<=8388608`),
		check("LC_MODEL_PREPARATION_UPLOADS_C03", `OCTET_LENGTH("MANIFEST_BYTES") BETWEEN 2 AND 8388608`),
		check("LC_MODEL_PREPARATION_UPLOADS_C04", `JSON_TYPEOF("DESCRIPTOR") IS NOT NULL AND JSON_TYPEOF("DESCRIPTOR")='object' AND OCTET_LENGTH("DESCRIPTOR")<=32768`),
		check("LC_MODEL_PREPARATION_UPLOADS_C05", `"STATE" IN ('uploading','verifying','accepted','failed')`),
		check("LC_MODEL_PREPARATION_UPLOADS_C06", `"ACTIVE_OFFSET" IS NULL OR "ACTIVE_OFFSET" BETWEEN 0 AND 2147483648`),
		check("LC_MODEL_PREPARATION_UPLOADS_C07", `"ERROR_CODE" IS NULL OR REGEXP_LIKE("ERROR_CODE",'^[A-Z][A-Z0-9_]{1,79}$','c')`),
		unique("LC_MODEL_PREPARATION_UPLOADS_U02", "TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID", "ID"),
		check("LC_MODEL_PREPARATION_UPLOADS_C08", `((CASE WHEN "ACTIVE_CHUNK_ID" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "ACTIVE_ASSET" IS NULL THEN 1 ELSE 0 END))`),
		check("LC_MODEL_PREPARATION_UPLOADS_C09", `((CASE WHEN "ACTIVE_CHUNK_ID" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "ACTIVE_OFFSET" IS NULL THEN 1 ELSE 0 END))`),
		check("LC_MODEL_PREPARATION_UPLOADS_C10", `((CASE WHEN "ACTIVE_CHUNK_ID" IS NULL THEN 1 ELSE 0 END)=(CASE WHEN "ACTIVE_EXPIRES_AT" IS NULL THEN 1 ELSE 0 END))`),
		check("LC_MODEL_PREPARATION_UPLOADS_C11", `((CASE WHEN "STATE"='accepted' THEN 1 ELSE 0 END)=(CASE WHEN "ACCEPTED_AT" IS NOT NULL THEN 1 ELSE 0 END))`)))
	// 000100_model_preparation_upload_chunks.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_PREPARATION_UPLOAD_CHUNKS", []column{column{Name: "UPLOAD_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ASSET", Type: "VARCHAR", Width: 100, Characters: true, Nullable: false, Default: ""},
		column{Name: "BYTE_OFFSET", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "SIZE_BYTES", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		check("LC_MODEL_PREPARATION_UPLOAD_CHUNKS_C01", `REGEXP_LIKE("ASSET",'^[A-Za-z0-9_.-]{1,100}$','c') AND "ASSET" NOT IN ('.','..')`),
		check("LC_MODEL_PREPARATION_UPLOAD_CHUNKS_C02", `"BYTE_OFFSET" BETWEEN 0 AND 2147483648 AND MOD("BYTE_OFFSET",8388608)=0`),
		check("LC_MODEL_PREPARATION_UPLOAD_CHUNKS_C03", `"SIZE_BYTES" BETWEEN 1 AND 8388608`),
		check("LC_MODEL_PREPARATION_UPLOAD_CHUNKS_C04", `REGEXP_LIKE("SHA256",'^[a-f0-9]{64}$','c')`),
		pk("LC_MODEL_PREPARATION_UPLOAD_CHUNKS_PK", "UPLOAD_ID", "ASSET", "BYTE_OFFSET")))
	// 000101_model_preparation_artifacts.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_PREPARATION_ARTIFACTS", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "PREPARATION_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "UPLOAD_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "MANIFEST_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "DESCRIPTOR", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "REPORT", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "REPORT_BYTES", Type: "BLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "STORAGE_PATH", Type: "VARCHAR", Width: 512, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		pk("LC_MODEL_PREPARATION_ARTIFACTS_PK", "ID"),
		unique("LC_MODEL_PREPARATION_ARTIFACTS_U01", "ATTEMPT_ID"),
		unique("LC_MODEL_PREPARATION_ARTIFACTS_U02", "UPLOAD_ID"),
		check("LC_MODEL_PREPARATION_ARTIFACTS_C01", `REGEXP_LIKE("MANIFEST_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_PREPARATION_ARTIFACTS_C02", `JSON_TYPEOF("DESCRIPTOR") IS NOT NULL AND JSON_TYPEOF("DESCRIPTOR")='object' AND OCTET_LENGTH("DESCRIPTOR")<=32768`),
		check("LC_MODEL_PREPARATION_ARTIFACTS_C03", `JSON_TYPEOF("REPORT") IS NOT NULL AND JSON_TYPEOF("REPORT")='object' AND OCTET_LENGTH("REPORT")<=8388608`),
		check("LC_MODEL_PREPARATION_ARTIFACTS_C04", `OCTET_LENGTH("REPORT_BYTES") BETWEEN 2 AND 8388608`),
		unique("LC_MODEL_PREPARATION_ARTIFACTS_U03", "TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID", "ID"),
		check("LC_MODEL_PREPARATION_ARTIFACTS_C05", `"ID"="UPLOAD_ID"`),
		check("LC_MODEL_PREPARATION_ARTIFACTS_C06", `"STORAGE_PATH"=('artifacts/'||"PREPARATION_ID"||'/'||"ATTEMPT_ID"||'/'||"ID")`)))
	// 000119_model_preparation_reviews.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_PREPARATION_REVIEWS", []column{column{Name: "ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "PREPARATION_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ARTIFACT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "REPORT_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "EXPECTED_REVISION", Type: "BIGINT", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "DECISION", Type: "VARCHAR", Width: 16, Characters: true, Nullable: false, Default: ""},
		column{Name: "MODEL_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: true, Default: ""},
		column{Name: "MODEL_NAME", Type: "VARCHAR", Width: 100, Characters: true, Nullable: false, Default: ""},
		column{Name: "REVIEWED_BY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "AUDIENCE", Type: "VARCHAR", Width: 16, Characters: true, Nullable: false, Default: ""},
		column{Name: "REASON", Type: "VARCHAR", Width: 500, Characters: true, Nullable: false, Default: ""},
		column{Name: "IDEMPOTENCY_KEY", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "FINGERPRINT", Type: "VARBINARY", Width: 32, Characters: false, Nullable: false, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		pk("LC_MODEL_PREPARATION_REVIEWS_PK", "ID"),
		check("LC_MODEL_PREPARATION_REVIEWS_C01", `REGEXP_LIKE("REPORT_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_PREPARATION_REVIEWS_C02", `"EXPECTED_REVISION" BETWEEN 1 AND 9007199254740990`),
		check("LC_MODEL_PREPARATION_REVIEWS_C03", `"DECISION" IN('approve','reject')`),
		check("LC_MODEL_PREPARATION_REVIEWS_C04", `"AUDIENCE"='admin'`),
		check("LC_MODEL_PREPARATION_REVIEWS_C05", `CHAR_LENGTH(TRIM("REASON"))>0`),
		check("LC_MODEL_PREPARATION_REVIEWS_C06", `OCTET_LENGTH("FINGERPRINT")=32`),
		check("MODEL_REVIEW_APPROVAL_IDENTITY_REQUIRED", `("DECISION"='approve' AND "MODEL_ID" IS NOT NULL AND "MODEL_ID"="ARTIFACT_ID" AND CHAR_LENGTH(TRIM("MODEL_NAME"))>0) OR ("DECISION"='reject' AND "MODEL_ID" IS NULL AND "MODEL_NAME"='')`),
		unique("LC_MODEL_PREPARATION_REVIEWS_U01", "TENANT_ID", "WORKSPACE_ID", "ID"),
		unique("LC_MODEL_PREPARATION_REVIEWS_U02", "TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID"),
		unique("LC_MODEL_PREPARATION_REVIEWS_U03", "TENANT_ID", "WORKSPACE_ID", "REVIEWED_BY", "IDEMPOTENCY_KEY")))
	// 000120_model_derivations.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_DERIVATIONS", []column{column{Name: "MODEL_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "PREPARATION_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ATTEMPT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "ARTIFACT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "BASE_MODEL_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "REVIEW_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "CATALOG_DIGEST", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "CATALOG", Type: "CLOB", Width: 0, Characters: false, Nullable: false, Default: ""},
		column{Name: "REPORT_SHA256", Type: "VARCHAR", Width: 64, Characters: true, Nullable: false, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		pk("LC_MODEL_DERIVATIONS_PK", "MODEL_ID"),
		unique("LC_MODEL_DERIVATIONS_U01", "ARTIFACT_ID"),
		unique("LC_MODEL_DERIVATIONS_U02", "REVIEW_ID"),
		check("LC_MODEL_DERIVATIONS_C01", `REGEXP_LIKE("CATALOG_DIGEST",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_DERIVATIONS_C02", `JSON_TYPEOF("CATALOG") IS NOT NULL AND JSON_TYPEOF("CATALOG")='object' AND OCTET_LENGTH("CATALOG")<=16384`),
		check("LC_MODEL_DERIVATIONS_C03", `REGEXP_LIKE("REPORT_SHA256",'^[a-f0-9]{64}$','c')`),
		check("LC_MODEL_DERIVATIONS_C04", `"MODEL_ID"="ARTIFACT_ID" AND "MODEL_ID"<>"BASE_MODEL_ID"`),
		unique("LC_MODEL_DERIVATIONS_U03", "TENANT_ID", "WORKSPACE_ID", "MODEL_ID")))
	// 000122_model_publications.sql defines the retained semantic bounds, not executable DM SQL.
	b.create(nativeTable("MODEL_PUBLICATIONS", []column{column{Name: "MODEL_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "TENANT_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "WORKSPACE_ID", Type: "CHAR", Width: 36, Characters: false, Nullable: false, Default: ""},
		column{Name: "CREATED_AT", Type: "TIMESTAMP", Width: 6, Characters: false, Nullable: false, Default: "CURRENT_TIMESTAMP"}},
		pk("LC_MODEL_PUBLICATIONS_PK", "MODEL_ID")))
	b.foreign("MODEL_HOLDOUTS", "LC_MODEL_HOLDOUTS_F01", "WORKSPACES", []string{"TENANT_ID", "WORKSPACE_ID"}, []string{"TENANT_ID", "ID"})
	b.foreign("MODEL_HOLDOUTS", "LC_MODEL_HOLDOUTS_F02", "DATASET_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "DATASET_VERSION_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "DATASET_ID", "ID"})
	b.foreign("MODEL_HOLDOUTS", "LC_MODEL_HOLDOUTS_F03", "USERS", []string{"CREATED_BY"}, []string{"ID"})
	b.index("MODEL_HOLDOUT_PAGE_IDX", "MODEL_HOLDOUTS", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, []bool{false, false, true, true})
	b.foreign("MODEL_HOLDOUT_SOURCES", "LC_MODEL_HOLDOUT_SOURCES_F01", "MODEL_HOLDOUTS", []string{"TENANT_ID", "WORKSPACE_ID", "HOLDOUT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.index("MODEL_HOLDOUT_SOURCE_RESERVATION_IDX", "MODEL_HOLDOUT_SOURCES", []string{"TENANT_ID", "WORKSPACE_ID", "SOURCE_SHA256"}, []bool{false, false, false})
	b.foreign("MODEL_HOLDOUT_PROMPTS", "LC_MODEL_HOLDOUT_PROMPTS_F01", "MODEL_HOLDOUTS", []string{"TENANT_ID", "WORKSPACE_ID", "HOLDOUT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.index("MODEL_HOLDOUT_PROMPT_RESERVATION_IDX", "MODEL_HOLDOUT_PROMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "PROMPT_SHA256"}, []bool{false, false, false})
	b.foreign("MODEL_PREPARATIONS", "LC_MODEL_PREPARATIONS_F01", "WORKSPACES", []string{"TENANT_ID", "WORKSPACE_ID"}, []string{"TENANT_ID", "ID"})
	b.foreign("MODEL_PREPARATIONS", "LC_MODEL_PREPARATIONS_F02", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATIONS", "LC_MODEL_PREPARATIONS_F03", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "CURRENT_NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATIONS", "LC_MODEL_PREPARATIONS_F04", "MODEL_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "MODEL_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATIONS", "LC_MODEL_PREPARATIONS_F05", "TRAINING_JOBS", []string{"TENANT_ID", "WORKSPACE_ID", "TRAINING_JOB_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATIONS", "LC_MODEL_PREPARATIONS_F06", "TRAINING_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "TRAINING_JOB_ID", "TRAINING_ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.foreign("MODEL_PREPARATIONS", "LC_MODEL_PREPARATIONS_F07", "TRAINING_ARTIFACTS", []string{"TENANT_ID", "WORKSPACE_ID", "TRAINING_JOB_ID", "TRAINING_ARTIFACT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "JOB_ID", "ID"})
	b.foreign("MODEL_PREPARATIONS", "LC_MODEL_PREPARATIONS_F08", "MODEL_HOLDOUTS", []string{"TENANT_ID", "WORKSPACE_ID", "HELDOUT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATIONS", "LC_MODEL_PREPARATIONS_F09", "USERS", []string{"CREATED_BY"}, []string{"ID"})
	b.foreign("MODEL_PREPARATIONS", "LC_MODEL_PREPARATIONS_F10", "USERS", []string{"REQUESTED_BY"}, []string{"ID"})
	b.foreign("MODEL_PREPARATIONS", "LC_MODEL_PREPARATIONS_F11", "USERS", []string{"CANCEL_REQUESTED_BY"}, []string{"ID"})
	b.foreign("MODEL_PREPARATIONS", "MODEL_PREPARATION_CURRENT_ATTEMPT_FK", "MODEL_PREPARATION_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "ID", "CURRENT_ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ID"})
	b.foreign("MODEL_PREPARATIONS", "MODEL_PREPARATION_PENDING_RECOVERY_FK", "MODEL_PREPARATION_RECOVERIES", []string{"TENANT_ID", "WORKSPACE_ID", "ID", "PENDING_RECOVERY_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ID"})
	b.foreign("MODEL_PREPARATIONS", "MODEL_PREPARATION_REVIEW_SCOPE", "MODEL_PREPARATION_REVIEWS", []string{"TENANT_ID", "WORKSPACE_ID", "REVIEW_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATIONS", "MODEL_PREPARATION_DERIVED_SCOPE", "MODEL_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "DERIVED_MODEL_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.index("MODEL_PREPARATION_SCOPE_PAGE_IDX", "MODEL_PREPARATIONS", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "ID"}, []bool{false, false, true, true})
	b.index("MODEL_PREPARATION_QUEUE_IDX", "MODEL_PREPARATIONS", []string{"STATE", "NEXT_DISPATCH_AT", "CREATED_AT", "ID"}, []bool{false, false, false, false})
	b.foreign("MODEL_PREPARATION_ATTEMPTS", "LC_MODEL_PREPARATION_ATTEMPTS_F01", "MODEL_PREPARATIONS", []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATION_ATTEMPTS", "LC_MODEL_PREPARATION_ATTEMPTS_F02", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATION_ATTEMPTS", "LC_MODEL_PREPARATION_ATTEMPTS_F03", "NODE_TASKS", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "ID"})
	b.foreign("MODEL_PREPARATION_ATTEMPTS", "LC_MODEL_PREPARATION_ATTEMPTS_F04", "USERS", []string{"REQUESTED_BY"}, []string{"ID"})
	b.index("MODEL_PREPARATION_ATTEMPT_PAGE_IDX", "MODEL_PREPARATION_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "CREATED_AT", "ID"}, []bool{false, false, false, true, true})
	b.foreign("MODEL_PREPARATION_EVENTS", "LC_MODEL_PREPARATION_EVENTS_F01", "MODEL_PREPARATION_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ID"})
	b.foreign("MODEL_PREPARATION_RECOVERIES", "LC_MODEL_PREPARATION_RECOVERIES_F01", "MODEL_PREPARATIONS", []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATION_RECOVERIES", "LC_MODEL_PREPARATION_RECOVERIES_F02", "MODEL_PREPARATION_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "PREVIOUS_ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ID"})
	b.foreign("MODEL_PREPARATION_RECOVERIES", "LC_MODEL_PREPARATION_RECOVERIES_F03", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "PREVIOUS_NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATION_RECOVERIES", "LC_MODEL_PREPARATION_RECOVERIES_F04", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "NEXT_NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATION_RECOVERIES", "LC_MODEL_PREPARATION_RECOVERIES_F05", "MODEL_PREPARATION_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "DISPATCHED_ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ID"})
	b.foreign("MODEL_PREPARATION_RECOVERIES", "LC_MODEL_PREPARATION_RECOVERIES_F06", "USERS", []string{"REQUESTED_BY"}, []string{"ID"})
	b.foreign("MODEL_PREPARATION_UPLOADS", "LC_MODEL_PREPARATION_UPLOADS_F01", "MODEL_PREPARATION_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ID"})
	b.foreign("MODEL_PREPARATION_UPLOAD_CHUNKS", "LC_MODEL_PREPARATION_UPLOAD_CHUNKS_F01", "MODEL_PREPARATION_UPLOADS", []string{"UPLOAD_ID"}, []string{"ID"})
	b.foreign("MODEL_PREPARATION_ARTIFACTS", "LC_MODEL_PREPARATION_ARTIFACTS_F01", "MODEL_PREPARATION_ATTEMPTS", []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ID"})
	b.foreign("MODEL_PREPARATION_ARTIFACTS", "LC_MODEL_PREPARATION_ARTIFACTS_F02", "MODEL_PREPARATION_UPLOADS", []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID", "UPLOAD_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID", "ID"})
	b.foreign("MODEL_PREPARATION_REVIEWS", "LC_MODEL_PREPARATION_REVIEWS_F01", "MODEL_PREPARATION_ARTIFACTS", []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID", "ARTIFACT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID", "ID"})
	b.foreign("MODEL_PREPARATION_REVIEWS", "LC_MODEL_PREPARATION_REVIEWS_F02", "MODEL_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "MODEL_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_PREPARATION_REVIEWS", "LC_MODEL_PREPARATION_REVIEWS_F03", "USERS", []string{"REVIEWED_BY"}, []string{"ID"})
	b.foreign("MODEL_DERIVATIONS", "LC_MODEL_DERIVATIONS_F01", "MODEL_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "MODEL_ID", "CATALOG_DIGEST"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID", "CATALOG_DIGEST"})
	b.foreign("MODEL_DERIVATIONS", "LC_MODEL_DERIVATIONS_F02", "MODEL_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "BASE_MODEL_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_DERIVATIONS", "LC_MODEL_DERIVATIONS_F03", "MODEL_PREPARATION_REVIEWS", []string{"TENANT_ID", "WORKSPACE_ID", "REVIEW_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"})
	b.foreign("MODEL_DERIVATIONS", "LC_MODEL_DERIVATIONS_F04", "MODEL_PREPARATION_ARTIFACTS", []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID", "ARTIFACT_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "PREPARATION_ID", "ATTEMPT_ID", "ID"})
	b.foreign("MODEL_PUBLICATIONS", "LC_MODEL_PUBLICATIONS_F01", "MODEL_DERIVATIONS", []string{"TENANT_ID", "WORKSPACE_ID", "MODEL_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "MODEL_ID"})
	lifecycleGPUPreparation(b)
	b.trigger("LC_MODEL_HOLDOUT_IMMUTABLE_UPDATE", "MODEL_HOLDOUTS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_HOLDOUT_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_HOLDOUT_IMMUTABLE_DELETE", "MODEL_HOLDOUTS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_HOLDOUT_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_HOLDOUT_SOURCE_IMMUTABLE_UPDATE", "MODEL_HOLDOUT_SOURCES", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_HOLDOUT_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_HOLDOUT_SOURCE_IMMUTABLE_DELETE", "MODEL_HOLDOUT_SOURCES", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_HOLDOUT_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_HOLDOUT_PROMPT_IMMUTABLE_UPDATE", "MODEL_HOLDOUT_PROMPTS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_HOLDOUT_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_HOLDOUT_PROMPT_IMMUTABLE_DELETE", "MODEL_HOLDOUT_PROMPTS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_HOLDOUT_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PREPARATION_IMMUTABLE_UPDATE", "MODEL_PREPARATIONS", "UPDATE", "BEFORE", `BEGIN
    IF ((:NEW."ID" IS NULL AND :OLD."ID" IS NOT NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NOT NULL AND :NEW."ID"<>:OLD."ID")) OR ((:NEW."TENANT_ID" IS NULL AND :OLD."TENANT_ID" IS NOT NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NOT NULL AND :NEW."TENANT_ID"<>:OLD."TENANT_ID")) OR ((:NEW."WORKSPACE_ID" IS NULL AND :OLD."WORKSPACE_ID" IS NOT NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NOT NULL AND :NEW."WORKSPACE_ID"<>:OLD."WORKSPACE_ID")) OR ((:NEW."NODE_ID" IS NULL AND :OLD."NODE_ID" IS NOT NULL) OR (:NEW."NODE_ID" IS NOT NULL AND :OLD."NODE_ID" IS NULL) OR (:NEW."NODE_ID" IS NOT NULL AND :OLD."NODE_ID" IS NOT NULL AND :NEW."NODE_ID"<>:OLD."NODE_ID")) OR ((:NEW."MODEL_ID" IS NULL AND :OLD."MODEL_ID" IS NOT NULL) OR (:NEW."MODEL_ID" IS NOT NULL AND :OLD."MODEL_ID" IS NULL) OR (:NEW."MODEL_ID" IS NOT NULL AND :OLD."MODEL_ID" IS NOT NULL AND :NEW."MODEL_ID"<>:OLD."MODEL_ID")) OR ((:NEW."TRAINING_JOB_ID" IS NULL AND :OLD."TRAINING_JOB_ID" IS NOT NULL) OR (:NEW."TRAINING_JOB_ID" IS NOT NULL AND :OLD."TRAINING_JOB_ID" IS NULL) OR (:NEW."TRAINING_JOB_ID" IS NOT NULL AND :OLD."TRAINING_JOB_ID" IS NOT NULL AND :NEW."TRAINING_JOB_ID"<>:OLD."TRAINING_JOB_ID")) OR ((:NEW."TRAINING_ATTEMPT_ID" IS NULL AND :OLD."TRAINING_ATTEMPT_ID" IS NOT NULL) OR (:NEW."TRAINING_ATTEMPT_ID" IS NOT NULL AND :OLD."TRAINING_ATTEMPT_ID" IS NULL) OR (:NEW."TRAINING_ATTEMPT_ID" IS NOT NULL AND :OLD."TRAINING_ATTEMPT_ID" IS NOT NULL AND :NEW."TRAINING_ATTEMPT_ID"<>:OLD."TRAINING_ATTEMPT_ID")) OR ((:NEW."TRAINING_ARTIFACT_ID" IS NULL AND :OLD."TRAINING_ARTIFACT_ID" IS NOT NULL) OR (:NEW."TRAINING_ARTIFACT_ID" IS NOT NULL AND :OLD."TRAINING_ARTIFACT_ID" IS NULL) OR (:NEW."TRAINING_ARTIFACT_ID" IS NOT NULL AND :OLD."TRAINING_ARTIFACT_ID" IS NOT NULL AND :NEW."TRAINING_ARTIFACT_ID"<>:OLD."TRAINING_ARTIFACT_ID")) OR ((:NEW."HELDOUT_ID" IS NULL AND :OLD."HELDOUT_ID" IS NOT NULL) OR (:NEW."HELDOUT_ID" IS NOT NULL AND :OLD."HELDOUT_ID" IS NULL) OR (:NEW."HELDOUT_ID" IS NOT NULL AND :OLD."HELDOUT_ID" IS NOT NULL AND :NEW."HELDOUT_ID"<>:OLD."HELDOUT_ID")) OR ((:NEW."GPU_UUID" IS NULL AND :OLD."GPU_UUID" IS NOT NULL) OR (:NEW."GPU_UUID" IS NOT NULL AND :OLD."GPU_UUID" IS NULL) OR (:NEW."GPU_UUID" IS NOT NULL AND :OLD."GPU_UUID" IS NOT NULL AND :NEW."GPU_UUID"<>:OLD."GPU_UUID")) OR ((:NEW."INPUT_SNAPSHOT" IS NULL AND :OLD."INPUT_SNAPSHOT" IS NOT NULL) OR (:NEW."INPUT_SNAPSHOT" IS NOT NULL AND :OLD."INPUT_SNAPSHOT" IS NULL) OR (:NEW."INPUT_SNAPSHOT" IS NOT NULL AND :OLD."INPUT_SNAPSHOT" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."INPUT_SNAPSHOT",:OLD."INPUT_SNAPSHOT")<>0)) OR ((:NEW."INPUT_SHA256" IS NULL AND :OLD."INPUT_SHA256" IS NOT NULL) OR (:NEW."INPUT_SHA256" IS NOT NULL AND :OLD."INPUT_SHA256" IS NULL) OR (:NEW."INPUT_SHA256" IS NOT NULL AND :OLD."INPUT_SHA256" IS NOT NULL AND :NEW."INPUT_SHA256"<>:OLD."INPUT_SHA256")) OR ((:NEW."CONFIGURATION" IS NULL AND :OLD."CONFIGURATION" IS NOT NULL) OR (:NEW."CONFIGURATION" IS NOT NULL AND :OLD."CONFIGURATION" IS NULL) OR (:NEW."CONFIGURATION" IS NOT NULL AND :OLD."CONFIGURATION" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."CONFIGURATION",:OLD."CONFIGURATION")<>0)) OR ((:NEW."CONFIGURATION_SHA256" IS NULL AND :OLD."CONFIGURATION_SHA256" IS NOT NULL) OR (:NEW."CONFIGURATION_SHA256" IS NOT NULL AND :OLD."CONFIGURATION_SHA256" IS NULL) OR (:NEW."CONFIGURATION_SHA256" IS NOT NULL AND :OLD."CONFIGURATION_SHA256" IS NOT NULL AND :NEW."CONFIGURATION_SHA256"<>:OLD."CONFIGURATION_SHA256")) OR ((:NEW."EXECUTION_RECIPE" IS NULL AND :OLD."EXECUTION_RECIPE" IS NOT NULL) OR (:NEW."EXECUTION_RECIPE" IS NOT NULL AND :OLD."EXECUTION_RECIPE" IS NULL) OR (:NEW."EXECUTION_RECIPE" IS NOT NULL AND :OLD."EXECUTION_RECIPE" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."EXECUTION_RECIPE",:OLD."EXECUTION_RECIPE")<>0)) OR ((:NEW."CREATED_BY" IS NULL AND :OLD."CREATED_BY" IS NOT NULL) OR (:NEW."CREATED_BY" IS NOT NULL AND :OLD."CREATED_BY" IS NULL) OR (:NEW."CREATED_BY" IS NOT NULL AND :OLD."CREATED_BY" IS NOT NULL AND :NEW."CREATED_BY"<>:OLD."CREATED_BY")) OR ((:NEW."CREATION_AUDIENCE" IS NULL AND :OLD."CREATION_AUDIENCE" IS NOT NULL) OR (:NEW."CREATION_AUDIENCE" IS NOT NULL AND :OLD."CREATION_AUDIENCE" IS NULL) OR (:NEW."CREATION_AUDIENCE" IS NOT NULL AND :OLD."CREATION_AUDIENCE" IS NOT NULL AND :NEW."CREATION_AUDIENCE"<>:OLD."CREATION_AUDIENCE")) OR ((:NEW."REASON" IS NULL AND :OLD."REASON" IS NOT NULL) OR (:NEW."REASON" IS NOT NULL AND :OLD."REASON" IS NULL) OR (:NEW."REASON" IS NOT NULL AND :OLD."REASON" IS NOT NULL AND :NEW."REASON"<>:OLD."REASON")) OR ((:NEW."CREATION_KEY" IS NULL AND :OLD."CREATION_KEY" IS NOT NULL) OR (:NEW."CREATION_KEY" IS NOT NULL AND :OLD."CREATION_KEY" IS NULL) OR (:NEW."CREATION_KEY" IS NOT NULL AND :OLD."CREATION_KEY" IS NOT NULL AND :NEW."CREATION_KEY"<>:OLD."CREATION_KEY")) OR ((:NEW."CREATION_FINGERPRINT" IS NULL AND :OLD."CREATION_FINGERPRINT" IS NOT NULL) OR (:NEW."CREATION_FINGERPRINT" IS NOT NULL AND :OLD."CREATION_FINGERPRINT" IS NULL) OR (:NEW."CREATION_FINGERPRINT" IS NOT NULL AND :OLD."CREATION_FINGERPRINT" IS NOT NULL AND :NEW."CREATION_FINGERPRINT"<>:OLD."CREATION_FINGERPRINT")) OR ((:NEW."CREATED_AT" IS NULL AND :OLD."CREATED_AT" IS NOT NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NOT NULL AND :NEW."CREATED_AT"<>:OLD."CREATED_AT")) THEN
        RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_INPUTS_IMMUTABLE');
    END IF;
END;`)
	b.trigger("LC_MODEL_PREPARATION_IMMUTABLE_DELETE", "MODEL_PREPARATIONS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PREPARATION_ATTEMPT_IMMUTABLE_UPDATE", "MODEL_PREPARATION_ATTEMPTS", "UPDATE", "BEFORE", `BEGIN
    IF ((:NEW."ID" IS NULL AND :OLD."ID" IS NOT NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NOT NULL AND :NEW."ID"<>:OLD."ID")) OR ((:NEW."TENANT_ID" IS NULL AND :OLD."TENANT_ID" IS NOT NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NOT NULL AND :NEW."TENANT_ID"<>:OLD."TENANT_ID")) OR ((:NEW."WORKSPACE_ID" IS NULL AND :OLD."WORKSPACE_ID" IS NOT NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NOT NULL AND :NEW."WORKSPACE_ID"<>:OLD."WORKSPACE_ID")) OR ((:NEW."PREPARATION_ID" IS NULL AND :OLD."PREPARATION_ID" IS NOT NULL) OR (:NEW."PREPARATION_ID" IS NOT NULL AND :OLD."PREPARATION_ID" IS NULL) OR (:NEW."PREPARATION_ID" IS NOT NULL AND :OLD."PREPARATION_ID" IS NOT NULL AND :NEW."PREPARATION_ID"<>:OLD."PREPARATION_ID")) OR ((:NEW."NODE_ID" IS NULL AND :OLD."NODE_ID" IS NOT NULL) OR (:NEW."NODE_ID" IS NOT NULL AND :OLD."NODE_ID" IS NULL) OR (:NEW."NODE_ID" IS NOT NULL AND :OLD."NODE_ID" IS NOT NULL AND :NEW."NODE_ID"<>:OLD."NODE_ID")) OR ((:NEW."TASK_ID" IS NULL AND :OLD."TASK_ID" IS NOT NULL) OR (:NEW."TASK_ID" IS NOT NULL AND :OLD."TASK_ID" IS NULL) OR (:NEW."TASK_ID" IS NOT NULL AND :OLD."TASK_ID" IS NOT NULL AND :NEW."TASK_ID"<>:OLD."TASK_ID")) OR ((:NEW."ATTEMPT_NUMBER" IS NULL AND :OLD."ATTEMPT_NUMBER" IS NOT NULL) OR (:NEW."ATTEMPT_NUMBER" IS NOT NULL AND :OLD."ATTEMPT_NUMBER" IS NULL) OR (:NEW."ATTEMPT_NUMBER" IS NOT NULL AND :OLD."ATTEMPT_NUMBER" IS NOT NULL AND :NEW."ATTEMPT_NUMBER"<>:OLD."ATTEMPT_NUMBER")) OR ((:NEW."REQUESTED_BY" IS NULL AND :OLD."REQUESTED_BY" IS NOT NULL) OR (:NEW."REQUESTED_BY" IS NOT NULL AND :OLD."REQUESTED_BY" IS NULL) OR (:NEW."REQUESTED_BY" IS NOT NULL AND :OLD."REQUESTED_BY" IS NOT NULL AND :NEW."REQUESTED_BY"<>:OLD."REQUESTED_BY")) OR ((:NEW."REQUESTED_AUDIENCE" IS NULL AND :OLD."REQUESTED_AUDIENCE" IS NOT NULL) OR (:NEW."REQUESTED_AUDIENCE" IS NOT NULL AND :OLD."REQUESTED_AUDIENCE" IS NULL) OR (:NEW."REQUESTED_AUDIENCE" IS NOT NULL AND :OLD."REQUESTED_AUDIENCE" IS NOT NULL AND :NEW."REQUESTED_AUDIENCE"<>:OLD."REQUESTED_AUDIENCE")) OR ((:NEW."REQUESTED_EPOCH" IS NULL AND :OLD."REQUESTED_EPOCH" IS NOT NULL) OR (:NEW."REQUESTED_EPOCH" IS NOT NULL AND :OLD."REQUESTED_EPOCH" IS NULL) OR (:NEW."REQUESTED_EPOCH" IS NOT NULL AND :OLD."REQUESTED_EPOCH" IS NOT NULL AND :NEW."REQUESTED_EPOCH"<>:OLD."REQUESTED_EPOCH")) OR ((:NEW."MEMBERSHIP_AUTHORITY" IS NULL AND :OLD."MEMBERSHIP_AUTHORITY" IS NOT NULL) OR (:NEW."MEMBERSHIP_AUTHORITY" IS NOT NULL AND :OLD."MEMBERSHIP_AUTHORITY" IS NULL) OR (:NEW."MEMBERSHIP_AUTHORITY" IS NOT NULL AND :OLD."MEMBERSHIP_AUTHORITY" IS NOT NULL AND :NEW."MEMBERSHIP_AUTHORITY"<>:OLD."MEMBERSHIP_AUTHORITY")) OR ((:NEW."COMMAND_SHA256" IS NULL AND :OLD."COMMAND_SHA256" IS NOT NULL) OR (:NEW."COMMAND_SHA256" IS NOT NULL AND :OLD."COMMAND_SHA256" IS NULL) OR (:NEW."COMMAND_SHA256" IS NOT NULL AND :OLD."COMMAND_SHA256" IS NOT NULL AND :NEW."COMMAND_SHA256"<>:OLD."COMMAND_SHA256")) OR ((:NEW."CREATED_AT" IS NULL AND :OLD."CREATED_AT" IS NOT NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NOT NULL AND :NEW."CREATED_AT"<>:OLD."CREATED_AT")) THEN
        RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_ATTEMPT_INPUTS_IMMUTABLE');
    END IF;
    IF :OLD."STOP_TOKEN_HASH" IS NOT NULL AND (((:NEW."STOP_TOKEN_HASH" IS NULL AND :OLD."STOP_TOKEN_HASH" IS NOT NULL) OR (:NEW."STOP_TOKEN_HASH" IS NOT NULL AND :OLD."STOP_TOKEN_HASH" IS NULL) OR (:NEW."STOP_TOKEN_HASH" IS NOT NULL AND :OLD."STOP_TOKEN_HASH" IS NOT NULL AND :NEW."STOP_TOKEN_HASH"<>:OLD."STOP_TOKEN_HASH"))) THEN
        RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_STOP_AUTHORITY_IMMUTABLE');
    END IF;
    IF :OLD."PHYSICAL_STOPPED_AT" IS NOT NULL AND (((:NEW."PHYSICAL_STOPPED_AT" IS NULL AND :OLD."PHYSICAL_STOPPED_AT" IS NOT NULL) OR (:NEW."PHYSICAL_STOPPED_AT" IS NOT NULL AND :OLD."PHYSICAL_STOPPED_AT" IS NULL) OR (:NEW."PHYSICAL_STOPPED_AT" IS NOT NULL AND :OLD."PHYSICAL_STOPPED_AT" IS NOT NULL AND :NEW."PHYSICAL_STOPPED_AT"<>:OLD."PHYSICAL_STOPPED_AT")) OR ((:NEW."PHYSICAL_STOP_RECEIPT" IS NULL AND :OLD."PHYSICAL_STOP_RECEIPT" IS NOT NULL) OR (:NEW."PHYSICAL_STOP_RECEIPT" IS NOT NULL AND :OLD."PHYSICAL_STOP_RECEIPT" IS NULL) OR (:NEW."PHYSICAL_STOP_RECEIPT" IS NOT NULL AND :OLD."PHYSICAL_STOP_RECEIPT" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."PHYSICAL_STOP_RECEIPT",:OLD."PHYSICAL_STOP_RECEIPT")<>0)) OR ((:NEW."PHYSICAL_STOP_SHA256" IS NULL AND :OLD."PHYSICAL_STOP_SHA256" IS NOT NULL) OR (:NEW."PHYSICAL_STOP_SHA256" IS NOT NULL AND :OLD."PHYSICAL_STOP_SHA256" IS NULL) OR (:NEW."PHYSICAL_STOP_SHA256" IS NOT NULL AND :OLD."PHYSICAL_STOP_SHA256" IS NOT NULL AND :NEW."PHYSICAL_STOP_SHA256"<>:OLD."PHYSICAL_STOP_SHA256"))) THEN
        RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_STOP_PROOF_IMMUTABLE');
    END IF;
    IF :OLD."OUTCOME_SHA256" IS NOT NULL AND (((:NEW."OUTCOME" IS NULL AND :OLD."OUTCOME" IS NOT NULL) OR (:NEW."OUTCOME" IS NOT NULL AND :OLD."OUTCOME" IS NULL) OR (:NEW."OUTCOME" IS NOT NULL AND :OLD."OUTCOME" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."OUTCOME",:OLD."OUTCOME")<>0)) OR ((:NEW."OUTCOME_BYTES" IS NULL AND :OLD."OUTCOME_BYTES" IS NOT NULL) OR (:NEW."OUTCOME_BYTES" IS NOT NULL AND :OLD."OUTCOME_BYTES" IS NULL) OR (:NEW."OUTCOME_BYTES" IS NOT NULL AND :OLD."OUTCOME_BYTES" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."OUTCOME_BYTES",:OLD."OUTCOME_BYTES")<>0)) OR ((:NEW."OUTCOME_SHA256" IS NULL AND :OLD."OUTCOME_SHA256" IS NOT NULL) OR (:NEW."OUTCOME_SHA256" IS NOT NULL AND :OLD."OUTCOME_SHA256" IS NULL) OR (:NEW."OUTCOME_SHA256" IS NOT NULL AND :OLD."OUTCOME_SHA256" IS NOT NULL AND :NEW."OUTCOME_SHA256"<>:OLD."OUTCOME_SHA256"))) THEN
        RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_OUTCOME_IMMUTABLE');
    END IF;
END;`)
	b.trigger("LC_MODEL_PREPARATION_ATTEMPT_IMMUTABLE_DELETE", "MODEL_PREPARATION_ATTEMPTS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PREPARATION_EVENT_IMMUTABLE_UPDATE", "MODEL_PREPARATION_EVENTS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PREPARATION_EVENT_IMMUTABLE_DELETE", "MODEL_PREPARATION_EVENTS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PREPARATION_RECOVERY_IMMUTABLE_UPDATE", "MODEL_PREPARATION_RECOVERIES", "UPDATE", "BEFORE", `BEGIN
    IF ((:NEW."ID" IS NULL AND :OLD."ID" IS NOT NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NOT NULL AND :NEW."ID"<>:OLD."ID")) OR ((:NEW."TENANT_ID" IS NULL AND :OLD."TENANT_ID" IS NOT NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NOT NULL AND :NEW."TENANT_ID"<>:OLD."TENANT_ID")) OR ((:NEW."WORKSPACE_ID" IS NULL AND :OLD."WORKSPACE_ID" IS NOT NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NOT NULL AND :NEW."WORKSPACE_ID"<>:OLD."WORKSPACE_ID")) OR ((:NEW."PREPARATION_ID" IS NULL AND :OLD."PREPARATION_ID" IS NOT NULL) OR (:NEW."PREPARATION_ID" IS NOT NULL AND :OLD."PREPARATION_ID" IS NULL) OR (:NEW."PREPARATION_ID" IS NOT NULL AND :OLD."PREPARATION_ID" IS NOT NULL AND :NEW."PREPARATION_ID"<>:OLD."PREPARATION_ID")) OR ((:NEW."PREVIOUS_ATTEMPT_ID" IS NULL AND :OLD."PREVIOUS_ATTEMPT_ID" IS NOT NULL) OR (:NEW."PREVIOUS_ATTEMPT_ID" IS NOT NULL AND :OLD."PREVIOUS_ATTEMPT_ID" IS NULL) OR (:NEW."PREVIOUS_ATTEMPT_ID" IS NOT NULL AND :OLD."PREVIOUS_ATTEMPT_ID" IS NOT NULL AND :NEW."PREVIOUS_ATTEMPT_ID"<>:OLD."PREVIOUS_ATTEMPT_ID")) OR ((:NEW."PREVIOUS_NODE_ID" IS NULL AND :OLD."PREVIOUS_NODE_ID" IS NOT NULL) OR (:NEW."PREVIOUS_NODE_ID" IS NOT NULL AND :OLD."PREVIOUS_NODE_ID" IS NULL) OR (:NEW."PREVIOUS_NODE_ID" IS NOT NULL AND :OLD."PREVIOUS_NODE_ID" IS NOT NULL AND :NEW."PREVIOUS_NODE_ID"<>:OLD."PREVIOUS_NODE_ID")) OR ((:NEW."NEXT_NODE_ID" IS NULL AND :OLD."NEXT_NODE_ID" IS NOT NULL) OR (:NEW."NEXT_NODE_ID" IS NOT NULL AND :OLD."NEXT_NODE_ID" IS NULL) OR (:NEW."NEXT_NODE_ID" IS NOT NULL AND :OLD."NEXT_NODE_ID" IS NOT NULL AND :NEW."NEXT_NODE_ID"<>:OLD."NEXT_NODE_ID")) OR ((:NEW."NEXT_ATTEMPT_NUMBER" IS NULL AND :OLD."NEXT_ATTEMPT_NUMBER" IS NOT NULL) OR (:NEW."NEXT_ATTEMPT_NUMBER" IS NOT NULL AND :OLD."NEXT_ATTEMPT_NUMBER" IS NULL) OR (:NEW."NEXT_ATTEMPT_NUMBER" IS NOT NULL AND :OLD."NEXT_ATTEMPT_NUMBER" IS NOT NULL AND :NEW."NEXT_ATTEMPT_NUMBER"<>:OLD."NEXT_ATTEMPT_NUMBER")) OR ((:NEW."REQUESTED_BY" IS NULL AND :OLD."REQUESTED_BY" IS NOT NULL) OR (:NEW."REQUESTED_BY" IS NOT NULL AND :OLD."REQUESTED_BY" IS NULL) OR (:NEW."REQUESTED_BY" IS NOT NULL AND :OLD."REQUESTED_BY" IS NOT NULL AND :NEW."REQUESTED_BY"<>:OLD."REQUESTED_BY")) OR ((:NEW."REQUESTED_AUDIENCE" IS NULL AND :OLD."REQUESTED_AUDIENCE" IS NOT NULL) OR (:NEW."REQUESTED_AUDIENCE" IS NOT NULL AND :OLD."REQUESTED_AUDIENCE" IS NULL) OR (:NEW."REQUESTED_AUDIENCE" IS NOT NULL AND :OLD."REQUESTED_AUDIENCE" IS NOT NULL AND :NEW."REQUESTED_AUDIENCE"<>:OLD."REQUESTED_AUDIENCE")) OR ((:NEW."REQUESTED_EPOCH" IS NULL AND :OLD."REQUESTED_EPOCH" IS NOT NULL) OR (:NEW."REQUESTED_EPOCH" IS NOT NULL AND :OLD."REQUESTED_EPOCH" IS NULL) OR (:NEW."REQUESTED_EPOCH" IS NOT NULL AND :OLD."REQUESTED_EPOCH" IS NOT NULL AND :NEW."REQUESTED_EPOCH"<>:OLD."REQUESTED_EPOCH")) OR ((:NEW."MEMBERSHIP_AUTHORITY" IS NULL AND :OLD."MEMBERSHIP_AUTHORITY" IS NOT NULL) OR (:NEW."MEMBERSHIP_AUTHORITY" IS NOT NULL AND :OLD."MEMBERSHIP_AUTHORITY" IS NULL) OR (:NEW."MEMBERSHIP_AUTHORITY" IS NOT NULL AND :OLD."MEMBERSHIP_AUTHORITY" IS NOT NULL AND :NEW."MEMBERSHIP_AUTHORITY"<>:OLD."MEMBERSHIP_AUTHORITY")) OR ((:NEW."REASON" IS NULL AND :OLD."REASON" IS NOT NULL) OR (:NEW."REASON" IS NOT NULL AND :OLD."REASON" IS NULL) OR (:NEW."REASON" IS NOT NULL AND :OLD."REASON" IS NOT NULL AND :NEW."REASON"<>:OLD."REASON")) OR ((:NEW."CREATION_KEY" IS NULL AND :OLD."CREATION_KEY" IS NOT NULL) OR (:NEW."CREATION_KEY" IS NOT NULL AND :OLD."CREATION_KEY" IS NULL) OR (:NEW."CREATION_KEY" IS NOT NULL AND :OLD."CREATION_KEY" IS NOT NULL AND :NEW."CREATION_KEY"<>:OLD."CREATION_KEY")) OR ((:NEW."CREATION_FINGERPRINT" IS NULL AND :OLD."CREATION_FINGERPRINT" IS NOT NULL) OR (:NEW."CREATION_FINGERPRINT" IS NOT NULL AND :OLD."CREATION_FINGERPRINT" IS NULL) OR (:NEW."CREATION_FINGERPRINT" IS NOT NULL AND :OLD."CREATION_FINGERPRINT" IS NOT NULL AND :NEW."CREATION_FINGERPRINT"<>:OLD."CREATION_FINGERPRINT")) OR ((:NEW."CREATED_AT" IS NULL AND :OLD."CREATED_AT" IS NOT NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NOT NULL AND :NEW."CREATED_AT"<>:OLD."CREATED_AT")) THEN
        RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_RECOVERY_IMMUTABLE');
    END IF;
    IF :OLD."APPLIED_AT" IS NOT NULL AND (((:NEW."DISPATCHED_ATTEMPT_ID" IS NULL AND :OLD."DISPATCHED_ATTEMPT_ID" IS NOT NULL) OR (:NEW."DISPATCHED_ATTEMPT_ID" IS NOT NULL AND :OLD."DISPATCHED_ATTEMPT_ID" IS NULL) OR (:NEW."DISPATCHED_ATTEMPT_ID" IS NOT NULL AND :OLD."DISPATCHED_ATTEMPT_ID" IS NOT NULL AND :NEW."DISPATCHED_ATTEMPT_ID"<>:OLD."DISPATCHED_ATTEMPT_ID")) OR ((:NEW."APPLIED_AT" IS NULL AND :OLD."APPLIED_AT" IS NOT NULL) OR (:NEW."APPLIED_AT" IS NOT NULL AND :OLD."APPLIED_AT" IS NULL) OR (:NEW."APPLIED_AT" IS NOT NULL AND :OLD."APPLIED_AT" IS NOT NULL AND :NEW."APPLIED_AT"<>:OLD."APPLIED_AT"))) THEN
        RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_RECOVERY_APPLIED_IMMUTABLE');
    END IF;
END;`)
	b.trigger("LC_MODEL_PREPARATION_RECOVERY_IMMUTABLE_DELETE", "MODEL_PREPARATION_RECOVERIES", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PREPARATION_UPLOAD_IMMUTABLE_UPDATE", "MODEL_PREPARATION_UPLOADS", "UPDATE", "BEFORE", `BEGIN
    IF ((:NEW."ID" IS NULL AND :OLD."ID" IS NOT NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NULL) OR (:NEW."ID" IS NOT NULL AND :OLD."ID" IS NOT NULL AND :NEW."ID"<>:OLD."ID")) OR ((:NEW."TENANT_ID" IS NULL AND :OLD."TENANT_ID" IS NOT NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NULL) OR (:NEW."TENANT_ID" IS NOT NULL AND :OLD."TENANT_ID" IS NOT NULL AND :NEW."TENANT_ID"<>:OLD."TENANT_ID")) OR ((:NEW."WORKSPACE_ID" IS NULL AND :OLD."WORKSPACE_ID" IS NOT NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NULL) OR (:NEW."WORKSPACE_ID" IS NOT NULL AND :OLD."WORKSPACE_ID" IS NOT NULL AND :NEW."WORKSPACE_ID"<>:OLD."WORKSPACE_ID")) OR ((:NEW."PREPARATION_ID" IS NULL AND :OLD."PREPARATION_ID" IS NOT NULL) OR (:NEW."PREPARATION_ID" IS NOT NULL AND :OLD."PREPARATION_ID" IS NULL) OR (:NEW."PREPARATION_ID" IS NOT NULL AND :OLD."PREPARATION_ID" IS NOT NULL AND :NEW."PREPARATION_ID"<>:OLD."PREPARATION_ID")) OR ((:NEW."ATTEMPT_ID" IS NULL AND :OLD."ATTEMPT_ID" IS NOT NULL) OR (:NEW."ATTEMPT_ID" IS NOT NULL AND :OLD."ATTEMPT_ID" IS NULL) OR (:NEW."ATTEMPT_ID" IS NOT NULL AND :OLD."ATTEMPT_ID" IS NOT NULL AND :NEW."ATTEMPT_ID"<>:OLD."ATTEMPT_ID")) OR ((:NEW."MANIFEST_SHA256" IS NULL AND :OLD."MANIFEST_SHA256" IS NOT NULL) OR (:NEW."MANIFEST_SHA256" IS NOT NULL AND :OLD."MANIFEST_SHA256" IS NULL) OR (:NEW."MANIFEST_SHA256" IS NOT NULL AND :OLD."MANIFEST_SHA256" IS NOT NULL AND :NEW."MANIFEST_SHA256"<>:OLD."MANIFEST_SHA256")) OR ((:NEW."MANIFEST" IS NULL AND :OLD."MANIFEST" IS NOT NULL) OR (:NEW."MANIFEST" IS NOT NULL AND :OLD."MANIFEST" IS NULL) OR (:NEW."MANIFEST" IS NOT NULL AND :OLD."MANIFEST" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."MANIFEST",:OLD."MANIFEST")<>0)) OR ((:NEW."MANIFEST_BYTES" IS NULL AND :OLD."MANIFEST_BYTES" IS NOT NULL) OR (:NEW."MANIFEST_BYTES" IS NOT NULL AND :OLD."MANIFEST_BYTES" IS NULL) OR (:NEW."MANIFEST_BYTES" IS NOT NULL AND :OLD."MANIFEST_BYTES" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."MANIFEST_BYTES",:OLD."MANIFEST_BYTES")<>0)) OR ((:NEW."DESCRIPTOR" IS NULL AND :OLD."DESCRIPTOR" IS NOT NULL) OR (:NEW."DESCRIPTOR" IS NOT NULL AND :OLD."DESCRIPTOR" IS NULL) OR (:NEW."DESCRIPTOR" IS NOT NULL AND :OLD."DESCRIPTOR" IS NOT NULL AND DBMS_LOB.COMPARE(:NEW."DESCRIPTOR",:OLD."DESCRIPTOR")<>0)) OR ((:NEW."CREATED_AT" IS NULL AND :OLD."CREATED_AT" IS NOT NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NULL) OR (:NEW."CREATED_AT" IS NOT NULL AND :OLD."CREATED_AT" IS NOT NULL AND :NEW."CREATED_AT"<>:OLD."CREATED_AT")) THEN
        RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_UPLOAD_IMMUTABLE');
    END IF;
    IF :OLD."ACCEPTED_AT" IS NOT NULL AND (((:NEW."STATE" IS NULL AND :OLD."STATE" IS NOT NULL) OR (:NEW."STATE" IS NOT NULL AND :OLD."STATE" IS NULL) OR (:NEW."STATE" IS NOT NULL AND :OLD."STATE" IS NOT NULL AND :NEW."STATE"<>:OLD."STATE")) OR ((:NEW."ACCEPTED_AT" IS NULL AND :OLD."ACCEPTED_AT" IS NOT NULL) OR (:NEW."ACCEPTED_AT" IS NOT NULL AND :OLD."ACCEPTED_AT" IS NULL) OR (:NEW."ACCEPTED_AT" IS NOT NULL AND :OLD."ACCEPTED_AT" IS NOT NULL AND :NEW."ACCEPTED_AT"<>:OLD."ACCEPTED_AT"))) THEN
        RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_ACCEPTED_UPLOAD_IMMUTABLE');
    END IF;
END;`)
	b.trigger("LC_MODEL_PREPARATION_UPLOAD_IMMUTABLE_DELETE", "MODEL_PREPARATION_UPLOADS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PREPARATION_CHUNK_IMMUTABLE_UPDATE", "MODEL_PREPARATION_UPLOAD_CHUNKS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PREPARATION_CHUNK_IMMUTABLE_DELETE", "MODEL_PREPARATION_UPLOAD_CHUNKS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PREPARATION_ARTIFACT_IMMUTABLE_UPDATE", "MODEL_PREPARATION_ARTIFACTS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PREPARATION_ARTIFACT_IMMUTABLE_DELETE", "MODEL_PREPARATION_ARTIFACTS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PREPARATION_OUTPUT_RECORD_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_REVIEW_IMMUTABLE_UPDATE", "MODEL_PREPARATION_REVIEWS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PUBLICATION_PROVENANCE_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_REVIEW_IMMUTABLE_DELETE", "MODEL_PREPARATION_REVIEWS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PUBLICATION_PROVENANCE_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_DERIVATION_IMMUTABLE_UPDATE", "MODEL_DERIVATIONS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PUBLICATION_PROVENANCE_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_DERIVATION_IMMUTABLE_DELETE", "MODEL_DERIVATIONS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PUBLICATION_PROVENANCE_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PUBLICATION_IMMUTABLE_UPDATE", "MODEL_PUBLICATIONS", "UPDATE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PUBLICATION_PROVENANCE_IMMUTABLE');
END;`)
	b.trigger("LC_MODEL_PUBLICATION_IMMUTABLE_DELETE", "MODEL_PUBLICATIONS", "DELETE", "BEFORE", `BEGIN
    RAISE_APPLICATION_ERROR(-20061,'MODEL_PUBLICATION_PROVENANCE_IMMUTABLE');
END;`)
	lifecyclePublicationGuards(b)
	return b.fragment
}

func lifecyclePublicationGuards(b *lifecycleBuilder) {
	// The graph is checked again at each transition. JSON type-aware containment
	// rejects a string "true" or an array in place of the scalar gate boolean.
	b.trigger("LC_REVIEW_BINDING_INSERT", "MODEL_PREPARATION_REVIEWS", "INSERT", "BEFORE", `DECLARE MATCH_COUNT INTEGER; BEGIN
 SELECT COUNT(*) INTO MATCH_COUNT FROM "MODEL_PREPARATIONS" P
 JOIN "MODEL_PREPARATION_ATTEMPTS" A ON A."TENANT_ID"=P."TENANT_ID" AND A."WORKSPACE_ID"=P."WORKSPACE_ID" AND A."PREPARATION_ID"=P."ID" AND A."ID"=P."CURRENT_ATTEMPT_ID"
 JOIN "MODEL_PREPARATION_ARTIFACTS" F ON F."TENANT_ID"=A."TENANT_ID" AND F."WORKSPACE_ID"=A."WORKSPACE_ID" AND F."PREPARATION_ID"=A."PREPARATION_ID" AND F."ATTEMPT_ID"=A."ID"
 JOIN "GPU_LEASES" G ON G."TENANT_ID"=P."TENANT_ID" AND G."WORKSPACE_ID"=P."WORKSPACE_ID" AND G."MODEL_PREPARATION_ID"=P."ID"
 WHERE P."TENANT_ID"=:NEW."TENANT_ID" AND P."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND P."ID"=:NEW."PREPARATION_ID"
 AND P."STATE"='awaiting_review' AND P."REVISION"=:NEW."EXPECTED_REVISION" AND P."PENDING_RECOVERY_ID" IS NULL
 AND A."ID"=:NEW."ATTEMPT_ID" AND A."ATTEMPT_NUMBER"=P."ATTEMPT_NUMBER" AND A."NODE_ID"=P."CURRENT_NODE_ID"
 AND A."STATE"='prepared' AND A."PHYSICAL_STOPPED_AT" IS NOT NULL AND A."PHYSICAL_STOP_RECEIPT" IS NOT NULL AND A."PHYSICAL_STOP_SHA256" IS NOT NULL
 AND A."OUTCOME_SHA256"=:NEW."REPORT_SHA256" AND F."MANIFEST_SHA256"=:NEW."REPORT_SHA256" AND F."ID"=:NEW."ARTIFACT_ID"
 AND DBMS_LOB.COMPARE(A."OUTCOME_BYTES",F."REPORT_BYTES")=0
 AND LOWER(RAWTOHEX(DBMS_CRYPTO.HASH(F."REPORT_BYTES",DBMS_CRYPTO.HASH_SH256)))=:NEW."REPORT_SHA256"
 AND JSON_CONTAINS(F."REPORT",'"prepared"','$.status')=1 AND JSON_VALUE(F."REPORT",'$.status')='prepared'
 AND JSON_CONTAINS(F."REPORT",'true','$.gate_passed')=1 AND JSON_VALUE(F."REPORT",'$.gate_passed')='true'
 AND JSON_TYPE(JSON_EXTRACT(F."REPORT",'$.event_count'))='INTEGER'
 AND JSON_VALUE(F."REPORT",'$.event_count' RETURNING NUMBER)=A."LAST_EVENT_SEQUENCE"
 AND G."STATE"='released' AND G."NODE_ID"=A."NODE_ID" AND G."GPU_UUID"=P."GPU_UUID" AND G."REVISION"=A."ATTEMPT_NUMBER";
 IF MATCH_COUNT<>1 THEN RAISE_APPLICATION_ERROR(-20062,'MODEL_PREPARATION_REVIEW_BINDING_INVALID'); END IF; END;`)
	b.trigger("LC_DERIVED_STAGED_INSERT", "MODEL_VERSIONS", "INSERT", "BEFORE", `BEGIN
 IF :NEW."KIND"='merged_lora' AND :NEW."STATE"<>'staged' THEN RAISE_APPLICATION_ERROR(-20062,'MODEL_DERIVED_STAGING_REQUIRED'); END IF;
 END;`)
	b.trigger("LC_DERIVATION_GRAPH_INSERT", "MODEL_DERIVATIONS", "INSERT", "BEFORE", `DECLARE MATCH_COUNT INTEGER; BEGIN
 SELECT COUNT(*) INTO MATCH_COUNT FROM "MODEL_VERSIONS" M
 JOIN "MODEL_PREPARATION_REVIEWS" R ON R."TENANT_ID"=M."TENANT_ID" AND R."WORKSPACE_ID"=M."WORKSPACE_ID" AND R."MODEL_ID"=M."ID"
 JOIN "MODEL_PREPARATIONS" P ON P."TENANT_ID"=R."TENANT_ID" AND P."WORKSPACE_ID"=R."WORKSPACE_ID" AND P."ID"=R."PREPARATION_ID"
 WHERE M."ID"=:NEW."MODEL_ID" AND M."TENANT_ID"=:NEW."TENANT_ID" AND M."WORKSPACE_ID"=:NEW."WORKSPACE_ID"
 AND M."KIND"='merged_lora' AND M."STATE"='staged' AND M."CATALOG_DIGEST"=:NEW."CATALOG_DIGEST" AND DBMS_LOB.COMPARE(M."MANIFEST",:NEW."CATALOG")=0
 AND R."ID"=:NEW."REVIEW_ID" AND R."DECISION"='approve' AND R."ARTIFACT_ID"=:NEW."ARTIFACT_ID"
 AND R."PREPARATION_ID"=:NEW."PREPARATION_ID" AND R."ATTEMPT_ID"=:NEW."ATTEMPT_ID" AND R."REPORT_SHA256"=:NEW."REPORT_SHA256"
 AND R."REVIEWED_BY"=M."CREATED_BY" AND R."MODEL_NAME"=M."NAME"
 AND P."STATE"='awaiting_review' AND P."CURRENT_ATTEMPT_ID"=:NEW."ATTEMPT_ID" AND P."MODEL_ID"=:NEW."BASE_MODEL_ID" AND P."REVISION"=R."EXPECTED_REVISION";
 IF MATCH_COUNT<>1 THEN RAISE_APPLICATION_ERROR(-20062,'MODEL_DERIVED_REVIEW_GRAPH_REQUIRED'); END IF; END;`)
	b.trigger("LC_PREPARATION_REVIEW_INSERT", "MODEL_PREPARATIONS", "INSERT", "BEFORE", `BEGIN
 IF :NEW."STATE" IN ('approved','rejected') OR :NEW."REVIEW_ID" IS NOT NULL OR :NEW."DERIVED_MODEL_ID" IS NOT NULL THEN RAISE_APPLICATION_ERROR(-20062,'MODEL_PREPARATION_REVIEW_BINDING_INVALID'); END IF; END;`)
	immutable := []string{"STATE", "REVIEW_ID", "DERIVED_MODEL_ID", "CURRENT_ATTEMPT_ID", "ATTEMPT_NUMBER", "CURRENT_NODE_ID", "REVISION", "PENDING_RECOVERY_ID"}
	changed := []string{}
	for _, field := range immutable {
		changed = append(changed, lifecycleChanged(field, false))
	}
	b.trigger("LC_PREPARATION_REVIEW_UPDATE", "MODEL_PREPARATIONS", "UPDATE", "BEFORE",
		`DECLARE MATCH_COUNT INTEGER; BEGIN
 IF :OLD."STATE" IN ('approved','rejected') THEN
 IF `+strings.Join(changed, " OR ")+` THEN RAISE_APPLICATION_ERROR(-20062,'MODEL_PREPARATION_DECISION_IMMUTABLE'); END IF;
 ELSIF :NEW."STATE" IN ('approved','rejected') THEN
 IF :OLD."STATE"<>'awaiting_review' OR :NEW."REVISION"<>:OLD."REVISION"+1 OR `+lifecycleChanged("CURRENT_ATTEMPT_ID", false)+` OR :NEW."PENDING_RECOVERY_ID" IS NOT NULL THEN RAISE_APPLICATION_ERROR(-20062,'MODEL_PREPARATION_REVIEW_BINDING_INVALID'); END IF;
 SELECT COUNT(*) INTO MATCH_COUNT FROM "MODEL_PREPARATION_REVIEWS" R
 WHERE R."TENANT_ID"=:NEW."TENANT_ID" AND R."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND R."ID"=:NEW."REVIEW_ID"
 AND R."PREPARATION_ID"=:NEW."ID" AND R."ATTEMPT_ID"=:NEW."CURRENT_ATTEMPT_ID" AND R."EXPECTED_REVISION"=:OLD."REVISION"
 AND ((:NEW."STATE"='approved' AND R."DECISION"='approve' AND R."MODEL_ID"=:NEW."DERIVED_MODEL_ID" AND EXISTS(
 SELECT 1 FROM "MODEL_DERIVATIONS" D JOIN "MODEL_VERSIONS" M ON M."TENANT_ID"=D."TENANT_ID" AND M."WORKSPACE_ID"=D."WORKSPACE_ID" AND M."ID"=D."MODEL_ID"
 WHERE D."TENANT_ID"=:NEW."TENANT_ID" AND D."WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND D."MODEL_ID"=:NEW."DERIVED_MODEL_ID"
 AND D."REVIEW_ID"=R."ID" AND D."PREPARATION_ID"=:NEW."ID" AND D."ATTEMPT_ID"=:NEW."CURRENT_ATTEMPT_ID"
 AND D."BASE_MODEL_ID"=:NEW."MODEL_ID" AND M."KIND"='merged_lora' AND M."STATE"='staged'))
 OR (:NEW."STATE"='rejected' AND R."DECISION"='reject' AND R."MODEL_ID" IS NULL AND :NEW."DERIVED_MODEL_ID" IS NULL));
 IF MATCH_COUNT<>1 THEN RAISE_APPLICATION_ERROR(-20062,'MODEL_PREPARATION_REVIEW_BINDING_INVALID'); END IF;
 END IF; END;`)
	b.trigger("LC_PUBLICATION_GRAPH_INSERT", "MODEL_PUBLICATIONS", "INSERT", "BEFORE", `DECLARE MATCH_COUNT INTEGER; BEGIN
 SELECT COUNT(*) INTO MATCH_COUNT FROM "MODEL_DERIVATIONS" D
 JOIN "MODEL_VERSIONS" M ON M."TENANT_ID"=D."TENANT_ID" AND M."WORKSPACE_ID"=D."WORKSPACE_ID" AND M."ID"=D."MODEL_ID"
 JOIN "MODEL_PREPARATION_REVIEWS" R ON R."TENANT_ID"=D."TENANT_ID" AND R."WORKSPACE_ID"=D."WORKSPACE_ID" AND R."ID"=D."REVIEW_ID"
 JOIN "MODEL_PREPARATIONS" P ON P."TENANT_ID"=D."TENANT_ID" AND P."WORKSPACE_ID"=D."WORKSPACE_ID" AND P."ID"=D."PREPARATION_ID"
 WHERE D."MODEL_ID"=:NEW."MODEL_ID" AND D."TENANT_ID"=:NEW."TENANT_ID" AND D."WORKSPACE_ID"=:NEW."WORKSPACE_ID"
 AND D."CATALOG_DIGEST"=M."CATALOG_DIGEST" AND DBMS_LOB.COMPARE(D."CATALOG",M."MANIFEST")=0
 AND R."DECISION"='approve' AND R."MODEL_ID"=:NEW."MODEL_ID" AND R."ARTIFACT_ID"=D."ARTIFACT_ID"
 AND R."REVIEWED_BY"=M."CREATED_BY" AND R."MODEL_NAME"=M."NAME" AND R."REPORT_SHA256"=D."REPORT_SHA256"
 AND R."PREPARATION_ID"=D."PREPARATION_ID" AND R."ATTEMPT_ID"=D."ATTEMPT_ID"
 AND P."STATE"='approved' AND P."DERIVED_MODEL_ID"=:NEW."MODEL_ID" AND P."REVIEW_ID"=R."ID"
 AND P."CURRENT_ATTEMPT_ID"=D."ATTEMPT_ID" AND P."MODEL_ID"=D."BASE_MODEL_ID"
 AND P."REVISION"=R."EXPECTED_REVISION"+1 AND M."KIND"='merged_lora' AND M."STATE"='staged';
 IF MATCH_COUNT<>1 THEN RAISE_APPLICATION_ERROR(-20062,'MODEL_DERIVED_REVIEW_GRAPH_REQUIRED'); END IF; END;`)
	b.trigger("LC_DERIVED_READY_UPDATE", "MODEL_VERSIONS", "UPDATE", "BEFORE", `DECLARE MATCH_COUNT INTEGER; BEGIN
 IF :OLD."KIND"='merged_lora' THEN
 IF NOT (:NEW."STATE"=:OLD."STATE" OR (:OLD."STATE"='staged' AND :NEW."STATE"='ready') OR (:OLD."STATE"='ready' AND :NEW."STATE"='deleted')) THEN RAISE_APPLICATION_ERROR(-20062,'MODEL_DERIVED_STATE_INVALID'); END IF;
 IF :NEW."STATE"='ready' THEN
 SELECT COUNT(*) INTO MATCH_COUNT FROM "MODEL_DERIVATIONS" D
 JOIN "MODEL_PREPARATION_REVIEWS" R ON R."TENANT_ID"=D."TENANT_ID" AND R."WORKSPACE_ID"=D."WORKSPACE_ID" AND R."ID"=D."REVIEW_ID"
 JOIN "MODEL_PREPARATIONS" P ON P."TENANT_ID"=D."TENANT_ID" AND P."WORKSPACE_ID"=D."WORKSPACE_ID" AND P."ID"=D."PREPARATION_ID"
 JOIN "MODEL_PUBLICATIONS" X ON X."TENANT_ID"=D."TENANT_ID" AND X."WORKSPACE_ID"=D."WORKSPACE_ID" AND X."MODEL_ID"=D."MODEL_ID"
 WHERE D."MODEL_ID"=:NEW."ID" AND D."TENANT_ID"=:NEW."TENANT_ID" AND D."WORKSPACE_ID"=:NEW."WORKSPACE_ID"
 AND D."CATALOG_DIGEST"=:NEW."CATALOG_DIGEST" AND DBMS_LOB.COMPARE(D."CATALOG",:NEW."MANIFEST")=0
 AND R."DECISION"='approve' AND R."MODEL_ID"=:NEW."ID" AND R."ARTIFACT_ID"=D."ARTIFACT_ID"
 AND R."REVIEWED_BY"=:NEW."CREATED_BY" AND R."MODEL_NAME"=:NEW."NAME" AND R."REPORT_SHA256"=D."REPORT_SHA256"
 AND R."PREPARATION_ID"=D."PREPARATION_ID" AND R."ATTEMPT_ID"=D."ATTEMPT_ID"
 AND P."STATE"='approved' AND P."DERIVED_MODEL_ID"=:NEW."ID" AND P."REVIEW_ID"=R."ID"
 AND P."CURRENT_ATTEMPT_ID"=D."ATTEMPT_ID" AND P."MODEL_ID"=D."BASE_MODEL_ID" AND P."REVISION"=R."EXPECTED_REVISION"+1;
 IF MATCH_COUNT<>1 THEN RAISE_APPLICATION_ERROR(-20062,'MODEL_DERIVED_REVIEW_GRAPH_REQUIRED'); END IF;
 END IF; END IF; END;`)
	b.trigger("LC_PUBLICATION_APPLY", "MODEL_PUBLICATIONS", "INSERT", "AFTER", `BEGIN
 UPDATE "MODEL_VERSIONS" SET "STATE"='ready' WHERE "TENANT_ID"=:NEW."TENANT_ID" AND "WORKSPACE_ID"=:NEW."WORKSPACE_ID" AND "ID"=:NEW."MODEL_ID" AND "KIND"='merged_lora' AND "STATE"='staged';
 IF SQL%ROWCOUNT<>1 THEN RAISE_APPLICATION_ERROR(-20062,'MODEL_DERIVED_PUBLICATION_REQUIRED'); END IF; END;`)
	// Enable the new state only after the complete retained graph guards exist.
	b.drop("LC_MODELS_DROP_BASE_ONLY", "MODEL_VERSIONS", "MODELS_APPROVED_KIND")
	b.constraint("MODEL_VERSIONS", check("LC_MODELS_PUBLICATION_KIND", `"MODEL_KEY"='qwen3-0.6b' AND (("KIND"='approved_base' AND "STATE" IN ('ready','deleted')) OR ("KIND"='merged_lora' AND "STATE" IN ('staged','ready','deleted')))`))
}
