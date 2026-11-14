package training_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
)

// This explicit interleaving test publishes a real DB task after the first authority probe.
// It cannot grant the generic node path a training lease or skip the next current-authority check.
type dispatchAfterEmptyProbe struct {
	*training.Service
	afterEmpty func()
}

func (d *dispatchAfterEmptyProbe) ClaimTraining(ctx context.Context, node, credential, request string) (agentwire.Claim, bool, error) {
	out, handled, e := d.Service.ClaimTraining(ctx, node, credential, request)
	if e == nil && !handled && d.afterEmpty != nil {
		fn := d.afterEmpty
		d.afterEmpty = nil
		fn()
	}
	return out, handled, e
}

func TestTrainingDispatchBetweenAuthorityProbeAndGenericClaimRetainsPendingFence(t *testing.T) {
	f := setup(t)
	hardware(t, f)
	job := queued(t, f)
	ctx := context.Background()
	f.s.Nodes.Training = &dispatchAfterEmptyProbe{Service: f.s, afterEmpty: func() {
		if worked, e := f.s.DispatchNext(ctx); e != nil || !worked {
			t.Fatal("actual dispatch interleaving", e)
		}
	}}
	_, e := f.s.Nodes.Claim(ctx, f.input.NodeID, f.credential, uuid.NewString())
	expected(t, e, "TRAINING_AUTHORITY_REQUIRED")
	if count(t, f, "SELECT count(*) FROM node_tasks WHERE state='pending' AND attempt=0 AND lease_token_hash IS NULL") != 1 || count(t, f, "SELECT count(*) FROM training_attempts WHERE stop_token_hash IS NULL") != 1 {
		t.Fatal("generic fallback leased training without current authority")
	}
	claim, e := f.s.Nodes.Claim(ctx, f.input.NodeID, f.credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.Training == nil || claim.Lease.Training.Scope.JobID != job.ID || claim.Lease.Attempt != 1 {
		t.Fatal("next fresh authority-bound claim failed", e)
	}
	if count(t, f, "SELECT count(*) FROM training_attempts WHERE stop_token_hash IS NOT NULL") != 1 {
		t.Fatal("delivered stop fence not persisted exactly once")
	}
}
