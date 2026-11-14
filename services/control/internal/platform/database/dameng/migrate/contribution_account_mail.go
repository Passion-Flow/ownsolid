package migrate

// accountMailFragment extends the existing fourteen-table account foundation.
// It does not recreate those tables, weaken their scope FKs, seed a second
// installation, or duplicate account_license_v2's SSO/preferences/license state.
// Registration and step numbers belong to the coordinator's central catalog.
func accountMailFragment(current map[string]table) schemaFragment {
	f := schemaFragment{FormatVersion: 1, ID: "account_mail_v2", Requires: []string{"foundation_v1"}}
	// The common catalog validates each referenced table before registration.
	// No table-shape changes are needed: all columns and keys already exist.
	_ = current
	indexStep := func(name, table string, columns []string, descending []bool) {
		i := index{Name: name, Table: table, Columns: columns, Descending: descending}
		f.Changes = append(f.Changes, schemaChange{ID: name, Kind: "index", SQL: i.ddl(), Index: &i})
	}
	indexStep("WORKSPACES_SCOPE_PAGE_IDX", "WORKSPACES", []string{"TENANT_ID", "CREATED_AT", "ID"}, []bool{false, true, true})
	indexStep("MEMBERSHIPS_SCOPE_PAGE_IDX", "WORKSPACE_MEMBERSHIPS", []string{"TENANT_ID", "WORKSPACE_ID", "CREATED_AT", "USER_ID"}, []bool{false, false, true, true})
	indexStep("MAIL_INVITATION_LATEST_IDX", "MAIL_DELIVERIES", []string{"INVITATION_ID", "CREATED_AT", "ID"}, []bool{false, true, true})
	indexStep("MAIL_USER_RESET_STATE_IDX", "MAIL_DELIVERIES", []string{"USER_ID", "PURPOSE", "STATE", "ID"}, []bool{false, false, false, false})
	indexStep("MAIL_LEASE_RECOVERY_IDX", "MAIL_DELIVERIES", []string{"STATE", "LOCKED_UNTIL", "CREATED_AT", "ID"}, []bool{false, false, false, false})
	triggerStep := func(name, table, body string) {
		t := trigger{Name: name, Table: table, Event: "UPDATE", Timing: "BEFORE EACH ROW", Body: body}
		f.Changes = append(f.Changes, schemaChange{ID: name, Kind: "trigger", SQL: "CREATE TRIGGER " + quoted(name) + " BEFORE UPDATE ON " + quoted(table) + " FOR EACH ROW " + body, Trigger: &t})
	}
	// Membership revocation deletes a row; rejoining supplies a new authority
	// UUID. Role/version changes must never transfer an existing authority.
	triggerStep("MEMBERSHIP_IDENTITY_GUARD", "WORKSPACE_MEMBERSHIPS", `BEGIN IF :NEW."USER_ID"<>:OLD."USER_ID" OR :NEW."TENANT_ID"<>:OLD."TENANT_ID" OR :NEW."WORKSPACE_ID"<>:OLD."WORKSPACE_ID" OR :NEW."AUTHORITY_ID"<>:OLD."AUTHORITY_ID" OR :NEW."CREATED_AT"<>:OLD."CREATED_AT" THEN RAISE_APPLICATION_ERROR(-20011,'MEMBERSHIP_IDENTITY_IMMUTABLE'); END IF; END;`)
	// Notes are scoped by their original tenant/workspace and author. Tombstones
	// and text/title/version updates remain allowed; a note cannot be relocated.
	triggerStep("WORKSPACE_NOTE_IDENTITY_GUARD", "WORKSPACE_NOTES", `BEGIN IF :NEW."ID"<>:OLD."ID" OR :NEW."TENANT_ID"<>:OLD."TENANT_ID" OR :NEW."WORKSPACE_ID"<>:OLD."WORKSPACE_ID" OR :NEW."AUTHOR_USER_ID"<>:OLD."AUTHOR_USER_ID" OR :NEW."CREATED_AT"<>:OLD."CREATED_AT" THEN RAISE_APPLICATION_ERROR(-20012,'WORKSPACE_NOTE_IDENTITY_IMMUTABLE'); END IF; END;`)
	// The recorded event is append-only. The existing retention owner may still
	// delete expired events; this guard adds no undeletable retention state.
	triggerStep("AUDIT_EVENT_UPDATE_GUARD", "AUDIT_EVENTS", `BEGIN RAISE_APPLICATION_ERROR(-20013,'AUDIT_EVENT_IMMUTABLE'); END;`)
	// Delivery retries retain recipient/source/message identity. Lease, payload
	// erasure, attempts, state and scheduling remain mutable within owner SQL.
	triggerStep("MAIL_DELIVERY_IDENTITY_GUARD", "MAIL_DELIVERIES", `BEGIN IF :NEW."ID"<>:OLD."ID" OR :NEW."PURPOSE"<>:OLD."PURPOSE" OR :NEW."RECIPIENT_EMAIL"<>:OLD."RECIPIENT_EMAIL" OR :NEW."LOCALE"<>:OLD."LOCALE" OR :NEW."MESSAGE_ID"<>:OLD."MESSAGE_ID" OR :NEW."PAYLOAD_SCHEMA_VERSION"<>:OLD."PAYLOAD_SCHEMA_VERSION" OR :NEW."CREATED_AT"<>:OLD."CREATED_AT" OR :NEW."INVITATION_ID"<>:OLD."INVITATION_ID" OR (:NEW."INVITATION_ID" IS NULL AND :OLD."INVITATION_ID" IS NOT NULL) OR (:NEW."INVITATION_ID" IS NOT NULL AND :OLD."INVITATION_ID" IS NULL) OR :NEW."INVITATION_GENERATION"<>:OLD."INVITATION_GENERATION" OR (:NEW."INVITATION_GENERATION" IS NULL AND :OLD."INVITATION_GENERATION" IS NOT NULL) OR (:NEW."INVITATION_GENERATION" IS NOT NULL AND :OLD."INVITATION_GENERATION" IS NULL) OR :NEW."RESET_TOKEN_ID"<>:OLD."RESET_TOKEN_ID" OR (:NEW."RESET_TOKEN_ID" IS NULL AND :OLD."RESET_TOKEN_ID" IS NOT NULL) OR (:NEW."RESET_TOKEN_ID" IS NOT NULL AND :OLD."RESET_TOKEN_ID" IS NULL) OR :NEW."USER_ID"<>:OLD."USER_ID" OR (:NEW."USER_ID" IS NULL AND :OLD."USER_ID" IS NOT NULL) OR (:NEW."USER_ID" IS NOT NULL AND :OLD."USER_ID" IS NULL) THEN RAISE_APPLICATION_ERROR(-20014,'MAIL_DELIVERY_IDENTITY_IMMUTABLE'); END IF; END;`)
	return f
}
