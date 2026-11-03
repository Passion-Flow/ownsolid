package database_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

// Test-owned rollback preserves contiguous journals for qualification only.
// The caller already holds the isolated installation transaction lock.
func modelReviewDown20Within(t *testing.T, tx pgx.Tx) {
	t.Helper()
	up, e := migrations.Files.ReadFile("postgresql/000020_model_review_identity.up.sql")
	if e != nil {
		t.Fatal(e)
	}
	down, e := migrations.Files.ReadFile("postgresql/000020_model_review_identity.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(up)
	if _, e = tx.Exec(t.Context(), string(down)); e != nil {
		t.Fatal(e)
	}
	tag, e := tx.Exec(t.Context(), `DELETE FROM schema_migrations WHERE version=20 AND checksum=$1`, hex.EncodeToString(sum[:]))
	if e != nil || tag.RowsAffected() != 1 {
		t.Fatal("exact isolated20 down journal", e)
	}
}
