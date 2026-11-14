//go:build training_artifact_integration && model_lifecycle_admission_integration && model_lifecycle_output_acceptance_integration

package training_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func actualDerivedDeployment(t *testing.T, g preparationScenario, catalog wire.DerivedCatalog) {
	t.Helper()
	ctx := context.Background()
	service := g.f.s.Inference
	input := deployments.CreateRequest{NodeID: g.node.NodeID, ModelID: catalog.ModelID, GPUUUID: g.input.GPUUUID, Reason: "Actual native accepted files with explicitly synthetic hardware and readiness"}
	old := input
	old.NodeID = g.f.input.NodeID
	if _, e := service.Create(ctx, g.f.admin, old, uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("old AI profile accepted derived deployment")
	}
	preparationHardware(t, g)
	alias := beginNativeAliasCutover(t, g)
	preparationHardware(t, g)
	key := uuid.NewString()
	d, e := service.Create(ctx, g.f.admin, input, key, uuid.NewString())
	if e != nil {
		t.Fatal("actual derived owning deployment", e)
	}
	if d.ModelKind != agentwire.DerivedModelKind || d.CatalogDigest == agentwire.ModelCatalogDigest() || d.ServingRecipeSHA256 == nil || *d.ServingRecipeSHA256 != agentwire.DerivedServingRecipeDigest() || d.ResourceState != "held" {
		t.Fatal("derived metadata/resource variant not persisted")
	}
	again, e := service.Create(ctx, g.f.admin, input, key, uuid.NewString())
	if e != nil || again.ID != d.ID {
		t.Fatal("derived intent replay", e)
	}
	changed := input
	changed.Reason = "Changed exact intent"
	if _, e = service.Create(ctx, g.f.admin, changed, key, uuid.NewString()); e == nil {
		t.Fatal("changed key body replayed")
	}
	if _, e = g.f.s.DB.Exec(ctx, "UPDATE deployments SET catalog_digest=$2 WHERE id=$1", d.ID, strings.Repeat("c", 64)); e == nil {
		t.Fatal("owning deployment identity changed")
	}
	claim, e := g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.Operation != agentwire.DerivedInferenceOperation || claim.Lease.Inference == nil {
		t.Fatal("actual derived command lease", e)
	}
	// Physical observation and generation proof are explicit test doubles. No vLLM process runs.
	observation := agentwire.InferenceObservation{GPUs: []agentwire.NVIDIAGPU{}, ObservedAt: time.Now().UTC()}
	observation.Deployment = &agentwire.DeploymentObservation{ModelKind: agentwire.DerivedModelKind, ServingRecipeSHA256: agentwire.DerivedServingRecipeDigest(), DeploymentID: d.ID, ModelID: catalog.ModelID, CatalogDigest: d.CatalogDigest, GPUUUID: d.GPUUUID, Revision: d.Revision, Managed: true, State: "running", Ready: true, ContainerID: strings.Repeat("a", 64), ImageID: agentwire.ApprovedDerivedServingRecipe().ImageConfigDigest, Verification: &agentwire.GenerationVerification{Model: agentwire.InferenceModelName, PromptSHA256: agentwire.InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 12, OutputTokens: 1, ObservedAt: time.Now().UTC()}}
	complete := func(lease agentwire.Lease, value agentwire.InferenceObservation) {
		t.Helper()
		if _, e := g.f.s.Nodes.Complete(ctx, g.node.NodeID, g.node.Credential, lease.TaskID, agentwire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Inference: &value}, uuid.NewString()); e != nil {
			t.Fatal("derived exact result", e)
		}
	}
	target := func(want bool) {
		t.Helper()
		tx, e := g.f.s.DB.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(ctx)
		value, e := service.ReadyGatewayTarget(ctx, tx, catalog.Scope.TenantID, catalog.Scope.WorkspaceID, g.node.NodeID, d.ID, d.Revision)
		if want && (e != nil || value.ModelID != catalog.ModelID || value.BackendModel != agentwire.InferenceModelName) {
			t.Fatal("derived gateway ready reference", e)
		}
		if !want && e == nil {
			t.Fatal("unconfirmed derived gateway target")
		}
	}
	target(false)
	complete(*claim.Lease, observation)
	d, e = service.Get(ctx, g.f.portal, catalog.Scope.WorkspaceID, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	target(true)
	alias.acceptDerived(t, g, d, catalog)
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/deployments/openapi.json")
	encoded, _ := json.Marshal(d)
	var response any
	json.Unmarshal(encoded, &response)
	if e = schemas["Deployment"].Validate(response); e != nil {
		t.Fatal("derived owning DTO/schema", e)
	}
	for _, action := range []string{"stop", "remove"} {
		next, e := service.Action(ctx, g.f.admin, d.ID, deployments.ActionRequest{Action: action, Revision: d.Revision, Reason: "Actual owning resource transition with synthetic positive physical report"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal("derived action", action, e)
		}
		d = next
		current, e := g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
		if e != nil || current.Lease == nil || current.Lease.Inference == nil || current.Lease.Inference.Action != action || current.Lease.Operation != agentwire.DerivedInferenceOperation {
			t.Fatal("derived action command", e)
		}
		value := observation
		deployment := *observation.Deployment
		value.Deployment = &deployment
		deployment.Revision = d.Revision
		deployment.Ready = false
		deployment.Verification = nil
		deployment.State = "stopped"
		if action == "remove" {
			deployment.State = "not_installed"
			deployment.Managed = false
			deployment.ContainerID = ""
			deployment.ImageID = ""
		}
		complete(*current.Lease, value)
		d, e = service.Get(ctx, g.f.portal, catalog.Scope.WorkspaceID, d.ID)
		if e != nil || d.ResourceState != "released" {
			t.Fatal("derived positive exact resource release", e)
		}
		target(false)
	}
	if d.RemovedAt == nil || count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE deployment_id=$1 AND state<>'released'", d.ID) != 0 {
		t.Fatal("derived remove did not retain metadata/release exact owner")
	}
	alias.restoreBase(t, g, catalog)
	t.Log("Actual real-file-derived owning deployment DB/current Admin/profile/idempotency/immutable catalog+BF16 recipe/exclusive GPU metadata/claim/result/DTO/gateway reference/stop/remove transitions. Every hardware/generation/container/physical-stop observation is explicitly synthetic; no physical vLLM or GPU qualification.")
}
