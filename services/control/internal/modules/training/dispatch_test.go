package training_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

func hardware(t *testing.T, f scenario) {
	t.Helper()
	o := agentwire.InferenceObservation{GPUs: []agentwire.NVIDIAGPU{{UUID: f.input.GPUUUID, Name: "Explicit synthetic GPU observation", DriverVersion: "580.1.0", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8.0, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, ObservedAt: time.Now().UTC()}
	if e := o.Validate(); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(o)
	if _, e := f.s.DB.Exec(context.Background(), `INSERT INTO inference_nodes(tenant_id,workspace_id,node_id,observation) VALUES($1,$2,$3,$4) ON CONFLICT(node_id) DO UPDATE SET observation=EXCLUDED.observation,received_at=clock_timestamp()`, f.admin.Me.Workspace.TenantID, f.input.WorkspaceID, f.input.NodeID, raw); e != nil {
		t.Fatal(e)
	}
}
func queued(t *testing.T, f scenario) training.Job {
	t.Helper()
	j, e := f.s.Create(context.Background(), f.portal, f.input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func count(t *testing.T, f scenario, sql string, args ...any) int {
	t.Helper()
	var count int
	if e := f.s.DB.QueryRow(context.Background(), sql, args...).Scan(&count); e != nil {
		t.Fatal(e)
	}
	return count
}

func TestActualTrainingDispatchFrozenTaskReservationAndActiveCancelDoesNotRelease(t *testing.T) {
	f := setup(t)
	hardware(t, f)
	j := queued(t, f)
	ctx := context.Background()
	worked, e := f.s.DispatchNext(ctx)
	if e != nil || !worked {
		t.Fatal("real dispatch", worked, e)
	}
	j, e = f.s.Get(ctx, f.portal, f.input.WorkspaceID, j.ID)
	if e != nil || j.State != "preparing" || j.AttemptNumber != 1 || j.CurrentAttemptID == nil {
		t.Fatal("persisted attempt", e)
	}
	var raw []byte
	var task, attempt, resourceState string
	var revision int64
	if e = f.s.DB.QueryRow(ctx, `SELECT t.id::text,t.payload,a.id::text,g.state,g.revision FROM node_tasks t JOIN training_attempts a ON a.task_id=t.id JOIN gpu_leases g ON g.training_job_id=a.job_id WHERE a.job_id=$1`, j.ID).Scan(&task, &raw, &attempt, &resourceState, &revision); e != nil {
		t.Fatal(e)
	}
	command, e := wire.DecodeCommand(raw)
	if e != nil || command.AttemptID != attempt || command.AttemptID != *j.CurrentAttemptID || command.Scope.JobID != j.ID || command.ConfigurationSHA256 != j.ConfigurationSHA256 || revision != 1 || resourceState != "held" {
		t.Fatal("typed immutable task", e)
	}
	if command.Inputs.Train.SizeBytes < 1 || command.Inputs.Validation.SizeBytes < 1 || task == "" {
		t.Fatal("missing actual partition inventory")
	}
	cancelled, e := f.s.Cancel(ctx, f.portal, f.input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision, Reason: "Durable cancellation requires later positive physical stop"}, uuid.NewString())
	if e != nil || cancelled.State != "cancelling" {
		t.Fatal(e)
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='held'", j.ID) != 1 {
		t.Fatal("cancel prematurely released possible physical owner")
	}
	second := queued(t, f)
	worked, e = f.s.DispatchNext(ctx)
	if e != nil || worked {
		t.Fatal("busy dispatch", worked, e)
	}
	second, e = f.s.Get(ctx, f.portal, f.input.WorkspaceID, second.ID)
	if e != nil || second.State != "queued" || second.WaitCode == nil || *second.WaitCode != "TRAINING_RESOURCE_BUSY" || second.AttemptNumber != 0 {
		t.Fatal("observable resource wait", e)
	}
}

func TestActualTrainingRevokedDelegationAndRecreatedMembershipCannotDispatch(t *testing.T) {
	for _, mode := range []string{"epoch", "membership_lifetime", "role"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			hardware(t, f)
			j := queued(t, f)
			ctx := context.Background()
			var e error
			switch mode {
			case "epoch":
				_, e = f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID)
			case "role":
				_, e = f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID)
			case "membership_lifetime":
				_, e = f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET authority_id=$3 WHERE workspace_id=$1 AND user_id=$2", f.input.WorkspaceID, f.portal.Me.User.ID, uuid.NewString())
			}
			if e != nil {
				t.Fatal(e)
			}
			worked, e := f.s.DispatchNext(ctx)
			if e != nil || !worked {
				t.Fatal(e)
			}
			var state, code string
			if e = f.s.DB.QueryRow(ctx, "SELECT state,error_code FROM training_jobs WHERE id=$1", j.ID).Scan(&state, &code); e != nil || state != "failed" || code != "TRAINING_ACTOR_UNAVAILABLE" {
				t.Fatal("revoked work dispatched", state, code, e)
			}
			if count(t, f, "SELECT count(*) FROM gpu_leases") != 0 || count(t, f, "SELECT count(*) FROM training_attempts") != 0 {
				t.Fatal("revoked authority allocated resources")
			}
		})
	}
}

