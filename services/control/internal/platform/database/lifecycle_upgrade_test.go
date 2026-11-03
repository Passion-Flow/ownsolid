package database_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func lifecyclePriorProjection(t *testing.T, p *pgxpool.Pool, columns map[string][]string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for table, names := range columns {
		where := ""
		if table == "schema_migrations" {
			where = " WHERE version<=15"
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

func TestM10CumulativeForwardCatalogPreservesM09(t *testing.T) {
	s := fixture.NewM09(t)
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
	if e = rows.Err(); e != nil || len(columns) != 50 {
		t.Fatal(len(columns), e)
	}
	before := lifecyclePriorProjection(t, s.DB, columns)
	if e = database.Migrate(t.Context(), s.DB, "lifecycle"); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, lifecyclePriorProjection(t, s.DB, columns)) {
		t.Fatal("prior table projections changed")
	}
	var tables, entries int
	if e = s.DB.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),(SELECT count(*) FROM schema_migrations)").Scan(&tables, &entries); e != nil || tables != 63 || entries != 16 {
		t.Fatal(tables, entries, e)
	}
	fresh := fixture.NewM10(t)
	if !reflect.DeepEqual(gatewayCatalog(t, s.DB), gatewayCatalog(t, fresh.DB)) {
		t.Fatal("fresh/upgraded catalogs differ")
	}
	if e = database.Migrate(t.Context(), s.DB, "lifecycle"); e != nil || !reflect.DeepEqual(before, lifecyclePriorProjection(t, s.DB, columns)) {
		t.Fatal("repeat changed original rows", e)
	}
	for _, disabled := range []string{"training", "platform", "datasets", "gateway"} {
		if database.Check(t.Context(), s.DB, disabled) == nil {
			t.Fatal("wrong schema admitted", disabled)
		}
	}
	t.Log(fmt.Sprintf("%d original projections, immutable 15-entry prefix, 63 tables/16 entries and fresh/upgrade/repeat equivalence", len(columns)))
}
