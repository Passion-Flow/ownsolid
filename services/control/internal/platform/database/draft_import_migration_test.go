package database_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func draftImportScripts(t *testing.T) (string, string) {
	t.Helper()
	up, err := migrations.Files.ReadFile("postgresql/000060_dataset_draft_import_receipts.up.sql")
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(up)) != "d7e8eb61bd8afa4c1a3a1363d092685d13c55ca35d6965257457247336066868" {
		t.Fatal("formal up differs from the immutable fa82645c handoff", err)
	}
	down, err := migrations.Files.ReadFile("postgresql/000060_dataset_draft_import_receipts.down.sql")
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(down)) != "30a30989f237d89b56af6c5bef12115b3242089463ea608db445b1eb9df5d7df" {
		t.Fatal("formal down differs from the qualified retention guard", err)
	}
	return string(up), string(down)
}

type draftImportMigrationFixture struct {
	db                 *pgxpool.Pool
	ctx                context.Context
	draft              string
	predecessorCatalog string
	down               string
}

func newDraftImportMigrationFixture(t *testing.T) draftImportMigrationFixture {
	t.Helper()
	up, down := draftImportScripts(t)
	owner := fixture.NewAtVersion(t, 59)
	password := fixture.Bootstrap(t, owner)
	actor := fixture.Login(t, owner, "admin@example.invalid", password, "admin")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	var workspace string
	if err := owner.DB.QueryRow(ctx, "SELECT default_workspace_id::text FROM installation_state WHERE id=1 AND bootstrap_complete").Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	service := &datasets.Service{DB: owner.DB, Identity: owner, Workspaces: &workspaces.Service{DB: owner.DB, Identity: owner}}
	data, err := service.Create(ctx, actor, datasets.CreateRequest{WorkspaceID: workspace, Name: "Legacy migration dataset", Origin: "Synthetic fixture", License: "CC0-1.0", RightsAsserted: true, Reason: "Preserve predecessor metadata"}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	draft, err := service.CreateDraft(ctx, actor, workspace, data.ID, datasets.DraftRequest{SourceName: "legacy.jsonl", SourceSHA256: strings.Repeat("a", 64), ExpectedBytes: 2, Reason: "Preserve predecessor draft"}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	f := draftImportMigrationFixture{db: owner.DB, ctx: ctx, draft: draft.ID, down: down}
	if err := owner.DB.QueryRow(ctx, database.CatalogSQL).Scan(&f.predecessorCatalog); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.DB.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f draftImportMigrationFixture) receiptSQL() string {
	return `INSERT INTO dataset_draft_import_candidates(tenant_id,workspace_id,dataset_id,draft_id,site_id,cell_id,source_format,report)
 SELECT tenant_id,workspace_id,dataset_id,id,site_id,cell_id,$2,'{"source_bytes":42,"output_bytes":31}'::jsonb FROM dataset_drafts WHERE id=$1`
}
func (f draftImportMigrationFixture) receipt(t *testing.T, format string) {
	t.Helper()
	changed, err := f.db.Exec(f.ctx, f.receiptSQL(), f.draft, format)
	if err != nil || changed.RowsAffected() != 1 {
		t.Fatal("scoped receipt fixture", err)
	}
}
func (f draftImportMigrationFixture) retained(t *testing.T) string {
	t.Helper()
	var state string
	if err := f.db.QueryRow(f.ctx, `SELECT jsonb_build_object('drafts',(SELECT jsonb_agg(to_jsonb(d) ORDER BY id) FROM dataset_drafts d),'receipts',(SELECT jsonb_agg(to_jsonb(c) ORDER BY draft_id) FROM dataset_draft_import_candidates c))::text`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}
func (f draftImportMigrationFixture) rollback(t *testing.T) error {
	t.Helper()
	tx, err := f.db.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, f.down); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}
func assertDraftImportRollbackRefusal(t *testing.T, err error, code string) {
	t.Helper()
	var refusal *pgconn.PgError
	if !errors.As(err, &refusal) || refusal.Code != code || code == "55000" && refusal.Message != "DATASET_DRAFT_IMPORT_DOWNGRADE_REQUIRES_RESTORE" {
		t.Fatalf("expected %s import retention refusal, got %v", code, err)
	}
}

func TestDatasetDraftImportPristineRollback(t *testing.T) {
	f := newDraftImportMigrationFixture(t)
	var before string
	if err := f.db.QueryRow(f.ctx, "SELECT to_jsonb(d)::text FROM dataset_drafts d WHERE id=$1", f.draft).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := f.rollback(t); err != nil {
		t.Fatal(err)
	}
	var catalog, after string
	if err := f.db.QueryRow(f.ctx, database.CatalogSQL).Scan(&catalog); err != nil || catalog != f.predecessorCatalog {
		t.Fatal("pristine rollback did not restore the exact predecessor catalog", err)
	}
	if err := f.db.QueryRow(f.ctx, "SELECT to_jsonb(d)::text FROM dataset_drafts d WHERE id=$1", f.draft).Scan(&after); err != nil || after != before {
		t.Fatal("pristine rollback changed the predecessor draft", err)
	}
}
func TestDatasetDraftImportRetainedRollback(t *testing.T) {
	for _, format := range []string{"csv", "parquet"} {
		t.Run(format, func(t *testing.T) {
			f := newDraftImportMigrationFixture(t)
			f.receipt(t, format)
			before := f.retained(t)
			for range 2 {
				assertDraftImportRollbackRefusal(t, f.rollback(t), "55000")
				if f.retained(t) != before {
					t.Fatal("refused rollback changed drafts or import receipts")
				}
			}
		})
	}
}
func TestDatasetDraftImportPriorSnapshotRollback(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			f := newDraftImportMigrationFixture(t)
			tx, err := f.db.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			var initial int
			if err = tx.QueryRow(f.ctx, "SELECT count(*) FROM dataset_draft_import_candidates").Scan(&initial); err != nil || initial != 0 {
				t.Fatal("pristine prior snapshot", err)
			}
			f.receipt(t, "parquet")
			before := f.retained(t)
			_, err = tx.Exec(f.ctx, f.down)
			assertDraftImportRollbackRefusal(t, err, "25001")
			if err = tx.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			if f.retained(t) != before {
				t.Fatal("prior snapshot guard changed the committed writer state")
			}
		})
	}
}
func TestDatasetDraftImportFreshIsolationRollback(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.ReadUncommitted, pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			f := newDraftImportMigrationFixture(t)
			tx, err := f.db.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			f.receipt(t, "csv")
			before := f.retained(t)
			_, err = tx.Exec(f.ctx, f.down)
			assertDraftImportRollbackRefusal(t, err, "55000")
			if err = tx.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			if f.retained(t) != before {
				t.Fatal("fresh isolation guard changed retained data")
			}
		})
	}
}
func TestDatasetDraftImportRollbackWaitsForCommittedWriter(t *testing.T) {
	f := newDraftImportMigrationFixture(t)
	writer, err := f.db.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(f.ctx)
	if changed, err := writer.Exec(f.ctx, f.receiptSQL(), f.draft, "parquet"); err != nil || changed.RowsAffected() != 1 {
		t.Fatal("uncommitted scoped receipt", err)
	}
	down, err := f.db.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	pid := down.Conn().PgConn().PID()
	downContext, cancelDown := context.WithCancel(f.ctx)
	result := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, err := down.Exec(downContext, f.down)
		result <- err
	}()
	defer func() {
		cancelDown()
		<-finished
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(f.ctx), time.Second)
		defer cancel()
		_ = down.Rollback(cleanup)
	}()
	wait, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	for {
		var blocked bool
		if err = f.db.QueryRow(wait, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='dataset_drafts'::regclass AND mode='AccessExclusiveLock' AND NOT granted)`, pid).Scan(&blocked); err != nil {
			t.Fatal("observe retained writer fence", err)
		}
		if blocked {
			break
		}
		select {
		case err := <-result:
			t.Fatal("rollback did not wait for its committed writer", err)
		case <-wait.Done():
			t.Fatal("rollback did not reach its table lock")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = writer.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	assertDraftImportRollbackRefusal(t, <-result, "55000")
	if err = down.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.db.QueryRow(f.ctx, "SELECT count(*) FROM dataset_draft_import_candidates WHERE draft_id=$1", f.draft).Scan(&count); err != nil || count != 1 {
		t.Fatal("rollback lost the concurrent committed receipt", err)
	}
}
func TestDatasetDraftImportScopeConstraints(t *testing.T) {
	f := newDraftImportMigrationFixture(t)
	f.receipt(t, "csv")
	if _, err := f.db.Exec(f.ctx, `INSERT INTO sites(id,display_name) VALUES('draft-alt-site','Other draft site');
 INSERT INTO cells(id,site_id,display_name) VALUES('draft-alt-cell',local_site_id(),'Other draft cell'),('draft-alt-site-cell','draft-alt-site','Other site cell')`); err != nil {
		t.Fatal(err)
	}
	before := f.retained(t)
	for _, tc := range []struct{ name, sql, code string }{
		{"other_cell", `UPDATE dataset_draft_import_candidates SET cell_id='draft-alt-cell' WHERE draft_id=$1`, "23503"},
		{"other_site", `UPDATE dataset_draft_import_candidates SET site_id='draft-alt-site',cell_id='draft-alt-site-cell' WHERE draft_id=$1`, "23503"},
		{"null_site", `UPDATE dataset_draft_import_candidates SET site_id=NULL WHERE draft_id=$1`, "23502"},
		{"null_cell", `UPDATE dataset_draft_import_candidates SET cell_id=NULL WHERE draft_id=$1`, "23502"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.db.Exec(f.ctx, tc.sql, f.draft)
			var refusal *pgconn.PgError
			if !errors.As(err, &refusal) || refusal.Code != tc.code || tc.code == "23503" && refusal.ConstraintName != "dataset_draft_import_candidate_scope_fk" {
				t.Fatal("scope constraint did not refuse", err)
			}
			if f.retained(t) != before {
				t.Fatal("scope refusal changed retained data")
			}
		})
	}
}
