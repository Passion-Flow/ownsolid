package nodes_test

import (
	"context"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

func TestComponentResultFencesOwnerWritesAtSettlement(t *testing.T) {
	ctx := t.Context()
	service, admin := setup(t)
	owner := &components.Service{Nodes: service}
	service.Components = owner
	profile := wire.SystemdComponentsProfile
	token, err := service.MintForProfile(ctx, admin, admin.Me.Workspace.ID, "Component result node", profile, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	handshake := wire.Handshake{
		ProtocolVersion: wire.ProfileVersion(profile), ExecutionProfile: profile,
		Capabilities: wire.Capabilities(profile), Facts: facts(),
	}
	node, err := service.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: handshake}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	handshake.Component = &wire.ComponentObservation{
		Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed",
		ObservedAt: handshake.Facts.ObservedAt,
	}
	if _, err := service.Heartbeat(ctx, node.NodeID, node.Credential, handshake); err != nil {
		t.Fatal(err)
	}
	task, err := owner.Request(ctx, admin, node.NodeID, "start", uuid.NewString(), "Check settlement authority", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	claim, err := service.Claim(ctx, node.NodeID, node.Credential, uuid.NewString())
	if err != nil || claim.Lease == nil || claim.Lease.TaskID != task.ID {
		t.Fatal("expected the exact component task lease", err)
	}
	acknowledge(t, service, node, claim)

	// A returning agent may still finish after its liveness lease expires.
	if _, err := service.DB.Exec(ctx, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", task.ID); err != nil {
		t.Fatal(err)
	}
	observation := *handshake.Component
	observation.State, observation.ErrorCode = "unknown", "COMPONENT_SYSTEMD_UNAVAILABLE"
	result := wire.Result{
		LeaseToken: claim.Lease.LeaseToken, Outcome: "failure", ErrorCode: observation.ErrorCode, Component: &observation,
	}
	snapshot := func() string {
		t.Helper()
		var raw string
		err := service.DB.QueryRow(ctx, `SELECT jsonb_build_array(to_jsonb(t),to_jsonb(c),to_jsonb(h))::text
			FROM node_tasks t
			JOIN managed_components c ON c.node_id=t.node_id AND c.component='node_exporter'
			JOIN audit_chain_head h ON h.singleton WHERE t.id=$1`, task.ID).Scan(&raw)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := snapshot()
	for _, failure := range []struct{ name, update string }{
		{"deadline expired", "UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1"},
		{"lease replaced", "UPDATE node_tasks SET lease_token_hash=decode(repeat('fe',32),'hex') WHERE id=$1"},
	} {
		t.Run(failure.name, func(t *testing.T) {
			service.Components = interruptedComponentResult{Service: owner, update: failure.update}
			t.Cleanup(func() { service.Components = owner })
			state, err := service.Complete(ctx, node.NodeID, node.Credential, task.ID, result, uuid.NewString())
			expected(t, err, "AGENT_LEASE_STALE")
			if state != "" || snapshot() != before {
				t.Fatal("refused settlement changed the task, component or audit head")
			}
		})
	}
	state, err := service.Complete(ctx, node.NodeID, node.Credential, task.ID, result, uuid.NewString())
	if err != nil || state != "failed" {
		t.Fatalf("expected the original result to settle, got %q, %v", state, err)
	}
	component, err := owner.Get(ctx, admin, admin.Me.Workspace.ID, node.NodeID)
	if err != nil || component.Observation == nil || component.Observation.ErrorCode != result.ErrorCode {
		t.Fatal("component observation did not persist", err)
	}
	settled := snapshot()
	state, err = service.Complete(ctx, node.NodeID, node.Credential, task.ID, result, uuid.NewString())
	if err != nil || state != "failed" || snapshot() != settled {
		t.Fatalf("expected unchanged exact result replay, got %q, %v", state, err)
	}
}

type interruptedComponentResult struct {
	*components.Service
	update string
}

func (o interruptedComponentResult) RecordResultWithin(ctx context.Context, q transaction.Executor, n nodes.Node, task nodes.Task, result wire.Result) error {
	if err := o.Service.RecordResultWithin(ctx, q, n, task, result); err != nil {
		return err
	}
	_, err := q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, o.update, task.ID))
	return err
}
