//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

type triggerBusiness struct {
	tenant, user, connector, binding, flow, ordinary string
	now                                              time.Time
}

func seedTriggerBusiness(t *testing.T, store *mysqlstore.Store) triggerBusiness {
	t.Helper()
	seedEvolutionBusiness(t, store)
	b := triggerBusiness{connector: uuid.NewString(), binding: uuid.NewString(), flow: uuid.NewString(), ordinary: uuid.NewString(), now: time.Date(2026, 10, 19, 5, 6, 7, 123456000, time.UTC)}
	if e := store.Pool().QueryRowContext(context.Background(), "SELECT tenant_id,primary_admin_user_id FROM installation_state WHERE id=1").Scan(&b.tenant, &b.user); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	token := sha256.Sum256([]byte(uuid.NewString()))
	migrationExec(t, store, "INSERT INTO sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at) VALUES(?,?,'portal',?,1,?,?,?)", b.ordinary, b.user, token[:], b.now, b.now, b.now.Add(time.Hour))
	migrationExec(t, store, "INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by) VALUES(?,?,'Synthetic trigger \u4e16\u754c','gitea','https://provider.example.invalid',?,'synthetic',?,?,1,?)", b.connector, b.tenant, uuid.NewString(), make([]byte, 12), make([]byte, 17), b.user)
	migrationExec(t, store, "INSERT INTO sso_bindings(id,tenant_id,connector_id,subject,user_id) VALUES(?,?,?,'9223372036854775807',?)", b.binding, b.tenant, b.connector, b.user)
	state, browser, cookie := sha256.Sum256([]byte(uuid.NewString())), sha256.Sum256([]byte(uuid.NewString())), sha256.Sum256([]byte(uuid.NewString()))
	migrationExec(t, store, "INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state_hash,browser_hash,previous_cookie_hash,verifier_key_id,verifier_nonce,verifier_ciphertext,created_at,expires_at) VALUES(?,?,?,1,1,'portal','login',?,?,?,'synthetic',?,?,?,?)", b.flow, b.tenant, b.connector, state[:], browser[:], cookie[:], make([]byte, 12), make([]byte, 59), b.now, b.now.Add(time.Hour))
	fingerprint := sha256.Sum256([]byte("Synthetic trigger intent"))
	migrationExec(t, store, "INSERT INTO sso_mutation_intents(tenant_id,actor_user_id,idempotency_key,operation,target_id,fingerprint,result) VALUES(?,?,?,'create_connector',?,?,JSON_OBJECT('id',?))", b.tenant, b.user, uuid.NewString(), b.connector, fingerprint[:], b.connector)
	return b
}

func finishTriggerFlow(t *testing.T, store *mysqlstore.Store, b triggerBusiness) {
	t.Helper()
	migrationExec(t, store, "UPDATE sso_flows SET state='claimed',claim_id=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=?", uuid.NewString(), b.flow)
	migrationExec(t, store, "UPDATE sso_flows SET state='succeeded',authenticated_user_id=?,finished_at=? WHERE id=?", b.user, b.now.Add(time.Minute), b.flow)
}

const triggerSessionInsert = "INSERT INTO sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at,sso_tenant_id,sso_connector_id,sso_binding_id,sso_auth_version,sso_flow_id) VALUES(?,?,'portal',?,1,?,?,?,?,?,?,1,?)"

func triggerSessionArguments(b triggerBusiness, id string) []any {
	token := sha256.Sum256([]byte(id))
	return []any{id, b.user, token[:], b.now, b.now, b.now.Add(time.Hour), b.tenant, b.connector, b.binding, b.flow}
}

