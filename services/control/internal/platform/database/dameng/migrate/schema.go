package migrate

import "fmt"

// All definitions below are native DM SQL contracts, independently authored
// from the actual M01/M02 schema. No PostgreSQL/MySQL text conversion is used.
func col(n, t string, w int, nullable bool, def string) column {
	return column{Name: n, Type: t, Width: w, Nullable: nullable, Default: def}
}
func text(n string, w int, nullable bool) column {
	c := col(n, "VARCHAR", w, nullable, "")
	c.Characters = true
	return c
}
func uuid(n string, nullable bool) column { return col(n, "CHAR", 36, nullable, "") }
func stamp(n string, nullable, defaultNow bool) column {
	d := ""
	if defaultNow {
		d = "CURRENT_TIMESTAMP(6)"
	}
	return col(n, "TIMESTAMP", 6, nullable, d)
}
func version(n string) column { return col(n, "BIGINT", 0, false, "1") }
func pk(name string, cols ...string) constraint {
	return constraint{Name: name, Kind: "P", Columns: cols}
}
func unique(name string, cols ...string) constraint {
	return constraint{Name: name, Kind: "U", Columns: cols}
}
func fk(name, target, key string, cols ...string) constraint {
	return constraint{Name: name, Kind: "R", Columns: cols, Reference: target, ReferenceKey: key}
}
func check(name, expr string) constraint { return constraint{Name: name, Kind: "C", Check: expr} }
func bounded(name, field string) constraint {
	return check(name, quoted(field)+" BETWEEN 1 AND 9007199254740991")
}
func locale(name string) constraint {
	return check(name, `REGEXP_LIKE("LOCALE", '^[a-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$', 'c')`)
}
func nativeTable(name string, columns []column, checks ...constraint) table {
	t := table{name, columns, checks}
	for _, c := range columns {
		if c.Type == "CHAR" && c.Width == 36 {
			t.Constraints = append(t.Constraints, check(name+"_"+c.Name+"_UUID", quoted(c.Name)+` IS NULL OR REGEXP_LIKE(`+quoted(c.Name)+`, '^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$', 'c')`))
		}
	}
	return t
}
func journalTable() table {
	return table{journalName, []column{col("STEP_NO", "INTEGER", 0, false, ""), text("SCOPE", 64, false), col("SOURCE_SHA256", "CHAR", 64, false, ""), text("OBJECT_NAME", 63, false), text("STATE", 16, false), stamp("STARTED_AT", false, true), stamp("APPLIED_AT", true, false)}, []constraint{pk("PO_SCHEMA_MIGRATIONS_PK", "STEP_NO"), check("PO_SCHEMA_MIGRATIONS_STEP", `"STEP_NO" BETWEEN 1 AND 256`), check("PO_SCHEMA_MIGRATIONS_STATE", `("STATE"='applying' AND "APPLIED_AT" IS NULL) OR ("STATE"='applied' AND "APPLIED_AT" IS NOT NULL)`), check("PO_SCHEMA_MIGRATIONS_HASH", `REGEXP_LIKE("SOURCE_SHA256", '^[a-f0-9]{64}$', 'c')`)}}
}

