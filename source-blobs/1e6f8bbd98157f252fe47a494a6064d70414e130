package database_test

import (
	"encoding/json"
	"errors"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestModelFamilyFormalMigrationRetainsLegacyIdentities(t *testing.T) {
	s := fixture.NewAtVersion(t, 60)
	fixture.Bootstrap(t, s)
	ctx := t.Context()
	var tenant, workspace, user string
	if err := s.DB.QueryRow(ctx, `SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text
		FROM installation_state WHERE id=1 AND bootstrap_complete`).Scan(&tenant, &workspace, &user); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(wire.ApprovedModel())
	if err != nil {
		t.Fatal(err)
	}
	versions := []string{uuid.NewString(), uuid.NewString()}
	for _, id := range versions {
		if _, err := s.DB.Exec(ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by)
			VALUES($1,$2,$3,'Synthetic retained version',$4,$5,$6::jsonb,'ready',$7)`,
			id, tenant, workspace, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, user); err != nil {
			t.Fatal(err)
		}
	}
	var before string
	if err := s.DB.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(v) ORDER BY id)::text FROM model_versions v`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, s.DB); err != nil {
		t.Fatal("formal nonempty predecessor upgrade", err)
	}
	var after string
	if err := s.DB.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(v) ORDER BY id)::text FROM model_versions v`).Scan(&after); err != nil || after != before {
		t.Fatal("family backfill rewrote legacy versions", err)
	}
	var mismatched int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM model_family_members m
		JOIN model_families f ON f.id=m.family_id JOIN model_versions v ON v.id=m.version_id
		WHERE (f.site_id,f.cell_id) IS DISTINCT FROM (v.site_id,v.cell_id)
		OR (m.site_id,m.cell_id) IS DISTINCT FROM (v.site_id,v.cell_id)`).Scan(&mismatched); err != nil || mismatched != 0 {
		t.Fatal("family backfill lost version cell ownership", mismatched, err)
	}
	families := make(map[string]string)
	for _, id := range versions {
		var family string
		if err := s.DB.QueryRow(ctx, `SELECT family_id::text FROM model_family_members
			WHERE tenant_id=$1 AND workspace_id=$2 AND version_id=$3`, tenant, workspace, id).Scan(&family); err != nil {
			t.Fatal(err)
		}
		if family == id || family == families[versions[0]] {
			t.Fatal("legacy model key/digest or version ID inferred a family")
		}
		families[id] = family
	}
	for range 2 {
		if err := database.Migrate(ctx, s.DB); err != nil {
			t.Fatal("repeated migration", err)
		}
		for version, family := range families {
			var retained string
			if err := s.DB.QueryRow(ctx, `SELECT family_id::text FROM model_family_members WHERE version_id=$1`, version).Scan(&retained); err != nil || retained != family {
				t.Fatal("repeat migration changed retained family identity", err)
			}
		}
	}
	settled := catalogUpgradeSnapshot(t, s.DB)
	down, err := migrations.Files.ReadFile("postgresql/000061_model_families.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, string(down))
	var denied *pgconn.PgError
	if !errors.As(err, &denied) || denied.Code != "0A000" || denied.Message != "MODEL_FAMILY_DOWNGRADE_REQUIRES_RETENTION" {
		t.Fatal("populated downgrade did not retain original family IDs", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if got := catalogUpgradeSnapshot(t, s.DB); got != settled {
		t.Fatal("refused downgrade changed schema or ledger")
	}
	for version, family := range families {
		var retained string
		if err := s.DB.QueryRow(ctx, `SELECT family_id::text FROM model_family_members WHERE version_id=$1`, version).Scan(&retained); err != nil || retained != family {
			t.Fatal("refused downgrade changed a retained family", err)
		}
	}
}

func TestModelFamilyCellScopeRejectsMismatchedReferences(t *testing.T) {
	s := fixture.New(t)
	fixture.Bootstrap(t, s)
	ctx := t.Context()
	var tenant, workspace, user, site, cell string
	if err := s.DB.QueryRow(ctx, `SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text,
		local_site_id(),local_cell_id() FROM installation_state WHERE id=1 AND bootstrap_complete`).Scan(&tenant, &workspace, &user, &site, &cell); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(wire.ApprovedModel())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name, site, cell string
		familyMismatch   bool
	}{
		{"version_site", "foreign-site", cell, false},
		{"version_cell", site, "foreign-cell", false},
		{"member_family_cell", site, "foreign-cell", true},
	} {
		t.Run(item.name, func(t *testing.T) {
			version, family := uuid.NewString(), uuid.NewString()
			tx, err := s.DB.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			familySite, familyCell := item.site, item.cell
			if item.familyMismatch {
				familySite, familyCell = site, cell
			}
			if _, err := tx.Exec(ctx, `INSERT INTO model_families(id,tenant_id,workspace_id,initial_version_id,site_id,cell_id)
				VALUES($1,$2,$3,$4,$5,$6)`, family, tenant, workspace, version, familySite, familyCell); err != nil {
				t.Fatal("stage family", err)
			}
			_, err = tx.Exec(ctx, `INSERT INTO model_family_members(version_id,tenant_id,workspace_id,family_id,site_id,cell_id)
				VALUES($1,$2,$3,$4,$5,$6)`, version, tenant, workspace, family, item.site, item.cell)
			if !item.familyMismatch {
				if err != nil {
					t.Fatal("stage scoped member", err)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by)
					VALUES($1,$2,$3,'Synthetic scope refusal',$4,$5,$6::jsonb,'ready',$7)`,
					version, tenant, workspace, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, user); err != nil {
					t.Fatal("stage local version", err)
				}
				_, err = tx.Exec(ctx, `SET CONSTRAINTS model_family_initial_version_cell_fk,model_family_member_version_cell_fk IMMEDIATE`)
			}
			var denied *pgconn.PgError
			if !errors.As(err, &denied) || denied.Code != "23503" {
				t.Fatal("foreign cell reference was accepted", err)
			}
			if item.familyMismatch && denied.ConstraintName != "model_family_member_family_cell_fk" {
				t.Fatal("member/family scope was not the refusing constraint", denied.ConstraintName)
			}
			if !item.familyMismatch && denied.ConstraintName != "model_family_initial_version_cell_fk" && denied.ConstraintName != "model_family_member_version_cell_fk" {
				t.Fatal("version scope was not the refusing constraint", denied.ConstraintName)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var retained int
			if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM model_versions WHERE id=$1)
				+(SELECT count(*) FROM model_family_members WHERE version_id=$1)
				+(SELECT count(*) FROM model_families WHERE id=$2)`, version, family).Scan(&retained); err != nil || retained != 0 {
				t.Fatal("refused scope retained partial family state", retained, err)
			}
		})
	}
}

func TestModelFamilyNonlocalPredecessorRefusesWithoutRelabeling(t *testing.T) {
	s := fixture.NewAtVersion(t, 60)
	fixture.Bootstrap(t, s)
	ctx := t.Context()
	manifest, err := json.Marshal(wire.ApprovedModel())
	if err != nil {
		t.Fatal(err)
	}
	version := uuid.NewString()
	if _, err := s.DB.Exec(ctx, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,cell_id)
		SELECT $1,tenant_id,default_workspace_id,'Synthetic inconsistent predecessor',$2,$3,$4::jsonb,'ready',primary_admin_user_id,'foreign-cell'
		FROM installation_state WHERE id=1 AND bootstrap_complete`,
		version, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest); err != nil {
		t.Fatal(err)
	}
	before := catalogUpgradeSnapshot(t, s.DB)
	for range 2 {
		err := database.Migrate(ctx, s.DB)
		var denied *pgconn.PgError
		if !errors.As(err, &denied) || denied.Code != "23503" || denied.ConstraintName != "model_family_initial_version_cell_fk" {
			t.Fatal("inconsistent predecessor was relabeled or failed outside scope validation", err)
		}
		if after := catalogUpgradeSnapshot(t, s.DB); after != before {
			t.Fatal("refused scope backfill changed schema or ledger")
		}
		var retained string
		if err := s.DB.QueryRow(ctx, "SELECT cell_id FROM model_versions WHERE id=$1", version).Scan(&retained); err != nil || retained != "foreign-cell" {
			t.Fatal("refused scope backfill relabeled a retained version", retained, err)
		}
	}
}

func TestModelFamilyEmptyRollbackRestoresPredecessorCatalog(t *testing.T) {
	s := fixture.NewAtVersion(t, 60)
	fixture.Bootstrap(t, s)
	ctx := t.Context()
	before := catalogUpgradeSnapshot(t, s.DB)
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for _, name := range []string{"000061_model_families.up.sql", "000061_model_families.down.sql"} {
		raw, err := migrations.Files.ReadFile("postgresql/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(raw)); err != nil {
			t.Fatal("empty formal SQL roundtrip", name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if after := catalogUpgradeSnapshot(t, s.DB); after != before {
		t.Fatal("empty rollback retained added platform keys or domain objects")
	}
}
