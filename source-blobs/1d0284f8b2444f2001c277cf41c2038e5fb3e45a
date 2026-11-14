package nodes_test

import (
	"encoding/json"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentcost"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInferenceHeartbeatStatementCost(t *testing.T) {
	ctx := t.Context()
	var recorder agentcost.Recorder
	account := fixture.NewConfigured(t, func(c *pgxpool.Config) {
		recorder.Configure(c)
		c.ConnConfig.RuntimeParams["auto_explain.log_parameter_max_length"] = "0"
	})
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	workspaces := &workspaces.Service{DB: account.DB, Identity: account}
	s := &nodes.Service{DB: account.DB, Identity: account, Workspaces: workspaces}
	s.Inference = &deployments.Service{Nodes: s, Models: &models.Service{DB: account.DB, Identity: account, Workspaces: workspaces}}

	profile := wire.DockerInferenceProfile
	token, err := s.MintForProfile(ctx, admin, admin.Me.Workspace.ID, "Inference heartbeat cost node", profile, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	h := wire.Handshake{ProtocolVersion: wire.ProfileVersion(profile), ExecutionProfile: profile, Capabilities: wire.Capabilities(profile), Facts: facts()}
	node, err := s.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: h}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, ObservedAt: h.Facts.ObservedAt}
	for _, probe := range []struct {
		name, credential, probeError, outcome string
	}{
		{name: "first report", credential: node.Credential},
		{name: "report update", credential: node.Credential, probeError: "INFERENCE_NVIDIA_UNAVAILABLE"},
		{name: "foreign credential", credential: security.Token(), outcome: "AGENT_UNAUTHORIZED"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			h.Inference.ProbeErrorCode = probe.probeError
			var out wire.Disposition
			var heartbeatError error
			cost, err := recorder.Record(func() {
				out, heartbeatError = s.Heartbeat(ctx, node.NodeID, probe.credential, h)
			})
			if err != nil {
				t.Fatal(err)
			}
			if probe.outcome != "" {
				expected(t, heartbeatError, probe.outcome)
				if out.Status != "" {
					t.Fatal("refused heartbeat returned disposition")
				}
			} else if heartbeatError != nil || out.Status != "active" {
				t.Fatal("heartbeat did not persist report", heartbeatError)
			}
			t.Logf("statements=%d rows=%d blocks=%d", len(cost.Statements), cost.Rows(), cost.Blocks())
			if n := len(cost.Statements); n == 0 || n > wire.CallHeartbeat.Cost().Statements {
				t.Errorf("recorded %d statements for a ceiling of %d", n, wire.CallHeartbeat.Cost().Statements)
			}
			if probe.outcome == "" {
				var persisted time.Time
				err := account.DB.QueryRow(ctx, `SELECT last_heartbeat_at FROM managed_nodes WHERE id=$1`, node.NodeID).Scan(&persisted)
				if err != nil || !out.ServerTime.Equal(persisted) {
					t.Fatal("reply did not use the committed heartbeat timestamp", err)
				}
			}
		})
	}
	var raw []byte
	err = account.DB.QueryRow(ctx, `SELECT observation FROM inference_nodes WHERE node_id=$1`, node.NodeID).Scan(&raw)
	var stored wire.InferenceObservation
	if err != nil || json.Unmarshal(raw, &stored) != nil || stored.ProbeErrorCode != "INFERENCE_NVIDIA_UNAVAILABLE" || !stored.ObservedAt.Equal(h.Inference.ObservedAt) {
		t.Fatal("wrong persisted report", err)
	}
}
