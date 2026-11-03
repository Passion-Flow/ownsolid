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
)

func workflowsPriorProjection(t *testing.T, p *pgxpool.Pool, columns map[string][]string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for table, names := range columns {
		where := ""
		if table == "schema_migrations" {
			where = " WHERE version<=18"
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

func TestM13CumulativeForwardCatalogPreservesM12(t *testing.T) {
	s := fixture.NewM12(t)
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
	if e = rows.Err(); e != nil || len(columns) != 74 {
		t.Fatal(len(columns), e)
	}
	before := workflowsPriorProjection(t, s.DB, columns)
	if e = database.Migrate(t.Context(), s.DB, "workflows"); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, workflowsPriorProjection(t, s.DB, columns)) {
		t.Fatal("prior table projections changed")
	}
	var tables, entries int
	if e = s.DB.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),(SELECT count(*) FROM schema_migrations)").Scan(&tables, &entries); e != nil || tables != 80 || entries != 19 {
		t.Fatal(tables, entries, e)
	}
	fresh := fixture.NewM13(t)
	if !reflect.DeepEqual(gatewayCatalog(t, s.DB), gatewayCatalog(t, fresh.DB)) {
		t.Fatal("fresh/upgraded catalogs differ")
	}
	if e = database.Migrate(t.Context(), s.DB, "workflows"); e != nil || !reflect.DeepEqual(before, workflowsPriorProjection(t, s.DB, columns)) {
		t.Fatal("repeat changed original rows", e)
	}
	for _, disabled := range []string{"knowledge", "sso", "lifecycle", "training", "platform", "datasets", "gateway"} {
		if database.Check(t.Context(), s.DB, disabled) == nil {
			t.Fatal("wrong schema admitted", disabled)
		}
	}
	t.Log(fmt.Sprintf("%d original projections, immutable 18-entry prefix, 80 tables/19 entries and fresh/upgrade/repeat equivalence", len(columns)))
}

func TestM13CumulativeCatalogFingerprintReadback(t *testing.T) {
	s := fixture.NewM13(t)
	var raw string
	if e := s.DB.QueryRow(t.Context(), database.WorkflowCatalogSQL).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	t.Logf("CURRENT_WORKFLOW_CATALOG_SHA256=%x", sha256.Sum256([]byte(raw)))
}

func TestM13CumulativeMalformedCatalogRefusesBeforeServingOrRestore(t *testing.T) {
	s := fixture.NewM13(t)
	for _, sql := range []string{
		"ALTER TABLE workflow_runs DROP COLUMN error_code",
		"ALTER TABLE workflow_runs ALTER COLUMN question DROP NOT NULL",
		"ALTER TABLE workflow_versions DISABLE TRIGGER workflow_publication_immutable",
		"DROP INDEX workflow_one_active_turn_idx",
		"ALTER TABLE workflows DROP CONSTRAINT workflow_schema1_draft",
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
		if database.CheckAccountProfileWithin(t.Context(), transaction.PostgreSQLTransaction(tx), "workflows") == nil {
			tx.Rollback(t.Context())
			t.Fatal("malformed catalog accepted")
		}
		if e = tx.Rollback(t.Context()); e != nil {
			t.Fatal(e)
		}
	}
	if e := database.Check(t.Context(), s.DB, "workflows"); e != nil {
		t.Fatal("valid catalog failed after rollback", e)
	}
	if e := database.Migrate(t.Context(), s.DB, "workflows"); e != nil {
		t.Fatal("exact repeated migration", e)
	}
}
