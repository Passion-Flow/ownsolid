//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// Schema fixtures contain synthetic provider/crypto bytes,not actual SSO proof.
func actualSSOTableMigration(t *testing.T) (*mysqlstore.Store, Catalog, mysqlstore.Configuration) {
	t.Helper()
	store, catalog, configuration := actualMigration(t)
	// MG03-A intentionally measures the immutable pre-trigger23-step contract.
	catalog.steps = catalog.steps[:23]
	return store, catalog, configuration
}

func ssoSchemaDigest(t *testing.T, store *mysqlstore.Store) string {
	t.Helper()
	values := []any{evolutionBusinessDigest(t, store)}
	for _, query := range []string{
		"SELECT JSON_ARRAY(id,tenant_id,name,provider,origin,client_id,secret_key_id,HEX(secret_nonce),HEX(secret_ciphertext),enabled,revision,auth_version,created_by,created_at,updated_at) FROM sso_connectors ORDER BY id",
		"SELECT JSON_ARRAY(id,tenant_id,connector_id,subject,user_id,active,revision,created_at,updated_at) FROM sso_bindings ORDER BY id",
		"SELECT JSON_ARRAY(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state,HEX(state_hash),HEX(browser_hash),HEX(previous_cookie_hash),previous_session_id,initiating_user_id,initiating_session_id,initiating_epoch,credential_version,reason,verifier_key_id,HEX(verifier_nonce),HEX(verifier_ciphertext),claim_id,authenticated_user_id,issued_session_id,error_code,created_at,expires_at,finished_at) FROM sso_flows ORDER BY id",
		"SELECT JSON_ARRAY(tenant_id,actor_user_id,idempotency_key,operation,target_id,HEX(fingerprint),result,created_at) FROM sso_mutation_intents ORDER BY tenant_id,actor_user_id,idempotency_key",
		"SELECT JSON_ARRAY(id,user_id,audience,HEX(token_hash),auth_epoch,created_at,last_seen_at,absolute_expires_at,revoked_at,revoke_reason,sso_tenant_id,sso_connector_id,sso_binding_id,sso_auth_version,sso_flow_id) FROM sessions ORDER BY id",
	} {
		rows, e := store.Pool().QueryContext(context.Background(), query)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		for rows.Next() {
			var raw []byte
			var value any
			if rows.Scan(&raw) != nil || json.Unmarshal(raw, &value) != nil {
				rows.Close()
				t.Fatal("actual SSO canonical typed row scan")
			}
			values = append(values, value)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
	}
	raw, e := json.Marshal(values)
	if e != nil {
		t.Fatal(e)
	}
	return hash(string(raw))
}

func ssoSchemaRefuses(t *testing.T, store *mysqlstore.Store, number uint16, query string, args ...any) {
	t.Helper()
	before := ssoSchemaDigest(t, store)
	_, e := store.Pool().ExecContext(context.Background(), query, args...)
	var actual *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &actual) || actual.Number != number {
		if actual == nil {
			t.Fatal("actual SSO schema refusal missing safe vendor metadata", mysqlstore.SafeError(e))
		}
		t.Fatalf("actual SSO schema refusal category: expected=%d actual=%d state=%s code=%s", number, actual.Number, string(actual.SQLState[:]), actual.Error())
	}
	if before != ssoSchemaDigest(t, store) {
		t.Fatal("rejected SSO mutation changed durable business state")
	}
}

