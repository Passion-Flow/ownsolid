package database_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func retirementCurrent(t *testing.T) (*identity.Service, retirementRows) {
	t.Helper()
	s := fixture.New(t)
	fixture.Bootstrap(t, s)
	return s, retirementSeed(t, s.DB)
}

func retirementGuards(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	var count int
	if e := p.QueryRow(t.Context(), `SELECT count(*) FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND contype='c' AND convalidated AND ((conrelid='datasets'::regclass AND conname='dataset_retirement_reason_required') OR (conrelid='dataset_versions'::regclass AND conname='dataset_version_retirement_reason_required'))`).Scan(&count); e != nil || count != 2 {
		t.Fatal("both validated owned retirement guards required", e)
	}
	if e := database.Check(t.Context(), p); e != nil {
		t.Fatal("current retirement schema readiness", e)
	}
}

func retirementRefuses(t *testing.T, p *pgxpool.Pool, statement string, args ...any) {
	t.Helper()
	before, journal := retirementBusinessDigest(t, p), retirementJournalPrefix(t, p)
	_, e := p.Exec(t.Context(), statement, args...)
	var constraint *pgconn.PgError
	if !errors.As(e, &constraint) || constraint.Code != "23514" {
		t.Fatal("actual retirement CHECK refusal category")
	}
	if retirementBusinessDigest(t, p) != before || retirementJournalPrefix(t, p) != journal {
		t.Fatal("refused retirement changed persisted rows or journal")
	}
}

func TestDatasetRetirementCurrentFreshGuards(t *testing.T) {
	s, r := retirementCurrent(t)
	retirementGuards(t, s.DB)
	for _, item := range []struct{ table, id string }{{"datasets", r.dataset}, {"dataset_versions", r.version}} {
		for _, invalid := range []struct{ name, assignment string }{{"NULL_reason", "retired_at=clock_timestamp(),retire_reason=NULL"}, {"empty_reason", "retired_at=clock_timestamp(),retire_reason=''"}, {"reason_without_retirement", "retired_at=NULL,retire_reason='Synthetic reason'"}} {
			t.Run(item.table+"_"+invalid.name, func(t *testing.T) {
				retirementRefuses(t, s.DB, "UPDATE "+item.table+" SET "+invalid.assignment+" WHERE id=$1", item.id)
			})
		}
	}
	t.Run("dataset_valid_retirement_retained_graph_and_name_reuse", func(t *testing.T) {
		retirementExec(t, s.DB, `UPDATE datasets SET retired_at=clock_timestamp(),retire_reason='Synthetic legitimate operator reason' WHERE id=$1`, r.dataset)
		retirementExec(t, s.DB, `INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,creation_key,creation_fingerprint) SELECT $1,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,$2,creation_fingerprint FROM datasets WHERE id=$3`, uuid.NewString(), uuid.NewString(), r.dataset)
		var count int
		if e := s.DB.QueryRow(t.Context(), `SELECT count(*) FROM datasets d JOIN dataset_drafts f ON f.dataset_id=d.id JOIN dataset_versions v ON v.id=f.published_version_id WHERE d.id=$1 AND d.retired_at IS NOT NULL AND d.retire_reason='Synthetic legitimate operator reason'`, r.dataset).Scan(&count); e != nil || count != 1 {
			t.Fatal("retirement lost referenced publication", e)
		}
	})
	t.Run("version_valid_retirement_preserves_publication", func(t *testing.T) {
		retirementExec(t, s.DB, `UPDATE dataset_versions SET retired_at=clock_timestamp(),retire_reason='Synthetic retained immutable artifact' WHERE id=$1`, r.version)
		var count int
		if e := s.DB.QueryRow(t.Context(), `SELECT count(*) FROM dataset_versions v JOIN dataset_drafts d ON d.published_version_id=v.id JOIN dataset_jobs j ON j.id=v.job_id JOIN dataset_job_attempts a ON a.id=v.attempt_id AND a.job_id=j.id WHERE v.id=$1 AND v.retired_at IS NOT NULL AND v.retire_reason IS NOT NULL`, r.version).Scan(&count); e != nil || count != 1 {
			t.Fatal("retirement lost immutable publication graph", e)
		}
	})
	before, journal := retirementBusinessDigest(t, s.DB), retirementJournalPrefix(t, s.DB)
	if e := database.Migrate(t.Context(), s.DB); e != nil || retirementBusinessDigest(t, s.DB) != before || retirementJournalPrefix(t, s.DB) != journal {
		t.Fatal("current retirement migration no-op changed accepted data", e)
	}
	retirementGuards(t, s.DB)
}

