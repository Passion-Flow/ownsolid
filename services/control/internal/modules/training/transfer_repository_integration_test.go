//go:build repository_integration

package training_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type trainingRuntimeStory struct {
	trainingOperatorStory
	credential string
	lease      agent.Lease
}

// The second target is genuinely enrolled to retain its returned machine secret.
// Only hardware/model/publication metadata are synthetic; partitions and all
// business transactions, leases, audit effects and file ranges are real.
func trainingRuntimeQueue(t *testing.T, cfg *wire.Configuration) trainingRuntimeStory {
	t.Helper()
	g := trainingCreatorSetup(t)
	join, e := g.s.Nodes.MintForProfile(g.ctx, g.a, g.f.Workspace, "Synthetic paired runtime target", agent.DockerAIProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := agent.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-runtime", LogicalCPUs: 4, MemoryTotalBytes: 17179869184, DiskPath: "/", DiskTotalBytes: 107374182400, DiskAvailableBytes: 53687091200, ObservedAt: time.Now().UTC(), AgentVersion: agent.AgentVersion}
	enrolled, e := g.s.Nodes.Enroll(g.ctx, agent.Enrollment{JoinToken: join.Token, Handshake: agent.Handshake{ProtocolVersion: agent.AIVersion, ExecutionProfile: agent.DockerAIProfile, Capabilities: agent.Capabilities(agent.DockerAIProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.input.NodeID = enrolled.NodeID
	if cfg != nil {
		g.input.Configuration = *cfg
		g.input.Configuration.Device = wire.ProductionProfile
	}
	j, e := g.create(uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.s.Inference = &deployments.Service{Nodes: g.s.Nodes, Models: g.s.Models}
	x := trainingRuntimeStory{trainingOperatorStory: trainingOperatorStory{g, j}, credential: enrolled.Credential}
	x.hardware(t)
	x.openStore(t)
	return x
}
func trainingRuntimeSetup(t *testing.T, cfg *wire.Configuration, claim bool) trainingRuntimeStory {
	t.Helper()
	x := trainingRuntimeQueue(t, cfg)
	if worked, e := x.s.DispatchNext(x.ctx); e != nil || !worked {
		t.Fatal("actual paired dispatch", worked, e)
	}
	var e error
	x.job, e = x.s.Get(x.ctx, x.a, x.f.Workspace, x.job.ID)
	if e != nil || x.job.CurrentAttemptID == nil {
		t.Fatal("persisted actual dispatch", e)
	}
	if claim {
		x.claim(t)
	}
	return x
}
func (g *trainingRuntimeStory) claim(t *testing.T) {
	t.Helper()
	v, handled, e := g.s.ClaimTraining(g.ctx, g.input.NodeID, g.credential, uuid.NewString())
	if e != nil || !handled || v.Lease == nil || v.Lease.Training == nil || v.Lease.Attempt != 1 {
		t.Fatal("actual paired claim", e)
	}
	g.lease = *v.Lease
}
func (g trainingRuntimeStory) stopReceipt(t *testing.T) wire.StopReceipt {
	t.Helper()
	c := g.lease.Training
	sha, e := c.Digest()
	if e != nil {
		t.Fatal(e)
	}
	return wire.StopReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), JournalID: uuid.NewString(), NodeID: g.input.NodeID, TaskID: g.lease.TaskID, Scope: c.Scope, AttemptID: c.AttemptID, AttemptNumber: c.AttemptNumber, CommandSHA256: sha, ImageID: *c.Recipe.Identity.ImageID, GPUUUID: c.GPUUUID, ContainerName: wire.ContainerName(c.AttemptID), Proof: "never_created", OwnedContainerAbsent: true, CompletedAt: time.Now().UTC()}
}
func (g trainingRuntimeStory) count(t *testing.T, pg, my string, args ...any) int {
	t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.statement(pg, my, args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func (g trainingRuntimeStory) wrapped(store transaction.Store) trainingRuntimeStory {
	g.trainingOperatorStory = trainingOperatorWrapped(g.trainingOperatorStory, store)
	return g
}
func (g trainingRuntimeStory) auditFailure(t *testing.T, action string) func() {
	t.Helper()
	g.exec(t, "CREATE FUNCTION explicit_training_runtime_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='"+action+"' THEN RAISE EXCEPTION 'explicit training runtime audit failure'; END IF; RETURN NEW; END;$$", "CREATE TRIGGER explicit_training_runtime_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='"+action+"' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='explicit training runtime audit failure'; END IF; END")
	if g.f.Store.Dialect() == transaction.PostgreSQL {
		g.exec(t, "CREATE TRIGGER explicit_training_runtime_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION explicit_training_runtime_audit_failure()", "")
	}
	return func() {
		g.exec(t, "DROP TRIGGER explicit_training_runtime_audit_failure ON audit_events", "DROP TRIGGER explicit_training_runtime_audit_failure")
		if g.f.Store.Dialect() == transaction.PostgreSQL {
			g.exec(t, "DROP FUNCTION explicit_training_runtime_audit_failure()", "")
		}
	}
}
func TestActualPairedTrainingMachineTransfer(t *testing.T) {
	t.Run("claim_renew_real_partition_range_and_atomic_event_replay", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, true)
		renewed, handled, e := g.s.RenewTraining(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken)
		if e != nil || !handled || renewed.ValidForMilliseconds <= 0 {
			t.Fatal("actual paired heartbeat", e)
		}
		raw, e := os.ReadFile(g.partition)
		if e != nil {
			t.Fatal(e)
		}
		request := wire.InputFileRequest{LeaseToken: g.lease.LeaseToken, Kind: "dataset", Asset: "train.jsonl", Length: int64(len(raw))}
		d, e := g.s.InputFile(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, request)
		if e != nil {
			t.Fatal(e)
		}
		got, e := io.ReadAll(io.NewSectionReader(d.File, d.Offset, d.Size))
		d.File.Close()
		if e != nil || !bytes.Equal(raw, got) || d.SHA256 != acceptedAdapterDigest(raw) {
			t.Fatal("actual range bytes/hash", e)
		}
		accepted, e := g.s.RecordEvents(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, prelude(*g.lease.Training))
		if e != nil || len(accepted) != 2 || accepted[1].JobState != "running" {
			t.Fatal("actual ordered prelude", e)
		}
		event := eventFixture(*g.lease.Training, 3, 1, "metric", map[string]any{"values": map[string]any{"loss": 1.25, "epoch": 0.1}})
		first, e := g.s.RecordEvent(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, event)
		if e != nil || first.Duplicate {
			t.Fatal(e)
		}
		duplicate, e := g.s.RecordEvent(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, event)
		if e != nil || !duplicate.Duplicate {
			t.Fatal("exact event replay", e)
		}
		changed := eventFixture(*g.lease.Training, 3, 1, "metric", map[string]any{"values": map[string]any{"loss": 2.25, "epoch": 0.1}})
		denied, e := g.s.RecordEvent(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, changed)
		if e == nil || e.Error() != "TRAINING_EVENT_CONFLICT" || !reflect.DeepEqual(denied, training.EventAcceptance{}) {
			t.Fatal("conflicting event projected", e)
		}
		if g.count(t, "SELECT count(*) FROM training_events WHERE attempt_id=$1", "SELECT COUNT(*) FROM training_events WHERE attempt_id=?", g.lease.Training.AttemptID) != 3 {
			t.Fatal("duplicate event persisted")
		}
	})

	t.Run("partition_final_authority_and_unknown_read_close_handle", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, true)
		raw, e := os.ReadFile(g.partition)
		if e != nil {
			t.Fatal(e)
		}
		request := wire.InputFileRequest{LeaseToken: g.lease.LeaseToken, Kind: "dataset", Asset: "train.jsonl", Length: int64(len(raw))}
		x := g.wrapped(&trainingCreatorStore{Store: g.f.Store, at: 4, unknown: true})
		d, e := x.s.InputFile(x.ctx, x.input.NodeID, x.credential, x.lease.TaskID, request)
		if e == nil || e.Error() != string(transaction.CommitUnknown) || d.File != nil || d.SHA256 != "" || d.Size != 0 {
			t.Fatal("unknown final range projected", e)
		}
		x = g.wrapped(&trainingCreatorStore{Store: g.f.Store, at: 4, before: func() {
			g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
		}})
		d, e = x.s.InputFile(x.ctx, x.input.NodeID, x.credential, x.lease.TaskID, request)
		if e == nil || e.Error() != "ACCESS_DENIED" || d.File != nil || d.SHA256 != "" {
			t.Fatal("final current authority lost", e)
		}
	})
	t.Run("claim_audit_rollback_and_uncertain_secret_zero", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, false)
		drop := g.auditFailure(t, "node.task_claimed")
		v, _, e := g.s.ClaimTraining(g.ctx, g.input.NodeID, g.credential, uuid.NewString())
		if e == nil || !reflect.DeepEqual(v, agent.Claim{}) {
			t.Fatal("claim audit leak", e)
		}
		if g.count(t, "SELECT count(*) FROM node_tasks WHERE node_id=$1 AND state='pending' AND attempt=0", "SELECT COUNT(*) FROM node_tasks WHERE node_id=? AND state='pending' AND attempt=0", g.input.NodeID) != 1 {
			t.Fatal("claim rollback lost pending work")
		}
		drop()
		// Candidate read, immutable input preflight, then real secret mutation COMMIT.
		x := g.wrapped(&trainingCreatorStore{Store: g.f.Store, at: 3, unknown: true})
		v, _, e = x.s.ClaimTraining(x.ctx, x.input.NodeID, x.credential, uuid.NewString())
		if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(v, agent.Claim{}) {
			t.Fatal("unknown claim leaked credential", e)
		}
		if g.count(t, "SELECT count(*) FROM training_attempts WHERE job_id=$1 AND stop_token_hash IS NOT NULL", "SELECT COUNT(*) FROM training_attempts WHERE job_id=? AND stop_token_hash IS NOT NULL", g.job.ID) != 1 {
			t.Fatal("durable delivered fence missing")
		}
		v, _, e = g.s.ClaimTraining(g.ctx, g.input.NodeID, g.credential, uuid.NewString())
		if e != nil || v.Lease != nil {
			t.Fatal("delivered lease was reissued", e)
		}
	})
	t.Run("current_epoch_and_exact_gpu_fence_refuse_range_and_renewal", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, true)
		g.exec(t, "UPDATE gpu_leases SET state='recovery_required' WHERE training_job_id=$1", "UPDATE gpu_leases SET state='recovery_required' WHERE training_job_id=?", g.job.ID)
		v, _, e := g.s.RenewTraining(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken)
		if e == nil || e.Error() != "TRAINING_ATTEMPT_STALE" || !reflect.DeepEqual(v, agent.RenewedLease{}) {
			t.Fatal("GPU fence ignored", e)
		}
		g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
		d, e := g.s.InputFile(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, wire.InputFileRequest{LeaseToken: g.lease.LeaseToken, Kind: "dataset", Asset: "train.jsonl", Length: 1})
		if e == nil || e.Error() != "ACCESS_DENIED" || d.File != nil || d.SHA256 != "" {
			t.Fatal("revoked range leaked", e)
		}
		stop := g.stopReceipt(t)
		accepted, e := g.s.RecordStop(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, stop, nil)
		if e != nil || accepted.State != "cancelled" || !accepted.GPUReleased {
			t.Fatal("revocation blocked exact physical release", e)
		}
	})
	t.Run("positive_stop_restart_and_stale_old_attempt", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, true)
		_, e := g.s.Recover(g.ctx, g.a, g.f.Workspace, g.job.ID, training.RecoveryRequest{ExpectedRevision: g.job.Revision, Reason: "Refuse recovery of a live owner", NodeID: g.input.NodeID, Mode: "restart"}, uuid.NewString(), uuid.NewString())
		if e == nil {
			t.Fatal("live owner recovered")
		}
		receipt := g.stopReceipt(t)
		accepted, e := g.s.RecordStop(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil)
		if e != nil || !accepted.GPUReleased || accepted.State != "failed" {
			t.Fatal(e)
		}
		again, e := g.s.RecordStop(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil)
		if e != nil || !again.Duplicate {
			t.Fatal("exact stop replay", e)
		}
		current, e := g.s.Get(g.ctx, g.a, g.f.Workspace, g.job.ID)
		if e != nil {
			t.Fatal(e)
		}
		input := training.RecoveryRequest{ExpectedRevision: current.Revision, Reason: "Explicit stopped-owner restart", NodeID: g.input.NodeID, Mode: "restart"}
		key := uuid.NewString()
		x := g.wrapped(&trainingCreatorStore{Store: g.f.Store, at: 2, unknown: true})
		queued, e := x.s.Recover(x.ctx, x.a, x.f.Workspace, x.job.ID, input, key, uuid.NewString())
		trainingCreatorWant(t, queued, e, string(transaction.CommitUnknown))
		queued, e = g.s.Recover(g.ctx, g.a, g.f.Workspace, g.job.ID, input, key, uuid.NewString())
		if e != nil || queued.PendingRecoveryID == nil || queued.State != "queued" {
			t.Fatal("explicit durable recovery readback", e)
		}
		replay, e := g.s.Recover(g.ctx, g.a, g.f.Workspace, g.job.ID, input, key, uuid.NewString())
		if e != nil || replay.ID != queued.ID || replay.Revision != queued.Revision {
			t.Fatal("recovery replay", e)
		}
		if worked, e := g.s.DispatchNext(g.ctx); e != nil || !worked {
			t.Fatal(e)
		}
		old := g.lease
		g.claim(t)
		if g.lease.Training.AttemptNumber != 2 || g.lease.Training.Resume != nil {
			t.Fatal("restart did not create exact fresh attempt")
		}
		denied, e := g.s.RecordStop(g.ctx, g.input.NodeID, g.credential, old.TaskID, old.LeaseToken, receipt, nil)
		if e == nil || !reflect.DeepEqual(denied, wire.StopAcceptance{}) {
			t.Fatal("old stop affected new owner", e)
		}
	})
	t.Run("uncertain_event_and_stop_publish_zero_then_explicit_replay", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, true)
		x := g.wrapped(&trainingCreatorStore{Store: g.f.Store, at: 2, unknown: true})
		accepted, e := x.s.RecordEvents(x.ctx, x.input.NodeID, x.credential, x.lease.TaskID, x.lease.LeaseToken, prelude(*x.lease.Training))
		if e == nil || e.Error() != string(transaction.CommitUnknown) || len(accepted) != 0 {
			t.Fatal("unknown events projected", e)
		}
		receipt := g.stopReceipt(t)
		receipt.Proof = "owned_container_removed"
		container := strings.Repeat("a", 64)
		receipt.ContainerID = &container
		x = g.wrapped(&trainingCreatorStore{Store: g.f.Store, at: 2, unknown: true})
		stopped, e := x.s.RecordStop(x.ctx, x.input.NodeID, x.credential, x.lease.TaskID, x.lease.LeaseToken, receipt, nil)
		if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(stopped, wire.StopAcceptance{}) {
			t.Fatal("unknown stop projected", e)
		}
		stopped, e = g.s.RecordStop(g.ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken, receipt, nil)
		if e != nil || !stopped.Duplicate || !stopped.GPUReleased {
			t.Fatal("independent exact stop recovery", e)
		}
	})
	t.Run("undelivered_reconcile_releases_only_proven_never_claimed", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, false)
		current, e := g.s.Get(g.ctx, g.a, g.f.Workspace, g.job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = g.s.Cancel(g.ctx, g.a, g.f.Workspace, g.job.ID, training.ActionRequest{ExpectedRevision: current.Revision, Reason: "Cancel never delivered task"}, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		g.exec(t, "UPDATE training_jobs SET next_dispatch_at=clock_timestamp() WHERE id=$1", "UPDATE training_jobs SET next_dispatch_at=UTC_TIMESTAMP(6) WHERE id=?", g.job.ID)
		if worked, e := g.s.ReconcileNext(g.ctx); e != nil || !worked {
			t.Fatal("never delivered reconcile", e)
		}
		if g.count(t, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='released'", "SELECT COUNT(*) FROM gpu_leases WHERE training_job_id=? AND state='released'", g.job.ID) != 1 {
			t.Fatal("never delivered GPU owner retained")
		}
	})
	t.Run("removed_node_challenge_and_exact_operator_replay", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, true)
		challenge, e := recovery.PrepareTrainingRemoval(g.ctx, g.owner, g.input.NodeID, g.job.ID, "Explicit isolated synthetic removal challenge")
		if e != nil || challenge.Validate(time.Now()) != nil {
			t.Fatal("actual paired removal challenge", e)
		}
		if g.count(t, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='recovery_required'", "SELECT COUNT(*) FROM gpu_leases WHERE training_job_id=? AND state='recovery_required'", g.job.ID) != 1 {
			t.Fatal("challenge released physical owner")
		}
		proof := g.stopReceipt(t)
		proof.Proof = "owned_container_removed"
		container := strings.Repeat("b", 64)
		proof.ContainerID = &container
		receipt := wire.RemovalReceipt{SchemaVersion: 1, ReceiptID: uuid.NewString(), Challenge: challenge, ChallengeSHA256: challenge.Digest(), Stop: proof, ObservedAt: time.Now().UTC()}
		drop := g.auditFailure(t, "training.removal_reconciled")
		if e = recovery.ReconcileTrainingRemoval(g.ctx, g.owner, receipt, "Synthetic physical evidence audit rollback"); e == nil {
			t.Fatal("failed audit released physical owner")
		}
		drop()
		if e = recovery.ReconcileTrainingRemoval(g.ctx, g.owner, receipt, "Explicit isolated root synthetic proof"); e != nil {
			t.Fatal(e)
		}
		if e = recovery.ReconcileTrainingRemoval(g.ctx, g.owner, receipt, "Exact explicit proof replay"); e != nil {
			t.Fatal(e)
		}
		if g.count(t, "SELECT count(*) FROM training_jobs WHERE id=$1 AND state='failed' AND accepted_artifact_id IS NULL", "SELECT COUNT(*) FROM training_jobs WHERE id=? AND state='failed' AND accepted_artifact_id IS NULL", g.job.ID) != 1 {
			t.Fatal("operator invented trained success")
		}
	})
	t.Run("cancelled_context_and_wrong_credential_project_zero", func(t *testing.T) {
		g := trainingRuntimeSetup(t, nil, true)
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		renewed, _, e := g.s.RenewTraining(ctx, g.input.NodeID, g.credential, g.lease.TaskID, g.lease.LeaseToken)
		if e == nil || e.Error() != string(transaction.Cancelled) || !reflect.DeepEqual(renewed, agent.RenewedLease{}) {
			t.Fatal("cancelled renewal", e)
		}
		renewed, _, e = g.s.RenewTraining(g.ctx, g.input.NodeID, security.Token(), g.lease.TaskID, g.lease.LeaseToken)
		if e == nil || e.Error() != "AGENT_UNAUTHORIZED" || !reflect.DeepEqual(renewed, agent.RenewedLease{}) {
			t.Fatal("wrong machine credential", e)
		}
	})
}
