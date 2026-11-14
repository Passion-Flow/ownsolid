package migrate

import "strings"

// These fragments have no central registration or I/O. Task delivery depends on
// the actual node task scope key; middleware metadata only needs foundation.
func taskDeliveriesFragment(current map[string]table) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "taskdeliveries_v2", Requires: []string{"foundation_v1", "nodes_components_v2"}}
	taskMiddlewareTables(&f, current, taskOutboxTable(), taskReceiptTable(), taskDeadTable())
	for _, i := range []index{
		{Name: "TASK_EVENT_OUTBOX_DUE", Table: "TASK_EVENT_OUTBOX", Columns: []string{"STATE", "NOT_BEFORE", "CREATED_AT", "ID"}, Descending: []bool{false, false, false, false}},
		{Name: "TASK_EVENT_OUTBOX_SCOPE", Table: "TASK_EVENT_OUTBOX", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "CREATED_AT", "ID"}, Descending: []bool{false, false, false, true, true}},
		{Name: "TASK_EVENT_DEAD_SCOPE", Table: "TASK_EVENT_DEAD_LETTERS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID", "CREATED_AT", "ID"}, Descending: []bool{false, false, false, true, true}},
	} {
		f.Changes = append(f.Changes, schemaChange{ID: i.Name, Kind: "index", SQL: i.ddl(), Index: &i})
	}
	for _, t := range taskNativeTriggers() { taskMiddlewareTrigger(&f, t) }
	return f
}

func middlewareFragment(current map[string]table) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "middleware_v2", Requires: []string{"foundation_v1"}}
	taskMiddlewareTables(&f, current, middlewareInstanceTable(), middlewareOperationTable())
	for _, i := range []index{
		{Name: "MIDDLEWARE_OPERATIONS_CLAIM", Table: "MIDDLEWARE_OPERATIONS", Columns: []string{"STATE", "CREATED_AT", "ID"}, Descending: []bool{false, false, false}},
		{Name: "MIDDLEWARE_OPERATIONS_EXPIRY", Table: "MIDDLEWARE_OPERATIONS", Columns: []string{"STATE", "LEASE_EXPIRES_AT", "ID"}, Descending: []bool{false, false, false}},
		{Name: "MIDDLEWARE_OPERATIONS_HISTORY", Table: "MIDDLEWARE_OPERATIONS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "INSTANCE_ID", "ID"}, Descending: []bool{false, false, false, false}},
	} {
		f.Changes = append(f.Changes, schemaChange{ID: i.Name, Kind: "index", SQL: i.ddl(), Index: &i})
	}
	immutable := taskNativeChanged([]string{"ID", "TENANT_ID", "WORKSPACE_ID", "INSTANCE_ID", "KIND", "REQUESTED_BY", "INTENT_KEY", "INTENT_FINGERPRINT", "INSTANCE_VERSION", "PROFILE_FINGERPRINT", "REQUEST_ID", "CREATED_AT"})
	immutable += ` OR DBMS_LOB.COMPARE(:NEW.INPUT_JSON,:OLD.INPUT_JSON)<>0 OR DBMS_LOB.COMPARE(:NEW.ACTOR_JSON,:OLD.ACTOR_JSON)<>0`
	taskMiddlewareTrigger(&f, trigger{Name: "MIDDLEWARE_OPERATION_UPDATE", Table: "MIDDLEWARE_OPERATIONS", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: `BEGIN
 IF ` + immutable + ` THEN RAISE_APPLICATION_ERROR(-20001,'MIDDLEWARE_INTENT_IMMUTABLE'); END IF;
 IF :OLD.STATE IN ('succeeded','failed','unknown') THEN RAISE_APPLICATION_ERROR(-20001,'MIDDLEWARE_TERMINAL_IMMUTABLE'); END IF;
END;`})
	taskMiddlewareTrigger(&f, trigger{Name: "MIDDLEWARE_OPERATION_DELETE", Table: "MIDDLEWARE_OPERATIONS", Event: "DELETE", Timing: "BEFORE EACH ROW", Body: `BEGIN RAISE_APPLICATION_ERROR(-20001,'MIDDLEWARE_HISTORY_IMMUTABLE'); END;`})
	return f
}

