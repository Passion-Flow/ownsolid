package distribution_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"testing"
	"time"
)

// Current database authority is real; ready physical observations use the
// distribution fixture's explicitly synthetic machine/deployment reports.
func TestAnswerTransactionRejectsChangedAliasAndGatewayAuthority(t *testing.T) {
	for _, scenarioName := range []string{"disabled_alias", "revoked_gateway", "stale_published_revision"} {
		t.Run(scenarioName, func(t *testing.T) {
			c := setup(t)
			a := c.access(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			recheck := func(snapshot distribution.Alias) error {
				tx, e := c.s.DB.Begin(ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback(ctx)
				return c.s.RecheckAnswerAliasInTransaction(ctx, tx, a.owner, a.alias.WorkspaceID, snapshot)
			}
			if e := recheck(a.alias); e != nil {
				t.Fatal("current actual alias refused", e)
			}
			switch scenarioName {
			case "disabled_alias":
				if _, e := c.s.DisableAlias(ctx, c.a, a.alias.WorkspaceID, a.alias.ID, a.alias.Revision, "Synthetic explicit alias revocation", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			case "revoked_gateway":
				if _, e := c.s.RevokeGateway(ctx, c.a, a.alias.WorkspaceID, a.gateway.Gateway.ID, "Synthetic explicit gateway revocation", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			case "stale_published_revision":
				a.alias.Revision++ // An explicitly different trusted snapshot must not authorize output.
			}
			check(t, recheck(a.alias), "MODEL_ALIAS_UNAVAILABLE")
		})
	}
}
