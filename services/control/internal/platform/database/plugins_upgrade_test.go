package database_test

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func pluginsPriorProjection(t *testing.T, p *pgxpool.Pool, columns map[string][]string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for table, names := range columns {
		where := ""
		if table == "schema_migrations" {
			where = " WHERE version<=19"
		}
		selected := make([]string, len(names))
		for i, name := range names {
			selected[i] = pgx.Identifier{name}.Sanitize()
		}
		var raw string
		if e := p.QueryRow(t.Context(), "SELECT COALESCE(jsonb_agg(v ORDER BY v::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(t) v FROM (SELECT "+strings.Join(selected, ",")+" FROM "+pgx.Identifier{table}.Sanitize()+where+") t) rows").Scan(&raw); e != nil {
			t.Fatal(table, e)
		}
		out[table] = raw
	}
	return out
}

func TestM14CumulativeForwardCatalogPreservesM13(t *testing.T) {
	s := fixture.NewM13(t)
	fixture.Bootstrap(t, s)
	_ = retirementSeed(t, s.DB)
	// Capture every original table/column rather than comparing only new empty rows.
	rows, e := s.DB.Query(t.Context(), `SELECT table_name,column_name FROM information_schema.columns WHERE table_schema=current_schema() ORDER BY table_name,ordinal_position`)
	if e != nil {
		t.Fatal(e)
	}
	columns := map[string][]string{}
	for rows.Next() {
		var table, name string
		if e = rows.Scan(&table, &name); e != nil {
			t.Fatal(e)
		}
		columns[table] = append(columns[table], name)
	}
	rows.Close()
	if e = rows.Err(); e != nil || len(columns) != 80 {
		t.Fatal(len(columns), e)
	}
	before := pluginsPriorProjection(t, s.DB, columns)
	if e = database.Migrate(t.Context(), s.DB, "plugins"); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, pluginsPriorProjection(t, s.DB, columns)) {
		t.Fatal("prior table projections changed")
	}
	var tables, entries int
	if e = s.DB.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),(SELECT count(*) FROM schema_migrations)").Scan(&tables, &entries); e != nil || tables != 84 || entries != 20 {
		t.Fatal(tables, entries, e)
	}
	fresh := fixture.NewM14(t)
	if !reflect.DeepEqual(gatewayCatalog(t, s.DB), gatewayCatalog(t, fresh.DB)) {
		t.Fatal("fresh/upgraded catalogs differ")
	}
	if e = database.Migrate(t.Context(), s.DB, "plugins"); e != nil || !reflect.DeepEqual(before, pluginsPriorProjection(t, s.DB, columns)) {
		t.Fatal("repeat changed original rows", e)
	}
	for _, disabled := range []string{"workflows", "knowledge", "sso", "lifecycle", "training", "platform", "datasets", "gateway"} {
		if database.Check(t.Context(), s.DB, disabled) == nil {
			t.Fatal("wrong schema admitted", disabled)
		}
	}
	t.Log(fmt.Sprintf("%d original projections, immutable 18-entry prefix, 80 tables/19 entries and fresh/upgrade/repeat equivalence", len(columns)))
}

func TestM14CumulativeCatalogFingerprintReadback(t *testing.T) {
	s := fixture.NewM14(t)
	var raw string
	if e := s.DB.QueryRow(t.Context(), database.PluginCatalogSQL).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	t.Logf("CURRENT_WORKFLOW_CATALOG_SHA256=%x", sha256.Sum256([]byte(raw)))
}

func TestM14CumulativeMalformedCatalogRefusesBeforeServingOrRestore(t *testing.T) {
	s := fixture.NewM14(t)
	for _, sql := range []string{
		"ALTER TABLE workflow_runs DROP COLUMN error_code",
		"ALTER TABLE workflow_runs ALTER COLUMN question DROP NOT NULL",
		"ALTER TABLE workflow_versions DISABLE TRIGGER workflow_publication_immutable",
		"DROP INDEX workflow_one_active_turn_idx",
		"ALTER TABLE workflows DROP CONSTRAINT workflow_supported_draft",
		"CREATE OR REPLACE FUNCTION workflow_immutable_publication() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$",
		"DROP TABLE workflow_mutation_intents",
		"CREATE TABLE future_owner(id integer)",
	} {
		tx, e := s.DB.Begin(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(t.Context(), sql); e != nil {
			tx.Rollback(t.Context())
			t.Fatal(e)
		}
		if database.CheckAccountProfileWithin(t.Context(), transaction.PostgreSQLTransaction(tx), "plugins") == nil {
			tx.Rollback(t.Context())
			t.Fatal("malformed catalog accepted")
		}
		if e = tx.Rollback(t.Context()); e != nil {
			t.Fatal(e)
		}
	}
	if e := database.Check(t.Context(), s.DB, "plugins"); e != nil {
		t.Fatal("valid catalog failed after rollback", e)
	}
	if e := database.Migrate(t.Context(), s.DB, "plugins"); e != nil {
		t.Fatal("exact repeated migration", e)
	}
}

// This explicit throwaway fixture has never imported a plugin or schema2 user data.
func TestM14CumulativeDisposableRollbackOnlyBeforeUse(t *testing.T) {
	s := fixture.NewM14(t)
	raw, e := migrations.Files.ReadFile("account/000020_plugins.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(t.Context())
	if _, e = tx.Exec(t.Context(), string(raw)); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(t.Context(), "DELETE FROM schema_migrations WHERE version=20"); e != nil {
		t.Fatal(e)
	}
	if e = database.CheckAccountProfileWithin(t.Context(), transaction.PostgreSQLTransaction(tx), "workflows"); e != nil {
		t.Fatal("eligible rollback changed M13 catalog", e)
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(t.Context(), s.DB, "plugins"); e != nil {
		t.Fatal("eligible re-upgrade", e)
	}
	fixture.Bootstrap(t, s)
	if _, e = s.DB.Exec(t.Context(), `INSERT INTO workflows(id,tenant_id,workspace_id,name,name_key,execution_authority,draft_graph,draft_sha256,created_by) SELECT gen_random_uuid(),tenant_id,default_workspace_id,'Synthetic used schema2','synthetic used schema2',gen_random_uuid(),'{"schema_version":2}'::jsonb,repeat('a',64),primary_admin_user_id FROM installation_state WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(t.Context(), string(raw)); e == nil {
		t.Fatal("used schema2 rollback lost user data")
	}
	if e = database.Check(t.Context(), s.DB, "plugins"); e != nil {
		t.Fatal(e)
	}
}
