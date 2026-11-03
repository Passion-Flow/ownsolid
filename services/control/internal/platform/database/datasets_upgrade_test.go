package database_test

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"reflect"
	"testing"
)

func TestM08CurrentCatalogUpgradeAndRetirementIntegrity(t *testing.T) {
	s := fixture.NewM07(t)
	fixture.Bootstrap(t, s)
	before := m06Snapshot(t, s.DB)
	var prefix string
	if e := s.DB.QueryRow(t.Context(), "SELECT jsonb_agg(to_jsonb(m) ORDER BY version)::text FROM schema_migrations m").Scan(&prefix); e != nil {
		t.Fatal(e)
	}
	if e := database.Migrate(t.Context(), s.DB, "datasets"); e != nil {
		t.Fatal(e)
	}
	var tables, entries int
	if e := s.DB.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),(SELECT count(*) FROM schema_migrations)").Scan(&tables, &entries); e != nil || tables != 42 || entries != 14 {
		t.Fatal(tables, entries, e)
	}
	// Existing table contents, including the complete timestamped prefix, are unchanged.
	var afterPrefix string
	if e := s.DB.QueryRow(t.Context(), "SELECT jsonb_agg(to_jsonb(m) ORDER BY version)::text FROM schema_migrations m WHERE version<=12").Scan(&afterPrefix); e != nil || afterPrefix != prefix {
		t.Fatal("prefix changed", e)
	}
	if !reflect.DeepEqual(before, m06Snapshot(t, s.DB)) {
		t.Fatal("accepted prior table content changed")
	}
	fresh := fixture.NewM08(t)
	if !reflect.DeepEqual(gatewayCatalog(t, s.DB), gatewayCatalog(t, fresh.DB)) {
		t.Fatal("fresh/upgraded catalogs differ")
	}
	r := retirementSeed(t, s.DB)
	for _, item := range []struct{ table, id string }{{"datasets", r.dataset}, {"dataset_versions", r.version}} {
		for _, assignment := range []string{"retired_at=clock_timestamp(),retire_reason=NULL", "retired_at=clock_timestamp(),retire_reason=''", "retired_at=NULL,retire_reason='reason'"} {
			retirementRefuses(t, s.DB, "UPDATE "+item.table+" SET "+assignment+" WHERE id=$1", item.id)
		}
	}
	digest, journal := retirementBusinessDigest(t, s.DB), retirementJournalPrefix(t, s.DB)
	if e := database.Migrate(t.Context(), s.DB, "datasets"); e != nil || digest != retirementBusinessDigest(t, s.DB) || journal != retirementJournalPrefix(t, s.DB) {
		t.Fatal("no-op changed state", e)
	}
}
func TestM08InvalidRetirementRefusesCurrentForwardIncrementAtomically(t *testing.T) {
	for _, table := range []string{"datasets", "dataset_versions"} {
		t.Run(table, func(t *testing.T) {
			s := fixture.NewM07(t)
			fixture.Bootstrap(t, s)
			raw, e := migrations.Files.ReadFile("postgresql/000008_datasets.up.sql")
			if e != nil {
				t.Fatal(e)
			}
			sum := sha256.Sum256(raw)
			retirementExec(t, s.DB, string(raw))
			retirementExec(t, s.DB, "INSERT INTO schema_migrations(version,checksum) VALUES(13,$1)", hex.EncodeToString(sum[:]))
			r := retirementSeed(t, s.DB)
			id := r.dataset
			if table == "dataset_versions" {
				id = r.version
			}
			retirementExec(t, s.DB, "UPDATE "+table+" SET retired_at=clock_timestamp(),retire_reason=NULL WHERE id=$1", id)
			before, journal := retirementBusinessDigest(t, s.DB), retirementJournalPrefix(t, s.DB)
			if database.Migrate(t.Context(), s.DB, "datasets") == nil {
				t.Fatal("invalid reason migration admitted")
			}
			var guards, entries int
			if e := s.DB.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname IN ('dataset_retirement_reason_required','dataset_version_retirement_reason_required')),(SELECT count(*) FROM schema_migrations)").Scan(&guards, &entries); e != nil || guards != 0 || entries != 13 || before != retirementBusinessDigest(t, s.DB) || journal != retirementJournalPrefix(t, s.DB) {
				t.Fatal("failed migration not atomic", e)
			}
			retirementExec(t, s.DB, "UPDATE "+table+" SET retire_reason='Synthetic operator supplied correction' WHERE id=$1", id)
			repaired := retirementBusinessDigest(t, s.DB)
			if e := database.Migrate(t.Context(), s.DB, "datasets"); e != nil || repaired != retirementBusinessDigest(t, s.DB) {
				t.Fatal("explicit correction retry changed data", e)
			}
			if e := database.Check(t.Context(), s.DB, "datasets"); e != nil {
				t.Fatal(e)
			}
		})
	}
}
