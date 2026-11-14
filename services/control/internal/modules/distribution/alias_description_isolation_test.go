package distribution_test

import (
	"errors"
	"testing"

	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func formalAliasDescriptionDown(t *testing.T) string {
	t.Helper()
	up, err := migrations.Files.ReadFile("postgresql/000059_model_alias_description.up.sql")
	if err != nil || string(up) != aliasDescriptionCandidateUp {
		t.Fatal("formal up script differs from the qualified fixture", err)
	}
	down, err := migrations.Files.ReadFile("postgresql/000059_model_alias_description.down.sql")
	if err != nil || string(down) != aliasDescriptionCandidateDown {
		t.Fatal("formal down script differs from the qualified fixture", err)
	}
	return string(down)
}

func TestAliasDescriptionRollbackRejectsPriorIsolationSnapshot(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			c := setupAccount(t, fixture.NewAtVersion(t, 58))
			ctx := t.Context()
			alias := c.published(t)
			if _, err := c.s.DB.Exec(ctx, aliasDescriptionCandidateUp); err != nil {
				t.Fatal(err)
			}
			tx, err := c.s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			var initial string
			if err := tx.QueryRow(ctx, "SELECT description FROM model_aliases WHERE id=$1", alias.ID).Scan(&initial); err != nil || initial != "" {
				t.Fatal("pristine prior snapshot", initial, err)
			}
			const committed = "Retain text committed after the operator snapshot"
			if _, err := c.s.DB.Exec(ctx, "UPDATE model_aliases SET description=$2 WHERE id=$1", alias.ID, committed); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, formalAliasDescriptionDown(t))
			var refusal *pgconn.PgError
			if !errors.As(err, &refusal) || refusal.Code != "25001" {
				t.Fatal("prior isolation snapshot did not refuse before DDL", err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var retained string
			if err := c.s.DB.QueryRow(ctx, "SELECT description FROM model_aliases WHERE id=$1", alias.ID).Scan(&retained); err != nil || retained != committed {
				t.Fatal("refused downgrade changed the committed writer state", retained, err)
			}
		})
	}
}

func TestAliasDescriptionRollbackFreshIsolationPreservesRetainedState(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.ReadUncommitted, pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			c := setupAccount(t, fixture.NewAtVersion(t, 58))
			ctx := t.Context()
			alias := c.published(t)
			if _, err := c.s.DB.Exec(ctx, aliasDescriptionCandidateUp); err != nil {
				t.Fatal(err)
			}
			tx, err := c.s.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := c.s.DB.Exec(ctx, "UPDATE model_aliases SET description='Retain committed text' WHERE id=$1", alias.ID); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, formalAliasDescriptionDown(t))
			assertAliasDescriptionRollbackError(t, err)
		})
	}
}
