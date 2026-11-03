//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// Hardware is an explicit test double transported through the actual credentialed heartbeat.
// No physical NVIDIA observation or preparation execution is asserted by these tests.
func preparationHardware(t *testing.T, g preparationScenario) {
	t.Helper()
	ctx := context.Background()
	n, e := g.f.s.Nodes.Get(ctx, g.f.admin, "", g.node.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	n.Facts.ObservedAt = time.Now().UTC()
	o := agentwire.InferenceObservation{GPUs: []agentwire.NVIDIAGPU{{UUID: g.input.GPUUUID, Name: "Explicit synthetic preparation GPU", DriverVersion: "580.1.0", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8.0, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, ObservedAt: time.Now().UTC()}
	_, e = g.f.s.Nodes.Heartbeat(ctx, g.node.NodeID, g.node.Credential, agentwire.Handshake{ProtocolVersion: agentwire.ModelLifecycleVersion, ExecutionProfile: agentwire.DockerModelLifecycleProfile, Capabilities: agentwire.Capabilities(agentwire.DockerModelLifecycleProfile), Facts: n.Facts, Inference: &o})
	if e != nil {
		t.Fatal("real credentialed synthetic observation", e)
	}
}

func preparationQueued(t *testing.T, g preparationScenario) modelpreparations.Preparation {
	t.Helper()
	p, e := g.service.Create(context.Background(), g.f.portal, g.input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func preparationDue(t *testing.T, g preparationScenario, id string) {
	t.Helper()
	if _, e := g.f.s.DB.Exec(context.Background(), "UPDATE model_preparations SET next_dispatch_at=clock_timestamp()-interval '1 second' WHERE id=$1", id); e != nil {
		t.Fatal(e)
	}
}

func preparationDispatched(t *testing.T, g preparationScenario) modelpreparations.Preparation {
	t.Helper()
	preparationHardware(t, g)
	p := preparationQueued(t, g)
	if worked, e := g.service.DispatchNext(context.Background()); e != nil || !worked {
		t.Fatal("actual preparation dispatch", worked, e)
	}
	p, e := g.service.Get(context.Background(), g.f.portal, p.WorkspaceID, p.ID)
	if e != nil || p.State != "preparing" || p.AttemptNumber != 1 || p.CurrentAttemptID == nil {
		t.Fatal("actual durable attempt", e, p.State)
	}
	return p
}

func preparationClaimed(t *testing.T, g preparationScenario) agentwire.Lease {
	t.Helper()
	claim, e := g.f.s.Nodes.Claim(context.Background(), g.node.NodeID, g.node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.Preparation == nil {
		t.Fatal("actual preparation authority claim", e)
	}
	return *claim.Lease
}

func TestActualCpuModelPreparationDispatchObservationSharedGPUAndUndeliveredRelease(t *testing.T) {
	g := newPreparationScenario(t)
	ctx := context.Background()
	p := preparationQueued(t, g)
	if worked, e := g.service.DispatchNext(ctx); e != nil || worked {
		t.Fatal("missing physical observation", worked, e)
	}
	p, e := g.service.Get(ctx, g.f.portal, p.WorkspaceID, p.ID)
	if e != nil || p.WaitCode == nil || *p.WaitCode != "MODEL_PREPARATION_PREFLIGHT_REQUIRED" || p.AttemptNumber != 0 {
		t.Fatal("observable preflight refusal", e)
	}
	preparationHardware(t, g)
	activeTraining := dispatched(t, g.f)
	preparationDue(t, g, p.ID)
	if worked, e := g.service.DispatchNext(ctx); e != nil || worked {
		t.Fatal("shared physical training owner", worked, e)
	}
	p, e = g.service.Get(ctx, g.f.portal, p.WorkspaceID, p.ID)
	if e != nil || p.WaitCode == nil || *p.WaitCode != "MODEL_PREPARATION_RESOURCE_BUSY" || p.AttemptNumber != 0 {
		t.Fatal("third owner bypassed shared lease", e)
	}
	if count(t, g.f, "SELECT count(*) FROM node_tasks WHERE operation=$1", wire.RunOperation) != 0 {
		t.Fatal("waiting created executable side effect")
	}
	_, e = g.f.s.Cancel(ctx, g.f.portal, activeTraining.WorkspaceID, activeTraining.ID, training.ActionRequest{ExpectedRevision: activeTraining.Revision, Reason: "Release only transactionally never delivered training"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if worked, e := g.f.s.ReconcileNext(ctx); e != nil || !worked {
		t.Fatal("actual prior owner undelivered reconciliation", worked, e)
	}
	preparationDue(t, g, p.ID)
	if worked, e := g.service.DispatchNext(ctx); e != nil || !worked {
		t.Fatal("actual shared owner transfer", worked, e)
	}
	p, e = g.service.Get(ctx, g.f.portal, p.WorkspaceID, p.ID)
	if e != nil || p.CurrentAttemptID == nil || p.AttemptNumber != 1 {
		t.Fatal(e)
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='held'", p.ID) != 1 {
		t.Fatal("missing exclusive preparation owner")
	}
	var raw []byte
	if e = g.f.s.DB.QueryRow(ctx, "SELECT t.payload FROM node_tasks t JOIN model_preparation_attempts a ON a.task_id=t.id WHERE a.id=$1", *p.CurrentAttemptID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	c, e := wire.DecodeCommand(raw)
	if e != nil || c.Scope.PreparationID != p.ID || c.AttemptID != *p.CurrentAttemptID || len(c.Inputs.AdapterFiles) != 3 || c.Inputs.Heldout.SizeBytes < 1 {
		t.Fatal("actual exact frozen command", e)
	}
	_, e = g.service.Cancel(ctx, g.f.portal, p.WorkspaceID, p.ID, modelpreparations.ActionRequest{ExpectedRevision: p.Revision, Reason: "Cancel undelivered preparation"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	preparationDue(t, g, p.ID)
	if worked, e := g.service.ReconcileNext(ctx); e != nil || !worked {
		t.Fatal("actual undelivered release", worked, e)
	}
	p, e = g.service.Get(ctx, g.f.portal, p.WorkspaceID, p.ID)
	if e != nil || p.State != "cancelled" {
		t.Fatal("cancelled state", e, p.State)
	}
	var receipt struct {
		Proof      string `json:"proof"`
		CommandSHA string `json:"command_sha256"`
	}
	if e = g.f.s.DB.QueryRow(ctx, "SELECT physical_stop_receipt FROM model_preparation_attempts WHERE id=$1", *p.CurrentAttemptID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	sha, _ := c.Digest()
	if json.Unmarshal(raw, &receipt) != nil || receipt.Proof != "control_never_delivered" || receipt.CommandSHA != sha || count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='released'", p.ID) != 1 {
		t.Fatal("positive never-delivered proof missing")
	}
}

func TestActualCpuModelPreparationConcurrentClaimRenewAndGenericBypassRefusal(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationDispatched(t, g)
	ctx := context.Background()
	_, e := g.f.s.Nodes.Claim(ctx, g.node.NodeID, security.Token(), uuid.NewString())
	expected(t, e, "AGENT_UNAUTHORIZED")
	var claims [2]agentwire.Claim
	var errs [2]error
	var group sync.WaitGroup
	for i := range claims {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			claims[i], errs[i] = g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
		}(i)
	}
	group.Wait()
	var lease *agentwire.Lease
	for i, c := range claims {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if c.Lease != nil {
			if lease != nil {
				t.Fatal("delivered twice")
			}
			lease = c.Lease
		}
	}
	if lease == nil || lease.Preparation == nil || lease.Preparation.Scope.PreparationID != p.ID || lease.Preparation.AttemptID != *p.CurrentAttemptID || lease.Training != nil {
		t.Fatal("typed exclusive claim")
	}
	renewed, e := g.f.s.Nodes.Renew(ctx, g.node.NodeID, g.node.Credential, lease.TaskID, lease.LeaseToken)
	if e != nil || renewed.ValidForMilliseconds < 1 {
		t.Fatal("current authority renewal", e)
	}
	e = g.f.s.Nodes.WithLease(ctx, g.node.NodeID, g.node.Credential, lease.TaskID, lease.LeaseToken, func(pgx.Tx, nodes.Node, nodes.Task) error { t.Fatal("generic preparation effect bypass"); return nil })
	expected(t, e, "MODEL_PREPARATION_AUTHORITY_REQUIRED")
	_, e = g.f.s.Nodes.Renew(ctx, g.node.NodeID, g.node.Credential, lease.TaskID, security.Token())
	expected(t, e, "AGENT_LEASE_STALE")
	if count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE id=$1 AND octet_length(stop_token_hash)=32", *p.CurrentAttemptID) != 1 {
		t.Fatal("missing durable independent stop authority")
	}
	if _, e = g.f.s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", lease.TaskID); e != nil {
		t.Fatal(e)
	}
	_, e = g.f.s.Nodes.Renew(ctx, g.node.NodeID, g.node.Credential, lease.TaskID, lease.LeaseToken)
	expected(t, e, "AGENT_LEASE_STALE")
	preparationDue(t, g, p.ID)
	if worked, e := g.service.ReconcileNext(ctx); e != nil || !worked {
		t.Fatal("delivered expiry retains hardware", worked, e)
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='recovery_required'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE id=$1 AND physical_stopped_at IS NULL", *p.CurrentAttemptID) != 1 {
		t.Fatal("expired lease falsely proved physical stop")
	}
}

func TestActualCpuModelPreparationRevokedAuthorityAndNodeFenceRetainPhysicalOwner(t *testing.T) {
	for _, mode := range []string{"epoch", "membership", "role", "cancel", "node_removed"} {
		t.Run(mode, func(t *testing.T) {
			g := newPreparationScenario(t)
			p := preparationDispatched(t, g)
			l := preparationClaimed(t, g)
			ctx := context.Background()
			var e error
			switch mode {
			case "epoch":
				_, e = g.f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", g.f.portal.Me.User.ID)
			case "membership":
				_, e = g.f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET authority_id=$3 WHERE workspace_id=$1 AND user_id=$2", p.WorkspaceID, g.f.portal.Me.User.ID, uuid.NewString())
			case "role":
				_, e = g.f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", p.WorkspaceID, g.f.portal.Me.User.ID)
			case "cancel":
				_, e = g.service.Cancel(ctx, g.f.portal, p.WorkspaceID, p.ID, modelpreparations.ActionRequest{ExpectedRevision: p.Revision, Reason: "Stop current preparation authority"}, uuid.NewString())
			case "node_removed":
				n, readError := g.f.s.Nodes.Get(ctx, g.f.admin, "", g.node.NodeID)
				if readError != nil {
					t.Fatal(readError)
				}
				_, e = g.f.s.Nodes.Update(ctx, g.f.admin, n.ID, n.Name, "removed", n.Version, "Fence preparation without asserting physical stop", uuid.NewString())
			}
			if e != nil {
				t.Fatal(e)
			}
			_, e = g.f.s.Nodes.Renew(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken)
			if mode == "node_removed" {
				expected(t, e, "AGENT_UNAUTHORIZED")
			} else {
				expected(t, e, "ACCESS_DENIED")
			}
			preparationDue(t, g, p.ID)
			_, e = g.service.ReconcileNext(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='recovery_required'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE id=$1 AND physical_stopped_at IS NULL", *p.CurrentAttemptID) != 1 {
				t.Fatal("fence released uncertain owner")
			}
		})
	}
}

func TestActualCpuModelPreparationDispatchAndReleaseAuditFailuresRollbackEveryOwner(t *testing.T) {
	g := newPreparationScenario(t)
	ctx := context.Background()
	preparationHardware(t, g)
	p := preparationQueued(t, g)
	if _, e := g.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_execution_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action IN ('model.preparation_dispatched','model.preparation_undelivered_stopped') THEN RAISE EXCEPTION 'SYNTHETIC_PREPARATION_EXECUTION_AUDIT_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_execution_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_execution_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e := g.service.DispatchNext(ctx); e == nil {
		t.Fatal("dispatch audit failure accepted")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_attempts") != 0 || count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id IS NOT NULL") != 0 || count(t, g.f, "SELECT count(*) FROM node_tasks WHERE operation=$1", wire.RunOperation) != 0 {
		t.Fatal("partial dispatch survived audit rollback")
	}
	if _, e := g.f.s.DB.Exec(ctx, "ALTER TABLE audit_events DISABLE TRIGGER reject_preparation_execution_audit"); e != nil {
		t.Fatal(e)
	}
	if worked, e := g.service.DispatchNext(ctx); e != nil || !worked {
		t.Fatal("retry after dispatch audit repair", worked, e)
	}
	p, e := g.service.Get(ctx, g.f.portal, p.WorkspaceID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = g.service.Cancel(ctx, g.f.portal, p.WorkspaceID, p.ID, modelpreparations.ActionRequest{ExpectedRevision: p.Revision, Reason: "Audit failure must not release physical owner"}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = g.f.s.DB.Exec(ctx, "ALTER TABLE audit_events ENABLE TRIGGER reject_preparation_execution_audit"); e != nil {
		t.Fatal(e)
	}
	preparationDue(t, g, p.ID)
	if _, e = g.service.ReconcileNext(ctx); e == nil {
		t.Fatal("release audit failure accepted")
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='held'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE id=$1 AND physical_stopped_at IS NULL", *p.CurrentAttemptID) != 1 {
		t.Fatal("partial release survived audit rollback")
	}
	if _, e = g.f.s.DB.Exec(ctx, "ALTER TABLE audit_events DISABLE TRIGGER reject_preparation_execution_audit"); e != nil {
		t.Fatal(e)
	}
	if worked, e := g.service.ReconcileNext(ctx); e != nil || !worked {
		t.Fatal("retry after release audit repair", worked, e)
	}
}

func TestActualCpuModelPreparationFrozenCommandTamperCannotRetainClaim(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationDispatched(t, g)
	ctx := context.Background()
	var task string
	var raw []byte
	if e := g.f.s.DB.QueryRow(ctx, "SELECT t.id::text,t.payload FROM node_tasks t JOIN model_preparation_attempts a ON a.task_id=t.id WHERE a.id=$1", *p.CurrentAttemptID).Scan(&task, &raw); e != nil {
		t.Fatal(e)
	}
	c, e := wire.DecodeCommand(raw)
	if e != nil {
		t.Fatal(e)
	}
	c.Configuration.MinimumAccuracyBasisPoints = 3000
	c.ConfigurationSHA256, _ = c.Configuration.Digest()
	raw, _ = json.Marshal(c)
	if _, e = g.f.s.DB.Exec(ctx, "UPDATE node_tasks SET payload=$2 WHERE id=$1", task, raw); e != nil {
		t.Fatal(e)
	}
	_, e = g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	expected(t, e, "MODEL_PREPARATION_ATTEMPT_STALE")
	if count(t, g.f, "SELECT attempt FROM node_tasks WHERE id=$1", task) != 0 || count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE id=$1 AND stop_token_hash IS NULL", *p.CurrentAttemptID) != 1 {
		t.Fatal("rejected command retained claim authority")
	}
}

func TestActualCpuModelPreparationClaimCancellationRaceCannotReleaseDeliveredAttempt(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationDispatched(t, g)
	ctx := context.Background()
	start := make(chan struct{})
	var group sync.WaitGroup
	var claim agentwire.Claim
	var claimError, cancelError error
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		claim, claimError = g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	}()
	go func() {
		defer group.Done()
		<-start
		_, cancelError = g.service.Cancel(ctx, g.f.portal, p.WorkspaceID, p.ID, modelpreparations.ActionRequest{ExpectedRevision: p.Revision, Reason: "Concurrent claim and cancellation"}, uuid.NewString())
	}()
	close(start)
	group.Wait()
	if cancelError != nil || claimError != nil && claimError.Error() != "ACCESS_DENIED" {
		t.Fatal(claimError, cancelError)
	}
	preparationDue(t, g, p.ID)
	if _, e := g.service.ReconcileNext(ctx); e != nil {
		t.Fatal(e)
	}
	state := "released"
	if claim.Lease != nil {
		state = "recovery_required"
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state=$2", p.ID, state) != 1 {
		t.Fatal("race confused never-delivered and delivered ownership")
	}
}