func TestDatasetRetirementUpgrade18PreservesData(t *testing.T) {
	s := retirementFrozen18(t)
	retirementSeed(t, s.DB)
	before, journal := retirementBusinessDigest(t, s.DB), retirementJournalPrefix(t, s.DB)
	if e := database.Migrate(t.Context(), s.DB); e != nil {
		t.Fatal("valid18-to19 upgrade", e)
	}
	if retirementBusinessDigest(t, s.DB) != before || retirementJournalPrefix(t, s.DB) != journal {
		t.Fatal("retirement upgrade rewrote accepted rows or old journal")
	}
	retirementGuards(t, s.DB)
	t.Log("RETIREMENT_VALID18_TO19_PRESERVED business_sha256=" + before)
}

func TestDatasetRetirementInvalidOldRowsRollbackAndExplicitRepair(t *testing.T) {
	for _, table := range []string{"datasets", "dataset_versions"} {
		t.Run(table, func(t *testing.T) {
			s := retirementFrozen18(t)
			r := retirementSeed(t, s.DB)
			id := r.dataset
			if table == "dataset_versions" {
				id = r.version
			}
			retirementExec(t, s.DB, "UPDATE "+table+" SET retired_at=clock_timestamp(),retire_reason=NULL WHERE id=$1", id)
			before, journal := retirementBusinessDigest(t, s.DB), retirementJournalPrefix(t, s.DB)
			e := database.Migrate(t.Context(), s.DB)
			var constraint *pgconn.PgError
			if !errors.As(e, &constraint) || constraint.Code != "23514" {
				t.Fatal("invalid old reason did not refuse actual migration")
			}
			var guards, versions int
			if e = s.DB.QueryRow(t.Context(), `SELECT count(*) FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conname IN ('dataset_retirement_reason_required','dataset_version_retirement_reason_required')`).Scan(&guards); e != nil || guards != 0 {
				t.Fatal("failed retirement migration left a partial first-table guard", e)
			}
			if e = s.DB.QueryRow(t.Context(), `SELECT count(*) FROM schema_migrations`).Scan(&versions); e != nil || versions != 18 || retirementBusinessDigest(t, s.DB) != before || retirementJournalPrefix(t, s.DB) != journal {
				t.Fatal("failed retirement migration changed old data/journal", e)
			}
			if e = database.Check(t.Context(), s.DB); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
				t.Fatal("invalid old schema served current readiness", e)
			}
			// This reason is deliberately supplied by this synthetic test operator;
			// product migration never invents or writes a customer reason.
			retirementExec(t, s.DB, "UPDATE "+table+" SET retire_reason='Synthetic operator-supplied correction' WHERE id=$1", id)
			repaired := retirementBusinessDigest(t, s.DB)
			if e = database.Migrate(t.Context(), s.DB); e != nil || retirementBusinessDigest(t, s.DB) != repaired || retirementJournalPrefix(t, s.DB) != journal {
				t.Fatal("explicit repaired retry changed accepted data", e)
			}
			retirementGuards(t, s.DB)
		})
	}
}

func TestDatasetRetirementExplicitIsolatedDownAndReapply(t *testing.T) {
	s, _ := retirementCurrent(t)
	before, journal := retirementBusinessDigest(t, s.DB), retirementJournalPrefix(t, s.DB)
	up, e := migrations.Files.ReadFile("postgresql/000019_dataset_retirement_reason.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	down, e := migrations.Files.ReadFile("postgresql/000019_dataset_retirement_reason.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(up)
	tx, e := s.DB.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(t.Context())
	if _, e = tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock($1)`, database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	modelReviewDown20Within(t, tx)
	if _, e = tx.Exec(t.Context(), string(down)); e != nil {
		t.Fatal(e)
	}
	tag, e := tx.Exec(t.Context(), `DELETE FROM schema_migrations WHERE version=19 AND checksum=$1`, hex.EncodeToString(sum[:]))
	if e != nil || tag.RowsAffected() != 1 {
		t.Fatal("exact isolated19 down journal", e)
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if retirementBusinessDigest(t, s.DB) != before || retirementJournalPrefix(t, s.DB) != journal {
		t.Fatal("explicit isolated down changed data")
	}
	if e = database.Check(t.Context(), s.DB); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("current artifact accepted explicitly downgraded schema", e)
	}
	if e = database.Migrate(t.Context(), s.DB); e != nil || retirementBusinessDigest(t, s.DB) != before || retirementJournalPrefix(t, s.DB) != journal {
		t.Fatal("retirement reapply changed accepted data", e)
	}
	retirementGuards(t, s.DB)
}
