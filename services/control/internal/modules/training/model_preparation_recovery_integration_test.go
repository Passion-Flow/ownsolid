//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
)

func preparationStoppedUndelivered(t *testing.T, g preparationScenario) modelpreparations.Preparation {
	t.Helper()
	p := preparationDispatched(t, g)
	ctx := context.Background()
	_, e := g.service.Cancel(ctx, g.f.portal, p.WorkspaceID, p.ID, modelpreparations.ActionRequest{ExpectedRevision: p.Revision, Reason: "Prepare explicit positively never-delivered restart test"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	preparationDue(t, g, p.ID)
	if worked, e := g.service.ReconcileNext(ctx); e != nil || !worked {
		t.Fatal("positive undelivered stop", worked, e)
	}
	p, e = g.service.Get(ctx, g.f.portal, p.WorkspaceID, p.ID)
	if e != nil || p.State != "cancelled" {
		t.Fatal(e)
	}
	return p
}

func TestActualCpuModelPreparationExplicitRecoveryCreatesOneNewFrozenAttemptAndReplay(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationStoppedUndelivered(t, g)
	ctx := context.Background()
	oldAttempt := *p.CurrentAttemptID
	input := modelpreparations.RecoveryRequest{ExpectedRevision: p.Revision, NodeID: g.node.NodeID, Reason: "Explicit same-input deterministic restart after positive stop"}
	key := uuid.NewString()
	bad := input
	bad.ExpectedRevision--
	_, e := g.service.Recover(ctx, g.f.portal, p.WorkspaceID, p.ID, bad, key, uuid.NewString())
	expected(t, e, "MODEL_PREPARATION_REVISION_CONFLICT")
	bad = input
	bad.NodeID = g.f.input.NodeID
	_, e = g.service.Recover(ctx, g.f.portal, p.WorkspaceID, p.ID, bad, key, uuid.NewString())
	expected(t, e, "AGENT_PROFILE_REJECTED")
	queued, e := g.service.Recover(ctx, g.f.portal, p.WorkspaceID, p.ID, input, key, uuid.NewString())
	if e != nil || queued.State != "queued" || queued.PendingRecoveryID == nil || queued.AttemptNumber != 1 || *queued.CurrentAttemptID != oldAttempt || queued.ConfigurationSHA256 != p.ConfigurationSHA256 || queued.InputSHA256 != p.InputSHA256 || queued.CancelRequestedAt != nil {
		t.Fatal("explicit frozen recovery", e)
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='released'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE preparation_id=$1", p.ID) != 1 {
		t.Fatal("recovery took premature GPU/attempt")
	}
	installed := g.f.s.Identity.Config.ModelPreparation
	g.f.s.Identity.Config.ModelPreparation.ArtifactRoot = ""
	again, e := g.service.Recover(ctx, g.f.portal, p.WorkspaceID, p.ID, input, key, uuid.NewString())
	if e != nil || again.ID != p.ID || again.PendingRecoveryID == nil || *again.PendingRecoveryID != *queued.PendingRecoveryID {
		t.Fatal("exact replay before changed new-job configuration", e)
	}
	g.f.s.Identity.Config.ModelPreparation = installed
	bad = input
	bad.Reason = "Changed body cannot replay"
	_, e = g.service.Recover(ctx, g.f.portal, p.WorkspaceID, p.ID, bad, key, uuid.NewString())
	expected(t, e, "IDEMPOTENCY_CONFLICT")
	if worked, e := g.service.DispatchNext(ctx); e != nil || !worked {
		t.Fatal("real authorized second attempt", worked, e)
	}
	p, e = g.service.Get(ctx, g.f.portal, p.WorkspaceID, p.ID)
	if e != nil || p.AttemptNumber != 2 || p.PendingRecoveryID != nil || p.CurrentAttemptID == nil || *p.CurrentAttemptID == oldAttempt {
		t.Fatal("new attempt identity", e)
	}
	l := preparationClaimed(t, g)
	if l.Preparation.AttemptNumber != 2 || l.Preparation.ConfigurationSHA256 != queued.ConfigurationSHA256 || l.Preparation.Scope.PreparationID != p.ID {
		t.Fatal("frozen restarted lease")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_recoveries WHERE preparation_id=$1 AND dispatched_attempt_id=$2 AND applied_at IS NOT NULL", p.ID, *p.CurrentAttemptID) != 1 || count(t, g.f, "SELECT revision FROM gpu_leases WHERE model_preparation_id=$1 AND state='held'", p.ID) != 2 {
		t.Fatal("explicit recovery not atomically applied")
	}
}

func TestActualCpuModelPreparationRecoveryCannotReplaceDeliveredUnstoppedOwner(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationDispatched(t, g)
	l := preparationClaimed(t, g)
	ctx := context.Background()
	if _, e := g.f.s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second',deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", l.TaskID); e != nil {
		t.Fatal(e)
	}
	preparationDue(t, g, p.ID)
	if _, e := g.service.ReconcileNext(ctx); e != nil {
		t.Fatal(e)
	}
	p, e := g.service.Get(ctx, g.f.portal, p.WorkspaceID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = g.service.Recover(ctx, g.f.portal, p.WorkspaceID, p.ID, modelpreparations.RecoveryRequest{ExpectedRevision: p.Revision, NodeID: g.node.NodeID, Reason: "An expired token is not a physical stop proof"}, uuid.NewString(), uuid.NewString())
	expected(t, e, "MODEL_PREPARATION_PHYSICAL_STOP_REQUIRED")
	if count(t, g.f, "SELECT count(*) FROM model_preparation_recoveries WHERE preparation_id=$1", p.ID) != 0 || count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='recovery_required'", p.ID) != 1 {
		t.Fatal("recovery replaced uncertain physical owner")
	}
}

func TestActualCpuModelPreparationRecoveryAuditRollbackAndRoleRecheckAfterSourceIO(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationStoppedUndelivered(t, g)
	ctx := context.Background()
	input := modelpreparations.RecoveryRequest{ExpectedRevision: p.Revision, NodeID: g.node.NodeID, Reason: "Source and audit recovery authority"}
	key := uuid.NewString()
	if _, e := g.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_recovery_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_recovery_requested' THEN RAISE EXCEPTION 'SYNTHETIC_PREPARATION_RECOVERY_AUDIT_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_recovery_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_recovery_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e := g.service.Recover(ctx, g.f.portal, p.WorkspaceID, p.ID, input, key, uuid.NewString()); e == nil {
		t.Fatal("recovery audit failure accepted")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_recoveries") != 0 || count(t, g.f, "SELECT count(*) FROM model_preparations WHERE id=$1 AND state='cancelled' AND pending_recovery_id IS NULL", p.ID) != 1 {
		t.Fatal("partial recovery intent survived rollback")
	}
	if _, e := g.f.s.DB.Exec(ctx, "DROP TRIGGER reject_preparation_recovery_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	actual := g.service.Datasets.(*datasets.Service)
	paused := &pausedPreparationSource{Service: actual, entered: make(chan struct{}), release: make(chan struct{})}
	g.service.Datasets = paused
	var once sync.Once
	release := func() { once.Do(func() { close(paused.release) }) }
	t.Cleanup(release)
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, e := g.service.Recover(bounded, g.f.portal, p.WorkspaceID, p.ID, input, key, uuid.NewString())
		done <- e
	}()
	select {
	case <-paused.entered:
	case <-bounded.Done():
		t.Fatal("recovery source did not pause")
	}
	if _, e := g.f.s.DB.Exec(bounded, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", p.WorkspaceID, g.f.portal.Me.User.ID); e != nil {
		release()
		t.Fatal("recovery source IO retained Workspace lock", e)
	}
	release()
	select {
	case e := <-done:
		expected(t, e, "ACCESS_DENIED")
	case <-bounded.Done():
		t.Fatal("recovery source did not join")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_recoveries") != 0 {
		t.Fatal("revoked recovery committed")
	}
}

func TestActualCpuModelPreparationRestoreInvalidationRetainsOwnerAndRollsBackAtomically(t *testing.T) {
	for _, failAudit := range []bool{false, true} {
		name := "success"
		if failAudit {
			name = "failure_rollback"
		}
		t.Run(name, func(t *testing.T) {
			g := newPreparationScenario(t)
			p := preparationDispatched(t, g)
			l := preparationClaimed(t, g)
			ctx := context.Background()
			if failAudit {
				if _, e := g.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_restore() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.error_code='MODEL_PREPARATION_RESTORE_INVALIDATED' THEN RAISE EXCEPTION 'SYNTHETIC_PREPARATION_RESTORE_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_restore BEFORE UPDATE ON model_preparations FOR EACH ROW EXECUTE FUNCTION reject_preparation_restore()`); e != nil {
					t.Fatal(e)
				}
			}
			e := recovery.InvalidateRestored(ctx, g.f.s.Identity, "Synthetic scoped preparation database restoration")
			if failAudit {
				if e == nil {
					t.Fatal("restore failure accepted")
				}
				if _, e = g.f.s.Nodes.Renew(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken); e != nil {
					t.Fatal("restore failure left partial authority invalidation", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if count(t, g.f, "SELECT count(*) FROM model_preparations WHERE id=$1 AND state='recovery_required' AND error_code='MODEL_PREPARATION_RESTORE_INVALIDATED'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='recovery_required'", p.ID) != 1 {
				t.Fatal("restore lost physical ownership")
			}
			if _, e = g.f.s.Nodes.Renew(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken); e == nil {
				t.Fatal("restored authority renewed previous work")
			}
		})
	}
}

func TestActualCpuModelPreparationSameKeyRecoveryOutsideLockScansCommitOneIntent(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationStoppedUndelivered(t, g)
	ctx := context.Background()
	policy := g.f.s.Identity.Config.Policy()
	policy.ModelLifecycle.ScanConcurrency = 2
	policy.ModelLifecycle.ScanSeconds = 20
	g.f.s.Identity.Config.Runtime = &policy
	service, e := modelpreparations.New(g.f.s.DB, g.f.s.Identity, g.f.s.Workspaces, g.f.s.Datasets)
	if e != nil {
		t.Fatal(e)
	}
	service.Store = g.store
	service.Training = g.f.s
	service.Nodes = g.f.s.Nodes
	service.Models = g.f.s.Models
	service.Inference = g.f.s.Inference
	paused := &pausedPreparationSource{Service: g.f.s.Datasets, entered: make(chan struct{}), release: make(chan struct{})}
	service.Datasets = paused
	var once sync.Once
	release := func() { once.Do(func() { close(paused.release) }) }
	t.Cleanup(release)
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	input := modelpreparations.RecoveryRequest{ExpectedRevision: p.Revision, NodeID: g.node.NodeID, Reason: "Concurrent identical explicit recovery"}
	key := uuid.NewString()
	go func() {
		_, e := service.Recover(bounded, g.f.portal, p.WorkspaceID, p.ID, input, key, uuid.NewString())
		done <- e
	}()
	select {
	case <-paused.entered:
	case <-bounded.Done():
		t.Fatal("recovery did not reach actual source read")
	}
	second, e := service.Recover(bounded, g.f.portal, p.WorkspaceID, p.ID, input, key, uuid.NewString())
	if e != nil || second.State != "queued" || second.PendingRecoveryID == nil {
		release()
		t.Fatal("second recovery scan could not commit outside first source IO", e)
	}
	release()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal("same-key recovery replay failed", e)
		}
	case <-bounded.Done():
		t.Fatal("first recovery did not join")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_recoveries WHERE preparation_id=$1", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_recovery_requested' AND target_id=$1", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='released'", p.ID) != 1 {
		t.Fatal("duplicate recovery or premature physical effect")
	}
	if _, e = g.f.s.DB.Exec(ctx, "UPDATE model_preparation_recoveries SET next_node_id=$2 WHERE id=$1", *second.PendingRecoveryID, g.f.input.NodeID); e == nil {
		t.Fatal("recovery target was mutable")
	}
}
