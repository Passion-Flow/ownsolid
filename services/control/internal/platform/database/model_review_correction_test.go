package database_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func modelReviewGuard(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	var count int
	if e := p.QueryRow(t.Context(), `SELECT count(*) FROM pg_constraint WHERE conrelid='model_preparation_reviews'::regclass AND conname='model_review_approval_identity_required' AND contype='c' AND convalidated`).Scan(&count); e != nil || count != 1 {
		t.Fatal("validated review identity guard required", e)
	}
	if e := database.Check(t.Context(), p); e != nil {
		t.Fatal("review identity schema readiness", e)
	}
}

func TestModelReviewCurrentIdentityGuards(t *testing.T) {
	s := modelReviewFrozen19(t)
	if e := database.Migrate(t.Context(), s.DB); e != nil {
		t.Fatal(e)
	}
	r := modelReviewSeed(t, s.DB)
	for _, item := range []struct {
		name, decision, modelName string
		model                     any
	}{
		{"approve_NULL_model", "approve", "Synthetic nonempty name", nil},
		{"approve_mismatched_model", "approve", "Synthetic nonempty name", r.model},
		{"approve_empty_name", "approve", "", r.artifact},
		{"reject_with_model", "reject", "", r.model},
		{"reject_with_name", "reject", "Unexpected name", nil},
	} {
		t.Run(item.name, func(t *testing.T) {
			before, journal := retirementBusinessDigest(t, s.DB), modelReviewJournal(t, s.DB)
			_, e := s.DB.Exec(t.Context(), modelReviewInsert, modelReviewArguments(r, item.decision, item.model, item.modelName)...)
			var refusal *pgconn.PgError
			if !errors.As(e, &refusal) || refusal.Code != "23514" || item.name == "approve_NULL_model" && refusal.ConstraintName != "model_review_approval_identity_required" {
				t.Fatal("actual model review identity CHECK category missing")
			}
			if retirementBusinessDigest(t, s.DB) != before || modelReviewJournal(t, s.DB) != journal {
				t.Fatal("refused model review changed persisted graph or journal")
			}
		})
	}
	modelReviewGuard(t, s.DB)
}

func TestModelReviewCurrentCompleteApproval(t *testing.T) {
	s := modelReviewFrozen19(t)
	if e := database.Migrate(t.Context(), s.DB); e != nil {
		t.Fatal(e)
	}
	modelReviewPublish(t, s.DB, modelReviewSeed(t, s.DB))
	modelReviewGuard(t, s.DB)
}

func TestModelReviewCurrentCompleteRejection(t *testing.T) {
	s := modelReviewFrozen19(t)
	if e := database.Migrate(t.Context(), s.DB); e != nil {
		t.Fatal(e)
	}
	r := modelReviewSeed(t, s.DB)
	arguments := modelReviewArguments(r, "reject", nil, "")
	tx, e := s.DB.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(t.Context())
	if _, e = tx.Exec(t.Context(), modelReviewInsert, arguments...); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(t.Context(), `UPDATE model_preparations SET state='rejected',attempt_number=1,current_attempt_id=$1,review_id=$2 WHERE id=$3`, r.attempt, arguments[0], r.preparation); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = s.DB.QueryRow(t.Context(), `SELECT count(*) FROM model_preparations p JOIN model_preparation_reviews r ON r.id=p.review_id WHERE p.id=$1 AND p.state='rejected' AND p.derived_model_id IS NULL AND r.decision='reject' AND r.model_id IS NULL AND r.model_name=''`, r.preparation).Scan(&count); e != nil || count != 1 {
		t.Fatal("valid model rejection did not persist", e)
	}
	modelReviewGuard(t, s.DB)
}

func TestModelReviewUpgrade19PreservesCompleteApproval(t *testing.T) {
	s := modelReviewFrozen19(t)
	modelReviewPublish(t, s.DB, modelReviewSeed(t, s.DB))
	before, journal := retirementBusinessDigest(t, s.DB), modelReviewJournal(t, s.DB)
	for _, phase := range []string{"upgrade", "noop"} {
		if e := database.Migrate(t.Context(), s.DB); e != nil || retirementBusinessDigest(t, s.DB) != before || modelReviewJournal(t, s.DB) != journal {
			t.Fatal("model review upgrade changed accepted graph or prefix", phase, e)
		}
		modelReviewGuard(t, s.DB)
	}
}

func TestModelReviewInvalidOldApprovalRefusesAtomicUpgrade(t *testing.T) {
	s := modelReviewFrozen19(t)
	r := modelReviewSeed(t, s.DB)
	retirementExec(t, s.DB, modelReviewInsert, modelReviewArguments(r, "approve", nil, "Synthetic corrupt historical approval")...)
	before, journal := retirementBusinessDigest(t, s.DB), modelReviewJournal(t, s.DB)
	e := database.Migrate(t.Context(), s.DB)
	var refusal *pgconn.PgError
	if !errors.As(e, &refusal) || refusal.Code != "23514" || refusal.ConstraintName != "model_review_approval_identity_required" {
		t.Fatal("invalid historical approval did not refuse migration20")
	}
	var versions, guards int
	if e = s.DB.QueryRow(t.Context(), `SELECT count(*) FROM schema_migrations`).Scan(&versions); e != nil || versions != 19 {
		t.Fatal("failed upgrade changed immutable journal", e)
	}
	if e = s.DB.QueryRow(t.Context(), `SELECT count(*) FROM pg_constraint WHERE conrelid='model_preparation_reviews'::regclass AND conname='model_review_approval_identity_required'`).Scan(&guards); e != nil || guards != 0 || retirementBusinessDigest(t, s.DB) != before || modelReviewJournal(t, s.DB) != journal {
		t.Fatal("failed model approval migration changed graph or left partial guard", e)
	}
	if e = database.Check(t.Context(), s.DB); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("corrupt historical approval served current readiness", e)
	}
	// No repair is invented: immutable human review provenance requires a
	// separately governed owner/L3 reconciliation, not automatic identity adoption.
}

func TestModelReviewExplicitIsolatedDownAndReapply(t *testing.T) {
	s := modelReviewFrozen19(t)
	modelReviewPublish(t, s.DB, modelReviewSeed(t, s.DB))
	if e := database.Migrate(t.Context(), s.DB); e != nil {
		t.Fatal(e)
	}
	before, journal := retirementBusinessDigest(t, s.DB), modelReviewJournal(t, s.DB)
	up, e := migrations.Files.ReadFile("postgresql/000020_model_review_identity.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	down, e := migrations.Files.ReadFile("postgresql/000020_model_review_identity.down.sql")
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
	if _, e = tx.Exec(t.Context(), string(down)); e != nil {
		t.Fatal(e)
	}
	tag, e := tx.Exec(t.Context(), `DELETE FROM schema_migrations WHERE version=20 AND checksum=$1`, hex.EncodeToString(sum[:]))
	if e != nil || tag.RowsAffected() != 1 {
		t.Fatal("exact isolated20 down journal", e)
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(t.Context(), s.DB); e == nil || e.Error() != "SCHEMA_INCOMPATIBLE" {
		t.Fatal("explicit isolated down served current readiness", e)
	}
	if e = database.Migrate(t.Context(), s.DB); e != nil || retirementBusinessDigest(t, s.DB) != before || modelReviewJournal(t, s.DB) != journal {
		t.Fatal("isolated model review reapply changed accepted graph/prefix", e)
	}
	modelReviewGuard(t, s.DB)
}
