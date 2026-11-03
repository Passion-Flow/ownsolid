//go:build mysql_integration

package mysqlmigrate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func actualMigration(t *testing.T) (*mysqlstore.Store, Catalog, mysqlstore.Configuration) {
	t.Helper()
	c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_MYSQL_MIGRATION_CONFIGURATION"), "")
	if e != nil || !strings.HasPrefix(c.Database, "m20_migration_") || !strings.HasSuffix(c.Database, "_test") {
		t.Fatal("MYSQL_MIGRATION_ACTUAL_NEW_ISOLATED_CONFIGURATION_REQUIRED")
	}
	s, _, e := mysqlstore.Open(context.Background(), c)
	if e != nil {
		t.Fatal("actual migration connector", e)
	}
	t.Cleanup(func() {
		if s.Close() != nil {
			t.Error("actual pool close")
		}
	})
	catalog, e := DefaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	return s, catalog, c
}
func migrationExec(t *testing.T, s *mysqlstore.Store, query string, args ...any) {
	t.Helper()
	if _, e := s.Pool().ExecContext(context.Background(), query, args...); e != nil {
		t.Fatal("actual fixture SQL", mysqlstore.SafeError(e))
	}
}
func migrationRun(t *testing.T, s *mysqlstore.Store, catalog Catalog, mode Mode) Report {
	t.Helper()
	r, e := Run(context.Background(), s, catalog, testOptions(), mode, nil)
	if e != nil || !r.Ready || !r.LockReleased || r.AppliedSteps != catalog.Steps() {
		t.Fatal("actual migration", r, e)
	}
	return r
}
func migrationDigest(t *testing.T, s *mysqlstore.Store, catalog Catalog) string {
	t.Helper()
	values := []any{}
	for _, query := range []string{
		"SELECT JSON_ARRAY(step,source_sha256,object_name,object_sha256,state,DATE_FORMAT(started_at,'%Y-%m-%dT%H:%i:%s.%fZ'),DATE_FORMAT(applied_at,'%Y-%m-%dT%H:%i:%s.%fZ')) FROM schema_migrations ORDER BY step",
		"SELECT JSON_ARRAY(id,bootstrap_complete,tenant_id,default_workspace_id,primary_admin_user_id,DATE_FORMAT(bootstrapped_at,'%Y-%m-%dT%H:%i:%s.%fZ')) FROM installation_state ORDER BY id",
		"SELECT JSON_ARRAY(id,tenant_id,workspace_id,title,`text`,author_user_id,version) FROM workspace_notes ORDER BY id",
	} {
		rows, e := s.Pool().QueryContext(context.Background(), query)
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		for rows.Next() {
			var raw []byte
			var value any
			if rows.Scan(&raw) != nil || json.Unmarshal(raw, &value) != nil {
				rows.Close()
				t.Fatal("canonical typed rows")
			}
			values = append(values, value)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
	}
	for _, item := range catalog.steps {
		if item.Kind != createTable {
			continue
		}
		var count int
		if e := s.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM `"+item.Object+"`").Scan(&count); e != nil {
			t.Fatal(mysqlstore.SafeError(e))
		}
		values = append(values, []any{item.Object, count})
	}
	raw, e := json.Marshal(values)
	if e != nil {
		t.Fatal(e)
	}
	return hash(string(raw))
}

