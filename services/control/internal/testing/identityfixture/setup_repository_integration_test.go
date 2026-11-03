//go:build repository_integration

package identityfixture

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

func TestM16ExplicitSetupReturnsCanonicalBusinessPool(t *testing.T) {
	s := NewM16WithSetup(t, SetupOptions{TimeoutSeconds: 180, StatementTimeoutSeconds: 30})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if e := database.Check(ctx, s.DB, "localization"); e != nil {
		t.Fatal("new M16 setup catalog", e)
	}
	var schema, statement, lock string
	if e := s.DB.QueryRow(ctx, "SELECT current_schema(),current_setting('statement_timeout'),current_setting('lock_timeout')").Scan(&schema, &statement, &lock); e != nil || statement != "5s" || lock != "3s" {
		t.Fatal("setup policy escaped into business", statement, lock, e)
	}
	var consumers int
	if e := s.DB.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1", "projectone-identity-setup-"+schema).Scan(&consumers); e != nil || consumers != 0 {
		t.Fatal("setup consumers were not joined", consumers, e)
	}
	_, e := s.DB.Exec(ctx, "SELECT pg_sleep(6)")
	var failure *pgconn.PgError
	if !errors.As(e, &failure) || failure.Code != "57014" {
		t.Fatal("business statement did not retain actual 5s server budget", e)
	}
	var readback int
	if e = s.DB.QueryRow(ctx, "SELECT 1").Scan(&readback); e != nil || readback != 1 {
		t.Fatal("business connection failed to recover after timed-out statement", e)
	}
	password := Bootstrap(t, s)
	a := Login(t, s, "admin@example.invalid", password, "portal")
	if a.Me.User.ID == "" || a.Me.Workspace.ID == "" {
		t.Fatal("canonical business data/authentication unavailable after setup")
	}
}
