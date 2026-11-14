//go:build training_artifact_integration && model_lifecycle_admission_integration && model_lifecycle_output_acceptance_integration

package training_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// Only physical GPU/container/generation data is synthetic. The browser invokes real
// HTTP/current authorization, actual approved model files and PostgreSQL ownership.
func derivedBrowserPhysicalDouble(t *testing.T, g preparationScenario, model string) func() {
	t.Helper()
	node, e := g.f.s.Nodes.Get(context.Background(), g.f.admin, "", g.node.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var group sync.WaitGroup
	errorsFound := make(chan error, 1)
	completed := 0
	group.Add(1)
	t.Cleanup(func() { cancel(); group.Wait() })
	go func() {
		defer group.Done()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		fail := func(e error) {
			if ctx.Err() == nil {
				t.Log("explicit browser physical double failed", e)
				errorsFound <- e
			}
		}
		var last *wire.DeploymentObservation
		existing, e := g.f.s.Inference.List(ctx, g.f.admin, g.input.WorkspaceID)
		if e != nil {
			t.Fatal(e)
		}
		for _, d := range existing {
			if d.NodeID == g.node.NodeID && d.RemovedAt == nil && d.Observation != nil {
				last = d.Observation
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				node.Facts.ObservedAt = time.Now().UTC()
				gpu := wire.NVIDIAGPU{UUID: g.input.GPUUUID, Name: "Explicit browser physical GPU double", DriverVersion: "580.1.0", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}
				observation := wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{gpu}, Deployment: last, ObservedAt: time.Now().UTC()}
				if _, e := g.f.s.Nodes.Heartbeat(ctx, g.node.NodeID, g.node.Credential, wire.Handshake{ProtocolVersion: wire.ModelLifecycleVersion, ExecutionProfile: wire.DockerModelLifecycleProfile, Capabilities: wire.Capabilities(wire.DockerModelLifecycleProfile), Facts: node.Facts, Inference: &observation}); e != nil {
					fail(e)
					return
				}
				lease, e := g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
				if e != nil {
					fail(e)
					return
				}
				if lease.Lease == nil {
					continue
				}
				command := lease.Lease.Inference
				if command == nil {
					fail(errors.New("UNEXPECTED_BROWSER_PHYSICAL_DOUBLE_COMMAND"))
					return
				}
				derived := command.Derived != nil
				if derived && (lease.Lease.Operation != wire.DerivedInferenceOperation || command.ModelID != model) || !derived && (lease.Lease.Operation != wire.InferenceOperation(command.Action) || command.ModelID != g.f.input.ModelID || command.CatalogDigest != wire.ModelCatalogDigest()) {
					fail(errors.New("UNEXPECTED_BROWSER_PHYSICAL_DOUBLE_COMMAND"))
					return
				}
				deployment := wire.DeploymentObservation{DeploymentID: command.DeploymentID, ModelID: command.ModelID, CatalogDigest: command.CatalogDigest, GPUUUID: command.GPUUUID, Revision: command.Revision, State: "stopped", Managed: true, ContainerID: strings.Repeat("a", 64), ImageID: wire.ApprovedInferenceRecipe().ImageConfigDigest}
				if derived {
					deployment.ModelKind = wire.DerivedModelKind
					deployment.ServingRecipeSHA256 = command.Derived.ServingRecipeSHA256
					deployment.ImageID = wire.ApprovedDerivedServingRecipe().ImageConfigDigest
				}
				switch command.Action {
				case "deploy":
					deployment.State, deployment.Ready = "running", true
					deployment.Verification = &wire.GenerationVerification{Model: wire.InferenceModelName, PromptSHA256: wire.InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 12, OutputTokens: 1, ObservedAt: time.Now().UTC()}
				case "stop":
				case "remove":
					deployment.State, deployment.Managed, deployment.ContainerID, deployment.ImageID = "not_installed", false, "", ""
				default:
					fail(errors.New("UNEXPECTED_BROWSER_PHYSICAL_DOUBLE_ACTION"))
					return
				}
				observation.Deployment = &deployment
				if _, e = g.f.s.Nodes.Complete(ctx, g.node.NodeID, g.node.Credential, lease.Lease.TaskID, wire.Result{LeaseToken: lease.Lease.LeaseToken, Outcome: "success", Inference: &observation}, uuid.NewString()); e != nil {
					fail(e)
					return
				}
				last = &deployment
				completed++
			}
		}
	}()
	return func() {
		cancel()
		group.Wait()
		select {
		case e := <-errorsFound:
			t.Fatal("explicit derived browser physical double", e)
		default:
		}
		if completed != 8 {
			t.Fatal("browser did not exercise exact base stop/remove, derived deploy/stop/remove and redeployed base deploy/stop/remove", completed)
		}
	}
}

