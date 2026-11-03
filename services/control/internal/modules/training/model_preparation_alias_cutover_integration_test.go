//go:build training_artifact_integration && model_lifecycle_admission_integration && model_lifecycle_output_acceptance_integration

package training_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	gatewaywire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// Model files, current authority, PostgreSQL and immutable alias/key history are real.
// Every physical container/GPU/generation observation below is an explicit test double.
type nativeAliasCutover struct {
	service            *distribution.Service
	alias              distribution.Alias
	gateway            distribution.Provisioned
	key                distribution.MintedKey
	originalRequest    string
	originalDeployment string
	live               func(*testing.T, string)
}

var nativeAliasGatewaySetup func(*testing.T, preparationScenario, *distribution.Service) func(*nativeAliasCutover) func(*testing.T, string)

func nativeInferenceResult(t *testing.T, g preparationScenario, d deployments.Deployment, state string) {
	t.Helper()
	ctx := context.Background()
	claimed, e := g.f.s.Nodes.Claim(ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	if e != nil || claimed.Lease == nil || claimed.Lease.Inference == nil || claimed.Lease.Inference.DeploymentID != d.ID || claimed.Lease.Inference.Revision != d.Revision {
		t.Fatal("explicit physical test-double exact command", e)
	}
	c := claimed.Lease.Inference
	o := agentwire.DeploymentObservation{DeploymentID: d.ID, ModelID: c.ModelID, CatalogDigest: c.CatalogDigest, GPUUUID: c.GPUUUID, Revision: c.Revision, State: state}
	if c.Derived != nil {
		o.ModelKind = agentwire.DerivedModelKind
		o.ServingRecipeSHA256 = c.Derived.ServingRecipeSHA256
	}
	if state != "not_installed" {
		o.Managed = true
		o.ContainerID = strings.Repeat("a", 64)
		o.ImageID = agentwire.ApprovedInferenceRecipe().ImageConfigDigest
	}
	if state == "running" {
		o.Ready = true
		o.Verification = &agentwire.GenerationVerification{Model: agentwire.InferenceModelName, PromptSHA256: agentwire.InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 12, OutputTokens: 1, ObservedAt: time.Now().UTC()}
	}
	result := agentwire.Result{LeaseToken: claimed.Lease.LeaseToken, Outcome: "success", Inference: &agentwire.InferenceObservation{GPUs: []agentwire.NVIDIAGPU{}, Deployment: &o, ObservedAt: time.Now().UTC()}}
	if _, e = g.f.s.Nodes.Complete(ctx, g.node.NodeID, g.node.Credential, claimed.Lease.TaskID, result, uuid.NewString()); e != nil {
		t.Fatal("explicit positive physical test-double result", e)
	}
}

func nativeBaseDeployment(t *testing.T, g preparationScenario) deployments.Deployment {
	t.Helper()
	preparationHardware(t, g)
	d, e := g.f.s.Inference.Create(context.Background(), g.f.admin, deployments.CreateRequest{NodeID: g.node.NodeID, ModelID: g.f.input.ModelID, GPUUUID: g.input.GPUUUID, Reason: "Actual approved base files with explicit synthetic physical reports"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("actual base replacement", e)
	}
	nativeInferenceResult(t, g, d, "running")
	d, e = g.f.s.Inference.Get(context.Background(), g.f.admin, g.f.input.WorkspaceID, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	return d
}

func nativeRemoveDeployment(t *testing.T, g preparationScenario, d deployments.Deployment) {
	t.Helper()
	for _, action := range []string{"stop", "remove"} {
		next, e := g.f.s.Inference.Action(context.Background(), g.f.admin, d.ID, deployments.ActionRequest{Action: action, Revision: d.Revision, Reason: "Explicit synthetic positive stop before actual next model intent"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal("physical test-double cleanup intent", e)
		}
		d = next
		state := "stopped"
		if action == "remove" {
			state = "not_installed"
		}
		nativeInferenceResult(t, g, d, state)
	}
}

func (a *nativeAliasCutover) change(t *testing.T, g preparationScenario, input distribution.CutoverRequest) {
	t.Helper()
	input.ExpectedRevision = a.alias.Revision
	input.Reason = "Actual accepted base/derived same-key cutover with explicit synthetic physical reports"
	result, e := a.service.Cutover(context.Background(), g.f.admin, a.alias.WorkspaceID, a.alias.ID, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("actual native alias", input.Action, e)
	}
	a.alias = result.Alias
	if a.live != nil && input.Action != "resume" {
		a.live(t, "")
	}
}

func (a *nativeAliasCutover) useOriginalKey(t *testing.T, deployment string) string {
	t.Helper()
	ctx := context.Background()
	if a.live != nil {
		a.live(t, deployment)
	}
	authorized, e := a.service.AuthorizeModels(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, *a.key.Secret)
	if e != nil || len(authorized.Models) != 1 || authorized.Models[0].DeploymentID != deployment || authorized.Models[0].ID != a.alias.ID {
		t.Fatal("same original secret did not bind current exact target", e)
	}
	id := uuid.NewString()
	input := gatewaywire.AdmissionRequest{RequestID: id, APIKey: *a.key.Secret, Model: a.alias.Name, MaximumOutputTokens: 16}
	if _, e = a.service.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, input, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = a.service.Report(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, gatewaywire.Outcome{RequestID: id, Status: "succeeded", Usage: &gatewaywire.Usage{PromptTokens: 12, CompletionTokens: 1, TotalTokens: 13}, ElapsedMilliseconds: 1, OutputStarted: true}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	return id
}

func beginNativeAliasCutover(t *testing.T, g preparationScenario) *nativeAliasCutover {
	t.Helper()
	ctx := context.Background()
	s := &distribution.Service{DB: g.f.s.DB, Identity: g.f.s.Identity, Workspaces: g.f.s.Workspaces, Nodes: g.f.s.Nodes, Deployments: g.f.s.Inference}
	var bind func(*nativeAliasCutover) func(*testing.T, string)
	if nativeAliasGatewaySetup != nil {
		bind = nativeAliasGatewaySetup(t, g, s)
	}
	previous := g.f.s.Models.Referenced
	g.f.s.Models.Referenced = func(ctx context.Context, tx transaction.Executor, tenant, workspace, model string) (bool, error) {
		found, e := s.HasModelReferences(ctx, tx, tenant, workspace, model)
		if found || e != nil || previous == nil {
			return found, e
		}
		return previous(ctx, tx, tenant, workspace, model)
	}
	base := nativeBaseDeployment(t, g)
	gateway, e := s.Provision(ctx, g.f.admin, distribution.ProvisionRequest{WorkspaceID: g.f.input.WorkspaceID, NodeID: g.node.NodeID, Name: "Actual native lifecycle alias", ClientOrigin: "https://gateway.example.invalid", Reason: "Isolated native actual model cutover"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	alias, e := s.Publish(ctx, g.f.admin, distribution.PublishRequest{WorkspaceID: g.f.input.WorkspaceID, GatewayID: gateway.Gateway.ID, Name: "native-model", DeploymentID: base.ID, DeploymentRevision: base.Revision, Reason: "Actual base-file deployment publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	key, e := s.MintKey(ctx, g.f.portal, distribution.KeyRequest{WorkspaceID: alias.WorkspaceID, AliasID: alias.ID, Label: "Native same-key lifecycle", TTLSeconds: 3600, Reason: "Synthetic isolated acceptance only"}, uuid.NewString(), uuid.NewString())
	if e != nil || key.Secret == nil {
		t.Fatal(e)
	}
	a := &nativeAliasCutover{service: s, alias: alias, gateway: gateway, key: key, originalDeployment: base.ID}
	if bind != nil {
		a.live = bind(a)
	}
	a.originalRequest = a.useOriginalKey(t, base.ID)
	a.change(t, g, distribution.CutoverRequest{Action: "pause"})
	nativeRemoveDeployment(t, g, base)
	return a
}

func (a *nativeAliasCutover) acceptDerived(t *testing.T, g preparationScenario, derived deployments.Deployment, catalog wire.DerivedCatalog) {
	t.Helper()
	if catalog.ModelID == catalog.BaseModelID || derived.ModelID != catalog.ModelID || catalog.BaseModelID != g.f.input.ModelID {
		t.Fatal("actual distinct derivative identity")
	}
	a.change(t, g, distribution.CutoverRequest{Action: "retarget", DeploymentID: &derived.ID, DeploymentRevision: &derived.Revision})
	a.change(t, g, distribution.CutoverRequest{Action: "resume"})
	a.useOriginalKey(t, derived.ID)
	a.change(t, g, distribution.CutoverRequest{Action: "pause"})
	source := int64(1)
	_, e := a.service.Cutover(context.Background(), g.f.admin, a.alias.WorkspaceID, a.alias.ID, distribution.CutoverRequest{Action: "rollback", ExpectedRevision: a.alias.Revision, DeploymentID: &derived.ID, DeploymentRevision: &derived.Revision, SourceRevision: &source, Reason: "Must refuse a real ready derivative for the recorded base identity"}, uuid.NewString(), uuid.NewString())
	if e == nil || e.Error() != "ALIAS_ROLLBACK_TARGET_INVALID" {
		t.Fatal("wrong recorded model accepted", e)
	}
	var retained string
	if e = g.f.s.DB.QueryRow(context.Background(), "SELECT deployment_id::text FROM gateway_requests WHERE id=$1", a.originalRequest).Scan(&retained); e != nil || retained != a.originalDeployment {
		t.Fatal("original admitted target changed", e)
	}
}

func (a *nativeAliasCutover) restoreBase(t *testing.T, g preparationScenario, catalog wire.DerivedCatalog) {
	t.Helper()
	ctx := context.Background()
	base := nativeBaseDeployment(t, g)
	source := int64(1)
	a.change(t, g, distribution.CutoverRequest{Action: "rollback", DeploymentID: &base.ID, DeploymentRevision: &base.Revision, SourceRevision: &source})
	a.change(t, g, distribution.CutoverRequest{Action: "resume"})
	a.useOriginalKey(t, base.ID)
	history, e := a.service.TargetHistory(ctx, g.f.admin, a.alias.WorkspaceID, a.alias.ID, url.Values{"limit": {"100"}})
	if e != nil || len(history.Items) != 7 {
		t.Fatal("native alias target history", e, len(history.Items))
	}
	foundDerived, foundRollback := false, false
	for _, record := range history.Items {
		if record.ModelID == catalog.ModelID {
			if record.ModelKind != agentwire.DerivedModelKind || record.ServingRecipeSHA256 == nil || *record.ServingRecipeSHA256 != agentwire.DerivedServingRecipeDigest() {
				t.Fatal("derived immutable history omitted BF16 recipe")
			}
			foundDerived = true
		}
		if record.Action == "rolled_back" {
			if record.ModelID != catalog.BaseModelID || record.SourceRevision == nil || *record.SourceRevision != 1 {
				t.Fatal("selected base history not retained")
			}
			foundRollback = true
		}
	}
	if !foundDerived || !foundRollback || count(t, g.f, "SELECT count(*) FROM api_keys WHERE alias_id=$1", a.alias.ID) != 1 {
		t.Fatal("same-key immutable base/derived history missing")
	}
	a.change(t, g, distribution.CutoverRequest{Action: "pause"})
	nativeRemoveDeployment(t, g, base)
	t.Log("Real complete approved base and distinct BF16 derivative files, owning deployment validation and actual PostgreSQL same-original-key admission/outcomes, paused same-node retarget, wrong recorded-model rollback refusal, fresh base redeployment/selected rollback and immutable history. Physical reports are explicit test doubles; no GPU/upstream generation qualification.")
}
