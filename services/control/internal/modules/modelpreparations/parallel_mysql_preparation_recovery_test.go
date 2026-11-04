//go:build repository_integration

package modelpreparations

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// SQL/command envelopes are synthetic; no GPU, worker or model file is executed.
// Each case uses the real selected repository and its unchanged schema guards.
func parallelPreparationRuntime(t *testing.T, delivered bool, alter ...func(*wire.Command)) (reviewStory, string, string) {
	t.Helper()
	g := reviewSetup(t)
	p, e := g.s.Get(g.ctx, g.a, g.f.Workspace, g.id)
	if e != nil {
		t.Fatal(e)
	}
	credential, token := security.Token(), security.Token()
	g.id, g.attempt, g.task = uuid.NewString(), uuid.NewString(), uuid.NewString()
	p.ID, p.GPUUUID = g.id, "GPU-"+uuid.NewString()
	g.c = commandForPreparation(p, workspaces.Workspace{ID: g.f.Workspace, TenantID: g.f.Tenant}, g.attempt, 1)
	payload := g.c
	for _, change := range alter {
		change(&payload)
	}
	sha, e := g.c.Digest()
	if e != nil {
		t.Fatal(e)
	}
	j := func(v any) reviewJSON { return reviewJSON(reviewRaw(t, v)) }
	g.exec(t, `UPDATE managed_nodes SET status='active',credential_hash=$1,last_heartbeat_at=clock_timestamp() WHERE id=$2`, `UPDATE managed_nodes SET status='active',credential_hash=?,last_heartbeat_at=UTC_TIMESTAMP(6) WHERE id=?`, security.Digest("node-credential:v1:"+credential), g.node)
	g.seed(t, "model_preparations", map[string]any{"id": g.id, "node_id": g.node, "current_node_id": g.node, "model_id": p.ModelID, "training_job_id": p.TrainingJobID, "training_attempt_id": p.TrainingAttemptID, "training_artifact_id": p.TrainingArtifactID, "heldout_id": p.HeldoutID, "gpu_uuid": p.GPUUUID, "input_snapshot": j(p.inputs), "input_sha256": p.InputSHA256, "configuration": j(p.Configuration), "configuration_sha256": p.ConfigurationSHA256, "execution_recipe": j(p.Recipe), "created_by": g.f.User, "requested_by": g.f.User, "creation_audience": "admin", "requested_audience": "admin", "requested_epoch": 1, "reason": "Synthetic control recovery intent", "creation_key": uuid.NewString(), "creation_fingerprint": bytes.Repeat([]byte{6}, 32), "state": "preparing"})
	task := map[string]any{"id": g.task, "node_id": g.node, "operation": agent.ModelPreparationRunOperation, "payload": j(payload), "state": "pending", "idempotency_key": uuid.NewString(), "requested_by": g.f.User, "deadline_at": time.Now().UTC().Add(10 * time.Minute)}
	attempt := map[string]any{"id": g.attempt, "preparation_id": g.id, "node_id": g.node, "task_id": g.task, "attempt_number": 1, "state": "pending", "requested_by": g.f.User, "requested_audience": "admin", "requested_epoch": 1, "command_sha256": sha}
	if delivered {
		stop := stopTokenHash(token)
		task["state"], task["attempt"], task["lease_token_hash"], task["lease_expires_at"] = "running", 1, security.Digest("node-lease:v1:"+token), time.Now().UTC().Add(10*time.Minute)
		attempt["state"], attempt["stop_token_hash"] = "preparing", stop[:]
	}
	g.seed(t, "node_tasks", task)
	g.seed(t, "model_preparation_attempts", attempt)
	g.exec(t, `UPDATE model_preparations SET attempt_number=1,current_attempt_id=$1 WHERE id=$2`, `UPDATE model_preparations SET attempt_number=1,current_attempt_id=? WHERE id=?`, g.attempt, g.id)
	g.seed(t, "gpu_leases", map[string]any{"id": uuid.NewString(), "node_id": g.node, "gpu_uuid": p.GPUUUID, "model_preparation_id": g.id, "revision": 1, "state": "held"})
	return g, credential, token
}