func TestActualMySQLTriggersUpgradeAndGuards(t *testing.T) {
	store, catalog, _ := actualMigration(t)
	prefix := catalog
	prefix.steps = catalog.steps[:23]
	migrationRun(t, store, prefix, Apply)
	b := seedTriggerBusiness(t, store)
	before := ssoSchemaDigest(t, store)
	migrationRun(t, store, catalog, Apply)
	migrationRun(t, store, catalog, Readiness)
	if before != ssoSchemaDigest(t, store) {
		t.Fatal("trigger adoption changed nonempty business rows")
	}
	t.Run("connector_identity_revision_security_and_retention", func(t *testing.T) {
		for _, query := range []string{"UPDATE sso_connectors SET name='changed'", "UPDATE sso_connectors SET origin='https://other.example.invalid',revision=revision+1", "UPDATE sso_connectors SET enabled=0,revision=revision+1", "UPDATE sso_connectors SET auth_version=auth_version+2,revision=revision+1", "DELETE FROM sso_connectors"} {
			ssoSchemaRefuses(t, store, 1644, query)
		}
		migrationExec(t, store, "UPDATE sso_connectors SET name='Updated synthetic \u4e16\u754c',revision=revision+1")
		tx, e := store.Pool().BeginTx(context.Background(), nil)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer tx.Rollback()
		if _, e = tx.ExecContext(context.Background(), "UPDATE sso_connectors SET enabled=0,revision=revision+1,auth_version=auth_version+1"); e != nil {
			t.Fatal("valid versioned security change refused", mysqlstore.SafeError(e))
		}
	})
	t.Run("binding_identity_revision_and_retention", func(t *testing.T) {
		for _, query := range []string{"UPDATE sso_bindings SET active=0", "UPDATE sso_bindings SET subject='7',revision=revision+1", "UPDATE sso_bindings SET created_at=TIMESTAMPADD(SECOND,1,created_at),revision=revision+1", "DELETE FROM sso_bindings"} {
			ssoSchemaRefuses(t, store, 1644, query)
		}
		migrationExec(t, store, "UPDATE sso_bindings SET active=0,revision=revision+1")
		migrationExec(t, store, "UPDATE sso_bindings SET active=1,revision=revision+1")
	})
	t.Run("immutable_intent", func(t *testing.T) {
		ssoSchemaRefuses(t, store, 1644, "UPDATE sso_mutation_intents SET result=JSON_OBJECT('changed',1)")
		ssoSchemaRefuses(t, store, 1644, "DELETE FROM sso_mutation_intents")
	})
	t.Run("flow_insert_and_pending_transition", func(t *testing.T) {
		ssoSchemaRefuses(t, store, 1644, "INSERT INTO sso_flows(id,tenant_id,connector_id,connector_revision,auth_version,audience,mode,state,state_hash,browser_hash,previous_cookie_hash,created_at,expires_at,finished_at,error_code) SELECT ?,tenant_id,connector_id,connector_revision,auth_version,audience,mode,'failed',UNHEX(SHA2(?,256)),browser_hash,previous_cookie_hash,created_at,expires_at,created_at,'SYNTHETIC_FAILURE' FROM sso_flows WHERE id=?", uuid.NewString(), uuid.NewString(), b.flow)
		ssoSchemaRefuses(t, store, 1644, "UPDATE sso_flows SET state='pending'")
		ssoSchemaRefuses(t, store, 1644, "UPDATE sso_flows SET state='claimed',claim_id=?,browser_hash=UNHEX(SHA2('changed',256)),verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL", uuid.NewString())
		ssoSchemaRefuses(t, store, 1644, "UPDATE sso_flows SET state='succeeded',claim_id=?,authenticated_user_id=?,finished_at=created_at,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL", uuid.NewString(), b.user)
	})
	migrationExec(t, store, "UPDATE sso_flows SET state='claimed',claim_id=?,verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL WHERE id=?", uuid.NewString(), b.flow)
	t.Run("claim_and_terminal_immutability", func(t *testing.T) {
		ssoSchemaRefuses(t, store, 1644, "UPDATE sso_flows SET state='failed',finished_at=created_at,error_code='SYNTHETIC_FAILURE',claim_id=?", uuid.NewString())
		ssoSchemaRefuses(t, store, 1644, "UPDATE sso_flows SET state='pending',claim_id=NULL,verifier_key_id='synthetic',verifier_nonce=?,verifier_ciphertext=?", make([]byte, 12), make([]byte, 59))
	})
	migrationExec(t, store, "UPDATE sso_flows SET state='succeeded',authenticated_user_id=?,finished_at=? WHERE id=?", b.user, b.now.Add(time.Minute), b.flow)
	issued := uuid.NewString()
	migrationExec(t, store, triggerSessionInsert, triggerSessionArguments(b, issued)...)
	t.Run("exact_once_issuance_and_terminal_snapshot", func(t *testing.T) {
		ssoSchemaRefuses(t, store, 1644, triggerSessionInsert, triggerSessionArguments(b, uuid.NewString())...)
		ssoSchemaRefuses(t, store, 1644, "UPDATE sso_flows SET issued_session_id=NULL")
		ssoSchemaRefuses(t, store, 1644, "UPDATE sso_flows SET issued_session_id=?,error_code=NULL", uuid.NewString())
		ssoSchemaRefuses(t, store, 1644, "UPDATE sso_flows SET state='succeeded'")
	})
	var actual string
	if e := store.Pool().QueryRowContext(context.Background(), "SELECT issued_session_id FROM sso_flows WHERE id=?", b.flow).Scan(&actual); e != nil || actual != issued {
		t.Fatal("actual trigger did not persist exact session issuance", mysqlstore.SafeError(e))
	}
	t.Log("MYSQL_TRIGGER_BUSINESS_SHA256=" + ssoSchemaDigest(t, store))
}

