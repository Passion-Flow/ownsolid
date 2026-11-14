//go:build repository_integration

package models_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestActualModelFamilyRestrictedPrincipal(t *testing.T) {
	x := familySetup(t)
	for _, statement := range []string{
		`CREATE TABLE forbidden_family_ddl(id integer)`,
		`ALTER TABLE model_family_members DISABLE TRIGGER ALL`,
		`TRUNCATE model_families CASCADE`,
		`UPDATE schema_migrations SET checksum=checksum WHERE version=60`,
		`SELECT * FROM audit_chain_head`,
	} {
		_, err := x.f.PostgreSQL.Exec(x.ctx, statement)
		var denied *pgconn.PgError
		if !errors.As(err, &denied) || denied.Code != "42501" {
			t.Fatal("Control principal exceeded its original grants", statement, err)
		}
	}
	var version int
	if err := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil || version < 61 {
		t.Fatal("Control journal SELECT refused", version, err)
	}
	before := familyID(t, x, x.model)
	x.s.Referenced = func(context.Context, transaction.Executor, string, string, string) (bool, error) { return false, nil }
	retirementRetire(t, x, x.model)
	detail, err := x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, x.model, x.model)
	if err != nil || detail.State != "retired" || detail.FamilyID != before {
		t.Fatal("restricted principal lost retained exact history", err)
	}
	if err := x.s.ReadyWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, x.model); err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatal("restricted retirement granted execution", err)
	}
}

func TestActualModelFamilyFormalCellScope(t *testing.T) {
	x := familySetup(t)
	var site, cell string
	if err := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT local_site_id(),local_cell_id()`).Scan(&site, &cell); err != nil {
		t.Fatal(err)
	}
	foreignSite, foreignCell := "model-foreign-site", "model-foreign-cell"
	if site == foreignSite {
		foreignSite = "model-other-site"
	}
	if cell == foreignCell {
		foreignCell = "model-other-cell"
	}
	for _, item := range []struct{ name, site, cell string }{
		{"site", foreignSite, cell}, {"cell", site, foreignCell},
	} {
		t.Run(item.name, func(t *testing.T) {
			id, family := uuid.NewString(), uuid.NewString()
			raw, err := json.Marshal(wire.ApprovedModel())
			if err != nil {
				t.Fatal(err)
			}
			insert := func(mismatched bool) error {
				tx, err := x.f.PostgreSQL.Begin(x.ctx)
				if err != nil {
					return err
				}
				defer tx.Rollback(context.Background())
				if _, err = tx.Exec(x.ctx, `INSERT INTO model_families(id,tenant_id,workspace_id,initial_version_id,site_id,cell_id) VALUES($1,$2,$3,$4,$5,$6)`, family, x.f.Tenant, x.f.Workspace, id, item.site, item.cell); err != nil {
					return err
				}
				if _, err = tx.Exec(x.ctx, `INSERT INTO model_family_members(version_id,tenant_id,workspace_id,family_id,site_id,cell_id) VALUES($1,$2,$3,$4,$5,$6)`, id, x.f.Tenant, x.f.Workspace, family, item.site, item.cell); err != nil {
					return err
				}
				versionSite, versionCell := item.site, item.cell
				if mismatched {
					versionSite, versionCell = site, cell
				}
				if _, err = tx.Exec(x.ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,site_id,cell_id)
 VALUES($1,$2,$3,'Synthetic foreign family','qwen3-0.6b',$4,$5::jsonb,'ready',$6,$7,$8)`, id, x.f.Tenant, x.f.Workspace, wire.ModelCatalogDigest(), raw, x.f.User, versionSite, versionCell); err != nil {
					return err
				}
				if _, err = tx.Exec(x.ctx, `SET CONSTRAINTS model_family_initial_version_cell_fk,model_family_member_version_cell_fk IMMEDIATE`); err != nil {
					return err
				}
				return tx.Commit(x.ctx)
			}
			err = insert(true)
			var denied *pgconn.PgError
			if !errors.As(err, &denied) || denied.Code != "23503" ||
				denied.ConstraintName != "model_family_initial_version_cell_fk" && denied.ConstraintName != "model_family_member_version_cell_fk" {
				t.Fatal("mismatched scope did not fail composite FK", err)
			}
			var count int
			if err := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT (SELECT count(*) FROM model_versions WHERE id=$1)+(SELECT count(*) FROM model_family_members WHERE version_id=$1)+(SELECT count(*) FROM model_families WHERE id=$2)`, id, family).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed scope retained partial state", count, err)
			}
			// Consistent foreign records are deliberate synthetic operator inputs.
			// They satisfy referential integrity but must not pass local service scope.
			if err := insert(false); err != nil {
				t.Fatal("consistent foreign fixture", err)
			}
			before := x.audits(t)
			familyDenied(t, x, x.admin, x.f.Workspace, id, models.HistoryQuery{}, "RESOURCE_NOT_FOUND")
			for _, anchor := range []string{id, x.model} {
				detail, err := x.s.HistoricalVersion(x.ctx, x.admin, x.f.Workspace, anchor, id)
				if err == nil || err.Error() != "RESOURCE_NOT_FOUND" || detail.ID != "" {
					t.Fatal("foreign retained detail escaped local scope", err)
				}
			}
			err = x.f.Store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
				selected, err := x.s.StageVersionFamilyWithin(ctx, q, x.admin, x.f.Workspace, uuid.NewString(), id, "Synthetic foreign anchor refusal", uuid.NewString())
				if selected != "" {
					t.Error("foreign selection released a family ID")
				}
				return err
			})
			modelReadError(t, err, "RESOURCE_NOT_FOUND")
			if err := x.s.ReadyWithin(x.ctx, x.f.Store, x.f.Tenant, x.f.Workspace, id); err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
				t.Fatal("foreign execution reference admitted", err)
			}
			x.s.Referenced = func(context.Context, transaction.Executor, string, string, string) (bool, error) { return false, nil }
			retired, err := x.s.Retire(x.ctx, x.admin, id, "Synthetic foreign retirement refusal", uuid.NewString())
			if err == nil || err.Error() != "RESOURCE_NOT_FOUND" || retired.ID != "" {
				t.Fatal("foreign retirement escaped scope", err)
			}
			var state string
			if err := x.f.PostgreSQL.QueryRow(x.ctx, `SELECT state FROM model_versions WHERE id=$1`, id).Scan(&state); err != nil || state != "ready" || x.audits(t) != before {
				t.Fatal("foreign refusal changed model or audit", err)
			}
			if err := familyInsert(x.ctx, x.f.Store, x, uuid.NewString(), "deleted", time.Now().UTC().Truncate(time.Microsecond)); err != nil {
				t.Fatal("local publication after refusal", err)
			}
			page, err := x.s.History(x.ctx, x.admin, x.f.Workspace, x.model, models.HistoryQuery{})
			if err != nil || len(page.Items) != 1 || page.Items[0].ID != x.model {
				t.Fatal("local history after foreign refusal", err)
			}
		})
	}
}