func parallelPreparationState(t *testing.T, g reviewStory, state, gpu string, stopped bool, audits int) {
	t.Helper()
	var actual, physical string
	var isStopped bool
	var n int
	e := g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT p.state,g.state,a.physical_stopped_at IS NOT NULL FROM model_preparations p JOIN model_preparation_attempts a ON a.id=p.current_attempt_id JOIN gpu_leases g ON g.model_preparation_id=p.id WHERE p.id=$1`, `SELECT p.state,g.state,a.physical_stopped_at IS NOT NULL FROM model_preparations p JOIN model_preparation_attempts a ON a.id=p.current_attempt_id JOIN gpu_leases g ON g.model_preparation_id=p.id WHERE p.id=?`, g.id)).Scan(&actual, &physical, &isStopped)
	if e != nil || actual != state || physical != gpu || isStopped != stopped {
		t.Fatalf("durable state=%s/%s/%t expected=%s/%s/%t: %v", actual, physical, isStopped, state, gpu, stopped, e)
	}
	e = g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*) FROM audit_events WHERE target_id=$1 AND action IN ('model.preparation_undelivered_stopped','model.preparation_physical_stop_required')`, `SELECT count(*) FROM audit_events WHERE target_id=? AND action IN ('model.preparation_undelivered_stopped','model.preparation_physical_stop_required')`, g.id)).Scan(&n)
	if e != nil || n != audits {
		t.Fatal("atomic reconciliation audit", n, audits, e)
	}
}

func TestParallelMySQLPreparationRecovery(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		name := "undelivered_profile_change"
		if delivered {
			name = "delivered_profile_change"
		}
		t.Run(name, func(t *testing.T) {
			g, _, _ := parallelPreparationRuntime(t, delivered)
			g.exec(t, `UPDATE managed_nodes SET execution_profile=$1,protocol_version=$2,capabilities=$3 WHERE id=$4`, `UPDATE managed_nodes SET execution_profile=?,protocol_version=?,capabilities=CAST(? AS JSON) WHERE id=?`, agent.ReadOnlyProfile, agent.ProfileVersion(agent.ReadOnlyProfile), string(reviewRaw(t, agent.Capabilities(agent.ReadOnlyProfile))), g.node)
			worked, e := g.s.ReconcileNext(g.ctx)
			if e != nil || !worked {
				t.Fatal("retained control reconciliation", worked, e)
			}
			if delivered {
				parallelPreparationState(t, g, "recovery_required", "recovery_required", false, 1)
			} else {
				parallelPreparationState(t, g, "failed", "released", true, 1)
			}
		})
	}
	t.Run("nonpending_zero_claim_is_not_never_delivered", func(t *testing.T) {
		g, _, _ := parallelPreparationRuntime(t, false)
		g.exec(t, `UPDATE node_tasks SET state='failed',completed_at=clock_timestamp() WHERE id=$1`, `UPDATE node_tasks SET state='failed',completed_at=UTC_TIMESTAMP(6) WHERE id=?`, g.task)
		worked, e := g.s.ReconcileNext(g.ctx)
		if e != nil || !worked {
			t.Fatal(worked, e)
		}
		parallelPreparationState(t, g, "recovery_required", "recovery_required", false, 1)
	})
	// ADR 0011. The delivery columns exist on the PostgreSQL lineage only.
	deliveryCase := func(t *testing.T) reviewStory {
		t.Helper()
		g, _, _ := parallelPreparationRuntime(t, true)
		if g.f.Store.Dialect() != transaction.PostgreSQL {
			t.Skip("task delivery columns are PostgreSQL only until CMP-201 and CMP-202")
		}
		g.exec(t, `UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, ``, g.task)
		return g
	}
	t.Run("expired_unacknowledged_lease_returns_for_delivery", func(t *testing.T) {
		g := deliveryCase(t)
		if _, e := g.s.ReconcileNext(g.ctx); e != nil {
			t.Fatal(e)
		}
		parallelPreparationState(t, g, "preparing", "held", false, 0)
		var state string
		var attempt int
		if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT state,attempt FROM node_tasks WHERE id=$1`, ``, g.task)).Scan(&state, &attempt); e != nil || state != "pending" || attempt != 0 {
			t.Fatal("released task", state, attempt, e)
		}
	})
	t.Run("exhausted_deliveries_are_proven_never_delivered", func(t *testing.T) {
		g := deliveryCase(t)
		g.exec(t, `UPDATE node_tasks SET deliveries=$1 WHERE id=$2`, ``, g.s.Identity.Config.Policy().Nodes.MaximumTaskDeliveries, g.task)
		worked, e := g.s.ReconcileNext(g.ctx)
		if e != nil || !worked {
			t.Fatal(worked, e)
		}
		parallelPreparationState(t, g, "failed", "released", true, 1)
	})
	t.Run("expired_acknowledged_lease_keeps_the_physical_owner", func(t *testing.T) {
		g := deliveryCase(t)
		g.exec(t, `UPDATE node_tasks SET acknowledged_attempt=1 WHERE id=$1`, ``, g.task)
		worked, e := g.s.ReconcileNext(g.ctx)
		if e != nil || !worked {
			t.Fatal(worked, e)
		}
		parallelPreparationState(t, g, "recovery_required", "recovery_required", false, 1)
	})
	t.Run("frozen_command_tamper_aborts_without_release", func(t *testing.T) {
		g, _, _ := parallelPreparationRuntime(t, false, func(c *wire.Command) { c.GPUUUID = "GPU-" + uuid.NewString() })
		worked, e := g.s.ReconcileNext(g.ctx)
		runtimeError(t, e, "MODEL_PREPARATION_ATTEMPT_STALE")
		if worked {
			t.Fatal("failed transaction reported work")
		}
		parallelPreparationState(t, g, "preparing", "held", false, 0)
	})
	t.Run("unknown_reconcile_commit_reports_no_work_and_no_replay", func(t *testing.T) {
		g, _, _ := parallelPreparationRuntime(t, false)
		g.exec(t, `UPDATE managed_nodes SET status='removed',credential_hash=NULL WHERE id=$1`, `UPDATE managed_nodes SET status='removed',credential_hash=NULL WHERE id=?`, g.node)
		identity := *g.s.Identity
		identity.Storage = &reviewUnknownStore{Store: g.f.Store, failAt: 1}
		identity.DB = nil
		workspace := *g.s.Workspaces
		workspace.Identity, workspace.DB = &identity, nil
		s := *g.s
		s.Identity, s.Workspaces, s.DB = &identity, &workspace, nil
		worked, e := s.ReconcileNext(g.ctx)
		runtimeError(t, e, string(transaction.CommitUnknown))
		if worked {
			t.Fatal("unknown commit reported success")
		}
		parallelPreparationState(t, g, "failed", "released", true, 1)
		worked, e = g.s.ReconcileNext(g.ctx)
		if e != nil || worked {
			t.Fatal("completed reconciliation replayed", worked, e)
		}
	})
	t.Run("cancelled_context_has_no_effect", func(t *testing.T) {
		g, _, _ := parallelPreparationRuntime(t, false)
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		worked, e := g.s.ReconcileNext(ctx)
		if e == nil || worked {
			t.Fatal("cancelled reconciliation accepted")
		}
		parallelPreparationState(t, g, "preparing", "held", false, 0)
	})
}