func TestActualMySQLTriggersConcurrentIssuanceAndRollback(t *testing.T) {
	store, catalog, _ := actualMigration(t)
	migrationRun(t, store, catalog, Apply)
	b := seedTriggerBusiness(t, store)
	finishTriggerFlow(t, store, b)
	// Identified actual database fault trigger. Different SQLSTATEs prove the
	// failed AFTER INSERT observed the prior BEFORE INSERT flow claim.
	fault := "CREATE TRIGGER synthetic_after_session_failure AFTER INSERT ON sessions FOR EACH ROW BEGIN IF NEW.sso_flow_id IS NOT NULL THEN IF EXISTS(SELECT 1 FROM sso_flows WHERE id=NEW.sso_flow_id AND issued_session_id=NEW.id) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SYNTHETIC_CLAIM_OBSERVED'; ELSE SIGNAL SQLSTATE '45001' SET MESSAGE_TEXT='SYNTHETIC_CLAIM_MISSING'; END IF; END IF; END;"
	migrationExec(t, store, fault)
	before := ssoSchemaDigest(t, store)
	_, e := store.Pool().ExecContext(context.Background(), triggerSessionInsert, triggerSessionArguments(b, uuid.NewString())...)
	var typed *mysqlstore.Error
	if !errors.As(mysqlstore.SafeError(e), &typed) || typed.Number != 1644 || string(typed.SQLState[:]) != "45000" || before != ssoSchemaDigest(t, store) {
		t.Fatal("actual after-claim failure did not roll back both rows", mysqlstore.SafeError(e))
	}
	migrationExec(t, store, "DROP TRIGGER synthetic_after_session_failure")
	migrationRun(t, store, catalog, Readiness)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type result struct {
		id  string
		tx  *sql.Tx
		err error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		tx, e := store.Pool().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		defer tx.Rollback()
		id := uuid.NewString()
		go func() {
			<-start
			_, e := tx.ExecContext(ctx, triggerSessionInsert, triggerSessionArguments(b, id)...)
			results <- result{id, tx, e}
		}()
	}
	close(start)
	winner := <-results
	if winner.err != nil {
		t.Fatal("first actual issuer failed", mysqlstore.SafeError(winner.err))
	}
	select {
	case <-results:
		t.Fatal("second issuer completed while the winning flow lock was held")
	case <-time.After(150 * time.Millisecond):
	}
	if e := winner.tx.Commit(); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	loser := <-results
	if !errors.As(mysqlstore.SafeError(loser.err), &typed) || typed.Number != 1644 {
		t.Fatal("contending session escaped exact issuance guard", mysqlstore.SafeError(loser.err))
	}
	_ = loser.tx.Rollback()
	var issued string
	var sessions int
	if e := store.Pool().QueryRowContext(ctx, "SELECT issued_session_id FROM sso_flows WHERE id=?", b.flow).Scan(&issued); e != nil || issued != winner.id {
		t.Fatal("actual winner/flow claim mismatch", mysqlstore.SafeError(e))
	}
	if e := store.Pool().QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&sessions); e != nil || sessions != 2 {
		t.Fatal("actual ordinary plus single SSO session count", mysqlstore.SafeError(e))
	}
	t.Log("MYSQL_TRIGGER_BUSINESS_SHA256=" + ssoSchemaDigest(t, store))
}

