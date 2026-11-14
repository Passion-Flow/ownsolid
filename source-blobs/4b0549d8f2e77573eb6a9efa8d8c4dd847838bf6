//go:build repository_integration

package training_test

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"testing"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// Real SQL commits first; the explicit test fault models a lost acknowledgement
// during shutdown. It never claims a wire COMMIT-loss or physical GPU test.
type parallelTrainingShutdownStore struct {
	transaction.Store
	at, calls int
}

func (s *parallelTrainingShutdownStore) Within(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls++
	e := s.Store.Within(ctx, read, fn)
	if e == nil && s.calls == s.at {
		return errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown})
	}
	return e
}

func TestParallelMySQLTrainingCommitCancellation(t *testing.T) {
	t.Run("creation_requires_explicit_same_key_readback", func(t *testing.T) {
		g := trainingCreatorSetup(t)
		key := uuid.NewString()
		store := &parallelTrainingShutdownStore{Store: g.f.Store, at: 2}
		x := trainingCreatorWrapped(g, store)
		out, e := x.create(key)
		trainingCreatorWant(t, out, e, string(transaction.CommitUnknown))
		if store.calls != 2 {
			t.Fatal("uncertain creation retried")
		}
		g.counts(t, 1)
		out, e = g.create(key)
		if e != nil || out.State != "queued" {
			t.Fatal("explicit durable creation readback", e)
		}
		g.counts(t, 1)
	})
	t.Run("claim_hides_secret_and_retains_delivered_owner", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, false)
		store := &parallelTrainingShutdownStore{Store: g.f.Store, at: 3}
		x := g.wrapped(store)
		out, handled, e := x.s.ClaimTraining(x.ctx, x.input.NodeID, x.credential, uuid.NewString())
		if !handled || e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(out, agent.Claim{}) || store.calls != 3 {
			t.Fatal("uncertain claim exposed a secret or retried", handled, e, store.calls)
		}
		if g.count(t, "SELECT count(*) FROM training_attempts WHERE job_id=$1 AND stop_token_hash IS NOT NULL", "SELECT COUNT(*) FROM training_attempts WHERE job_id=? AND stop_token_hash IS NOT NULL", g.job.ID) != 1 || g.count(t, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='held'", "SELECT COUNT(*) FROM gpu_leases WHERE training_job_id=? AND state='held'", g.job.ID) != 1 {
			t.Fatal("uncertain delivered owner was released")
		}
		out, _, e = g.s.ClaimTraining(g.ctx, g.input.NodeID, g.credential, uuid.NewString())
		if e != nil || out.Lease != nil {
			t.Fatal("delivered lease reissued", e)
		}
	})
	t.Run("history_returns_no_partial_projection", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, true)
		store := &parallelTrainingShutdownStore{Store: g.f.Store, at: 1}
		x := g.wrapped(store)
		out, e := x.s.Attempts(x.ctx, x.a, x.f.Workspace, x.job.ID, url.Values{})
		if e == nil || e.Error() != string(transaction.CommitUnknown) || len(out.Items) != 0 || out.NextCursor != nil || store.calls != 1 {
			t.Fatal("uncertain history exposed rows or retried", e)
		}
		out, e = g.s.Attempts(g.ctx, g.a, g.f.Workspace, g.job.ID, url.Values{})
		if e != nil || len(out.Items) != 1 || out.Items[0].AttemptNumber != 1 {
			t.Fatal("explicit attempt history readback", e)
		}
	})
	t.Run("stopped_recovery_reconciles_one_intent", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, true)
		stop, e := g.s.RecordStop(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, g.stopReceipt(t), nil)
		if e != nil || !stop.GPUReleased || stop.State != "failed" {
			t.Fatal("exact never-created stop", e)
		}
		current, e := g.s.Get(g.ctx, g.a, g.f.Workspace, g.job.ID)
		if e != nil {
			t.Fatal(e)
		}
		input := training.RecoveryRequest{ExpectedRevision: current.Revision, Reason: "Explicit recovery after acknowledged physical absence", NodeID: g.input.NodeID, Mode: "restart"}
		key := uuid.NewString()
		store := &parallelTrainingShutdownStore{Store: g.f.Store, at: 2}
		x := g.wrapped(store)
		out, e := x.s.Recover(x.ctx, x.a, x.f.Workspace, x.job.ID, input, key, uuid.NewString())
		trainingCreatorWant(t, out, e, string(transaction.CommitUnknown))
		if store.calls != 2 {
			t.Fatal("uncertain recovery retried")
		}
		out, e = g.s.Recover(g.ctx, g.a, g.f.Workspace, g.job.ID, input, key, uuid.NewString())
		if e != nil || out.State != "queued" || out.PendingRecoveryID == nil {
			t.Fatal("explicit stopped recovery readback", e)
		}
		if g.count(t, "SELECT count(*) FROM training_recoveries WHERE job_id=$1", "SELECT COUNT(*) FROM training_recoveries WHERE job_id=?", g.job.ID) != 1 {
			t.Fatal("recovery duplicated its durable intent")
		}
	})
}