func TestActualTrainingAndInferenceReservationRaceHasOnePhysicalOwner(t *testing.T) {
	f := setup(t)
	hardware(t, f)
	queued(t, f)
	ctx := context.Background()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var trained bool
	var trainError, inferenceError error
	wg.Add(2)
	go func() { defer wg.Done(); <-start; trained, trainError = f.s.DispatchNext(ctx) }()
	go func() {
		defer wg.Done()
		<-start
		_, inferenceError = f.s.Inference.Create(ctx, f.admin, deployments.CreateRequest{NodeID: f.input.NodeID, ModelID: f.input.ModelID, GPUUUID: f.input.GPUUUID, Reason: "Synthetic inference/training resource race"}, uuid.NewString(), uuid.NewString())
	}()
	close(start)
	wg.Wait()
	if trainError != nil {
		t.Fatal(trainError)
	}
	if trained && inferenceError == nil || !trained && inferenceError != nil {
		t.Fatal("race did not produce exactly one owner", trained, inferenceError)
	}
	if inferenceError != nil && inferenceError.Error() != "INFERENCE_RESOURCE_BUSY" && inferenceError.Error() != "NODE_BUSY" {
		t.Fatal(inferenceError)
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state IN ('held','recovery_required')") != 1 || count(t, f, "SELECT count(*) FROM node_tasks WHERE state IN ('pending','running')") != 1 {
		t.Fatal("shared physical uniqueness failed")
	}
}

func TestActualTrainingPhysicalGPUConflictAcrossNodeIdentitiesAndBusyTaskRollback(t *testing.T) {
	for _, mode := range []string{"physical_gpu", "busy_task"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			hardware(t, f)
			ctx := context.Background()
			if mode == "physical_gpu" {
				first := queued(t, f)
				if worked, e := f.s.DispatchNext(ctx); e != nil || !worked {
					t.Fatal(e)
				}
				other := uuid.NewString()
				if _, e := f.s.DB.Exec(ctx, `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities) SELECT $2,tenant_id,workspace_id,'Synthetic re-enrolled physical GPU','active',decode(repeat('cd',32),'hex'),protocol_version,facts,clock_timestamp(),execution_profile,capabilities FROM managed_nodes WHERE id=$1`, f.input.NodeID, other); e != nil {
					t.Fatal(e)
				}
				f.input.NodeID = other
				hardware(t, f)
				second := queued(t, f)
				worked, e := f.s.DispatchNext(ctx)
				if e != nil || worked {
					t.Fatal("physical UUID conflict", e)
				}
				j, e := f.s.Get(ctx, f.portal, f.input.WorkspaceID, second.ID)
				if e != nil || j.WaitCode == nil || *j.WaitCode != "TRAINING_RESOURCE_BUSY" {
					t.Fatal("new identity bypassed held GPU", e)
				}
				if count(t, f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='held'", first.ID) != 1 || count(t, f, "SELECT count(*) FROM training_attempts") != 1 {
					t.Fatal("physical ownership duplicated")
				}
			} else {
				if _, e := f.s.DB.Exec(ctx, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at) VALUES($1,$2,$3,$4,'collect_facts.v1','pending',$5,$6,clock_timestamp()+interval '1 minute')`, uuid.NewString(), f.admin.Me.Workspace.TenantID, f.input.WorkspaceID, f.input.NodeID, uuid.NewString(), f.admin.Me.User.ID); e != nil {
					t.Fatal(e)
				}
				j := queued(t, f)
				worked, e := f.s.DispatchNext(ctx)
				if e != nil || worked {
					t.Fatal(e)
				}
				j, e = f.s.Get(ctx, f.portal, f.input.WorkspaceID, j.ID)
				if e != nil || j.WaitCode == nil || *j.WaitCode != "NODE_BUSY" {
					t.Fatal("busy task wait", e)
				}
				if count(t, f, "SELECT count(*) FROM gpu_leases") != 0 || count(t, f, "SELECT count(*) FROM training_attempts") != 0 {
					t.Fatal("busy task left partial reservation")
				}
			}
		})
	}
}
