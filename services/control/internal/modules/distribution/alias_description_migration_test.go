package distribution_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func applyAliasDescriptionRollback(t *testing.T, c scenario) error {
	t.Helper()
	ctx := t.Context()
	tx, err := c.s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, aliasDescriptionCandidateDown); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func aliasDescriptionRetainedState(t *testing.T, c scenario) string {
	t.Helper()
	var state string
	if err := c.s.DB.QueryRow(t.Context(), `SELECT jsonb_build_object(
        'aliases',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM model_aliases a),
        'intents',(SELECT jsonb_agg(to_jsonb(i) ORDER BY tenant_id,workspace_id,actor_user_id,idempotency_key)
          FROM model_alias_description_intents i))::text`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertAliasDescriptionRollbackError(t *testing.T, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55000" || pgErr.Message != "MODEL_ALIAS_DESCRIPTION_DOWNGRADE_REQUIRES_RESTORE" {
		t.Fatalf("expected retained description downgrade refusal, got %v", err)
	}
}

func assertAliasDescriptionRollbackRefused(t *testing.T, c scenario) {
	t.Helper()
	before := aliasDescriptionRetainedState(t, c)
	for range 2 {
		assertAliasDescriptionRollbackError(t, applyAliasDescriptionRollback(t, c))
		if after := aliasDescriptionRetainedState(t, c); after != before {
			t.Fatal("refused downgrade changed descriptions or retained replay intents")
		}
	}
}

func TestAliasDescriptionRollbackPreservesRetainedState(t *testing.T) {
	for _, mode := range []string{"pristine", "published_text", "cleared_revision", "retained_intent"} {
		t.Run(mode, func(t *testing.T) {
			c := setupAccount(t, fixture.NewAtVersion(t, 58))
			ctx := t.Context()
			alias := c.published(t)
			var predecessorCatalog string
			if err := c.s.DB.QueryRow(ctx, database.CatalogSQL).Scan(&predecessorCatalog); err != nil {
				t.Fatal(err)
			}
			if _, err := c.s.DB.Exec(ctx, aliasDescriptionCandidateUp); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "published_text":
				// Explicit fixture state isolates the publication text predicate at revision 1.
				if _, err := c.s.DB.Exec(ctx, "UPDATE model_aliases SET description='Retain published intent' WHERE id=$1", alias.ID); err != nil {
					t.Fatal(err)
				}
			case "cleared_revision":
				// Empty visible text still carries an independent concurrency revision.
				if _, err := c.s.DB.Exec(ctx, "UPDATE model_aliases SET description_revision=2 WHERE id=$1", alias.ID); err != nil {
					t.Fatal(err)
				}
			case "retained_intent":
				// The valid scoped intent must independently prevent destruction.
				if _, err := c.s.DB.Exec(ctx, `INSERT INTO model_alias_description_intents
                    (tenant_id,workspace_id,actor_user_id,idempotency_key,alias_id,result_revision,fingerprint)
                    VALUES($1,$2,$3,$4,$5,1,$6)`, c.a.Me.Workspace.TenantID, alias.WorkspaceID,
					c.a.Me.User.ID, uuid.NewString(), alias.ID, make([]byte, 32)); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "pristine" {
				assertAliasDescriptionRollbackRefused(t, c)
				return
			}
			if err := applyAliasDescriptionRollback(t, c); err != nil {
				t.Fatal(err)
			}
			var restoredCatalog string
			if err := c.s.DB.QueryRow(ctx, database.CatalogSQL).Scan(&restoredCatalog); err != nil || restoredCatalog != predecessorCatalog {
				t.Fatal("pristine downgrade did not restore the exact predecessor catalog", err)
			}
			got, err := c.s.Alias(ctx, c.a, alias.WorkspaceID, alias.ID)
			if err != nil || got.ID != alias.ID || got.Revision != alias.Revision || got.DeploymentID != alias.DeploymentID ||
				got.DeploymentRevision != alias.DeploymentRevision || got.Description != nil || got.DescriptionRevision != nil {
				t.Fatal("pristine downgrade changed the retained alias", err)
			}
		})
	}
}

func TestAliasDescriptionRollbackWaitsForCommittedWriter(t *testing.T) {
	c := setupAccount(t, fixture.NewAtVersion(t, 58))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	alias := c.published(t)
	if _, err := c.s.DB.Exec(ctx, aliasDescriptionCandidateUp); err != nil {
		t.Fatal(err)
	}
	writer, err := c.s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(ctx)
	if _, err := writer.Exec(ctx, "UPDATE model_aliases SET description='Committed while downgrade waits' WHERE id=$1", alias.ID); err != nil {
		t.Fatal(err)
	}
	downgrade, err := c.s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer downgrade.Rollback(ctx)
	pid := downgrade.Conn().PgConn().PID()
	result := make(chan error, 1)
	go func() {
		_, err := downgrade.Exec(ctx, aliasDescriptionCandidateDown)
		if err == nil {
			err = downgrade.Commit(ctx)
		} else {
			_ = downgrade.Rollback(ctx)
		}
		result <- err
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := c.s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock')", pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("downgrade did not wait for the active writer: %v", err)
		case <-ctx.Done():
			t.Fatal("downgrade did not reach its table lock", ctx.Err())
		case <-ticker.C:
		}
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertAliasDescriptionRollbackError(t, <-result)
	var description string
	if err := c.s.DB.QueryRow(ctx, "SELECT description FROM model_aliases WHERE id=$1", alias.ID).Scan(&description); err != nil || description != "Committed while downgrade waits" {
		t.Fatal("downgrade lost the writer committed before its lock", err)
	}
}