func TestParallelMySQLPreparationEvents(t *testing.T) {
	t.Run("failed_batch_rolls_back_every_event", func(t *testing.T) {
		g, credential, token := parallelPreparationRuntime(t, true)
		events := runtimeEvents(g.id, g.attempt, 3)
		accepted, e := g.s.RecordEvents(g.ctx, g.node, credential, g.task, token, [][]byte{events[0], events[2]})
		runtimeError(t, e, "MODEL_PREPARATION_EVENT_ORDER_CONFLICT")
		if len(accepted) != 0 {
			t.Fatal("failed batch disclosed acceptance")
		}
		var n int
		if e = g.f.Store.QueryRow(g.ctx, g.f.Statement(`SELECT count(*) FROM model_preparation_events WHERE attempt_id=$1`, `SELECT count(*) FROM model_preparation_events WHERE attempt_id=?`, g.attempt)).Scan(&n); e != nil || n != 0 {
			t.Fatal("partial event commit", n, e)
		}
		accepted, e = g.s.RecordEvents(g.ctx, g.node, credential, g.task, token, events[:1])
		if e != nil || len(accepted) != 1 || accepted[0].Duplicate {
			t.Fatal("fresh event", e)
		}
		accepted, e = g.s.RecordEvents(g.ctx, g.node, credential, g.task, token, events[:1])
		if e != nil || len(accepted) != 1 || !accepted[0].Duplicate {
			t.Fatal("exact replay", e)
		}
	})
	t.Run("current_actor_revocation_fences_events", func(t *testing.T) {
		g, credential, token := parallelPreparationRuntime(t, true)
		g.exec(t, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?`, g.f.User)
		accepted, e := g.s.RecordEvents(g.ctx, g.node, credential, g.task, token, runtimeEvents(g.id, g.attempt, 3)[:1])
		runtimeError(t, e, "ACCESS_DENIED")
		if len(accepted) != 0 {
			t.Fatal("revoked actor returned events")
		}
	})
}
