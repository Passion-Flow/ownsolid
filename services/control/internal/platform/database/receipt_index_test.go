package database_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func receiptPGDefinition(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	var valid, ready, unique bool
	var kind string
	var columns []string
	e := p.QueryRow(context.Background(), `SELECT i.indisvalid,i.indisready,i.indisunique,am.amname,ARRAY(SELECT a.attname::text FROM unnest(i.indkey) WITH ORDINALITY AS k(number,position) JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=k.number ORDER BY k.position) FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_am am ON am.oid=c.relam WHERE i.indexrelid='audit_operation_lookup_idx'::regclass AND i.indrelid='audit_events'::regclass`).Scan(&valid, &ready, &unique, &kind, &columns)
	if e != nil || !valid || !ready || unique || kind != "btree" || strings.Join(columns, ",") != "request_id,actor_user_id,audience,tenant_id" {
		t.Fatal("actual PostgreSQL receipt index definition", e)
	}
}
func receiptPGBusiness(t *testing.T, p *pgxpool.Pool) string {
	t.Helper()
	parts := []string{}
	for _, table := range []string{"users", "password_credentials", "workspaces", "workspace_memberships", "sessions", "audit_events"} {
		// Table identifiers are fixed owned test contracts, never caller values.
		var value string
		e := p.QueryRow(context.Background(), "SELECT COALESCE(jsonb_agg(to_jsonb(row) ORDER BY to_jsonb(row)::text)::text,'[]') FROM "+table+" AS row").Scan(&value)
		if e != nil {
			t.Fatal(e)
		}
		parts = append(parts, value)
	}
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func receiptPGSeed(t *testing.T, p *pgxpool.Pool, count int) (string, string, string, string) {
	t.Helper()
	var user, tenant, workspace string
	e := p.QueryRow(context.Background(), "SELECT primary_admin_user_id::text,tenant_id::text,default_workspace_id::text FROM installation_state WHERE id=1").Scan(&user, &tenant, &workspace)
	if e != nil {
		t.Fatal(e)
	}
	if count < 1 || count > 1000 {
		t.Fatal("bounded synthetic workload required")
	}
	if _, e = p.Exec(context.Background(), `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,request_id) SELECT gen_random_uuid(),clock_timestamp(),$1,$2,$3,'admin','workspace.updated','success','workspace',$3,gen_random_uuid() FROM generate_series(1,$4::integer)`, user, tenant, workspace, count); e != nil {
		t.Fatal(e)
	}
	var request string
	if e = p.QueryRow(context.Background(), "SELECT request_id::text FROM audit_events WHERE action='workspace.updated' LIMIT 1").Scan(&request); e != nil {
		t.Fatal(e)
	}
	return request, user, tenant, workspace
}
func TestReceiptIndexPostgreSQLFreshWorkloadAndPlan(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	ctx := context.Background()
	request, user, tenant, workspace := receiptPGSeed(t, s.DB, 1000)
	receiptPGDefinition(t, s.DB)
	if e := database.Check(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	before := receiptPGBusiness(t, s.DB)
	if e := database.Migrate(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	if before != receiptPGBusiness(t, s.DB) {
		t.Fatal("fresh idempotence changed business/audit rows")
	}
	if _, e := s.DB.Exec(ctx, "ANALYZE audit_events"); e != nil {
		t.Fatal(e)
	}
	var raw []byte
	e := s.DB.QueryRow(ctx, `EXPLAIN (FORMAT JSON) SELECT action,workspace_id::text,target_type,target_id FROM audit_events WHERE request_id=$1 AND actor_user_id=$2 AND audience=$3 AND tenant_id=$4 AND outcome='success' AND action IN ('workspace.created','workspace.updated','workspace.archived','workspace.restored','workspace.member_added','workspace.member_updated','workspace.member_removed','workspace.note_created','workspace.note_updated','workspace.note_deleted') LIMIT 2`, request, user, "admin", tenant).Scan(&raw)
	if e != nil {
		t.Fatal(e)
	}
	var plan any
	if json.Unmarshal(raw, &plan) != nil {
		t.Fatal("actual PostgreSQL JSON plan")
	}
	selected := false
	var visit func(any)
	visit = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if v["Index Name"] == "audit_operation_lookup_idx" {
				selected = true
			}
			for _, item := range v {
				visit(item)
			}
		case []any:
			for _, item := range v {
				visit(item)
			}
		}
	}
	visit(plan)
	if !selected {
		t.Fatal("actual PostgreSQL receipt query did not select owned index")
	}
	service := &workspaces.Service{Identity: s, DB: s.DB}
	out, e := service.Operation(ctx, a, request)
	if e != nil || out.State != workspaces.OperationCommitted || out.Event == nil || out.Event.WorkspaceID != workspace {
		t.Fatal("actual indexed receipt", out, e)
	}
	var count int
	if e = s.DB.QueryRow(ctx, "SELECT COUNT(*) FROM audit_events WHERE action='workspace.updated'").Scan(&count); e != nil || count != 1000 {
		t.Fatal("actual PostgreSQL workload cardinality", count, e)
	}
	t.Log("POSTGRES_RECEIPT_INDEX_WORKLOAD_AUDIT_ROWS=1000;actual planner selected audit_operation_lookup_idx;actual authorized receipt matched;no latency or concurrency claim")
}
func TestReceiptIndexPostgreSQLUpgradeFailureRollbackAndReapply(t *testing.T) {
	s := fixture.NewM01(t)
	ctx := context.Background()
	entries, e := fs.ReadDir(migrations.Files, "postgresql")
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		var version int
		if _, e = fmt.Sscanf(entry.Name(), "%d_", &version); e != nil {
			t.Fatal(e)
		}
		if version <= 1 || version >= 18 {
			continue
		}
		raw, e := migrations.Files.ReadFile("postgresql/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		tx, e := s.DB.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, string(raw)); e != nil {
			tx.Rollback(ctx)
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)", version, hex.EncodeToString(sum[:])); e != nil {
			tx.Rollback(ctx)
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
	}
	fixture.Bootstrap(t, s)
	receiptPGSeed(t, s.DB, 1)
	before := receiptPGBusiness(t, s.DB)
	if e = database.Check(ctx, s.DB); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("current artifact accepted17", e)
	}
	if _, e = s.DB.Exec(ctx, "CREATE INDEX audit_operation_lookup_idx ON audit_events(actor_user_id)"); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(ctx, s.DB); e == nil {
		t.Fatal("conflicting wrong index silently accepted")
	}
	var versions int
	if e = s.DB.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&versions); e != nil || versions != 17 || before != receiptPGBusiness(t, s.DB) {
		t.Fatal("failed18 migration partially changed journal or data", versions, e)
	}
	if _, e = s.DB.Exec(ctx, "DROP INDEX audit_operation_lookup_idx"); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	receiptPGDefinition(t, s.DB)
	if e = database.Check(ctx, s.DB); e != nil || before != receiptPGBusiness(t, s.DB) {
		t.Fatal("populated17 to current preservation", e)
	}
	up, e := migrations.Files.ReadFile("postgresql/000018_audit_operation_lookup.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	down, e := migrations.Files.ReadFile("postgresql/000018_audit_operation_lookup.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	checksum := sha256.Sum256(up)
	retirementUp, e := migrations.Files.ReadFile("postgresql/000019_dataset_retirement_reason.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	retirementDown, e := migrations.Files.ReadFile("postgresql/000019_dataset_retirement_reason.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	retirementChecksum := sha256.Sum256(retirementUp)
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	// This exclusive synthetic scope follows the explicit installation-operator
	// lock and reconciles the exact18 checksum in the same actual transaction.
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	// Explicit isolated asset qualification reverses20/19 before18, preserving
	// contiguous journal order. Runtime operators never execute down assets.
	modelReviewDown20Within(t, tx)
	if _, e = tx.Exec(ctx, string(retirementDown)); e != nil {
		t.Fatal(e)
	}
	retirementTag, e := tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=19 AND checksum=$1", hex.EncodeToString(retirementChecksum[:]))
	if e != nil || retirementTag.RowsAffected() != 1 {
		t.Fatal("exact isolated19 rollback metadata", e)
	}
	if _, e = tx.Exec(ctx, string(down)); e != nil {
		t.Fatal(e)
	}
	tag, e := tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=18 AND checksum=$1", hex.EncodeToString(checksum[:]))
	if e != nil || tag.RowsAffected() != 1 {
		t.Fatal("exact rollback metadata", e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	var absent bool
	if e = s.DB.QueryRow(ctx, "SELECT to_regclass('audit_operation_lookup_idx') IS NULL").Scan(&absent); e != nil || !absent || before != receiptPGBusiness(t, s.DB) {
		t.Fatal("index-only down changed domain data", e)
	}
	if e = database.Check(ctx, s.DB); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("current artifact accepted downgraded schema", e)
	}
	if e = database.Migrate(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	receiptPGDefinition(t, s.DB)
	if e = database.Check(ctx, s.DB); e != nil || before != receiptPGBusiness(t, s.DB) {
		t.Fatal("actual18+19+20 reapply or data preservation", e)
	}
	retirementGuards(t, s.DB)
	modelReviewGuard(t, s.DB)
	t.Logf("POSTGRES_RECEIPT_INDEX_UPGRADE_FAILURE_REPAIR_DOWN_REAPPLY business_sha256=%s", before)
}
