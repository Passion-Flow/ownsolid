package distribution_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"strings"
	"sync"
	"testing"
	"time"
)

type scenario struct {
	password  string
	s         *distribution.Service
	a, p      identity.Auth
	node      agentwire.Enrolled
	handshake agentwire.Handshake
}

func setup(t *testing.T) scenario {
	t.Helper()
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	a := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	p := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	w := &workspaces.Service{DB: account.DB, Identity: account}
	n := &nodes.Service{DB: account.DB, Identity: account, Workspaces: w}
	m := &models.Service{DB: account.DB, Identity: account, Workspaces: w}
	d := &deployments.Service{Nodes: n, Models: m}
	n.Inference = d
	m.Referenced = d.HasModelReferences
	s := &distribution.Service{DB: account.DB, Identity: account, Workspaces: w, Nodes: n, Deployments: d}
	m.Referenced = s.HasModelReferences
	// Explicit synthetic machine facts exercise business authority, never real NVIDIA qualification.
	h := agentwire.Handshake{ProtocolVersion: agentwire.InferenceVersion, ExecutionProfile: agentwire.DockerInferenceProfile, Capabilities: agentwire.Capabilities(agentwire.DockerInferenceProfile), Facts: agentwire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-gateway-node", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: agentwire.AgentVersion}}
	minted, e := n.MintForProfile(context.Background(), a, a.Me.Workspace.ID, "Synthetic gateway node", agentwire.DockerInferenceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	enrolled, e := n.Enroll(context.Background(), agentwire.Enrollment{JoinToken: minted.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return scenario{password: password, s: s, a: a, p: p, node: enrolled, handshake: h}
}
func (c scenario) input() distribution.ProvisionRequest {
	return distribution.ProvisionRequest{WorkspaceID: c.a.Me.Workspace.ID, NodeID: c.node.NodeID, Name: "Synthetic gateway", ClientOrigin: "https://gateway.example.invalid", Reason: "Synthetic acceptance"}
}
func check(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("wanted %s; observed %v", code, e)
	}
}

func TestGatewayOneTimeCredentialIntentIsolationAndAuditedRevocation(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	key := uuid.NewString()
	first, e := c.s.Provision(ctx, c.a, c.input(), key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if !first.Created || !first.SecretAvailable || first.Secret == nil || !strings.HasPrefix(*first.Secret, "gw1."+first.Gateway.ID+".") {
		t.Fatal("one-time credential missing")
	}
	again, e := c.s.Provision(ctx, c.a, c.input(), key, uuid.NewString())
	if e != nil || again.Secret != nil || again.SecretAvailable || again.Created || again.Gateway.ID != first.Gateway.ID {
		t.Fatal("intent replay disclosed or replaced a credential", e)
	}
	changed := c.input()
	changed.Name = "Changed gateway"
	_, e = c.s.Provision(ctx, c.a, changed, key, uuid.NewString())
	check(t, e, "IDEMPOTENCY_CONFLICT")
	_, e = c.s.Provision(ctx, c.p, c.input(), uuid.NewString(), uuid.NewString())
	check(t, e, "ACCESS_DENIED")
	foreign, e := c.s.Workspaces.Create(ctx, c.a, "Other synthetic Workspace", c.a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	wrong := c.input()
	wrong.WorkspaceID = foreign.ID
	_, e = c.s.Provision(ctx, c.a, wrong, uuid.NewString(), uuid.NewString())
	check(t, e, "RESOURCE_NOT_FOUND")
	var persisted string
	if e = c.s.DB.QueryRow(ctx, "SELECT row_to_json(g)::text FROM gateway_instances g WHERE id=$1", first.Gateway.ID).Scan(&persisted); e != nil {
		t.Fatal(e)
	}
	secret := strings.Split(*first.Secret, ".")[2]
	if strings.Contains(persisted, secret) || strings.Contains(persisted, *first.Secret) {
		t.Fatal("raw credential persisted")
	}
	listed, e := c.s.Gateways(ctx, c.a, c.a.Me.Workspace.ID)
	if e != nil || len(listed) != 1 || listed[0].ID != first.Gateway.ID {
		t.Fatal("scoped persisted inventory", e)
	}
	revoked, e := c.s.RevokeGateway(ctx, c.a, c.a.Me.Workspace.ID, first.Gateway.ID, "Synthetic revoke", uuid.NewString())
	if e != nil || revoked.State != "revoked" {
		t.Fatal("revocation", e)
	}
	var audits int
	if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action IN ('gateway.instance_created','gateway.instance_revoked')", first.Gateway.ID).Scan(&audits); e != nil || audits != 2 {
		t.Fatal("atomic audit", e, audits)
	}
}

func TestGatewayConcurrentProvisionCannotCreateTwoActiveInstances(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, e := c.s.Provision(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
			results <- e
		}()
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for e := range results {
		if e == nil {
			successes++
		} else if e.Error() == "GATEWAY_INSTANCE_EXISTS" {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("instance exclusivity", successes, conflicts)
	}
}

func TestGatewayAuditFailureRollsBackAuthorityAndNeverReturnsSecret(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	_, e := c.s.DB.Exec(ctx, `CREATE FUNCTION synthetic_gateway_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='gateway.instance_created' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER synthetic_gateway_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION synthetic_gateway_audit_failure();`)
	if e != nil {
		t.Fatal(e)
	}
	out, e := c.s.Provision(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
	if e == nil || out.Secret != nil || out.Gateway.ID != "" {
		t.Fatal("rolled-back authority or secret returned")
	}
	var count int
	if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_instances").Scan(&count); e != nil || count != 0 {
		t.Fatal("failed audit left authority", e)
	}
}

func (c scenario) readyDeployment(t *testing.T) deployments.Deployment {
	t.Helper()
	ctx := context.Background()
	model := uuid.NewString()
	manifest, _ := json.Marshal(agentwire.ApprovedModel())
	if _, e := c.s.DB.Exec(ctx, "INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic catalog metadata',$4,$5,$6,'ready',$7)", model, c.a.Me.Workspace.TenantID, c.a.Me.Workspace.ID, agentwire.ApprovedModel().ModelKey, agentwire.ModelCatalogDigest(), manifest, c.a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	const gpu = "GPU-12345678-1234-1234-1234-123456789abc"
	h := c.handshake
	h.Inference = &agentwire.InferenceObservation{GPUs: []agentwire.NVIDIAGPU{{UUID: gpu, Name: "Synthetic GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, ObservedAt: time.Now().UTC()}
	if _, e := c.s.Nodes.Heartbeat(ctx, c.node.NodeID, c.node.Credential, h); e != nil {
		t.Fatal(e)
	}
	deployment, e := c.s.Deployments.Create(ctx, c.a, deployments.CreateRequest{NodeID: c.node.NodeID, ModelID: model, GPUUUID: gpu, Reason: "Synthetic inference intent"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := c.s.Nodes.Claim(ctx, c.node.NodeID, c.node.Credential, uuid.NewString())
	if e != nil || claimed.Lease == nil || claimed.Lease.Inference == nil {
		t.Fatal("typed lease", e)
	}
	lease := claimed.Lease
	observation := &agentwire.DeploymentObservation{DeploymentID: deployment.ID, ModelID: model, CatalogDigest: agentwire.ModelCatalogDigest(), GPUUUID: gpu, Revision: deployment.Revision, State: "running", Managed: true, Ready: true, ContainerID: strings.Repeat("a", 64), ImageID: agentwire.ApprovedInferenceRecipe().ImageConfigDigest, Verification: &agentwire.GenerationVerification{Model: agentwire.InferenceModelName, PromptSHA256: agentwire.InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 10, OutputTokens: 1, ObservedAt: time.Now().UTC()}}
	result := agentwire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Inference: &agentwire.InferenceObservation{GPUs: []agentwire.NVIDIAGPU{}, Deployment: observation, ObservedAt: time.Now().UTC()}}
	if state, e := c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, result, uuid.NewString()); e != nil || state != "succeeded" {
		t.Fatal("synthetic machine completion", e)
	}
	return deployment
}

func TestAliasRequiresReadyScopedRevisionPersistsHistoryAndDisables(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	deployment := c.readyDeployment(t)
	gateway, e := c.s.Provision(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	input := distribution.PublishRequest{WorkspaceID: c.a.Me.Workspace.ID, GatewayID: gateway.Gateway.ID, Name: "public-alias", DeploymentID: deployment.ID, DeploymentRevision: deployment.Revision, Reason: "Synthetic publication"}
	key := uuid.NewString()
	alias, e := c.s.Publish(ctx, c.a, input, key, uuid.NewString())
	if e != nil || !alias.Available {
		t.Fatal("actual persisted publication", e)
	}
	again, e := c.s.Publish(ctx, c.a, input, key, uuid.NewString())
	if e != nil || again.ID != alias.ID {
		t.Fatal("publish replay", e)
	}
	_, e = c.s.Publish(ctx, c.p, input, uuid.NewString(), uuid.NewString())
	check(t, e, "ACCESS_DENIED")
	stale := input
	stale.Name = "stale-alias"
	stale.DeploymentRevision++
	_, e = c.s.Publish(ctx, c.a, stale, uuid.NewString(), uuid.NewString())
	check(t, e, "MODEL_ALIAS_UNAVAILABLE")
	foreign := input
	foreign.Name = "foreign-alias"
	foreign.DeploymentID = uuid.NewString()
	_, e = c.s.Publish(ctx, c.a, foreign, uuid.NewString(), uuid.NewString())
	check(t, e, "RESOURCE_NOT_FOUND")
	listed, e := c.s.Aliases(ctx, c.p, c.a.Me.Workspace.ID)
	if e != nil || len(listed) != 1 || !listed[0].Available {
		t.Fatal("portal publication inventory", e)
	}
	disabled, e := c.s.DisableAlias(ctx, c.a, c.a.Me.Workspace.ID, alias.ID, alias.Revision, "Synthetic disable", uuid.NewString())
	if e != nil || disabled.Available || disabled.DisabledAt == nil || disabled.Revision != 2 {
		t.Fatal("disable", e)
	}
	_, e = c.s.DisableAlias(ctx, c.a, c.a.Me.Workspace.ID, alias.ID, 1, "Synthetic stale disable", uuid.NewString())
	check(t, e, "VERSION_CONFLICT")
	listed, e = c.s.Aliases(ctx, c.p, c.a.Me.Workspace.ID)
	if e != nil || len(listed) != 0 {
		t.Fatal("disabled alias remained visible", e)
	}
	var history, audits int
	if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM model_alias_history WHERE alias_id=$1", alias.ID).Scan(&history); e != nil || history != 2 {
		t.Fatal("immutable publication history", e)
	}
	if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action IN ('model.alias_published','model.alias_disabled')", alias.ID).Scan(&audits); e != nil || audits != 2 {
		t.Fatal("atomic publication audit", e)
	}
}
