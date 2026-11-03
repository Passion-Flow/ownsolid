//go:build mysql_integration

package mysqlmigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func membershipRowsDigest(t *testing.T, s *mysqlstore.Store) string {
	t.Helper()
	rows, e := s.Pool().QueryContext(context.Background(), "SELECT JSON_ARRAY(user_id,tenant_id,workspace_id,role,version,created_at,updated_at,authority_id) FROM workspace_memberships ORDER BY user_id,workspace_id")
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer rows.Close()
	values := []any{}
	for rows.Next() {
		var raw []byte
		var value any
		if rows.Scan(&raw) != nil || json.Unmarshal(raw, &value) != nil {
			t.Fatal("actual membership row projection")
		}
		values = append(values, value)
	}
	if rows.Err() != nil {
		t.Fatal(mysqlstore.SafeError(rows.Err()))
	}
	raw, _ := json.Marshal(values)
	return hash(string(raw))
}
func membershipSeededPrefix(t *testing.T) (*mysqlstore.Store, Catalog) {
	t.Helper()
	s, catalog, _ := actualMigration(t)
	prefix := catalog
	prefix.steps = catalog.steps[:32]
	migrationRun(t, s, prefix, Apply)
	seedEvolutionBusiness(t, s)
	var tenant, workspace string
	if e := s.Pool().QueryRowContext(context.Background(), "SELECT tenant_id,default_workspace_id FROM installation_state").Scan(&tenant, &workspace); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	for _, role := range []string{"member", "viewer"} {
		id := uuid.NewString()
		email := "synthetic+" + id + "@example.invalid"
		migrationExec(t, s, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,?,?,'Synthetic lifetime member','en','active')", id, email, email)
		migrationExec(t, s, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,?)", id, tenant, workspace, role)
	}
	return s, catalog
}
func membershipCurrent(t *testing.T, s *mysqlstore.Store) {
	t.Helper()
	var total, distinct, nulls int
	if e := s.Pool().QueryRowContext(context.Background(), "SELECT count(*),count(DISTINCT authority_id),COALESCE(SUM(authority_id IS NULL),0) FROM workspace_memberships").Scan(&total, &distinct, &nulls); e != nil || total != 3 || distinct != 3 || nulls != 0 {
		t.Fatal("actual distinct nonempty membership backfill", mysqlstore.SafeError(e))
	}
	rows, e := s.Pool().QueryContext(context.Background(), "SELECT authority_id FROM workspace_memberships")
	if e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil {
			t.Fatal("authority scan")
		}
		value, e := uuid.Parse(id)
		if e != nil || value == uuid.Nil || value.String() != id {
			t.Fatal("canonical lifetime UUID missing")
		}
	}
	if rows.Err() != nil {
		t.Fatal(mysqlstore.SafeError(rows.Err()))
	}
}
func TestActualMySQLMembershipUpgradeAndLifetime(t *testing.T) {
	s, catalog := membershipSeededPrefix(t)
	before := evolutionBusinessDigest(t, s)
	migrationRun(t, s, catalog, Apply)
	membershipCurrent(t, s)
	if before != evolutionBusinessDigest(t, s) {
		t.Fatal("membership upgrade altered existing business values")
	}
	var user, tenant, workspace, old string
	if e := s.Pool().QueryRowContext(context.Background(), "SELECT user_id,tenant_id,workspace_id,authority_id FROM workspace_memberships WHERE role='member'").Scan(&user, &tenant, &workspace, &old); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	migrationExec(t, s, "UPDATE workspace_memberships SET role='viewer',version=version+1 WHERE user_id=? AND workspace_id=?", user, workspace)
	var current string
	if e := s.Pool().QueryRowContext(context.Background(), "SELECT authority_id FROM workspace_memberships WHERE user_id=? AND workspace_id=?", user, workspace).Scan(&current); e != nil || current != old {
		t.Fatal("role update changed membership lifetime", mysqlstore.SafeError(e))
	}
	beforeRows := membershipRowsDigest(t, s)
	for _, item := range []struct {
		value  any
		number uint16
	}{{nil, 1048}, {"invalid", 3819}, {"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", 3819}} {
		_, e := s.Pool().ExecContext(context.Background(), "UPDATE workspace_memberships SET authority_id=? WHERE user_id=? AND workspace_id=?", item.value, user, workspace)
		var safe *mysqlstore.Error
		if !errors.As(mysqlstore.SafeError(e), &safe) || safe.Number != item.number {
			t.Fatal("invalid authority was not safely refused", mysqlstore.SafeError(e))
		}
		if membershipRowsDigest(t, s) != beforeRows {
			t.Fatal("invalid authority changed rows")
		}
	}
	migrationExec(t, s, "DELETE FROM workspace_memberships WHERE user_id=? AND workspace_id=?", user, workspace)
	migrationExec(t, s, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'member')", user, tenant, workspace)
	if e := s.Pool().QueryRowContext(context.Background(), "SELECT authority_id FROM workspace_memberships WHERE user_id=? AND workspace_id=?", user, workspace).Scan(&current); e != nil || current == old {
		t.Fatal("removed authority revived", mysqlstore.SafeError(e))
	}
	membershipCurrent(t, s)
	beforeRows = membershipRowsDigest(t, s)
	events := 0
	r, e := Run(context.Background(), s, catalog, testOptions(), Apply, func(Event) { events++ })
	if e != nil || !r.Ready || events != 0 || beforeRows != membershipRowsDigest(t, s) {
		t.Fatal("idempotent migration reassigned lifetime", r, e)
	}
	t.Log("MYSQL_MEMBERSHIP_BUSINESS_SHA256=" + hash(evolutionBusinessDigest(t, s)+beforeRows))
}
func TestActualMySQLMembershipFresh(t *testing.T) {
	s, catalog, _ := actualMigration(t)
	migrationRun(t, s, catalog, Apply)
	migrationRun(t, s, catalog, Readiness)
	seedEvolutionBusiness(t, s)
	var id string
	if e := s.Pool().QueryRowContext(context.Background(), "SELECT authority_id FROM workspace_memberships").Scan(&id); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	if parsed, e := uuid.Parse(id); e != nil || parsed == uuid.Nil || parsed.String() != id {
		t.Fatal("fresh bootstrap omitted default lifetime")
	}
	migrationRun(t, s, catalog, Readiness)
	var logBin bool
	var format string
	if e := s.Pool().QueryRowContext(context.Background(), "SELECT @@global.log_bin,@@global.binlog_format").Scan(&logBin, &format); e != nil {
		t.Fatal(mysqlstore.SafeError(e))
	}
	t.Logf("MYSQL_MEMBERSHIP_DECLARED_LOG_BIN=%t FORMAT=%s;no replicated-target claim", logBin, format)
}
func membershipInterrupted(t *testing.T, step int, stage string) {
	t.Helper()
	s, catalog := membershipSeededPrefix(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, e := Run(ctx, s, catalog, testOptions(), Apply, func(event Event) {
		if event.Step == step && event.Stage == stage {
			cancel()
		}
	})
	if e == nil || r.DirtyStep != step || !r.LockReleased {
		t.Fatal("actual membership interruption not journaled", r, e)
	}
	if _, e = Run(context.Background(), s, catalog, testOptions(), Apply, nil); e == nil || e.Error() != CodeReconciliationRequired {
		t.Fatal("membership dirty step automatically replayed", e)
	}
	assigned := ""
	if step == 34 && stage == Verified || step == 35 {
		assigned = membershipRowsDigest(t, s)
	}
	migrationRun(t, s, catalog, Reconcile)
	membershipCurrent(t, s)
	if assigned != "" && assigned != membershipRowsDigest(t, s) {
		t.Fatal("reconciliation overwrote assigned authority")
	}
}
func TestActualMySQLMembershipBeforeAdd(t *testing.T)      { membershipInterrupted(t, 33, Applying) }
func TestActualMySQLMembershipAfterAdd(t *testing.T)       { membershipInterrupted(t, 33, Verified) }
func TestActualMySQLMembershipBeforeSeed(t *testing.T)     { membershipInterrupted(t, 34, Applying) }
func TestActualMySQLMembershipAfterSeed(t *testing.T)      { membershipInterrupted(t, 34, Verified) }
func TestActualMySQLMembershipBeforeRequired(t *testing.T) { membershipInterrupted(t, 35, Applying) }
func TestActualMySQLMembershipAfterRequired(t *testing.T)  { membershipInterrupted(t, 35, Verified) }
func TestActualMySQLMembershipPrefilledAndDrift(t *testing.T) {
	s, catalog := membershipSeededPrefix(t)
	prefix := catalog
	prefix.steps = catalog.steps[:33]
	migrationRun(t, s, prefix, Apply)
	migrationExec(t, s, membershipAuthoritySeedSQL)
	before := membershipRowsDigest(t, s)
	if _, e := Run(context.Background(), s, catalog, testOptions(), Apply, nil); e == nil || e.Error() != CodeUnjournaledObject {
		t.Fatal("unjournaled nonempty prefill adopted", e)
	}
	if before != membershipRowsDigest(t, s) {
		t.Fatal("refused prefill changed authority")
	}
	migrationRun(t, s, catalog, Reconcile)
	if before != membershipRowsDigest(t, s) {
		t.Fatal("explicit prefill reconciliation changed authority")
	}
	migrationExec(t, s, "ALTER TABLE workspace_memberships DROP CHECK membership_authority_canonical")
	if _, e := Run(context.Background(), s, catalog, testOptions(), Readiness, nil); e == nil || e.Error() != CodeDefinitionDrift {
		t.Fatal("authority CHECK drift accepted", e)
	}
}
func TestActualMySQLMembershipReadback(t *testing.T) {
	s, catalog, _ := actualMigration(t)
	migrationRun(t, s, catalog, Readiness)
	membershipCurrent(t, s)
	t.Log("MYSQL_MEMBERSHIP_BUSINESS_SHA256=" + hash(evolutionBusinessDigest(t, s)+membershipRowsDigest(t, s)))
	var missing sql.NullString
	if e := s.Pool().QueryRowContext(context.Background(), "SELECT authority_id FROM workspace_memberships ORDER BY user_id LIMIT 1").Scan(&missing); e != nil || !missing.Valid {
		t.Fatal("restored membership authority missing", mysqlstore.SafeError(e))
	}
}

func TestActualMySQLMembershipSeedFailure(t *testing.T) {
	s, catalog := membershipSeededPrefix(t)
	prefix := catalog
	prefix.steps = catalog.steps[:33]
	migrationRun(t, s, prefix, Apply)
	defer func() { migrationExec(t, s, "DROP TRIGGER IF EXISTS synthetic_membership_seed_fault") }()
	r, e := Run(context.Background(), s, catalog, testOptions(), Apply, func(event Event) {
		if event.Step == 34 && event.Stage == Applying {
			migrationExec(t, s, "CREATE TRIGGER synthetic_membership_seed_fault BEFORE UPDATE ON workspace_memberships FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic membership seed fault'")
		}
	})
	if e == nil || e.Error() != CodeDataOutcomeUnknown || r.DirtyStep != 34 || !r.LockReleased {
		t.Fatal("actual data-step failure did not preserve uncertainty", r, e)
	}
	var missing int
	if e = s.Pool().QueryRowContext(context.Background(), "SELECT count(*) FROM workspace_memberships WHERE authority_id IS NULL").Scan(&missing); e != nil || missing != 3 {
		t.Fatal("failed data statement partially committed", mysqlstore.SafeError(e))
	}
	migrationExec(t, s, "DROP TRIGGER synthetic_membership_seed_fault")
	migrationRun(t, s, catalog, Reconcile)
	membershipCurrent(t, s)
}
