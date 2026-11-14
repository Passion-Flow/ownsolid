package database_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestAVerificationBlockedByALockIsUnansweredNotIncompatible(t *testing.T) {
	s := fixture.New(t)
	ctx := t.Context()
	holder, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer holder.Rollback(ctx)
	if _, e = holder.Exec(ctx, `LOCK TABLE cells IN ACCESS EXCLUSIVE MODE`); e != nil {
		t.Fatal(e)
	}
	e = database.Check(ctx, s.DB)
	if e == nil || e.Error() != "SCHEMA_UNAVAILABLE" || !database.Unanswered(e) {
		t.Fatalf("a lock was read as a verdict: %v", e)
	}
	if e = holder.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, s.DB); e != nil {
		t.Fatal("the schema did not verify once the lock was gone:", e)
	}
}

func TestAMissingRelationIsStillAVerdict(t *testing.T) {
	s := fixture.New(t)
	if _, e := s.DB.Exec(t.Context(), `ALTER TABLE cells RENAME TO cells_moved`); e != nil {
		t.Fatal(e)
	}
	e := database.Check(t.Context(), s.DB)
	if e == nil || database.Unanswered(e) {
		t.Fatalf("a missing table was taken for a busy database: %v", e)
	}
}

func TestUnansweredNamesOnlyChecksWithoutAVerdict(t *testing.T) {
	for code, want := range map[string]bool{
		"SCHEMA_UNAVAILABLE": true, "DATABASE_UNAVAILABLE": true, "DATABASE_NETWORK_UNREACHABLE": true, "DATABASE_DNS_UNRESOLVED": true, "DATABASE_CONNECTION_LIMIT_REACHED": true,
		"SCHEMA_INCOMPATIBLE": false, "SCHEMA_CATALOG_INCOMPATIBLE": false, "SCHEMA_CELL_IDENTITY_INVALID": false,
		"DATABASE_RUNTIME_PRIVILEGES_INCOMPATIBLE": false, "DATABASE_AUTHENTICATION_FAILED": false, "INSTALLATION_LOCK_LOST": false,
	} {
		if database.Unanswered(errors.New(code)) != want {
			t.Errorf("%s: want %v", code, want)
		}
	}
	if database.Unanswered(nil) {
		t.Error("no failure is unanswered")
	}
}

func TestAReadinessCheckDoesNotWaitOutTheLockTimeout(t *testing.T) {
	s := fixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if e := database.CheckRuntimeReady(t.Context(), nil); e == nil {
		t.Fatal("a missing store was ready")
	}
	store, e := transaction.FromPostgreSQL(s.DB)
	if e != nil {
		t.Fatal(e)
	}
	// The fixture is the schema owner, which serving refuses: the probe must reach the same verdict.
	want := database.CheckRuntimeWithin(ctx, store)
	if e = database.CheckRuntimeReady(ctx, store); e == nil || want == nil || e.Error() != want.Error() || database.Unanswered(e) {
		t.Fatalf("the probe said %v where serving says %v", e, want)
	}
	holder, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer holder.Rollback(ctx)
	if _, e = holder.Exec(ctx, `LOCK TABLE cells IN ACCESS EXCLUSIVE MODE`); e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	e = database.CheckRuntimeReady(ctx, store)
	if e == nil || !database.Unanswered(e) {
		t.Fatalf("a locked table gave a verdict: %v", e)
	}
	// The fixture's lock timeout is 3 s; the probe must give up long before it.
	if waited := time.Since(started); waited > time.Second {
		t.Fatalf("the probe waited %s for a lock", waited)
	}
}
