//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func sessionFKDataDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	rows, e := s.Pool().QueryContext(context.Background(), `SELECT JSON_ARRAY(id,user_id,audience,HEX(token_hash),auth_epoch,created_at,last_seen_at,absolute_expires_at,revoked_at,revoke_reason,sso_tenant_id,sso_connector_id,sso_binding_id,sso_auth_version,sso_flow_id) FROM sessions ORDER BY id`)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer rows.Close()
	values := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			t.Fatal("canonical real session row")
		}
		values = append(values, raw)
	}
	if rows.Err() != nil {
		t.Fatal(mysqlstore.SafeError(rows.Err()))
	}
	raw, _ := json.Marshal(values)
	return evolutionBusinessDigest(t, s) + hash(string(raw))
}

func sessionFKJournalPrefix(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	rows, e := s.Pool().QueryContext(context.Background(), `SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) FROM schema_migrations WHERE step<=36 ORDER BY step`)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer rows.Close()
	values := []json.RawMessage{}
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			t.Fatal("canonical real journal prefix")
		}
		values = append(values, raw)
	}
	if rows.Err() != nil || len(values) != 36 {
		t.Fatal("immutable36 journal rows required")
	}
	raw, _ := json.Marshal(values)
	return hash(string(raw))
}

func sessionFKSeed(t *testing.T, s *mysqlstore.Store) (*identity.Service, identity.Auth) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	seedEvolutionBusiness(t, s)
	store, e := transaction.FromMySQL(s)
	if e != nil {
		t.Fatal(e)
	}
	accounts := &identity.Service{Storage: store}
	var user string
	if e := s.Pool().QueryRowContext(context.Background(), `SELECT primary_admin_user_id FROM installation_state WHERE id=1`).Scan(&user); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	var issued identity.IssuedSession
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = accounts.IssueSessionWithin(ctx, q, user, 1, "admin", "", uuid.NewString(), nil)
		return e
	})
	if e != nil {
		t.Fatal("actual ordinary session after schema migration", e)
	}
	a, e := accounts.Authenticate(ctx, issued.Token, "admin")
	if e != nil {
		t.Fatal(e)
	}
	return accounts, a
}