func taskMiddlewareTables(f *schemaFragment, current map[string]table, tables ...table) {
	known := make(map[string]table, len(current)+len(tables))
	for k, v := range current { known[k] = cloneTable(v) }
	for _, t := range tables {
		f.Changes = append(f.Changes, schemaChange{ID: t.Name, Kind: "table", SQL: extensionTableSQL(t, known, nil), After: &t})
		known[t.Name] = cloneTable(t)
	}
}
func taskMiddlewareTrigger(f *schemaFragment, t trigger) {
	f.Changes = append(f.Changes, schemaChange{ID: t.Name, Kind: "trigger", SQL: "CREATE TRIGGER " + quoted(t.Name) + " BEFORE " + t.Event + " ON " + quoted(t.Table) + " FOR EACH ROW " + t.Body, Trigger: &t})
}
func taskNativeStamp(name string, nullable, now bool) column {
	c := stamp(name, nullable, false)
	if now { c.Default = "SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))" }
	return c
}
func taskNativeTextDefault(name string, width int, value string) column {
	c := text(name, width, false); c.Default = value; return c
}
func taskNativeDigest(name string, nullable bool) column { return col(name, "CHAR", 64, nullable, "") }
func taskNativeDigestCheck(name, field string, nullable bool) constraint {
	e := "REGEXP_LIKE(" + quoted(field) + ",'^[a-f0-9]{64}$','c')"
	if nullable { e = quoted(field) + " IS NULL OR (" + e + ")" }
	return check(name, e)
}
func taskNativeJSON(name, field, bound string, nullable bool) constraint {
	q := quoted(field)
	e := "JSON_TYPE("+q+") IS NOT NULL AND JSON_TYPE("+q+")='OBJECT' AND OCTET_LENGTH("+q+")<="+bound
	if nullable { e = q+" IS NULL OR ("+e+")" }
	return check(name, e)
}
// Native catalog metadata has no precision/scale pair. Decimal text preserves
// the entire NATS uint64 range, without BIGINT overflow or lossy floating point.
// No sequence ordering is performed; identity comparisons use canonical strings.
func taskNativeSequence(name string, nullable bool) column { return text(name, 20, nullable) }
func taskNativeSequenceCheck(name, field string, nullable bool) constraint {
	q := quoted(field)
	e := "REGEXP_LIKE("+q+",'^[1-9][0-9]{0,19}$','c') AND (CHAR_LENGTH("+q+")<20 OR "+q+"<='18446744073709551615')"
	if nullable { e = q+" IS NULL OR ("+e+")" }
	return check(name, e)
}

const taskNativeErrorCodes = `('TASK_BUS_READBACK_CONFLICT','TASK_BUS_IDENTITY_CHANGED','TASK_BUS_PROFILE_REJECTED','TASK_BUS_PUBLISH_UNKNOWN','TASK_EVENT_SOURCE_OBSOLETE','TASK_EVENT_SOURCE_CHANGED','TASK_BUS_PUBLISH_EXHAUSTED','TASK_BUS_CONSUME_EXHAUSTED','TRANSPORT_RETENTION_EXPIRED','TASK_EVENT_ENVELOPE_REJECTED','TASK_EVENT_UNCOMMITTED_SOURCE','TASK_EVENT_STALE_DELIVERY','TASK_BUS_RESTORE_FENCED')`

