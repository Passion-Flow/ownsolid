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

func localizationPriorProjection(t *testing.T, p *pgxpool.Pool, columns map[string][]string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for table, names := range columns {
		where := ""
		if table == "schema_migrations" {
			where = " WHERE version<=21"
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

func TestM16CumulativeForwardCatalogPreservesM15(t *testing.T) {
	s := fixture.NewM15(t)
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
	if e = rows.Err(); e != nil || len(columns) != 86 {
		t.Fatal(len(columns), e)
	}
	before := localizationPriorProjection(t, s.DB, columns)
	if e = database.Migrate(t.Context(), s.DB, "localization"); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, localizationPriorProjection(t, s.DB, columns)) {
		t.Fatal("prior table projections changed")
	}
	var tables, entries int
	if e = s.DB.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),(SELECT count(*) FROM schema_migrations)").Scan(&tables, &entries); e != nil || tables != 86 || entries != 22 {
		t.Fatal(tables, entries, e)
	}
	fresh := fixture.NewM16(t)
	if !reflect.DeepEqual(gatewayCatalog(t, s.DB), gatewayCatalog(t, fresh.DB)) {
		t.Fatal("fresh/upgraded catalogs differ")
	}
	if e = database.Migrate(t.Context(), s.DB, "localization"); e != nil || !reflect.DeepEqual(before, localizationPriorProjection(t, s.DB, columns)) {
		t.Fatal("repeat changed original rows", e)
	}
	for _, disabled := range []string{"portal", "plugins", "workflows", "knowledge", "sso", "lifecycle", "training", "platform", "datasets", "gateway"} {
		if database.Check(t.Context(), s.DB, disabled) == nil {
			t.Fatal("wrong schema admitted", disabled)
		}
	}
	t.Log(fmt.Sprintf("%d original projections, immutable 21-entry prefix, 86 tables/22 entries and fresh/upgrade/repeat equivalence", len(columns)))
}

func TestM16CumulativeCatalogFingerprintReadback(t *testing.T) {
	s := fixture.NewM16(t)
	var raw string
	if e := s.DB.QueryRow(t.Context(), database.LocalizationCatalogSQL).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	t.Logf("CURRENT_WORKFLOW_CATALOG_SHA256=%x", sha256.Sum256([]byte(raw)))
}

func TestM16CumulativeMalformedCatalogRefusesBeforeServingOrRestore(t *testing.T) {
	s := fixture.NewM16(t)
	for _, sql := range []string{
		"ALTER TABLE workflow_runs DROP COLUMN error_code",
		"ALTER TABLE workflow_runs ALTER COLUMN question DROP NOT NULL",
		"ALTER TABLE workflow_versions DISABLE TRIGGER workflow_publication_immutable",
		"DROP INDEX workflow_one_active_turn_idx",
		"ALTER TABLE workflows DROP CONSTRAINT workflow_supported_draft",
		"CREATE OR REPLACE FUNCTION workflow_immutable_publication() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$",
		"DROP TABLE workflow_mutation_intents",
		"CREATE TABLE future_owner(id integer)",
		"ALTER TABLE users DISABLE TRIGGER user_preferences_initialize",
		"DROP INDEX terminal_notification_unread_idx",
		"ALTER TABLE invitations DROP CONSTRAINT invitations_locale_check",
		"ALTER TABLE mail_deliveries ALTER COLUMN locale TYPE text",
		"ALTER TABLE terminal_notifications DISABLE TRIGGER terminal_notification_provenance",
		"ALTER TABLE user_preferences ALTER COLUMN theme DROP NOT NULL",
	} {
		tx, e := s.DB.Begin(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(t.Context(), sql); e != nil {
			tx.Rollback(t.Context())
			t.Fatal(e)
		}
		if database.CheckAccountProfileWithin(t.Context(), transaction.PostgreSQLTransaction(tx), "localization") == nil {
			tx.Rollback(t.Context())
			t.Fatal("malformed catalog accepted")
		}
		if e = tx.Rollback(t.Context()); e != nil {
			t.Fatal(e)
		}
	}
	if e := database.Check(t.Context(), s.DB, "localization"); e != nil {
		t.Fatal("valid catalog failed after rollback", e)
	}
	if e := database.Migrate(t.Context(), s.DB, "localization"); e != nil {
		t.Fatal("exact repeated migration", e)
	}
}

func TestM16CumulativeDisposableRollbackOnlyBeforeUse(t *testing.T) {
	s := fixture.NewM16(t)
	password := fixture.Bootstrap(t, s)
	down, e := migrations.Files.ReadFile("account/000022_localization.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := s.DB.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(t.Context(), string(down)); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(t.Context(), "DELETE FROM schema_migrations WHERE version=22"); e != nil {
		t.Fatal(e)
	}
	if e = database.CheckAccountProfileWithin(t.Context(), transaction.PostgreSQLTransaction(tx), "portal"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(t.Context(), s.DB, "localization"); e != nil {
		t.Fatal(e)
	}
	a := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	invitation, e := s.Invite(t.Context(), a, "", "createInvitation", "00000000-0000-4000-8000-000000000001", "locale-downgrade@example.invalid", "en", 0, "00000000-0000-4000-8000-000000000002")
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range []struct{ table, key, id string }{{"users", "id", a.Me.User.ID}, {"invitations", "id", invitation.ID}, {"mail_deliveries", "invitation_id", invitation.ID}} {
		if _, e = s.DB.Exec(t.Context(), "UPDATE "+item.table+" SET locale='fr' WHERE "+item.key+"=$1", item.id); e != nil {
			t.Fatal(e)
		}
		tx, e = s.DB.Begin(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		_, e = tx.Exec(t.Context(), string(down))
		tx.Rollback(t.Context())
		if e == nil || !strings.Contains(e.Error(), "LOCALIZATION_DOWNGRADE_IN_USE") {
			t.Fatal(item.table, e)
		}
		var saved string
		if e = s.DB.QueryRow(t.Context(), "SELECT locale FROM "+item.table+" WHERE "+item.key+"=$1", item.id).Scan(&saved); e != nil || saved != "fr" {
			t.Fatal(saved, e)
		}
		if _, e = s.DB.Exec(t.Context(), "UPDATE "+item.table+" SET locale='en' WHERE "+item.key+"=$1", item.id); e != nil {
			t.Fatal(e)
		}
	}
	for _, value := range []string{"FR", "fr-ca", "en_us", "en-12345-67890-12345-67890", "en\n", "x", strings.Repeat("a", 36)} {
		if _, e = s.DB.Exec(t.Context(), "UPDATE users SET locale=$1 WHERE id=$2", value, a.Me.User.ID); e == nil {
			t.Fatal("invalid stored locale", value)
		}
	}
	for _, value := range []string{"fr", "ar", "ber-Latn", "fa-AF", "zh-CN", "sr-Latn-RS", "en-US-posix"} {
		if _, e = s.DB.Exec(t.Context(), "UPDATE users SET locale=$1 WHERE id=$2", value, a.Me.User.ID); e != nil {
			t.Fatal(value, e)
		}
	}
}