func foundationTables() []table {
	return []table{
		nativeTable("TENANTS", []column{uuid("ID", false), text("NAME", 100, false), text("STATUS", 16, false), stamp("CREATED_AT", false, true)}, pk("TENANTS_PK", "ID"), check("TENANTS_STATUS", `"STATUS" IN ('active','disabled')`)),
		nativeTable("WORKSPACES", []column{uuid("ID", false), uuid("TENANT_ID", false), text("NAME", 100, false), text("STATUS", 16, false), version("VERSION"), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true)}, pk("WORKSPACES_PK", "ID"), unique("WORKSPACES_TENANT_ID", "TENANT_ID", "ID"), unique("WORKSPACES_TENANT_NAME", "TENANT_ID", "NAME"), fk("WORKSPACES_TENANT_FK", "TENANTS", "TENANTS_PK", "TENANT_ID"), check("WORKSPACES_STATUS", `"STATUS" IN ('active','disabled')`), bounded("WORKSPACES_VERSION", "VERSION")),
		nativeTable("USERS", []column{uuid("ID", false), text("EMAIL_CANONICAL", 254, false), text("EMAIL_DISPLAY", 254, false), text("DISPLAY_NAME", 100, false), text("LOCALE", 35, false), text("STATUS", 16, false), col("IS_PLATFORM_ADMIN", "BIT", 0, false, "0"), stamp("EMAIL_VERIFIED_AT", true, false), version("AUTH_EPOCH"), version("VERSION"), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true)}, pk("USERS_PK", "ID"), unique("USERS_EMAIL_UK", "EMAIL_CANONICAL"), locale("USERS_LOCALE"), check("USERS_STATUS", `"STATUS" IN ('active','disabled')`), check("USERS_EMAIL_CANONICAL", `"EMAIL_CANONICAL"=LOWER("EMAIL_CANONICAL")`), bounded("USERS_AUTH_EPOCH", "AUTH_EPOCH"), bounded("USERS_VERSION", "VERSION")),
		nativeTable("PASSWORD_CREDENTIALS", []column{uuid("USER_ID", false), text("PASSWORD_PHC", 512, false), version("VERSION"), stamp("CHANGED_AT", false, true)}, pk("PASSWORD_CREDENTIALS_PK", "USER_ID"), fk("PASSWORD_CREDENTIALS_USER_FK", "USERS", "USERS_PK", "USER_ID"), bounded("PASSWORD_CREDENTIALS_VERSION", "VERSION")),
		nativeTable("WORKSPACE_MEMBERSHIPS", []column{uuid("USER_ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("ROLE", 16, false), uuid("AUTHORITY_ID", false), version("VERSION"), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true)}, pk("WORKSPACE_MEMBERSHIPS_PK", "USER_ID", "WORKSPACE_ID"), fk("WORKSPACE_MEMBERSHIPS_USER_FK", "USERS", "USERS_PK", "USER_ID"), fk("WORKSPACE_MEMBERSHIPS_SCOPE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), check("WORKSPACE_MEMBERSHIPS_ROLE", `"ROLE" IN ('owner','member','viewer')`), bounded("WORKSPACE_MEMBERSHIPS_VERSION", "VERSION")),
		nativeTable("INSTALLATION_STATE", []column{col("ID", "SMALLINT", 0, false, ""), col("BOOTSTRAP_COMPLETE", "BIT", 0, false, "0"), uuid("TENANT_ID", true), uuid("DEFAULT_WORKSPACE_ID", true), uuid("PRIMARY_ADMIN_USER_ID", true), stamp("BOOTSTRAPPED_AT", true, false)}, pk("INSTALLATION_STATE_PK", "ID"), check("INSTALLATION_STATE_SINGLETON", `"ID"=1`), fk("INSTALLATION_STATE_TENANT_FK", "TENANTS", "TENANTS_PK", "TENANT_ID"), fk("INSTALLATION_STATE_ADMIN_FK", "USERS", "USERS_PK", "PRIMARY_ADMIN_USER_ID"), fk("INSTALLATION_STATE_WORKSPACE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "DEFAULT_WORKSPACE_ID"), check("INSTALLATION_STATE_COMPLETE", `("BOOTSTRAP_COMPLETE"=1 AND "TENANT_ID" IS NOT NULL AND "DEFAULT_WORKSPACE_ID" IS NOT NULL AND "PRIMARY_ADMIN_USER_ID" IS NOT NULL AND "BOOTSTRAPPED_AT" IS NOT NULL) OR ("BOOTSTRAP_COMPLETE"=0 AND "TENANT_ID" IS NULL AND "DEFAULT_WORKSPACE_ID" IS NULL AND "PRIMARY_ADMIN_USER_ID" IS NULL AND "BOOTSTRAPPED_AT" IS NULL)`)),
		nativeTable("SESSIONS", []column{uuid("ID", false), uuid("USER_ID", false), text("AUDIENCE", 16, false), col("TOKEN_HASH", "VARBINARY", 32, false, ""), col("AUTH_EPOCH", "BIGINT", 0, false, ""), stamp("CREATED_AT", false, false), stamp("LAST_SEEN_AT", false, false), stamp("ABSOLUTE_EXPIRES_AT", false, false), stamp("REVOKED_AT", true, false), text("REVOKE_REASON", 48, true)}, pk("SESSIONS_PK", "ID"), unique("SESSIONS_TOKEN_UK", "TOKEN_HASH"), fk("SESSIONS_USER_FK", "USERS", "USERS_PK", "USER_ID"), check("SESSIONS_AUDIENCE", `"AUDIENCE" IN ('portal','admin')`), check("SESSIONS_TOKEN_LENGTH", `OCTET_LENGTH("TOKEN_HASH")=32`), bounded("SESSIONS_AUTH_EPOCH", "AUTH_EPOCH"), check("SESSIONS_EXPIRY", `"ABSOLUTE_EXPIRES_AT">"CREATED_AT" AND "LAST_SEEN_AT">="CREATED_AT"`), check("SESSIONS_REVOCATION", `("REVOKED_AT" IS NULL AND "REVOKE_REASON" IS NULL) OR ("REVOKED_AT" IS NOT NULL AND "REVOKE_REASON" IS NOT NULL)`)),
		invitationTable(), resetTable(), mailTable(), auditTable(),
		nativeTable("RATE_LIMIT_BUCKETS", []column{col("BUCKET_KEY", "VARBINARY", 32, false, ""), stamp("WINDOW_STARTED_AT", false, false), stamp("EXPIRES_AT", false, false), col("ATTEMPT_COUNT", "INTEGER", 0, false, ""), stamp("LAST_ADMITTED_AT", true, false)}, pk("RATE_LIMIT_BUCKETS_PK", "BUCKET_KEY"), check("RATE_LIMIT_BUCKETS_KEY", `OCTET_LENGTH("BUCKET_KEY")=32`), check("RATE_LIMIT_BUCKETS_WINDOW", `"EXPIRES_AT">"WINDOW_STARTED_AT" AND "ATTEMPT_COUNT">=0`)),
		nativeTable("IDEMPOTENCY_RECORDS", []column{uuid("ACTOR_USER_ID", false), text("OPERATION_ID", 64, false), uuid("IDEMPOTENCY_KEY", false), col("REQUEST_HASH", "VARBINARY", 32, false, ""), col("RESPONSE_STATUS", "INTEGER", 0, false, ""), col("RESPONSE_BODY", "CLOB", 0, false, ""), stamp("CREATED_AT", false, false), stamp("EXPIRES_AT", false, false)}, pk("IDEMPOTENCY_RECORDS_PK", "ACTOR_USER_ID", "OPERATION_ID", "IDEMPOTENCY_KEY"), fk("IDEMPOTENCY_RECORDS_ACTOR_FK", "USERS", "USERS_PK", "ACTOR_USER_ID"), check("IDEMPOTENCY_RECORDS_OPERATION", `"OPERATION_ID" IN ('createInvitation','resendInvitation')`), check("IDEMPOTENCY_RECORDS_HASH", `OCTET_LENGTH("REQUEST_HASH")=32`), check("IDEMPOTENCY_RECORDS_RESPONSE", `"RESPONSE_STATUS" IN (200,201) AND JSON_TYPE("RESPONSE_BODY") IS NOT NULL AND JSON_TYPE("RESPONSE_BODY")='OBJECT' AND OCTET_LENGTH("RESPONSE_BODY")<=16384`), check("IDEMPOTENCY_RECORDS_EXPIRY", `"EXPIRES_AT">"CREATED_AT"`)),
		nativeTable("WORKSPACE_NOTES", []column{uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("TITLE", 100, false), col("TEXT", "CLOB", 0, false, ""), uuid("AUTHOR_USER_ID", false), version("VERSION"), stamp("CREATED_AT", false, true), stamp("UPDATED_AT", false, true), stamp("DELETED_AT", true, false)}, pk("WORKSPACE_NOTES_PK", "ID"), fk("WORKSPACE_NOTES_AUTHOR_FK", "USERS", "USERS_PK", "AUTHOR_USER_ID"), fk("WORKSPACE_NOTES_SCOPE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), bounded("WORKSPACE_NOTES_VERSION", "VERSION"), check("WORKSPACE_NOTES_TEXT", `OCTET_LENGTH("TEXT")<=8192`)),
	}
}
func invitationTable() table {
	pending := text("PENDING_EMAIL", 254, true)
	pending.Virtual = `CASE WHEN "STATUS"='pending' THEN "EMAIL_CANONICAL" ELSE NULL END`
	role := text("ROLE", 16, false)
	role.Default = "'member'"
	return nativeTable("INVITATIONS", []column{uuid("ID", false), uuid("TENANT_ID", false), uuid("WORKSPACE_ID", false), text("EMAIL_CANONICAL", 254, false), text("LOCALE", 35, false), role, text("STATUS", 16, false), col("TOKEN_HASH", "VARBINARY", 32, true, ""), col("TOKEN_GENERATION", "INTEGER", 0, false, "1"), version("VERSION"), uuid("CREATED_BY", false), uuid("ACCEPTED_USER_ID", true), stamp("CREATED_AT", false, false), stamp("UPDATED_AT", false, false), stamp("EXPIRES_AT", false, false), stamp("ACCEPTED_AT", true, false), stamp("REVOKED_AT", true, false), pending}, pk("INVITATIONS_PK", "ID"), unique("INVITATIONS_TOKEN_UK", "TOKEN_HASH"), fk("INVITATIONS_CREATOR_FK", "USERS", "USERS_PK", "CREATED_BY"), fk("INVITATIONS_ACCEPTED_USER_FK", "USERS", "USERS_PK", "ACCEPTED_USER_ID"), fk("INVITATIONS_SCOPE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), locale("INVITATIONS_LOCALE"), bounded("INVITATIONS_VERSION", "VERSION"), check("INVITATIONS_ROLE", `"ROLE"='member'`), check("INVITATIONS_STATUS", `"STATUS" IN ('pending','accepted','revoked','expired')`), check("INVITATIONS_EMAIL", `"EMAIL_CANONICAL"=LOWER("EMAIL_CANONICAL")`), check("INVITATIONS_EXPIRY", `"EXPIRES_AT">"CREATED_AT" AND "TOKEN_GENERATION">=1`), check("INVITATIONS_TOKEN", `("STATUS"='pending' AND "TOKEN_HASH" IS NOT NULL AND OCTET_LENGTH("TOKEN_HASH")=32) OR ("STATUS"<>'pending' AND "TOKEN_HASH" IS NULL)`), check("INVITATIONS_ACCEPTED", `("STATUS"='accepted' AND "ACCEPTED_USER_ID" IS NOT NULL AND "ACCEPTED_AT" IS NOT NULL) OR ("STATUS"<>'accepted' AND "ACCEPTED_USER_ID" IS NULL AND "ACCEPTED_AT" IS NULL)`), check("INVITATIONS_REVOKED", `("STATUS"='revoked' AND "REVOKED_AT" IS NOT NULL) OR ("STATUS"<>'revoked' AND "REVOKED_AT" IS NULL)`))
}
func resetTable() table {
	return nativeTable("PASSWORD_RESET_TOKENS", []column{uuid("ID", false), uuid("USER_ID", false), text("AUDIENCE", 16, false), col("TOKEN_HASH", "VARBINARY", 32, true, ""), stamp("CREATED_AT", false, false), stamp("EXPIRES_AT", false, false), stamp("CONSUMED_AT", true, false), stamp("REVOKED_AT", true, false)}, pk("PASSWORD_RESET_TOKENS_PK", "ID"), unique("RESET_TOKENS_TOKEN_UK", "TOKEN_HASH"), unique("RESET_TOKENS_ID_USER", "ID", "USER_ID"), fk("RESET_TOKENS_USER_FK", "USERS", "USERS_PK", "USER_ID"), check("RESET_TOKENS_AUDIENCE", `"AUDIENCE" IN ('portal','admin')`), check("RESET_TOKENS_EXPIRY", `"EXPIRES_AT">"CREATED_AT"`), check("RESET_TOKENS_TERMINAL", `NOT ("CONSUMED_AT" IS NOT NULL AND "REVOKED_AT" IS NOT NULL)`), check("RESET_TOKENS_TOKEN", `("CONSUMED_AT" IS NULL AND "REVOKED_AT" IS NULL AND "TOKEN_HASH" IS NOT NULL AND OCTET_LENGTH("TOKEN_HASH")=32) OR (("CONSUMED_AT" IS NOT NULL OR "REVOKED_AT" IS NOT NULL) AND "TOKEN_HASH" IS NULL)`))
}
func mailTable() table {
	return nativeTable("MAIL_DELIVERIES", []column{uuid("ID", false), text("PURPOSE", 24, false), text("RECIPIENT_EMAIL", 254, false), text("LOCALE", 35, false), uuid("INVITATION_ID", true), col("INVITATION_GENERATION", "INTEGER", 0, true, ""), uuid("RESET_TOKEN_ID", true), uuid("USER_ID", true), text("STATE", 16, false), text("PAYLOAD_KEY_ID", 64, true), col("PAYLOAD_NONCE", "VARBINARY", 12, true, ""), col("PAYLOAD_CIPHERTEXT", "BLOB", 0, true, ""), col("PAYLOAD_SCHEMA_VERSION", "INTEGER", 0, false, "1"), col("ATTEMPT_COUNT", "INTEGER", 0, false, "0"), stamp("NEXT_ATTEMPT_AT", false, false), uuid("LEASE_TOKEN", true), stamp("LOCKED_UNTIL", true, false), text("ERROR_CATEGORY", 64, true), text("MESSAGE_ID", 254, false), stamp("CREATED_AT", false, false), stamp("UPDATED_AT", false, false), stamp("COMPLETED_AT", true, false)}, pk("MAIL_DELIVERIES_PK", "ID"), unique("MAIL_DELIVERIES_MESSAGE_UK", "MESSAGE_ID"), fk("MAIL_DELIVERIES_INVITATION_FK", "INVITATIONS", "INVITATIONS_PK", "INVITATION_ID"), fk("MAIL_DELIVERIES_USER_FK", "USERS", "USERS_PK", "USER_ID"), fk("MAIL_DELIVERIES_RESET_USER_FK", "PASSWORD_RESET_TOKENS", "RESET_TOKENS_ID_USER", "RESET_TOKEN_ID", "USER_ID"), locale("MAIL_DELIVERIES_LOCALE"), check("MAIL_DELIVERIES_POLICY", `"PAYLOAD_SCHEMA_VERSION"=1 AND "ATTEMPT_COUNT" BETWEEN 0 AND 5`), check("MAIL_DELIVERIES_STATE", `"STATE" IN ('queued','sending','sent','failed','cancelled')`), check("MAIL_DELIVERIES_PURPOSE", `("PURPOSE"='invitation' AND "INVITATION_ID" IS NOT NULL AND "INVITATION_GENERATION" IS NOT NULL AND "RESET_TOKEN_ID" IS NULL AND "USER_ID" IS NULL) OR ("PURPOSE"='password_reset' AND "RESET_TOKEN_ID" IS NOT NULL AND "INVITATION_ID" IS NULL AND "INVITATION_GENERATION" IS NULL AND "USER_ID" IS NOT NULL) OR ("PURPOSE"='password_changed' AND "USER_ID" IS NOT NULL AND "INVITATION_ID" IS NULL AND "INVITATION_GENERATION" IS NULL AND "RESET_TOKEN_ID" IS NULL)`), check("MAIL_DELIVERIES_PAYLOAD", `("STATE" IN ('queued','sending') AND "PAYLOAD_KEY_ID" IS NOT NULL AND "PAYLOAD_NONCE" IS NOT NULL AND OCTET_LENGTH("PAYLOAD_NONCE")=12 AND "PAYLOAD_CIPHERTEXT" IS NOT NULL AND "COMPLETED_AT" IS NULL) OR ("STATE" IN ('sent','failed','cancelled') AND "PAYLOAD_KEY_ID" IS NULL AND "PAYLOAD_NONCE" IS NULL AND "PAYLOAD_CIPHERTEXT" IS NULL AND "COMPLETED_AT" IS NOT NULL)`), check("MAIL_DELIVERIES_LEASE", `("STATE"='sending' AND "LEASE_TOKEN" IS NOT NULL AND "LOCKED_UNTIL" IS NOT NULL) OR ("STATE"<>'sending' AND "LEASE_TOKEN" IS NULL AND "LOCKED_UNTIL" IS NULL)`))
}
func auditTable() table {
	return nativeTable("AUDIT_EVENTS", []column{uuid("ID", false), stamp("OCCURRED_AT", false, false), uuid("ACTOR_USER_ID", true), uuid("SUBJECT_USER_ID", true), uuid("TENANT_ID", true), uuid("WORKSPACE_ID", true), text("AUDIENCE", 16, false), text("ACTION", 64, false), text("OUTCOME", 16, false), text("TARGET_TYPE", 48, true), uuid("TARGET_ID", true), uuid("REQUEST_ID", false), col("IDENTIFIER_DIGEST", "VARBINARY", 32, true, ""), text("REASON", 500, true), col("METADATA", "CLOB", 0, false, "'{}'")}, pk("AUDIT_EVENTS_PK", "ID"), fk("AUDIT_EVENTS_ACTOR_FK", "USERS", "USERS_PK", "ACTOR_USER_ID"), fk("AUDIT_EVENTS_SUBJECT_FK", "USERS", "USERS_PK", "SUBJECT_USER_ID"), fk("AUDIT_EVENTS_TENANT_FK", "TENANTS", "TENANTS_PK", "TENANT_ID"), fk("AUDIT_EVENTS_WORKSPACE_FK", "WORKSPACES", "WORKSPACES_TENANT_ID", "TENANT_ID", "WORKSPACE_ID"), check("AUDIT_EVENTS_AUDIENCE", `"AUDIENCE" IN ('portal','admin','operator','system')`), check("AUDIT_EVENTS_OUTCOME", `"OUTCOME" IN ('success','failure','accepted')`), check("AUDIT_EVENTS_SCOPE", `"WORKSPACE_ID" IS NULL OR "TENANT_ID" IS NOT NULL`), check("AUDIT_EVENTS_DIGEST", `"IDENTIFIER_DIGEST" IS NULL OR OCTET_LENGTH("IDENTIFIER_DIGEST")=32`), check("AUDIT_EVENTS_METADATA", `JSON_TYPE("METADATA") IS NOT NULL AND JSON_TYPE("METADATA")='OBJECT' AND OCTET_LENGTH("METADATA")<=4096`))
}
func foundationIndexes() []index {
	items := []index{
		{Name: "USERS_LIST_IDX", Table: "USERS", Columns: []string{"CREATED_AT", "ID"}, Descending: []bool{true, true}},
		{Name: "USERS_STATUS_LIST_IDX", Table: "USERS", Columns: []string{"STATUS", "CREATED_AT", "ID"}, Descending: []bool{false, true, true}},
		{Name: "MEMBERSHIPS_SCOPE_IDX", Table: "WORKSPACE_MEMBERSHIPS", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "USER_ID"}},
		{Name: "SESSIONS_USER_LIVE_IDX", Table: "SESSIONS", Columns: []string{"USER_ID", "REVOKED_AT"}},
		{Name: "SESSIONS_EXPIRY_IDX", Table: "SESSIONS", Columns: []string{"ABSOLUTE_EXPIRES_AT"}},
		{Name: "INVITATIONS_PENDING_EMAIL_IDX", Table: "INVITATIONS", Unique: true, Columns: []string{"PENDING_EMAIL"}},
		{Name: "INVITATIONS_LIST_IDX", Table: "INVITATIONS", Columns: []string{"CREATED_AT", "ID"}, Descending: []bool{true, true}},
		{Name: "RESET_TOKENS_USER_LIVE_IDX", Table: "PASSWORD_RESET_TOKENS", Columns: []string{"USER_ID", "CONSUMED_AT", "REVOKED_AT", "EXPIRES_AT"}},
		{Name: "MAIL_CLAIM_IDX", Table: "MAIL_DELIVERIES", Columns: []string{"STATE", "NEXT_ATTEMPT_AT"}},
		{Name: "MAIL_LIST_IDX", Table: "MAIL_DELIVERIES", Columns: []string{"CREATED_AT", "ID"}, Descending: []bool{true, true}},
		{Name: "AUDIT_LIST_IDX", Table: "AUDIT_EVENTS", Columns: []string{"OCCURRED_AT", "ID"}, Descending: []bool{true, true}},
		{Name: "AUDIT_ACTION_IDX", Table: "AUDIT_EVENTS", Columns: []string{"ACTION", "OCCURRED_AT", "ID"}, Descending: []bool{false, true, true}},
		{Name: "AUDIT_OPERATION_LOOKUP_IDX", Table: "AUDIT_EVENTS", Columns: []string{"REQUEST_ID", "ACTOR_USER_ID", "AUDIENCE", "TENANT_ID"}},
		{Name: "RATE_LIMIT_EXPIRY_IDX", Table: "RATE_LIMIT_BUCKETS", Columns: []string{"EXPIRES_AT"}},
		{Name: "IDEMPOTENCY_EXPIRY_IDX", Table: "IDEMPOTENCY_RECORDS", Columns: []string{"EXPIRES_AT"}},
		{Name: "NOTES_SCOPE_LIST_IDX", Table: "WORKSPACE_NOTES", Columns: []string{"TENANT_ID", "WORKSPACE_ID", "DELETED_AT", "CREATED_AT", "ID"}, Descending: []bool{false, false, false, true, true}},
	}
	for n := range items {
		if items[n].Descending == nil {
			items[n].Descending = make([]bool, len(items[n].Columns))
		}
	}
	return items
}

// Keep the fixed supported widths visible to metadata verification.
func typeName(c column) string {
	if c.Type == "TIMESTAMP" {
		return fmt.Sprintf("TIMESTAMP(%d)", c.Width)
	}
	return c.Type
}