func TestActualMySQLSSOSchemaUpgradeAndConstraints(t *testing.T) {
	store, catalog, _ := actualSSOTableMigration(t)
	prefix := catalog
	prefix.steps = catalog.steps[:18]
	migrationRun(t, store, prefix, Apply)
	seedEvolutionBusiness(t, store)
	ctx := context.Background()
	var tenant, user string
	if e := store.Pool().QueryRowContext(ctx, "SELECT tenant_id,primary_admin_user_id FROM installation_state WHERE id=1").Scan(&tenant, &user); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	ordinary := uuid.NewString()
	token := sha256.Sum256([]byte("Synthetic ordinary session " + uuid.NewString()))
	now := time.Date(2026, 10, 19, 4, 5, 6, 123456000, time.UTC)
	migrationExec(t, store, "INSERT INTO sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at) VALUES(?,?,'portal',?,1,?,?,?)", ordinary, user, token[:], now, now, now.Add(time.Hour))
	before := evolutionBusinessDigest(t, store)
	migrationRun(t, store, catalog, Apply)
	migrationRun(t, store, catalog, Readiness)
	if before != evolutionBusinessDigest(t, store) {
		t.Fatal("nonempty SSO schema upgrade changed original business rows")
	}
	var preserved int
	if e := store.Pool().QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id=? AND user_id=? AND token_hash=? AND created_at=? AND sso_tenant_id IS NULL AND sso_connector_id IS NULL AND sso_binding_id IS NULL AND sso_auth_version IS NULL AND sso_flow_id IS NULL", ordinary, user, token[:], now).Scan(&preserved); e != nil || preserved != 1 {
		t.Fatal("ordinary session identity/source changed", mysqlstore.SafeError(e))
	}
	otherTenant, otherUser := uuid.NewString(), uuid.NewString()
	migrationExec(t, store, "INSERT INTO tenants(id,name,status) VALUES(?,'Other synthetic tenant','active')", otherTenant)
	migrationExec(t, store, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,'other-sso@example.invalid','other-sso@example.invalid','Other synthetic user','en','active')", otherUser)
	connector, binding, flow, client := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	migrationExec(t, store, "INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by) VALUES(?,?,'Synthetic \u4e16\u754c🙂','gitea','https://provider.example.invalid',?,'synthetic',?,?,1,?)", connector, tenant, client, make([]byte, 12), make([]byte, 17), user)
	migrationExec(t, store, "INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES(?,?,?,'9223372036854775807',?)", binding, tenant, connector, user)
	stateHash, browserHash, cookieHash := sha256.Sum256([]byte(uuid.NewString())), sha256.Sum256([]byte(uuid.NewString())), sha256.Sum256([]byte(uuid.NewString()))
	migrationExec(t, store, "INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES(?,?,?,1,1,'portal','login',?,?,?,'synthetic',?,?,?,?)", flow, tenant, connector, stateHash[:], browserHash[:], cookieHash[:], make([]byte, 12), make([]byte, 59), now, now.Add(time.Hour))

	t.Run("connector_domain_and_crypto", func(t *testing.T) {
		for _, value := range []string{"", " ", "  "} {
			ssoSchemaRefuses(t, store, 3819, "UPDATE sso_connectors SET name=? WHERE id=?", value, connector)
		}
		for _, query := range []string{"UPDATE sso_connectors SET provider='other'", "UPDATE sso_connectors SET enabled=2", "UPDATE sso_connectors SET revision=0", "UPDATE sso_connectors SET auth_version=9007199254740992", "UPDATE sso_connectors SET secret_nonce=X'00'", "UPDATE sso_connectors SET secret_ciphertext=X'00'", "UPDATE sso_connectors SET client_id='AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA'"} {
			ssoSchemaRefuses(t, store, 3819, query)
		}
	})
	t.Run("subject_domain_and_scope", func(t *testing.T) {
		for _, subject := range []string{"", "0", "01", "-1", "+1", "1.0", "1 ", "1\n", "1\r", "9223372036854775808", "9999999999999999999"} {
			ssoSchemaRefuses(t, store, 3819, "UPDATE sso_bindings SET subject=? WHERE id=?", subject, binding)
		}
		ssoSchemaRefuses(t, store, 3988, "UPDATE sso_bindings SET subject=? WHERE id=?", "１２", binding)
		ssoSchemaRefuses(t, store, 1452, "UPDATE sso_bindings SET tenant_id=? WHERE id=?", otherTenant, binding)
		ssoSchemaRefuses(t, store, 1062, "INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES(?,?,?,'9223372036854775807',?)", uuid.NewString(), tenant, connector, otherUser)
	})
	t.Run("pending_flow_crypto_mode_and_time", func(t *testing.T) {
		for _, query := range []string{"UPDATE sso_flows SET verifier_key_id=NULL", "UPDATE sso_flows SET verifier_nonce=NULL", "UPDATE sso_flows SET verifier_nonce=X'00'", "UPDATE sso_flows SET verifier_ciphertext=X'00'", "UPDATE sso_flows SET claim_id='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'", "UPDATE sso_flows SET browser_hash=X'00'", "UPDATE sso_flows SET audience='other'", "UPDATE sso_flows SET mode='link'", "UPDATE sso_flows SET expires_at=created_at", "UPDATE sso_flows SET error_code='FAILED'"} {
			ssoSchemaRefuses(t, store, 3819, query)
		}
	})
	t.Run("link_presence_and_previous_session", func(t *testing.T) {
		tx, e := store.Pool().BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(ctx, "UPDATE sso_flows SET mode='link',previous_session_id=?,initiating_session_id=?,initiating_user_id=?,initiating_epoch=1,credential_version=1,reason='Synthetic explicit link' WHERE id=?", ordinary, ordinary, user, flow); e != nil {
			t.Fatal("complete link refused", mysqlstore.SafeError(e))
		}
		for _, query := range []string{"UPDATE sso_flows SET initiating_epoch=NULL", "UPDATE sso_flows SET credential_version=0", "UPDATE sso_flows SET initiating_session_id=NULL", "UPDATE sso_flows SET initiating_session_id='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'", "UPDATE sso_flows SET reason=' '"} {
			_, e = tx.ExecContext(ctx, query)
			var actual *mysqlstore.Error
			if !errors.As(mysqlstore.SafeError(e), &actual) || actual.Number != 3819 {
				t.Fatal("invalid link shape accepted", mysqlstore.SafeError(e))
			}
		}
	})
	claim := uuid.NewString()
	migrationExec(t, store, "UPDATE sso_flows SET state='claimed',claim_id=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=?", claim, flow)
	t.Run("claimed_and_terminal_shapes", func(t *testing.T) {
		for _, query := range []string{"UPDATE sso_flows SET claim_id=NULL", "UPDATE sso_flows SET state='succeeded',finished_at=expires_at", "UPDATE sso_flows SET state='failed',finished_at=expires_at", "UPDATE sso_flows SET state='failed',finished_at=expires_at,error_code='bad'", "UPDATE sso_flows SET state='failed',finished_at=expires_at,error_code='FAILED\n'", "UPDATE sso_flows SET state='expired',error_code='EXPIRED'"} {
			ssoSchemaRefuses(t, store, 3819, query)
		}
	})
	migrationExec(t, store, "UPDATE sso_flows SET state='succeeded',authenticated_user_id=?,finished_at=? WHERE id=?", user, now.Add(time.Minute), flow)
	ssosession := uuid.NewString()
	newToken := sha256.Sum256([]byte("Synthetic SSO session " + uuid.NewString()))
	migrationExec(t, store, "INSERT INTO sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at,sso_tenant_id,sso_connector_id,sso_binding_id,sso_auth_version,sso_flow_id) VALUES(?,?,'portal',?,1,?,?,?,?,?,?,1,?)", ssosession, user, newToken[:], now, now, now.Add(time.Hour), tenant, connector, binding, flow)
	t.Run("session_source_scope_and_single_flow", func(t *testing.T) {
		for _, column := range []string{"sso_tenant_id", "sso_connector_id", "sso_binding_id", "sso_auth_version", "sso_flow_id"} {
			ssoSchemaRefuses(t, store, 3819, "UPDATE sessions SET "+column+"=NULL WHERE id=?", ssosession)
		}
		ssoSchemaRefuses(t, store, 3819, "UPDATE sessions SET sso_auth_version=0 WHERE id=?", ssosession)
		ssoSchemaRefuses(t, store, 3819, "UPDATE sessions SET sso_tenant_id=? WHERE id=?", tenant, ordinary)
		ssoSchemaRefuses(t, store, 1452, "UPDATE sessions SET user_id=? WHERE id=?", otherUser, ssosession)
		ssoSchemaRefuses(t, store, 1452, "UPDATE sessions SET sso_tenant_id=? WHERE id=?", otherTenant, ssosession)
		ssoSchemaRefuses(t, store, 1452, "UPDATE sessions SET sso_binding_id=? WHERE id=?", uuid.NewString(), ssosession)
		ssoSchemaRefuses(t, store, 1452, "UPDATE sessions SET sso_flow_id=? WHERE id=?", uuid.NewString(), ssosession)
		duplicate := sha256.Sum256([]byte(uuid.NewString()))
		ssoSchemaRefuses(t, store, 1062, "INSERT INTO sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at,sso_tenant_id,sso_connector_id,sso_binding_id,sso_auth_version,sso_flow_id) SELECT ?,user_id,audience,?,auth_epoch,created_at,last_seen_at,absolute_expires_at,sso_tenant_id,sso_connector_id,sso_binding_id,sso_auth_version,sso_flow_id FROM sessions WHERE id=?", uuid.NewString(), duplicate[:], ssosession)
	})
	intent := uuid.NewString()
	fingerprint := sha256.Sum256([]byte("Synthetic SSO intent"))
	migrationExec(t, store, "INSERT INTO sso_mutation_intents(tenant_id,actor_user_id,idempotency_key,operation,target_id,fingerprint,result) VALUES(?,?,?,'create_connector',?,?,?)", tenant, user, intent, connector, fingerprint[:], `{"id":"`+connector+"\",\"label\":\"\u4e16\u754c🙂\"}")
	t.Run("intent_object_and_scope", func(t *testing.T) {
		for _, value := range []string{`[]`, `null`, `"text"`, `3`, `false`} {
			ssoSchemaRefuses(t, store, 3819, "UPDATE sso_mutation_intents SET result=?", value)
		}
		ssoSchemaRefuses(t, store, 3819, "UPDATE sso_mutation_intents SET operation='other'")
		ssoSchemaRefuses(t, store, 3819, "UPDATE sso_mutation_intents SET fingerprint=X'00'")
		ssoSchemaRefuses(t, store, 1062, "INSERT INTO sso_mutation_intents SELECT * FROM sso_mutation_intents")
	})
	var label, subject string
	if e := store.Pool().QueryRowContext(ctx, "SELECT name FROM sso_connectors WHERE id=?", connector).Scan(&label); e != nil || label != "Synthetic \u4e16\u754c🙂" {
		t.Fatal("actual connector Unicode readback", mysqlstore.SafeError(e))
	}
	if e := store.Pool().QueryRowContext(ctx, "SELECT subject FROM sso_bindings WHERE id=?", binding).Scan(&subject); e != nil || subject != "9223372036854775807" {
		t.Fatal("actual exact subject readback", mysqlstore.SafeError(e))
	}
	t.Log("MYSQL_SSO_SCHEMA_BUSINESS_SHA256=" + ssoSchemaDigest(t, store))
}

