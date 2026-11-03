package training_test

import (
	"context"
	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"testing"
	"time"
)

func TestExplicitLifecycleProfileRetainsActualTrainingAdmissionClaimRenewAndRestrictedStop(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	join, e := f.s.Nodes.MintForProfile(ctx, f.admin, f.input.WorkspaceID, "Explicit synthetic lifecycle training node", agentwire.DockerModelLifecycleProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := agentwire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-lifecycle-training", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: agentwire.AgentVersion}
	enrolled, e := f.s.Nodes.Enroll(ctx, agentwire.Enrollment{JoinToken: join.Token, Handshake: agentwire.Handshake{ProtocolVersion: agentwire.ModelLifecycleVersion, ExecutionProfile: agentwire.DockerModelLifecycleProfile, Capabilities: agentwire.Capabilities(agentwire.DockerModelLifecycleProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	f.input.NodeID = enrolled.NodeID
	f.credential = enrolled.Credential
	job := dispatched(t, f)
	lease := claimed(t, f)
	if lease.Training == nil || lease.Operation != agentwire.TrainingRunOperation {
		t.Fatal("composite profile lost existing training protocol")
	}
	if _, e = f.s.Nodes.Renew(ctx, enrolled.NodeID, enrolled.Credential, lease.TaskID, lease.LeaseToken); e != nil {
		t.Fatal("actual delegated training renewal", e)
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", lease.TaskID); e != nil {
		t.Fatal(e)
	}
	receipt := stopFixture(t, f, lease)
	result, e := f.s.RecordStop(ctx, enrolled.NodeID, enrolled.Credential, lease.TaskID, lease.LeaseToken, receipt, nil)
	if e != nil || result.State != "failed" || !result.GPUReleased {
		t.Fatal("exact expired-token positive synthetic stop", e)
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='released'", job.ID) != 1 {
		t.Fatal("retained composite training GPU owner")
	}
}