func taskOutboxTable() table {
	return nativeTable("TASK_EVENT_OUTBOX", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("NODE_ID", false), uuid("TASK_ID", false), uuid("NAMESPACE_ID", false), text("EVENT_KIND", 32, false), text("OPERATION", 64, false), col("EVENT_BYTES", "VARBINARY", 4096, false, ""), taskNativeDigest("LOGICAL_SHA256", false), uuid("DELIVERY_ID", false), col("GENERATION", "INTEGER", 0, false, ""), text("STATE", 16, false), col("OUTCOME_UNKNOWN", "BIT", 0, false, "0"), col("PUBLISH_ATTEMPTS", "INTEGER", 0, false, "0"), col("CONSUME_ATTEMPTS", "BIGINT", 0, false, "0"), uuid("CLAIM_ID", true), taskNativeStamp("CLAIM_EXPIRES_AT", true, false), taskNativeDigest("BROKER_IDENTITY", false), taskNativeSequence("BROKER_SEQUENCE", true), text("CONSUMER_NAME", 64, false), text("ERROR_CODE", 64, true), taskNativeStamp("NOT_BEFORE", false, false), taskNativeStamp("EXPIRES_AT", false, false), taskNativeStamp("CREATED_AT", false, false), taskNativeStamp("UPDATED_AT", false, false),
	}, pk("TASK_EVENT_OUTBOX_PK", "ID"), unique("TASK_EVENT_OUTBOX_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID", "ID"), unique("TASK_EVENT_OUTBOX_SOURCE_UK", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID", "EVENT_KIND"), unique("TASK_EVENT_OUTBOX_HASH_UK", "ID", "LOGICAL_SHA256"), unique("TASK_EVENT_OUTBOX_DELIVERY_UK", "DELIVERY_ID"), fk("TASK_EVENT_OUTBOX_TASK_FK", "NODE_TASKS", "TASKS_SCOPE_IDENTITY", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID"),
	check("TASK_EVENT_OUTBOX_KIND", `"EVENT_KIND"='node.task.queued.v1'`), check("TASK_EVENT_OUTBOX_BYTES", `OCTET_LENGTH("EVENT_BYTES") BETWEEN 1 AND 4096`), taskNativeDigestCheck("TASK_EVENT_OUTBOX_DIGEST", "LOGICAL_SHA256", false), taskNativeDigestCheck("TASK_EVENT_OUTBOX_BROKER", "BROKER_IDENTITY", false), taskNativeSequenceCheck("TASK_EVENT_OUTBOX_SEQUENCE", "BROKER_SEQUENCE", true),
	check("TASK_EVENT_OUTBOX_COUNTS", `"GENERATION">=1 AND "PUBLISH_ATTEMPTS">=0 AND "CONSUME_ATTEMPTS">=0`), check("TASK_EVENT_OUTBOX_STATE", `"STATE" IN ('prepared','sent','published','received','obsolete','dead')`), check("TASK_EVENT_OUTBOX_CLAIM", `("CLAIM_ID" IS NULL AND "CLAIM_EXPIRES_AT" IS NULL) OR ("CLAIM_ID" IS NOT NULL AND "CLAIM_EXPIRES_AT" IS NOT NULL)`), check("TASK_EVENT_OUTBOX_UNKNOWN", `"OUTCOME_UNKNOWN"=0 OR ("OUTCOME_UNKNOWN"=1 AND "STATE" IN ('sent','dead'))`), check("TASK_EVENT_OUTBOX_ACK", `"STATE" NOT IN ('published','received','obsolete') OR "BROKER_SEQUENCE" IS NOT NULL`), check("TASK_EVENT_OUTBOX_TIME", `"EXPIRES_AT">"CREATED_AT" AND "UPDATED_AT">="CREATED_AT"`), check("TASK_EVENT_OUTBOX_ERROR", `"ERROR_CODE" IS NULL OR "ERROR_CODE" IN `+taskNativeErrorCodes), check("TASK_EVENT_OUTBOX_SENT", `"STATE"<>'sent' OR "OUTCOME_UNKNOWN"=1`), check("TASK_EVENT_OUTBOX_PREPARED", `"STATE"<>'prepared' OR ("OUTCOME_UNKNOWN"=0 AND "PUBLISH_ATTEMPTS"=0 AND "CONSUME_ATTEMPTS"=0 AND "BROKER_SEQUENCE" IS NULL AND "CLAIM_ID" IS NULL AND "ERROR_CODE" IS NULL)`))
}
func taskReceiptTable() table {
	return nativeTable("TASK_EVENT_RECEIPTS", []column{
		uuid("ID", false), uuid("EVENT_ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("NODE_ID", false), uuid("TASK_ID", false), taskNativeDigest("LOGICAL_SHA256", false), uuid("DELIVERY_ID", false), col("GENERATION", "INTEGER", 0, false, ""), text("CONSUMER_NAME", 64, false), taskNativeDigest("BROKER_IDENTITY", false), taskNativeSequence("BROKER_SEQUENCE", false), taskNativeSequence("CONSUMER_SEQUENCE", false), text("OUTCOME", 16, false), text("TASK_STATE", 16, false), taskNativeStamp("CREATED_AT", false, false),
	}, pk("TASK_EVENT_RECEIPTS_PK", "ID"), unique("TASK_EVENT_RECEIPT_EVENT_UK", "EVENT_ID"), fk("TASK_EVENT_RECEIPT_SCOPE_FK", "TASK_EVENT_OUTBOX", "TASK_EVENT_OUTBOX_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID", "EVENT_ID"), fk("TASK_EVENT_RECEIPT_HASH_FK", "TASK_EVENT_OUTBOX", "TASK_EVENT_OUTBOX_HASH_UK", "EVENT_ID", "LOGICAL_SHA256"), taskNativeDigestCheck("TASK_EVENT_RECEIPT_DIGEST", "LOGICAL_SHA256", false), taskNativeDigestCheck("TASK_EVENT_RECEIPT_BROKER", "BROKER_IDENTITY", false), taskNativeSequenceCheck("TASK_EVENT_RECEIPT_SEQUENCE", "BROKER_SEQUENCE", false), taskNativeSequenceCheck("TASK_EVENT_RECEIPT_CONSUMER_SEQUENCE", "CONSUMER_SEQUENCE", false), check("TASK_EVENT_RECEIPT_GENERATION", `"GENERATION">=1`), check("TASK_EVENT_RECEIPT_OUTCOME", `"OUTCOME" IN ('received','obsolete')`), check("TASK_EVENT_RECEIPT_STATE", `"TASK_STATE" IN ('pending','running','succeeded','failed','cancelled')`))
}
func taskDeadTable() table {
	broker := taskNativeDigest("COORDINATE_BROKER", true); broker.Virtual = `CASE WHEN "BROKER_SEQUENCE" IS NOT NULL THEN "BROKER_IDENTITY" ELSE NULL END`
	sequence := taskNativeSequence("COORDINATE_SEQUENCE", true); sequence.Virtual = `CASE WHEN "BROKER_SEQUENCE" IS NOT NULL THEN "BROKER_SEQUENCE" ELSE NULL END`
	consumer := text("COORDINATE_CONSUMER", 64, true); consumer.Virtual = `CASE WHEN "BROKER_SEQUENCE" IS NOT NULL THEN "CONSUMER_NAME" ELSE NULL END`
	return nativeTable("TASK_EVENT_DEAD_LETTERS", []column{
		uuid("ID", false), uuid("EVENT_ID", true), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", true), uuid("NODE_ID", true), uuid("TASK_ID", true), col("GENERATION", "INTEGER", 0, true, ""), uuid("DELIVERY_ID", true), text("KIND", 16, false), col("OUTCOME_UNKNOWN", "BIT", 0, false, ""), taskNativeDigest("BROKER_IDENTITY", false), taskNativeSequence("BROKER_SEQUENCE", true), text("CONSUMER_NAME", 64, false), taskNativeDigest("ENVELOPE_SHA256", false), text("ERROR_CODE", 64, false), uuid("RETRY_ACTOR", true), uuid("RETRY_KEY", true), text("RETRY_REASON", 500, true), col("RETRY_FINGERPRINT", "VARBINARY", 32, true, ""), uuid("RETRY_DELIVERY_ID", true), col("RETRY_GENERATION", "INTEGER", 0, true, ""), taskNativeStamp("RETRIED_AT", true, false), taskNativeStamp("CREATED_AT", false, false), broker, sequence, consumer,
	}, pk("TASK_EVENT_DEAD_PK", "ID"), unique("TASK_EVENT_DEAD_GENERATION_UK", "EVENT_ID", "GENERATION"), unique("TASK_EVENT_DEAD_DELIVERY_UK", "DELIVERY_ID"), unique("TASK_EVENT_DEAD_COORDINATE_UK", "COORDINATE_BROKER", "COORDINATE_SEQUENCE", "COORDINATE_CONSUMER"), unique("TASK_EVENT_DEAD_RETRY_UK", "RETRY_ACTOR", "RETRY_KEY"), fk("TASK_EVENT_DEAD_SCOPE_FK", "TASK_EVENT_OUTBOX", "TASK_EVENT_OUTBOX_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID", "EVENT_ID"), fk("TASK_EVENT_DEAD_ACTOR_FK", "USERS", "USERS_PK", "RETRY_ACTOR"),
	check("TASK_EVENT_DEAD_KIND", `"KIND" IN ('publish','consume','expiry','poison')`), check("TASK_EVENT_DEAD_UNKNOWN", `"OUTCOME_UNKNOWN" IN (0,1)`), taskNativeDigestCheck("TASK_EVENT_DEAD_BROKER", "BROKER_IDENTITY", false), taskNativeDigestCheck("TASK_EVENT_DEAD_DIGEST", "ENVELOPE_SHA256", false), taskNativeSequenceCheck("TASK_EVENT_DEAD_SEQUENCE", "BROKER_SEQUENCE", true), check("TASK_EVENT_DEAD_FINGERPRINT", `"RETRY_FINGERPRINT" IS NULL OR OCTET_LENGTH("RETRY_FINGERPRINT")=32`), check("TASK_EVENT_DEAD_ERROR", `"ERROR_CODE" IN `+taskNativeErrorCodes),
	check("TASK_EVENT_DEAD_SCOPE", `("EVENT_ID" IS NOT NULL AND "WORKSPACE_ID" IS NOT NULL AND "NODE_ID" IS NOT NULL AND "TASK_ID" IS NOT NULL AND "GENERATION" IS NOT NULL AND "GENERATION">=1 AND "DELIVERY_ID" IS NOT NULL) OR ("EVENT_ID" IS NULL AND "WORKSPACE_ID" IS NULL AND "NODE_ID" IS NULL AND "TASK_ID" IS NULL AND "GENERATION" IS NULL AND "DELIVERY_ID" IS NULL AND "KIND"='poison')`),
	check("TASK_EVENT_DEAD_RETRY", `("RETRY_ACTOR" IS NULL AND "RETRY_KEY" IS NULL AND "RETRY_REASON" IS NULL AND "RETRY_FINGERPRINT" IS NULL AND "RETRY_DELIVERY_ID" IS NULL AND "RETRY_GENERATION" IS NULL AND "RETRIED_AT" IS NULL) OR ("EVENT_ID" IS NOT NULL AND "RETRY_ACTOR" IS NOT NULL AND "RETRY_KEY" IS NOT NULL AND "RETRY_REASON" IS NOT NULL AND "RETRY_FINGERPRINT" IS NOT NULL AND "RETRY_DELIVERY_ID" IS NOT NULL AND "RETRY_GENERATION" IS NOT NULL AND "RETRY_GENERATION">"GENERATION" AND "RETRIED_AT" IS NOT NULL AND "RETRIED_AT">="CREATED_AT")`))
}

func middlewareInstanceTable() table {
	return nativeTable("MIDDLEWARE_INSTANCES", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("PROFILE_KEY", 63, false), text("KIND", 24, false), text("NAME", 100, false), version("VERSION"), taskNativeTextDefault("STATE", 16, "'active'"), taskNativeDigest("PROFILE_FINGERPRINT", false), uuid("CREATED_BY", false), uuid("CREATION_KEY", false), taskNativeDigest("CREATION_FINGERPRINT", false), taskNativeStamp("CREATED_AT", false, true), taskNativeStamp("UPDATED_AT", false, true),
	}, pk("MIDDLEWARE_INSTANCES_PK", "ID"), unique("MIDDLEWARE_INSTANCES_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("MIDDLEWARE_INSTANCES_PROFILE_UK", "TENANT_ID", "WORKSPACE_ID", "PROFILE_KEY"), unique("MIDDLEWARE_INSTANCES_INTENT_UK", "TENANT_ID", "WORKSPACE_ID", "CREATED_BY", "CREATION_KEY"), fk("MIDDLEWARE_INSTANCES_ACTOR_FK", "USERS", "USERS_PK", "CREATED_BY"), fk("MIDDLEWARE_INSTANCES_WORKSPACE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), check("MIDDLEWARE_INSTANCES_PROFILE", `REGEXP_LIKE("PROFILE_KEY",'^[a-z][a-z0-9_]{0,62}$','c')`), check("MIDDLEWARE_INSTANCES_KIND", `"KIND" IN ('platform_owned','customer_external')`), check("MIDDLEWARE_INSTANCES_NAME", `CHAR_LENGTH("NAME")>0`), bounded("MIDDLEWARE_INSTANCES_VERSION", "VERSION"), check("MIDDLEWARE_INSTANCES_STATE", `"STATE" IN ('active','archived')`), taskNativeDigestCheck("MIDDLEWARE_INSTANCES_PROFILE_HASH", "PROFILE_FINGERPRINT", false), taskNativeDigestCheck("MIDDLEWARE_INSTANCES_CREATION_HASH", "CREATION_FINGERPRINT", false))
}
func middlewareOperationTable() table {
	return nativeTable("MIDDLEWARE_OPERATIONS", []column{
		uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), uuid("INSTANCE_ID", false), text("KIND", 24, false), taskNativeTextDefault("STATE", 16, "'queued'"), uuid("REQUESTED_BY", false), uuid("INTENT_KEY", false), taskNativeDigest("INTENT_FINGERPRINT", false), col("INPUT_JSON", "CLOB", 0, false, ""), col("ACTOR_JSON", "CLOB", 0, false, ""), col("INSTANCE_VERSION", "BIGINT", 0, false, ""), taskNativeDigest("PROFILE_FINGERPRINT", false), uuid("REQUEST_ID", false), uuid("ATTEMPT_ID", true), taskNativeStamp("LEASE_EXPIRES_AT", true, false), col("RESULT_JSON", "CLOB", 0, true, ""), taskNativeDigest("RESULT_SHA256", true), text("ERROR_CODE", 96, true), taskNativeStamp("CREATED_AT", false, true), taskNativeStamp("UPDATED_AT", false, true), taskNativeStamp("COMPLETED_AT", true, false),
	}, pk("MIDDLEWARE_OPERATIONS_PK", "ID"), unique("MIDDLEWARE_OPERATIONS_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "ID"), unique("MIDDLEWARE_OPERATIONS_INTENT_UK", "TENANT_ID", "WORKSPACE_ID", "REQUESTED_BY", "INTENT_KEY"), fk("MIDDLEWARE_OPERATIONS_ACTOR_FK", "USERS", "USERS_PK", "REQUESTED_BY"), fk("MIDDLEWARE_OPERATIONS_INSTANCE_FK", "MIDDLEWARE_INSTANCES", "MIDDLEWARE_INSTANCES_SCOPE_UK", "TENANT_ID", "WORKSPACE_ID", "INSTANCE_ID"), check("MIDDLEWARE_OPERATIONS_KIND", `"KIND" IN ('health','backup','isolated_restore','reconcile')`), check("MIDDLEWARE_OPERATIONS_STATE", `"STATE" IN ('queued','running','succeeded','failed','unknown')`), taskNativeDigestCheck("MIDDLEWARE_OPERATIONS_INTENT_HASH", "INTENT_FINGERPRINT", false), taskNativeDigestCheck("MIDDLEWARE_OPERATIONS_PROFILE_HASH", "PROFILE_FINGERPRINT", false), taskNativeDigestCheck("MIDDLEWARE_OPERATIONS_RESULT_HASH", "RESULT_SHA256", true), taskNativeJSON("MIDDLEWARE_OPERATIONS_INPUT", "INPUT_JSON", "65536", false), taskNativeJSON("MIDDLEWARE_OPERATIONS_ACTOR", "ACTOR_JSON", "65536", false), taskNativeJSON("MIDDLEWARE_OPERATIONS_RESULT", "RESULT_JSON", "16777216", true), bounded("MIDDLEWARE_OPERATIONS_VERSION", "INSTANCE_VERSION"), check("MIDDLEWARE_OPERATIONS_LEASE", `("STATE"='running' AND "ATTEMPT_ID" IS NOT NULL AND "LEASE_EXPIRES_AT" IS NOT NULL) OR ("STATE"<>'running' AND "LEASE_EXPIRES_AT" IS NULL)`), check("MIDDLEWARE_OPERATIONS_SUCCESS", `("STATE"='succeeded' AND "RESULT_JSON" IS NOT NULL AND "RESULT_SHA256" IS NOT NULL AND "ERROR_CODE" IS NULL AND "COMPLETED_AT" IS NOT NULL) OR ("STATE"<>'succeeded' AND "RESULT_JSON" IS NULL AND "RESULT_SHA256" IS NULL)`), check("MIDDLEWARE_OPERATIONS_FAILURE", `("STATE" IN ('failed','unknown') AND "ERROR_CODE" IS NOT NULL AND "COMPLETED_AT" IS NOT NULL) OR ("STATE" NOT IN ('failed','unknown') AND "ERROR_CODE" IS NULL)`), check("MIDDLEWARE_OPERATIONS_QUEUED", `"STATE"<>'queued' OR ("ATTEMPT_ID" IS NULL AND "COMPLETED_AT" IS NULL)`))
}

func taskNativeChanged(fields []string) string {
	out := make([]string, 0, len(fields))
	for _, name := range fields {
		a, b := ":NEW."+name, ":OLD."+name
		out = append(out, "("+a+"<>"+b+" OR ("+a+" IS NULL AND "+b+" IS NOT NULL) OR ("+a+" IS NOT NULL AND "+b+" IS NULL))")
	}
	return strings.Join(out, " OR ")
}

// TaskDeliveryNativeGuard is an immutable-by-copy projection for runtime schema
// verification. Migration and readiness share exact native definitions, not two
// independently maintained SQL files. It grants no DDL or migration capability.
type TaskDeliveryNativeGuard struct { Name, Table, Event, Body string }
func TaskDeliveryNativeGuards() []TaskDeliveryNativeGuard {
	out := []TaskDeliveryNativeGuard{}
	for _, t := range taskNativeTriggers() { out = append(out, TaskDeliveryNativeGuard{t.Name, t.Table, t.Event, t.Body}) }
	return out
}
func TaskDeliveryNativeGuardMatches(definition, schema string, guard TaskDeliveryNativeGuard) bool {
	if !identifier.MatchString(schema) { return false }
	for _, t := range taskNativeTriggers() {
		if t.Name == guard.Name && t.Table == guard.Table && t.Event == guard.Event && t.Body == guard.Body {
			return triggerDefinitionMatches(definition, schema, t)
		}
	}
	return false
}
func taskNativeTriggers() []trigger {
	stampText := func(value string) string { return `RTRIM(RTRIM(TO_CHAR(`+value+`,'YYYY-MM-DD"T"HH24:MI:SS.FF6'),'0'),'.')||'Z'` }
	// All interpolated values are native UUIDs, enums and UTC timestamps. Comparing
	// exact ASCII bytes also rejects duplicate keys, alternate JSON spellings,
	// unknown fields and noncanonical time precision before data is admitted.
	canonical := `'{"schema":1,"event_id":"'||:NEW.ID||'","namespace_id":"'||:NEW.NAMESPACE_ID||'","tenant_id":"'||:NEW.TENANT_ID||'","workspace_id":"'||:NEW.WORKSPACE_ID||'","node_id":"'||:NEW.NODE_ID||'","task_id":"'||:NEW.TASK_ID||'","kind":"'||:NEW.EVENT_KIND||'","operation":"'||:NEW.OPERATION||'","task_created_at":"'||`+stampText("source_time")+`||'","created_at":"'||`+stampText(":NEW.CREATED_AT")+`||'"}'`
	immutableOutbox := taskNativeChanged([]string{"ID", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID", "NAMESPACE_ID", "EVENT_KIND", "OPERATION", "EVENT_BYTES", "LOGICAL_SHA256", "CREATED_AT", "EXPIRES_AT", "BROKER_IDENTITY", "CONSUMER_NAME"})
	immutableDead := taskNativeChanged([]string{"ID", "EVENT_ID", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "TASK_ID", "GENERATION", "DELIVERY_ID", "KIND", "OUTCOME_UNKNOWN", "BROKER_IDENTITY", "BROKER_SEQUENCE", "CONSUMER_NAME", "ENVELOPE_SHA256", "ERROR_CODE", "CREATED_AT"})
	retainedParent := `SELECT COUNT(*) INTO source_count FROM TASK_EVENT_OUTBOX o JOIN NODE_TASKS t ON t.TENANT_ID=o.TENANT_ID AND t.WORKSPACE_ID=o.WORKSPACE_ID AND t.NODE_ID=o.NODE_ID AND t.ID=o.TASK_ID WHERE o.ID=:OLD.EVENT_ID AND o.OUTCOME_UNKNOWN=0 AND o.CLAIM_ID IS NULL AND o.STATE IN ('received','obsolete','dead') AND o.EXPIRES_AT<=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) AND t.STATE IN ('succeeded','failed','cancelled');`
	return []trigger{
		{Name: "TASK_EVENT_OUTBOX_INSERT", Table: "TASK_EVENT_OUTBOX", Event: "INSERT", Timing: "BEFORE EACH ROW", Body: `DECLARE
 source_time TIMESTAMP(6); source_operation VARCHAR(64); expected_bytes VARBINARY(4096);
BEGIN
 SELECT t.CREATED_AT,t.OPERATION INTO source_time,source_operation FROM NODE_TASKS t JOIN MANAGED_NODES n ON n.TENANT_ID=t.TENANT_ID AND n.WORKSPACE_ID=t.WORKSPACE_ID AND n.ID=t.NODE_ID JOIN WORKSPACES w ON w.TENANT_ID=n.TENANT_ID AND w.ID=n.WORKSPACE_ID WHERE t.TENANT_ID=:NEW.TENANT_ID AND t.WORKSPACE_ID=:NEW.WORKSPACE_ID AND t.NODE_ID=:NEW.NODE_ID AND t.ID=:NEW.TASK_ID AND t.STATE='pending' AND t.ATTEMPT=0 AND n.STATUS='active' AND w.STATUS='active';
 IF source_time IS NULL OR source_operation<>:NEW.OPERATION OR :NEW.GENERATION<>1 OR :NEW.STATE<>'prepared' OR :NEW.OUTCOME_UNKNOWN<>0 OR :NEW.PUBLISH_ATTEMPTS<>0 OR :NEW.CONSUME_ATTEMPTS<>0 OR :NEW.CLAIM_ID IS NOT NULL OR :NEW.BROKER_SEQUENCE IS NOT NULL OR :NEW.ERROR_CODE IS NOT NULL OR :NEW.NOT_BEFORE<>:NEW.CREATED_AT OR :NEW.UPDATED_AT<>:NEW.CREATED_AT OR :NEW.CREATED_AT<source_time THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_SOURCE_INVALID'); END IF;
 IF :NEW.OPERATION NOT IN ('collect_facts.v1','node_exporter.install.v1','node_exporter.start.v1','node_exporter.stop.v1','node_exporter.remove.v1','node_exporter.observe.v1','node_exporter.configure.v1','node_exporter.restart.v1','node_exporter.upgrade.v1','node_exporter.rollback.v1','node_exporter.logs.v1') THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_SOURCE_INVALID'); END IF;
 expected_bytes := UTL_RAW.CAST_TO_RAW(`+canonical+`);
 IF expected_bytes IS NULL OR :NEW.EVENT_BYTES<>expected_bytes OR LOWER(RAWTOHEX(DBMS_CRYPTO.HASH(:NEW.EVENT_BYTES,DBMS_CRYPTO.HASH_SH256)))<>:NEW.LOGICAL_SHA256 THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_SOURCE_INVALID'); END IF;
END;`},
		{Name: "TASK_EVENT_OUTBOX_UPDATE", Table: "TASK_EVENT_OUTBOX", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: `DECLARE source_count INTEGER;
BEGIN
 IF `+immutableOutbox+` OR :NEW.UPDATED_AT<:OLD.UPDATED_AT OR :OLD.STATE IN ('received','obsolete') THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_IMMUTABLE'); END IF;
 IF :NEW.GENERATION<>:OLD.GENERATION THEN
  SELECT COUNT(*) INTO source_count FROM TASK_EVENT_DEAD_LETTERS d WHERE d.EVENT_ID=:OLD.ID AND d.GENERATION=:OLD.GENERATION AND d.RETRY_GENERATION=:NEW.GENERATION AND d.RETRY_DELIVERY_ID=:NEW.DELIVERY_ID AND d.RETRIED_AT=:NEW.UPDATED_AT;
  IF :NEW.GENERATION<>:OLD.GENERATION+1 OR :NEW.DELIVERY_ID=:OLD.DELIVERY_ID OR :OLD.STATE<>'dead' OR :NEW.STATE<>'prepared' OR :NEW.OUTCOME_UNKNOWN<>0 OR :NEW.PUBLISH_ATTEMPTS<>0 OR :NEW.CONSUME_ATTEMPTS<>0 OR :NEW.BROKER_SEQUENCE IS NOT NULL OR :NEW.CLAIM_ID IS NOT NULL OR :NEW.ERROR_CODE IS NOT NULL OR :NEW.NOT_BEFORE<>:NEW.UPDATED_AT OR source_count<>1 THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_RETRY_INVALID'); END IF;
 ELSE
  IF :NEW.DELIVERY_ID<>:OLD.DELIVERY_ID OR :NEW.PUBLISH_ATTEMPTS<:OLD.PUBLISH_ATTEMPTS OR :NEW.CONSUME_ATTEMPTS<:OLD.CONSUME_ATTEMPTS OR NOT ((:OLD.STATE='prepared' AND :NEW.STATE IN ('prepared','sent','dead')) OR (:OLD.STATE='sent' AND :NEW.STATE IN ('sent','published','received','obsolete','dead')) OR (:OLD.STATE='published' AND :NEW.STATE IN ('published','received','obsolete','dead')) OR (:OLD.STATE='dead' AND :NEW.STATE IN ('dead','received','obsolete'))) THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_TRANSITION_INVALID'); END IF;
 END IF;
 IF :NEW.STATE IN ('received','obsolete') THEN
  SELECT COUNT(*) INTO source_count FROM TASK_EVENT_RECEIPTS r WHERE r.EVENT_ID=:NEW.ID AND r.LOGICAL_SHA256=:NEW.LOGICAL_SHA256 AND r.DELIVERY_ID=:NEW.DELIVERY_ID AND r.GENERATION=:NEW.GENERATION AND r.BROKER_IDENTITY=:NEW.BROKER_IDENTITY AND r.CONSUMER_NAME=:NEW.CONSUMER_NAME AND r.BROKER_SEQUENCE=:NEW.BROKER_SEQUENCE AND r.OUTCOME=:NEW.STATE;
  IF source_count<>1 THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_RECEIPT_INVALID'); END IF;
 END IF;
 IF :NEW.STATE='dead' THEN
  SELECT COUNT(*) INTO source_count FROM TASK_EVENT_DEAD_LETTERS d WHERE d.EVENT_ID=:NEW.ID AND d.GENERATION=:NEW.GENERATION AND d.ERROR_CODE=:NEW.ERROR_CODE;
  IF source_count<>1 THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_DLQ_INVALID'); END IF;
 END IF;
END;`},
		{Name: "TASK_EVENT_OUTBOX_DELETE", Table: "TASK_EVENT_OUTBOX", Event: "DELETE", Timing: "BEFORE EACH ROW", Body: `DECLARE source_count INTEGER;
BEGIN
 SELECT COUNT(*) INTO source_count FROM NODE_TASKS t WHERE t.TENANT_ID=:OLD.TENANT_ID AND t.WORKSPACE_ID=:OLD.WORKSPACE_ID AND t.NODE_ID=:OLD.NODE_ID AND t.ID=:OLD.TASK_ID AND t.STATE IN ('succeeded','failed','cancelled');
 IF :OLD.OUTCOME_UNKNOWN<>0 OR :OLD.CLAIM_ID IS NOT NULL OR :OLD.STATE NOT IN ('received','obsolete','dead') OR :OLD.EXPIRES_AT>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) OR source_count<>1 THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_RETENTION_REFUSED'); END IF;
END;`},
		{Name: "TASK_EVENT_RECEIPT_INSERT", Table: "TASK_EVENT_RECEIPTS", Event: "INSERT", Timing: "BEFORE EACH ROW", Body: `DECLARE source_count INTEGER;
BEGIN
 SELECT COUNT(*) INTO source_count FROM TASK_EVENT_OUTBOX o JOIN NODE_TASKS t ON t.TENANT_ID=o.TENANT_ID AND t.WORKSPACE_ID=o.WORKSPACE_ID AND t.NODE_ID=o.NODE_ID AND t.ID=o.TASK_ID WHERE o.ID=:NEW.EVENT_ID AND o.TENANT_ID=:NEW.TENANT_ID AND o.WORKSPACE_ID=:NEW.WORKSPACE_ID AND o.NODE_ID=:NEW.NODE_ID AND o.TASK_ID=:NEW.TASK_ID AND o.LOGICAL_SHA256=:NEW.LOGICAL_SHA256 AND o.DELIVERY_ID=:NEW.DELIVERY_ID AND o.GENERATION=:NEW.GENERATION AND o.BROKER_IDENTITY=:NEW.BROKER_IDENTITY AND o.CONSUMER_NAME=:NEW.CONSUMER_NAME AND o.STATE IN ('sent','published','dead') AND o.CREATED_AT<=:NEW.CREATED_AT AND t.STATE=:NEW.TASK_STATE;
 IF source_count<>1 THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_RECEIPT_INVALID'); END IF;
END;`},
		{Name: "TASK_EVENT_RECEIPT_UPDATE", Table: "TASK_EVENT_RECEIPTS", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: `BEGIN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_RECEIPT_IMMUTABLE'); END;`},
		{Name: "TASK_EVENT_RECEIPT_DELETE", Table: "TASK_EVENT_RECEIPTS", Event: "DELETE", Timing: "BEFORE EACH ROW", Body: `DECLARE source_count INTEGER; BEGIN `+retainedParent+` IF source_count<>1 THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_RETENTION_REFUSED'); END IF; END;`},
		{Name: "TASK_EVENT_DEAD_LETTER_INSERT", Table: "TASK_EVENT_DEAD_LETTERS", Event: "INSERT", Timing: "BEFORE EACH ROW", Body: `DECLARE source_count INTEGER;
BEGIN
 IF :NEW.RETRY_KEY IS NOT NULL THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_RETRY_INVALID'); END IF;
 IF :NEW.EVENT_ID IS NOT NULL THEN
  SELECT COUNT(*) INTO source_count FROM TASK_EVENT_OUTBOX o WHERE o.ID=:NEW.EVENT_ID AND o.TENANT_ID=:NEW.TENANT_ID AND o.WORKSPACE_ID=:NEW.WORKSPACE_ID AND o.NODE_ID=:NEW.NODE_ID AND o.TASK_ID=:NEW.TASK_ID AND o.GENERATION=:NEW.GENERATION AND o.DELIVERY_ID=:NEW.DELIVERY_ID AND o.BROKER_IDENTITY=:NEW.BROKER_IDENTITY AND o.CONSUMER_NAME=:NEW.CONSUMER_NAME AND o.STATE NOT IN ('received','obsolete') AND o.CREATED_AT<=:NEW.CREATED_AT AND :NEW.ENVELOPE_SHA256=LOWER(RAWTOHEX(DBMS_CRYPTO.HASH(UTL_RAW.CAST_TO_RAW('{"delivery_id":"'||o.DELIVERY_ID||'","event":'||UTL_RAW.CAST_TO_VARCHAR2(o.EVENT_BYTES)||'}'),DBMS_CRYPTO.HASH_SH256)));
  IF source_count<>1 THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_DLQ_INVALID'); END IF;
 ELSIF :NEW.KIND<>'poison' OR :NEW.BROKER_SEQUENCE IS NULL OR :NEW.OUTCOME_UNKNOWN<>0 THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_DLQ_INVALID'); END IF;
END;`},
		{Name: "TASK_EVENT_DEAD_LETTER_UPDATE", Table: "TASK_EVENT_DEAD_LETTERS", Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: `DECLARE actor_count INTEGER; source_count INTEGER;
BEGIN
 SELECT COUNT(*) INTO actor_count FROM USERS u WHERE u.ID=:NEW.RETRY_ACTOR AND u.STATUS='active' AND u.IS_PLATFORM_ADMIN=1;
 SELECT COUNT(*) INTO source_count FROM TASK_EVENT_OUTBOX o WHERE o.ID=:OLD.EVENT_ID AND o.GENERATION=:OLD.GENERATION AND o.STATE='dead' AND o.EXPIRES_AT>:NEW.RETRIED_AT;
 IF `+immutableDead+` OR :OLD.RETRY_KEY IS NOT NULL OR :NEW.RETRY_KEY IS NULL OR :NEW.RETRY_GENERATION<>:OLD.GENERATION+1 OR actor_count<>1 OR source_count<>1 THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_RETRY_INVALID'); END IF;
END;`},
		{Name: "TASK_EVENT_DEAD_LETTER_DELETE", Table: "TASK_EVENT_DEAD_LETTERS", Event: "DELETE", Timing: "BEFORE EACH ROW", Body: `DECLARE source_count INTEGER; BEGIN IF :OLD.EVENT_ID IS NOT NULL THEN `+retainedParent+` IF source_count<>1 THEN RAISE_APPLICATION_ERROR(-20001,'TASK_EVENT_RETENTION_REFUSED'); END IF; END IF; END;`},
	}
}
