package nodes_test

import (
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentcost"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func TestReadOnlyClaimChecksAuthorityOnce(t *testing.T) {
	const smallFleet = 50
	const largeFleet = smallFleet * agentcost.FleetRatio * 2
	idleSmall, dispatchSmall := measureReadOnlyClaims(t, smallFleet)
	idleLarge, dispatchLarge := measureReadOnlyClaims(t, largeFleet)
	for _, exchange := range []struct {
		name         string
		small, large agentcost.Cost
	}{
		{"idle claim", idleSmall, idleLarge},
		{"facts dispatch", dispatchSmall, dispatchLarge},
	} {
		t.Logf("%s: statements %d/%d, blocks %d/%d", exchange.name, len(exchange.small.Statements),
			len(exchange.large.Statements), exchange.small.Blocks(), exchange.large.Blocks())
		for _, violation := range agentcost.Violations(wire.CallClaim.Cost(), exchange.small, exchange.large) {
			t.Errorf("%s: %s", exchange.name, violation)
		}
		for i, cost := range []agentcost.Cost{exchange.small, exchange.large} {
			checks := 0
			for _, statement := range cost.Statements {
				if strings.HasPrefix(statement.Query, "SELECT credential_hash FROM managed_nodes") {
					checks++
				}
			}
			if checks != 1 {
				t.Errorf("%s on %d seeded nodes: got %d locked credential checks, want 1",
					exchange.name, []int{smallFleet, largeFleet}[i], checks)
			}
		}
	}
}

func measureReadOnlyClaims(t *testing.T, fleet int) (idle, dispatch agentcost.Cost) {
	t.Helper()
	ctx := t.Context()
	var recorder agentcost.Recorder
	account := fixture.NewConfigured(t, recorder.Configure)
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	s := &nodes.Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}}
	node := enroll(t, s, admin, "Claim cost node")
	_, e := account.DB.Exec(ctx, `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,protocol_version,facts,execution_profile,capabilities,credential_hash,last_heartbeat_at)
		SELECT gen_random_uuid(), n.tenant_id, n.workspace_id, 'claim-cost-'||i, n.status, n.protocol_version, n.facts,
		       n.execution_profile, n.capabilities, sha256(i::text::bytea), clock_timestamp()
		FROM managed_nodes n, generate_series(1,$2::integer) i WHERE n.id=$1`, node.NodeID, fleet)
	if e != nil {
		t.Fatal(e)
	}
	measure := func(expectedTask string) agentcost.Cost {
		t.Helper()
		cost, e := recorder.Record(func() {
			claim, e := s.ClaimFrom(ctx, node.NodeID, node.Credential, uuid.NewString(), wire.ClaimRequest{})
			if e != nil || claim.Resync != nil {
				t.Fatalf("claim failed: %v", e)
			}
			if expectedTask == "" && claim.Lease != nil ||
				expectedTask != "" && (claim.Lease == nil || claim.Lease.TaskID != expectedTask) {
				t.Fatal("claim did not return the expected task")
			}
		})
		if e != nil {
			t.Fatal(e)
		}
		return cost
	}
	idle = measure("")
	task, e := s.CreateTask(ctx, admin, node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return idle, measure(task.ID)
}
