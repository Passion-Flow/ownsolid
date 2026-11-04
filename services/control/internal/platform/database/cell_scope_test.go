package database_test

import (
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// globalTables belong to the global layer (ADR 0006). Every other table must carry
// site_id and cell_id, so a new table has to choose its layer here.
var globalTables = map[string]bool{
	"cells": true, "installation_state": true, "invitations": true, "mail_deliveries": true,
	"password_credentials": true, "password_reset_tokens": true, "platform_license_artifacts": true,
	"platform_license_state": true, "schema_migrations": true, "sessions": true, "sites": true,
	"sso_bindings": true, "sso_connectors": true, "sso_flows": true, "sso_mutation_intents": true,
	"tenants": true, "terminal_notifications": true, "user_preferences": true, "users": true,
	"workspace_memberships": true, "workspace_notes": true, "workspaces": true,
}

func TestEveryCellOwnedTableCarriesScope(t *testing.T) {
	s := fixture.New(t)
	rows, e := s.DB.Query(t.Context(), `SELECT t.tablename,count(*) FILTER (WHERE c.column_name IN ('site_id','cell_id') AND c.is_nullable='NO' AND c.column_default IS NOT NULL) FROM pg_tables t JOIN information_schema.columns c ON c.table_schema=t.schemaname AND c.table_name=t.tablename WHERE t.schemaname=current_schema() GROUP BY 1 ORDER BY 1`)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var table string
		var scoped int
		if e = rows.Scan(&table, &scoped); e != nil {
			t.Fatal(e)
		}
		seen++
		if want := map[bool]int{true: 0, false: 2}[globalTables[table]]; scoped != want && table != "cells" {
			t.Errorf("%s has %d scope columns, want %d", table, scoped, want)
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if seen != database.TableCount {
		t.Fatal(seen, database.TableCount)
	}
}

func TestNewRowsTakeTheLocalCell(t *testing.T) {
	s := fixture.New(t)
	fixture.Bootstrap(t, s)
	if _, e := s.DB.Exec(t.Context(), `INSERT INTO rate_limit_buckets(bucket_key,window_started_at,expires_at,attempt_count) VALUES(sha256('scope-test'),clock_timestamp(),clock_timestamp()+interval '1 minute',1)`); e != nil {
		t.Fatal(e)
	}
	var site, cell string
	if e := s.DB.QueryRow(t.Context(), `SELECT site_id,cell_id FROM rate_limit_buckets WHERE bucket_key=sha256('scope-test')`).Scan(&site, &cell); e != nil {
		t.Fatal(e)
	}
	if site != "default" || cell != "default" {
		t.Fatal(site, cell)
	}
}

func TestCheckRefusesAnotherCellIdentity(t *testing.T) {
	s := fixture.New(t)
	if e := database.Check(t.Context(), s.DB); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(t.Context(), `INSERT INTO cells(id,site_id,display_name) VALUES('other','default','Other')`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(t.Context(), `UPDATE cells SET is_local=(id='other')`); e != nil {
		t.Fatal(e)
	}
	if e := database.Check(t.Context(), s.DB); e == nil || e.Error() != "SCHEMA_CELL_IDENTITY_INVALID" {
		t.Fatal(e)
	}
}