// The actual browser performs every target change. Only initial scoped fixtures
// and physical observations are synthetic; keys never enter browser manifests.
func prepareBrowserAlias(t *testing.T, g preparationScenario, model string) (map[string]any, func(json.RawMessage)) {
	t.Helper()
	ctx := context.Background()
	base := nativeBaseDeployment(t, g)
	s := &distribution.Service{DB: g.f.s.DB, Identity: g.f.s.Identity, Workspaces: g.f.s.Workspaces, Nodes: g.f.s.Nodes, Deployments: g.f.s.Inference}
	gateway, e := s.Provision(ctx, g.f.admin, distribution.ProvisionRequest{WorkspaceID: g.input.WorkspaceID, NodeID: g.node.NodeID, Name: "Browser immutable model target", ClientOrigin: "https://gateway.example.invalid", Reason: "Explicit isolated browser fixture"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	alias, e := s.Publish(ctx, g.f.admin, distribution.PublishRequest{WorkspaceID: g.input.WorkspaceID, GatewayID: gateway.Gateway.ID, Name: "browser-model", DeploymentID: base.ID, DeploymentRevision: base.Revision, Reason: "Actual base files; synthetic physical report"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	key, e := s.MintKey(ctx, g.f.portal, distribution.KeyRequest{WorkspaceID: g.input.WorkspaceID, AliasID: alias.ID, Label: "Preserved browser key", TTLSeconds: 3600, Reason: "Explicit isolated browser key"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	var before []byte
	if e = g.f.s.DB.QueryRow(ctx, "SELECT secret_hash FROM api_keys WHERE id=$1", key.Key.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	return map[string]any{"alias_id": alias.ID, "base_model_id": g.f.input.ModelID, "base_deployment_id": base.ID}, func(raw json.RawMessage) {
		var result struct {
			Artifact           string `json:"artifact_id"`
			AliasID            string `json:"alias_id"`
			AliasRevision      int64  `json:"alias_revision"`
			RollbackDeployment string `json:"rollback_deployment_id"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Artifact != model || result.AliasID != alias.ID || result.AliasRevision != 8 || result.RollbackDeployment == "" {
			t.Fatal("actual browser target history result")
		}
		var after []byte
		var same bool
		if e = g.f.s.DB.QueryRow(ctx, "SELECT secret_hash,revoked_at IS NULL AND alias_id=$2 FROM api_keys WHERE id=$1", key.Key.ID, alias.ID).Scan(&after, &same); e != nil || !same || string(after) != string(before) {
			t.Fatal("browser changed original key", e)
		}
		var total, retarget, rollback int
		if e = g.f.s.DB.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE action='retargeted' AND model_id=$2),count(*) FILTER(WHERE action='rolled_back' AND model_id=$3 AND source_revision=1 AND deployment_id=$4) FROM model_alias_target_versions WHERE alias_id=$1`, alias.ID, model, g.f.input.ModelID, result.RollbackDeployment).Scan(&total, &retarget, &rollback); e != nil {
			t.Fatal(e)
		}
		if total != 8 || retarget != 1 || rollback != 1 {
			t.Fatal("actual browser immutable model target graph", total, retarget, rollback)
		}
		// The independent native gateway profile follows in this same isolated fixture.
		// Retire only the completed browser gateway through its actual operator boundary.
		if _, e = s.RevokeGateway(ctx, g.f.admin, alias.WorkspaceID, gateway.Gateway.ID, "Completed exact browser fixture before independent native gateway profile", uuid.NewString()); e != nil {
			t.Fatal("browser gateway fixture retirement", e)
		}
	}
}
