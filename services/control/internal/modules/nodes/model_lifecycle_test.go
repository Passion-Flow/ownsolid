package nodes_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

func TestM10ExplicitLifecycleEnrollmentRejectsLegacyUpgradeAndGenericPreparationLease(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	legacyJoin, e := s.MintForProfile(ctx, a, a.Me.Workspace.ID, "Preserved AI enrollment", wire.DockerAIProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	current := wire.Handshake{ProtocolVersion: wire.ModelLifecycleVersion, ExecutionProfile: wire.DockerModelLifecycleProfile, Capabilities: wire.Capabilities(wire.DockerModelLifecycleProfile), Facts: facts()}
	_, e = s.Enroll(ctx, wire.Enrollment{JoinToken: legacyJoin.Token, Handshake: current}, uuid.NewString())
	expected(t, e, "AGENT_PROFILE_REJECTED")
	old := wire.Handshake{ProtocolVersion: wire.AIVersion, ExecutionProfile: wire.DockerAIProfile, Capabilities: wire.Capabilities(wire.DockerAIProfile), Facts: facts()}
	legacy, e := s.Enroll(ctx, wire.Enrollment{JoinToken: legacyJoin.Token, Handshake: old}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Heartbeat(ctx, legacy.NodeID, legacy.Credential, current)
	expected(t, e, "AGENT_PROFILE_REJECTED")
	join, e := s.MintForProfile(ctx, a, a.Me.Workspace.ID, "Explicit lifecycle enrollment", wire.DockerModelLifecycleProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: old}, uuid.NewString())
	expected(t, e, "AGENT_PROFILE_REJECTED")
	enrolled, e := s.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: current}, uuid.NewString())
	if e != nil || enrolled.ProtocolVersion != wire.ModelLifecycleVersion {
		t.Fatal("explicit lifecycle identity", e)
	}
	// An explicitly synthetic task tests the fail-closed generic path, not real preparation dispatch.
	task := uuid.NewString()
	if _, e = s.DB.Exec(ctx, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,payload,request_fingerprint) VALUES($1,$2,$3,$4,'model.prepare.v1','pending',$5,$6,clock_timestamp()+interval '1 minute','{}',decode(repeat('ab',32),'hex'))`, task, a.Me.Workspace.TenantID, a.Me.Workspace.ID, enrolled.NodeID, uuid.NewString(), a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.Claim(ctx, enrolled.NodeID, enrolled.Credential, uuid.NewString())
	expected(t, e, "MODEL_PREPARATION_AUTHORITY_REQUIRED")
	var state string
	var attempt int
	var noToken bool
	if e = s.DB.QueryRow(ctx, "SELECT state,attempt,lease_token_hash IS NULL FROM node_tasks WHERE id=$1", task).Scan(&state, &attempt, &noToken); e != nil || state != "pending" || attempt != 0 || !noToken {
		t.Fatal("generic preparation lease escaped", e)
	}
	t.Run("actual_TLS_negotiated_schema_and_preparation_generic_refusal", func(t *testing.T) {
		server := httpfixture.NewTLSServer(t, transport.New(s.Identity))
		defer server.Close()
		client := httpfixture.New(t, server, "admin", httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json"))
		join, e := s.MintForProfile(ctx, a, a.Me.Workspace.ID, "TLS lifecycle node", wire.DockerModelLifecycleProfile, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		client.Call("POST", "/agent/v1/enroll", wire.Enrollment{JoinToken: join.Token, Handshake: old}, 403, "Problem")
		result := client.Call("POST", "/agent/v1/enroll", wire.Enrollment{JoinToken: join.Token, Handshake: current}, 201, "EnrolledResponse")
		if result["protocol_version"] != wire.ModelLifecycleVersion {
			t.Fatal("wrong negotiated TLS response")
		}
		client.Headers = map[string]string{"Authorization": "Bearer " + result["credential"].(string)}
		base := "/agent/v1/sites/default/cells/default/nodes/" + result["node_id"].(string)
		client.Call("POST", base+"/heartbeat", current, 200, "DispositionResponse")
		client.Call("POST", base+"/heartbeat", old, 403, "Problem")
		client.Headers = map[string]string{"Authorization": "Bearer " + enrolled.Credential}
		problem := client.Call("POST", "/agent/v1/sites/default/cells/default/nodes/"+enrolled.NodeID+"/tasks/claims", map[string]any{}, 403, "Problem")
		if problem["code"] != "MODEL_PREPARATION_AUTHORITY_REQUIRED" {
			t.Fatal("generic TLS lease exposed preparation", problem["code"])
		}
	})
}
