package databaseaccess

import (
	"encoding/json"
	"errors"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestModelFamilyRuntimeGrantUpgradeRetainsPublicationAndDenial(t *testing.T) {
	f, runtime := resumeTableGrantUpgrade(t, 60, "model_families")
	ctx := t.Context()
	var tenant, workspace, user string
	if err := runtime.QueryRow(ctx, `SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text
		FROM installation_state WHERE id=1 AND bootstrap_complete`).Scan(&tenant, &workspace, &user); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(wire.ApprovedModel())
	if err != nil {
		t.Fatal(err)
	}
	version := uuid.NewString()
	if _, err := runtime.Exec(ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by)
		VALUES($1,$2,$3,'Synthetic role publication',$4,$5,$6::jsonb,'ready',$7)`,
		version, tenant, workspace, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, user); err != nil {
		t.Fatal("original restricted runtime could not publish a family member", err)
	}
	var family string
	var local bool
	if err := runtime.QueryRow(ctx, `SELECT f.id::text,
		(f.site_id,f.cell_id,m.site_id,m.cell_id)=(local_site_id(),local_cell_id(),local_site_id(),local_cell_id())
		FROM model_families f
		JOIN model_family_members m ON (m.tenant_id,m.workspace_id,m.family_id)=(f.tenant_id,f.workspace_id,f.id)
		WHERE m.version_id=$1 AND f.initial_version_id=$1`, version).Scan(&family, &local); err != nil || family == version || !local {
		t.Fatal("runtime publication did not persist distinct exact family identity", err)
	}
	for _, statement := range []string{
		"UPDATE model_families SET initial_version_id=initial_version_id",
		"DELETE FROM model_families",
		"UPDATE model_family_members SET family_id=family_id",
		"DELETE FROM model_family_members",
	} {
		_, err := runtime.Exec(ctx, statement)
		var denied *pgconn.PgError
		if !errors.As(err, &denied) || denied.Code != "23514" || denied.Message != "MODEL_FAMILY_IMMUTABLE" {
			t.Fatal("domain trigger did not protect immutable family state", statement, err)
		}
	}
	for _, table := range []string{"model_families", "model_family_members"} {
		var count int
		if err := runtime.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatal("refused mutations changed retained family rows", table, count, err)
		}
	}
	// Current DML may stage an exact family only inside a transaction that also
	// supplies its required version/member. Commit must reject an orphan.
	tx, err := runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	orphan := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO model_families(id,tenant_id,workspace_id,initial_version_id)
		VALUES($1,$2,$3,$4)`, orphan, tenant, workspace, uuid.NewString()); err != nil {
		t.Fatal("deferred staging", err)
	}
	err = tx.Commit(ctx)
	var denied *pgconn.PgError
	if !errors.As(err, &denied) || denied.Code != "23503" {
		t.Fatal("orphan family escaped deferred commit validation", err)
	}
	var count int
	if err := f.lease.QueryRow(ctx, "SELECT count(*) FROM model_families WHERE id=$1", orphan).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed commit retained an orphan family", count, err)
	}
}
