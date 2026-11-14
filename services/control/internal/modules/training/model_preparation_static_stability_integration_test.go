//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"context"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/google/uuid"
)

func TestAPreparationKeepsRunningThroughAnOutage(t *testing.T) {
	g := newPreparationScenario(t)
	ctx := context.Background()
	p := preparationDispatched(t, g)
	var l agentwire.Lease = preparationClaimed(t, g)
	silence(t, g.f, l.TaskID, "model_preparations", p.ID)
	if worked, e := g.service.ReconcileNext(ctx); e != nil || worked {
		t.Fatal("the outage changed the preparation", worked, e)
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparations WHERE id=$1 AND state='preparing' AND wait_code='MODEL_PREPARATION_NODE_UNREACHABLE' AND error_code IS NULL", p.ID) != 1 {
		t.Fatal("lost contact is not shown, or the preparation moved")
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='held'", p.ID) != 1 {
		t.Fatal("the outage moved the GPU reservation")
	}
	again, e := g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	if e != nil || again.Lease != nil {
		t.Fatal("an attempt that may be running was delivered again", e)
	}
	if renewed, e := g.f.s.Nodes.Renew(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken); e != nil || renewed.ValidForMilliseconds < 1 {
		t.Fatal("renewal after the outage", e)
	}
	preparationDue(t, g, p.ID)
	if _, e = g.service.ReconcileNext(ctx); e != nil {
		t.Fatal(e)
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparations WHERE id=$1 AND wait_code IS NULL", p.ID) != 1 {
		t.Fatal("lost contact is still shown after the agent returned")
	}
	current, e := g.service.Get(ctx, g.f.portal, p.WorkspaceID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = g.service.Cancel(ctx, g.f.portal, p.WorkspaceID, p.ID, modelpreparations.ActionRequest{ExpectedRevision: current.Revision, Reason: "Cancelled after the outage"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = g.f.s.Nodes.Renew(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken)
	expected(t, e, "ACCESS_DENIED")
}
