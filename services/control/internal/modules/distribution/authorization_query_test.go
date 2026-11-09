package distribution_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type capacityQueryCounts struct {
	alias      atomic.Int64
	deployment atomic.Int64
}

func (c *capacityQueryCounts) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "SELECT count(*) FROM gateway_requests WHERE tenant_id=$1 AND workspace_id=$2 AND alias_id=$3 AND capacity_held") {
		c.alias.Add(1)
	}
	if strings.Contains(data.SQL, "SELECT count(*) FROM gateway_requests WHERE deployment_id=$1 AND capacity_held") {
		c.deployment.Add(1)
	}
	return ctx
}

func (*capacityQueryCounts) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestGatewayMachineAuthorizationOnlyCountsDeploymentCapacity(t *testing.T) {
	counts := &capacityQueryCounts{}
	account := fixture.NewConfigured(t, func(cfg *pgxpool.Config) { cfg.ConnConfig.Tracer = counts })
	c := setupAccount(t, account)
	a := c.access(t)
	ctx := t.Context()
	beforeModels := counts.alias.Load()
	models, err := c.s.AuthorizeModels(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, *a.key.Secret)
	if err != nil || len(models.Models) != 1 || models.Models[0].ID != a.alias.ID {
		t.Fatal("model authorization did not return the ready alias", err)
	}
	afterModels := counts.alias.Load()
	beforeDeployment := counts.deployment.Load()
	input := a.input()
	if _, err := c.s.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, input, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if got := afterModels - beforeModels; got != 0 {
		t.Errorf("model authorization ran %d administrative alias counts, want zero", got)
	}
	if got := counts.alias.Load() - afterModels; got != 0 {
		t.Errorf("admission ran %d administrative alias counts, want zero", got)
	}
	if got := counts.deployment.Load() - beforeDeployment; got != 1 {
		t.Errorf("admission ran %d deployment capacity checks, want one", got)
	}

	alias, err := c.s.Alias(ctx, c.a, a.alias.WorkspaceID, a.alias.ID)
	if err != nil || !alias.Available || alias.HeldRequests != 1 {
		t.Fatal("alias detail lost the active hold", alias.HeldRequests, err)
	}
	if _, err := c.s.Report(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, success(input.RequestID), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	alias, err = c.s.Alias(ctx, c.a, a.alias.WorkspaceID, a.alias.ID)
	if err != nil || !alias.Available || alias.HeldRequests != 0 {
		t.Fatal("alias detail retained a released hold", alias.HeldRequests, err)
	}
}