func TestActualMySQLTriggersBeforeCreateReconciliation(t *testing.T) {
	actualTriggerReconciliation(t, Applying)
}
func TestActualMySQLTriggersAfterCreateReconciliation(t *testing.T) {
	actualTriggerReconciliation(t, Verified)
}
func actualTriggerReconciliation(t *testing.T, stage string) {
	t.Helper()
	store, catalog, _ := actualMigration(t)
	prefix := catalog
	prefix.steps = catalog.steps[:23]
	migrationRun(t, store, prefix, Apply)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report, e := Run(ctx, store, catalog, testOptions(), Apply, func(event Event) {
		if event.Step == 24 && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || report.DirtyStep != 24 || !report.LockReleased {
		t.Fatal("actual trigger dirty state not retained", report, e)
	}
	if _, e = Run(context.Background(), store, catalog, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
		t.Fatal("dirty trigger automatically replayed", e)
	}
	migrationRun(t, store, catalog, Reconcile)
	migrationRun(t, store, catalog, Readiness)
}

func TestActualMySQLTriggersUnexpectedAndBodyDrift(t *testing.T) {
	store, catalog, _ := actualMigration(t)
	migrationRun(t, store, catalog, Apply)
	migrationExec(t, store, "CREATE TRIGGER synthetic_unexpected BEFORE DELETE ON tenants FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='SYNTHETIC_UNEXPECTED'")
	if _, e := Run(context.Background(), store, catalog, testOptions(), Readiness, nil); e == nil || e.Error() != CodeUnjournaledObject {
		t.Fatal("unexpected trigger was ignored", e)
	}
	migrationExec(t, store, "DROP TRIGGER synthetic_unexpected")
	migrationRun(t, store, catalog, Readiness)
	migrationExec(t, store, "DROP TRIGGER sso_connector_retained")
	migrationExec(t, store, strings.Replace(catalog.steps[24].sql, "SSO_CONNECTOR_RETAINED", "SYNTHETIC_CHANGED_BODY", 1))
	if _, e := Run(context.Background(), store, catalog, testOptions(), Readiness, nil); e == nil || e.Error() != CodeDefinitionDrift {
		t.Fatal("changed trigger body adopted", e)
	}
}

func TestActualMySQLTriggersFreshAndIdempotent(t *testing.T) {
	store, catalog, _ := actualMigration(t)
	migrationRun(t, store, catalog, Apply)
	events := 0
	report, e := Run(context.Background(), store, catalog, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !report.Ready || report.AppliedSteps != 32 || events != 0 {
		t.Fatal("actual full trigger catalog replayed on repeat", report, events, e)
	}
	migrationRun(t, store, catalog, Readiness)
}

func TestActualMySQLTriggersDirtyBodyRefusal(t *testing.T) {
	store, catalog, _ := actualMigration(t)
	prefix := catalog
	prefix.steps = catalog.steps[:23]
	migrationRun(t, store, prefix, Apply)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, e := Run(ctx, store, catalog, testOptions(), Apply, func(event Event) {
		if event.Step == 24 && event.Stage == Verified {
			cancel()
		}
	})
	if e == nil {
		t.Fatal("actual after-trigger dirty setup did not interrupt")
	}
	migrationExec(t, store, "DROP TRIGGER sso_connector_identity")
	migrationExec(t, store, strings.Replace(catalog.steps[23].sql, "SSO_CONNECTOR_IDENTITY_IMMUTABLE", "SYNTHETIC_CHANGED_IDENTITY", 1))
	if _, e = Run(context.Background(), store, catalog, testOptions(), Reconcile, nil); e == nil || e.Error() != CodeDefinitionDrift {
		t.Fatal("dirty unknown trigger body was reconciled", e)
	}
	var state string
	if e = store.Pool().QueryRowContext(context.Background(), "SELECT state FROM schema_migrations WHERE step=24").Scan(&state); e != nil || state != Applying {
		t.Fatal("refused trigger repair changed durable journal", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLTriggersCreationModeDrift(t *testing.T) {
	store, catalog, _ := actualMigration(t)
	migrationRun(t, store, catalog, Apply)
	migrationExec(t, store, "DROP TRIGGER sso_connector_retained")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, e := store.Pool().Conn(ctx)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer connection.Close()
	if _, e = connection.ExecContext(ctx, "SET SESSION sql_mode=''"); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	_, created := connection.ExecContext(ctx, catalog.steps[24].sql)
	_, restored := connection.ExecContext(ctx, "SET SESSION sql_mode=?", mysqlstore.SessionSQLModes)
	if created != nil || restored != nil {
		t.Fatal("actual stored-mode drift setup", mysqlstore.SafeError(created), mysqlstore.SafeError(restored))
	}
	if e = connection.Close(); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	if _, e = Run(context.Background(), store, catalog, testOptions(), Readiness, nil); e == nil || e.Error() != CodeMetadataInvalid {
		t.Fatal("actual trigger creation SQL mode was ignored", e)
	}
}

func TestActualMySQLTriggersPrepareDefinerDrift(t *testing.T) {
	store, catalog, _ := actualMigration(t)
	migrationRun(t, store, catalog, Apply)
}
func TestActualMySQLTriggersDefinerDriftReadback(t *testing.T) {
	store, catalog, _ := actualMigration(t)
	if _, e := Run(context.Background(), store, catalog, testOptions(), Readiness, nil); e == nil || e.Error() != CodeTriggerContextInvalid {
		t.Fatal("actual different Definer accepted", e)
	}
}

func TestActualMySQLTriggersReadback(t *testing.T) {
	store, catalog, _ := actualMigration(t)
	migrationRun(t, store, catalog, Readiness)
	var count int
	if e := store.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE()").Scan(&count); e != nil || count != 9 {
		t.Fatal("restored owned triggers missing", mysqlstore.SafeError(e))
	}
	ssoSchemaRefuses(t, store, 1644, "UPDATE sso_mutation_intents SET result=JSON_OBJECT('changed',1)")
	ssoSchemaRefuses(t, store, 1644, "UPDATE sso_flows SET issued_session_id=NULL")
	ssoSchemaRefuses(t, store, 1644, "DELETE FROM sso_bindings")
	t.Log("MYSQL_TRIGGER_BUSINESS_SHA256=" + ssoSchemaDigest(t, store))
}