func TestActualMySQLMigrationFreshAndConstraints(t *testing.T) {
	s, catalog, _ := actualMigration(t)
	ctx := context.Background()
	if r, e := Run(ctx, s, catalog, testOptions(), Readiness, nil); e == nil || e.Error() != CodeSchemaNotReady || !r.LockReleased {
		t.Fatal("empty schema readiness", r, e)
	}
	var events []Event
	r, e := Run(ctx, s, catalog, testOptions(), Apply, func(e Event) { events = append(events, e) })
	if e != nil || !r.Ready || !r.LockReleased || len(events) != catalog.Steps()*3 {
		t.Fatal("actual ordered migration", r, e)
	}
	for index, event := range events {
		if event.Step != index/3+1 || event.Stage != []string{Applying, Verified, Applied}[index%3] {
			t.Fatal("durable stage order")
		}
	}
	tenant, workspace, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	now := time.Date(2026, 10, 19, 3, 4, 5, 123456000, time.UTC)
	migrationExec(t, s, "INSERT INTO tenants(id,name,status) VALUES(?,?,'active')", tenant, "Synthetic \u4e16\u754c🙂")
	migrationExec(t, s, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Case','active')", workspace, tenant)
	migrationExec(t, s, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'case','active')", uuid.NewString(), tenant)
	migrationExec(t, s, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,? ,? ,? ,'en','active',1)", user, "synthetic@example.invalid", "synthetic@example.invalid", "Synthetic schema operator")
	// Constraint-only rows; no usable credential,mail dispatch or product login.
	migrationExec(t, s, "INSERT INTO password_credentials(user_id,password_phc) VALUES(?,?)", user, "synthetic-not-a-usable-password-hash")
	migrationExec(t, s, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", user, tenant, workspace)
	migrationExec(t, s, "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,`text`,author_user_id) VALUES(?,?,?,?,?,?)", uuid.NewString(), tenant, workspace, "Synthetic note", "Preserved \u4e16\u754c🙂\nsecond line", user)
	migrationExec(t, s, "UPDATE installation_state SET bootstrap_complete=1,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=? WHERE id=1", tenant, workspace, user, now)
	t.Run("short-hash-not-padded", func(t *testing.T) {
		_, e := s.Pool().ExecContext(ctx, "INSERT INTO sessions(id,user_id,audience,token_hash,auth_epoch,created_at,last_seen_at,absolute_expires_at) VALUES(?,?,'portal',?,1,?,?,?)", uuid.NewString(), user, bytes.Repeat([]byte{1}, 31), now, now, now.Add(time.Hour))
		if e == nil || mysqlstore.SafeError(e).Error() != "MYSQL_CHECK_CONFLICT" {
			t.Fatal("short hash admitted", mysqlstore.SafeError(e))
		}
	})
	t.Run("tenant-foreign-key-isolation", func(t *testing.T) {
		_, e := s.Pool().ExecContext(ctx, "INSERT INTO workspace_notes(id,tenant_id,workspace_id,title,`text`,author_user_id) VALUES(?,?,?,'wrong','wrong',?)", uuid.NewString(), uuid.NewString(), workspace, user)
		if e == nil || mysqlstore.SafeError(e).Error() != "MYSQL_FOREIGN_KEY_CONFLICT" {
			t.Fatal("cross-tenant row", mysqlstore.SafeError(e))
		}
	})
	t.Run("canonical-email-and-counter", func(t *testing.T) {
		for _, q := range []string{"UPDATE users SET email_canonical='Upper@example.invalid'", "UPDATE users SET version=9007199254740992", "UPDATE users SET is_platform_admin=2"} {
			if _, e := s.Pool().ExecContext(ctx, q); e == nil || mysqlstore.SafeError(e).Error() != "MYSQL_CHECK_CONFLICT" {
				t.Fatal("invalid identity state", mysqlstore.SafeError(e))
			}
		}
	})
	t.Run("pending-email-generated-unique-index", func(t *testing.T) {
		first, second := uuid.NewString(), uuid.NewString()
		insert := "INSERT INTO invitations(id,tenant_id,workspace_id,email_canonical,locale,status,token_hash,created_by,created_at,updated_at,expires_at) VALUES(?,?,?,'candidate@example.invalid','en','pending',?,?,?,?,?)"
		migrationExec(t, s, insert, first, tenant, workspace, bytes.Repeat([]byte{2}, 32), user, now, now, now.Add(time.Hour))
		_, e := s.Pool().ExecContext(ctx, insert, second, tenant, workspace, bytes.Repeat([]byte{3}, 32), user, now, now, now.Add(time.Hour))
		if e == nil || mysqlstore.SafeError(e).Error() != "MYSQL_UNIQUE_CONFLICT" {
			t.Fatal("pending email uniqueness", mysqlstore.SafeError(e))
		}
		migrationExec(t, s, "UPDATE invitations SET status='revoked',revoked_at=?,token_hash=NULL WHERE id=?", now, first)
		migrationExec(t, s, insert, second, tenant, workspace, bytes.Repeat([]byte{3}, 32), user, now, now, now.Add(time.Hour))
	})
	t.Run("audit-JSON-and-canonical-request", func(t *testing.T) {
		insert := "INSERT INTO audit_events(id,occurred_at,actor_user_id,audience,action,outcome,request_id,metadata) VALUES(?,?,?,'operator','schema.synthetic_fixture','accepted',?,?)"
		for _, raw := range []string{`[]`, `{"large":"` + strings.Repeat("x", 4096) + `"}`} {
			if _, e := s.Pool().ExecContext(ctx, insert, uuid.NewString(), now, user, uuid.NewString(), raw); e == nil || mysqlstore.SafeError(e).Error() != "MYSQL_CHECK_CONFLICT" {
				t.Fatal("invalid audit JSON", mysqlstore.SafeError(e))
			}
		}
		if _, e := s.Pool().ExecContext(ctx, insert, uuid.NewString(), now, user, "not-a-canonical-request", `{}`); e == nil || mysqlstore.SafeError(e).Error() != "MYSQL_CHECK_CONFLICT" {
			t.Fatal("invalid audit request", mysqlstore.SafeError(e))
		}
		migrationExec(t, s, insert, uuid.NewString(), now, user, uuid.NewString(), `{"kind":"synthetic"}`)
	})
	t.Run("mail-nonce-and-terminal-payload", func(t *testing.T) {
		insert := "INSERT INTO mail_deliveries(id,purpose,recipient_email,locale,user_id,state,payload_key_id,payload_nonce,payload_ciphertext,next_attempt_at,message_id,created_at,updated_at) VALUES(?,'password_changed','synthetic@example.invalid','en',?,'queued','synthetic-key',?,?,?, ?,?,?)"
		if _, e := s.Pool().ExecContext(ctx, insert, uuid.NewString(), user, bytes.Repeat([]byte{4}, 11), []byte{1}, now, uuid.NewString()+"@example.invalid", now, now); e == nil || mysqlstore.SafeError(e).Error() != "MYSQL_CHECK_CONFLICT" {
			t.Fatal("short nonce", mysqlstore.SafeError(e))
		}
		id := uuid.NewString()
		migrationExec(t, s, insert, id, user, bytes.Repeat([]byte{4}, 12), []byte{1}, now, uuid.NewString()+"@example.invalid", now, now)
		if _, e := s.Pool().ExecContext(ctx, "UPDATE mail_deliveries SET state='sent',completed_at=? WHERE id=?", now, id); e == nil || mysqlstore.SafeError(e).Error() != "MYSQL_CHECK_CONFLICT" {
			t.Fatal("terminal secret retained", mysqlstore.SafeError(e))
		}
		migrationExec(t, s, "UPDATE mail_deliveries SET state='cancelled',completed_at=?,payload_key_id=NULL,payload_nonce=NULL,payload_ciphertext=NULL WHERE id=?", now, id)
	})
	before := migrationDigest(t, s, catalog)
	migrationRun(t, s, catalog, Apply)
	after := migrationDigest(t, s, catalog)
	if before != after {
		t.Fatal("rerun changed installed identity/data/journal")
	}
	t.Log("MYSQL_MIGRATED_SYNTHETIC_ROWS_SHA256=" + after)
}

func TestActualMySQLMigrationCancellationAndExplicitReconciliation(t *testing.T) {
	s, catalog, _ := actualMigration(t)
	ctx, cancel := context.WithCancel(context.Background())
	r, e := Run(ctx, s, catalog, testOptions(), Apply, func(event Event) {
		if event.Step == 1 && event.Stage == Applying {
			cancel()
		}
	})
	cancel()
	if e == nil || r.Ready {
		t.Fatal("actual cancellation omitted")
	}
	if r, e = Run(context.Background(), s, catalog, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired || r.DirtyStep != 1 || !r.LockReleased {
		t.Fatal("dirty state silently resumed", r, e)
	}
	var count int
	if e = s.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='tenants'").Scan(&count); e != nil || count != 0 {
		t.Fatal("before-DDL cancellation did not precede DDL")
	}
	migrationRun(t, s, catalog, Reconcile)
}

func TestActualMySQLMigrationAfterDDLAndDriftRefusal(t *testing.T) {
	s, catalog, _ := actualMigration(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, e := Run(ctx, s, catalog, testOptions(), Apply, func(event Event) {
		if event.Step == 1 && event.Stage == Verified {
			cancel()
		}
	})
	cancel()
	if e == nil {
		t.Fatal("actual post-DDL cancellation omitted")
	}
	r, e := Run(context.Background(), s, catalog, testOptions(), Apply, nil)
	if e == nil || e.Error() != CodeReconciliationRequired || r.DirtyStep != 1 || r.PendingObjectSHA != catalog.steps[0].DefinitionSHA256 {
		t.Fatal("durable DDL/journal mismatch not observed", r, e)
	}
	migrationRun(t, s, catalog, Reconcile)
	migrationExec(t, s, "ALTER TABLE tenants ADD COLUMN unexpected INT NULL")
	if _, e = Run(context.Background(), s, catalog, testOptions(), Reconcile, nil); e == nil || e.Error() != CodeDefinitionDrift {
		t.Fatal("drift repaired or admitted", e)
	}
	var count int
	if e = s.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations").Scan(&count); e != nil || count != catalog.Steps() {
		t.Fatal("drift changed journal")
	}
}

func TestActualMySQLMigrationLockAndStoredJournalRefusals(t *testing.T) {
	s, catalog, c := actualMigration(t)
	ctx := context.Background()
	held, e := s.Pool().Conn(ctx)
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer held.Close()
	lock, _ := LockName(c.Database)
	var acquired int
	if e = held.QueryRowContext(ctx, "SELECT GET_LOCK(?,1)", lock).Scan(&acquired); e != nil || acquired != 1 {
		t.Fatal("contender lease")
	}
	r, e := Run(ctx, s, catalog, testOptions(), Apply, nil)
	if e == nil || e.Error() != CodeLockTimeout || r.Ready {
		t.Fatal("actual lock contention", r, e)
	}
	if e = held.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", lock).Scan(&acquired); e != nil || acquired != 1 {
		t.Fatal("contender release")
	}
	held.Close()
	migrationRun(t, s, catalog, Apply)
	migrationExec(t, s, "UPDATE schema_migrations SET source_sha256=? WHERE step=1", strings.Repeat("0", 64))
	if _, e = Run(ctx, s, catalog, testOptions(), Readiness, nil); e == nil || e.Error() != CodeChecksumMismatch {
		t.Fatal("stored checksum drift", e)
	}
	migrationExec(t, s, "UPDATE schema_migrations SET source_sha256=? WHERE step=1", catalog.steps[0].SourceSHA256)
	migrationExec(t, s, "INSERT INTO schema_migrations(step,source_sha256,object_name,object_sha256,state,started_at,applied_at) VALUES(99,?,'future',?,'applied',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))", strings.Repeat("1", 64), strings.Repeat("2", 64))
	if _, e = Run(ctx, s, catalog, testOptions(), Readiness, nil); e == nil || e.Error() != CodeJournalInvalid {
		t.Fatal("future schema admitted", e)
	}
	migrationExec(t, s, "DELETE FROM schema_migrations WHERE step=99")
	migrationRun(t, s, catalog, Readiness)
}

func TestActualMySQLMigrationReadback(t *testing.T) {
	s, catalog, _ := actualMigration(t)
	migrationRun(t, s, catalog, Readiness)
	var installed bool
	var content string
	if e := s.Pool().QueryRowContext(context.Background(), "SELECT bootstrap_complete FROM installation_state WHERE id=1").Scan(&installed); e != nil || !installed {
		t.Fatal("installed identity not preserved")
	}
	if e := s.Pool().QueryRowContext(context.Background(), "SELECT `text` FROM workspace_notes").Scan(&content); e != nil || content != "Preserved \u4e16\u754c🙂\nsecond line" {
		t.Fatal("typed synthetic note not preserved")
	}
	t.Log("MYSQL_MIGRATED_SYNTHETIC_ROWS_SHA256=" + migrationDigest(t, s, catalog))
}

func TestActualMySQLMigrationConcurrentApplyExactlyOnce(t *testing.T) {
	s, catalog, _ := actualMigration(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	options := testOptions()
	options.LockWaitSeconds = 5
	for range 2 {
		go func() {
			<-start
			r, e := Run(context.Background(), s, catalog, options, Apply, nil)
			if e == nil && (!r.Ready || !r.LockReleased) {
				e = fail("MYSQL_MIGRATION_TEST_INCOMPLETE")
			}
			results <- e
		}()
	}
	close(start)
	for range 2 {
		if e := <-results; e != nil {
			t.Fatal("actual concurrent operator", e)
		}
	}
	var count int
	if e := s.Pool().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations").Scan(&count); e != nil || count != catalog.Steps() {
		t.Fatal("duplicate concurrent migration journal")
	}
	migrationRun(t, s, catalog, Readiness)
}