func TestActualMySQLSSOSchemaFreshAndIdempotent(t *testing.T) {
	store, catalog, _ := actualSSOTableMigration(t)
	migrationRun(t, store, catalog, Apply)
	events := 0
	report, e := Run(context.Background(), store, catalog, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !report.Ready || events != 0 || report.AppliedSteps != 23 {
		t.Fatal("fresh current SSO catalog changed on repeated apply", report, e)
	}
	migrationRun(t, store, catalog, Readiness)
}

func TestActualMySQLSSOSchemaDefinitionDrift(t *testing.T) {
	store, catalog, _ := actualSSOTableMigration(t)
	migrationRun(t, store, catalog, Apply)
	migrationExec(t, store, "ALTER TABLE sso_bindings ADD COLUMN unexpected INT")
	if report, e := Run(context.Background(), store, catalog, testOptions(), Readiness, nil); e == nil || e.Error() != CodeDefinitionDrift || !report.LockReleased {
		t.Fatal("new SSO schema drift adopted", report, e)
	}
}

func TestActualMySQLSSOSchemaReadback(t *testing.T) {
	store, catalog, _ := actualSSOTableMigration(t)
	migrationRun(t, store, catalog, Readiness)
	for _, query := range []string{"SELECT COUNT(*) FROM sso_connectors", "SELECT COUNT(*) FROM sso_bindings", "SELECT COUNT(*) FROM sso_flows", "SELECT COUNT(*) FROM sso_mutation_intents"} {
		var count int
		if e := store.Pool().QueryRowContext(context.Background(), query).Scan(&count); e != nil || count != 1 {
			t.Fatal("restored synthetic SSO row missing", mysqlstore.SafeError(e))
		}
	}
	var sessions int
	if e := store.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM sessions").Scan(&sessions); e != nil || sessions != 2 {
		t.Fatal("restored actual ordinary/SSO sessions missing", mysqlstore.SafeError(e))
	}
	before := ssoSchemaDigest(t, store)
	ssoSchemaRefuses(t, store, 3819, "UPDATE sessions SET sso_auth_version=0 WHERE sso_flow_id IS NOT NULL")
	ssoSchemaRefuses(t, store, 3819, "UPDATE sso_bindings SET subject=?", strings.Repeat("9", 19))
	t.Log("MYSQL_SSO_SCHEMA_BUSINESS_SHA256=" + before)
}