func sessionFKDefinition(t *testing.T, s *mysqlstore.Store, catalog Catalog) {
	t.Helper()
	var definition string
	connection, e := s.Pool().Conn(context.Background())
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer connection.Close()
	definition, e = metadata(context.Background(), connection, "sessions", testOptions().MaximumMetadataBytes)
	if e != nil || definition != catalog.steps[37].DefinitionSHA256 {
		t.Fatal("complete measured final session definition", e)
	}
	var columns, foreignKeys, triggers int
	if e := connection.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='sessions' AND ((index_name='sessions_user_fk_idx' AND seq_in_index=1 AND column_name='user_id') OR (index_name='sessions_user_live_idx' AND ((seq_in_index=1 AND column_name='user_id') OR (seq_in_index=2 AND column_name='revoked_at'))))`).Scan(&columns); e != nil || columns != 3 {
		t.Fatal("stable and retained query index columns", mysqlstore.SafeError(e))
	}
	if e := connection.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM information_schema.referential_constraints WHERE constraint_schema=DATABASE() AND table_name='sessions'`).Scan(&foreignKeys); e != nil || foreignKeys != 3 {
		t.Fatal("all real session foreign keys retained", mysqlstore.SafeError(e))
	}
	if e := connection.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND event_object_table='sessions' AND trigger_name='sessions_sso_issuance' AND action_timing='BEFORE' AND event_manipulation='INSERT'`).Scan(&triggers); e != nil || triggers != 1 {
		t.Fatal("single-issuance source trigger retained", mysqlstore.SafeError(e))
	}
}

func sessionFKPrefix(t *testing.T) (*mysqlstore.Store, Catalog) {
	t.Helper()
	s, catalog, _ := actualMigration(t)
	prefix := catalog
	prefix.steps = catalog.steps[:36]
	migrationRun(t, s, prefix, Apply)
	sessionFKSeed(t, s)
	return s, catalog
}

func TestActualMySQLSessionForeignKeyIndexFresh(t *testing.T) {
	s, catalog, c := actualMigration(t)
	migrationRun(t, s, catalog, Apply)
	accounts, a := sessionFKSeed(t, s)
	sessionFKDefinition(t, s, catalog)
	before := sessionFKDataDigest(t, s)
	events := 0
	r, e := Run(context.Background(), s, catalog, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !r.Ready || events != 0 || sessionFKDataDigest(t, s) != before {
		t.Fatal("fresh38 ready/idempotence without data mutation", e)
	}
	// Reopen an independent verified TLS pool. Server restart is a separate gate.
	reopened, observation, e := mysqlstore.Open(context.Background(), c)
	if e != nil || !observation.TLSAuthenticated {
		t.Fatal("independent real verified TLS reconnect", e)
	}
	defer reopened.Close()
	migrationRun(t, reopened, catalog, Readiness)
	if sessionFKDataDigest(t, reopened) != before {
		t.Fatal("independent pool readback changed data")
	}
	_, e = s.Pool().ExecContext(context.Background(), `UPDATE sessions SET user_id=? WHERE id=?`, uuid.NewString(), a.SessionID)
	vendor, ok := mysqlstore.SafeError(e).(*mysqlstore.Error)
	if !ok || vendor.Number != 1452 || sessionFKDataDigest(t, s) != before {
		t.Fatal("session user FK must still refuse missing parent atomically", mysqlstore.SafeError(e))
	}
	migrationExec(t, s, `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6),revoke_reason='synthetic_migration_test' WHERE id=?`, a.SessionID)
	store, e := accounts.Repository()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		return accounts.RecheckWithin(ctx, q, a)
	})
	if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatal("actual revocation must still remove ordinary session authority", e)
	}
}

func TestActualMySQLSessionForeignKeyIndexUpgrade(t *testing.T) {
	s, catalog := sessionFKPrefix(t)
	before, journal := sessionFKDataDigest(t, s), sessionFKJournalPrefix(t, s)
	migrationRun(t, s, catalog, Apply)
	sessionFKDefinition(t, s, catalog)
	if sessionFKDataDigest(t, s) != before || sessionFKJournalPrefix(t, s) != journal {
		t.Fatal("36→38 changed existing session/business data or prior journal rows")
	}
	migrationRun(t, s, catalog, Readiness)
}

func sessionFKInterrupted(t *testing.T, step int, stage string) {
	t.Helper()
	s, catalog := sessionFKPrefix(t)
	before, journal := sessionFKDataDigest(t, s), sessionFKJournalPrefix(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, e := Run(ctx, s, catalog, testOptions(), Apply, func(event Event) {
		if event.Step == step && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || r.Ready || r.DirtyStep != step || !r.LockReleased {
		t.Fatal("exact interrupted DDL boundary must stay dirty", r, e)
	}
	if _, e = Run(context.Background(), s, catalog, testOptions(), Readiness, nil); e == nil {
		t.Fatal("dirty intermediate catalog served ready")
	}
	migrationRun(t, s, catalog, Reconcile)
	sessionFKDefinition(t, s, catalog)
	if sessionFKDataDigest(t, s) != before || sessionFKJournalPrefix(t, s) != journal {
		t.Fatal("explicit DDL reconciliation changed retained data/journal")
	}
}

func TestActualMySQLSessionForeignKeyIndexBeforeStable(t *testing.T) {
	sessionFKInterrupted(t, 37, Applying)
}
func TestActualMySQLSessionForeignKeyIndexAfterStable(t *testing.T) {
	sessionFKInterrupted(t, 37, Verified)
}
func TestActualMySQLSessionForeignKeyIndexBeforeLive(t *testing.T) {
	sessionFKInterrupted(t, 38, Applying)
}
func TestActualMySQLSessionForeignKeyIndexAfterLive(t *testing.T) {
	sessionFKInterrupted(t, 38, Verified)
}

func sessionFKWrongDefinition(t *testing.T, step int) {
	t.Helper()
	s, catalog := sessionFKPrefix(t)
	before, journal := sessionFKDataDigest(t, s), sessionFKJournalPrefix(t, s)
	r, e := Run(context.Background(), s, catalog, testOptions(), Apply, func(event Event) {
		if event.Step != step || event.Stage != Applying {
			return
		}
		if step == 37 {
			migrationExec(t, s, `ALTER TABLE sessions ADD INDEX sessions_user_fk_idx(user_id,revoked_at), DROP INDEX sessions_user_live_idx, ALGORITHM=INPLACE, LOCK=NONE`)
		} else {
			migrationExec(t, s, `ALTER TABLE sessions ADD INDEX sessions_user_live_idx(revoked_at,user_id), ALGORITHM=INPLACE, LOCK=NONE`)
		}
	})
	if e == nil || r.Ready || r.DirtyStep != step || !r.LockReleased {
		t.Fatal("wrong session index definition admitted", r, e)
	}
	if _, e := Run(context.Background(), s, catalog, testOptions(), Reconcile, nil); e == nil || e.Error() != CodeDefinitionDrift {
		t.Fatal("wrong session index silently reconciled", e)
	}
	// Privileged exact isolated repair keeps all foreign-key checks enabled.
	// No journal row/checksum is edited and no customer scope is selected.
	if step == 37 {
		migrationExec(t, s, `ALTER TABLE sessions DROP INDEX sessions_user_fk_idx, ADD INDEX sessions_user_fk_idx(user_id), ALGORITHM=INPLACE, LOCK=NONE`)
	} else {
		migrationExec(t, s, `ALTER TABLE sessions DROP INDEX sessions_user_live_idx, ADD INDEX sessions_user_live_idx(user_id,revoked_at), ALGORITHM=INPLACE, LOCK=NONE`)
	}
	migrationRun(t, s, catalog, Reconcile)
	sessionFKDefinition(t, s, catalog)
	if sessionFKDataDigest(t, s) != before || sessionFKJournalPrefix(t, s) != journal {
		t.Fatal("wrong-index repair changed retained data/journal")
	}
}

func TestActualMySQLSessionForeignKeyIndexWrongStableRepair(t *testing.T) {
	sessionFKWrongDefinition(t, 37)
}
func TestActualMySQLSessionForeignKeyIndexWrongLiveRepair(t *testing.T) {
	sessionFKWrongDefinition(t, 38)
}
