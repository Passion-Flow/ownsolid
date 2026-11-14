package database_test

import (
	"context"
	"os"
	"testing"

	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestGatewayAliasDescriptionCandidateCellScope(t *testing.T) {
	s := fixture.New(t)
	raw, err := os.ReadFile(os.Getenv("ARC_GW_CANDIDATE_SQL"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(t.Context()))
	if _, err := tx.Exec(t.Context(), string(raw)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(t.Context(), `SELECT count(*) FROM information_schema.columns
       WHERE table_schema=current_schema() AND table_name='model_alias_description_intents'
       AND column_name IN ('site_id','cell_id') AND is_nullable='NO' AND column_default IS NOT NULL`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("model_alias_description_intents has %d non-null scoped columns with defaults, want 2", count)
	}
}
