//go:build repository_integration && registry_action_integration

package imageregistry

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// Actual SQL/session/signed synthetic License/enrollment/task/owner callbacks.
// GPU/model/image inventory is explicitly synthetic fixture metadata. No image
// proof is accepted, no remote Registry/Docker is constructed and no GPU runs.
type registryActionStory struct {
	f               repositoryfixture.Fixture
	ctx             context.Context
	a               identity.Auth
	s               *Service
	d               *deployments.Service
	node            wire.Enrolled
	pin, model, gpu string
	deployment      deployments.Deployment
	handshake       wire.Handshake
}

func registryActionSetup(t *testing.T, pinned bool) registryActionStory {
	t.Helper()
	f := repositoryfixture.New(t)
	if f.PostgreSQL == nil {
		t.Fatal("REGISTRY_ACTION_ACTUAL_POSTGRESQL_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	i := &identity.Service{DB: f.PostgreSQL, HMACKey: make([]byte, 32)}
	if _, e := rand.Read(i.HMACKey); e != nil {
		t.Fatal(e)
	}
	i.Config.Server.AdminOrigin = "https://admin.example.invalid"
	i.Config.Server.PortalOrigin = "https://portal.example.invalid"
	w := &workspaces.Service{DB: f.PostgreSQL, Identity: i}
	n := &nodes.Service{DB: f.PostgreSQL, Identity: i, Workspaces: w}
	m := &models.Service{DB: f.PostgreSQL, Identity: i, Workspaces: w, RootDirectory: t.TempDir()}
	d := &deployments.Service{Nodes: n, Models: m}
	n.Inference = d
	g := registryActionStory{f: f, ctx: ctx, d: d, pin: uuid.NewString(), model: uuid.NewString(), gpu: "GPU-" + uuid.NewString()}
	g.exec(t, `INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')`, f.Tenant, f.Workspace, f.User)
	g.s = &Service{identity: i, workspaces: w, nodes: n, deployments: d, config: Defaults()}
	g.a = g.session(t, "admin")
	private, trust, e := licensewire.GenerateKey("explicit-registry-action-synthetic-license")
	if e != nil {
		t.Fatal(e)
	}
	lic, e := licensing.New(i, trust, licensewire.DefaultPolicy(), func() time.Time { return time.Now().UTC() })
	if e != nil {
		t.Fatal(e)
	}
	i.BusinessAdmission = lic
	g.s.license = lic
	status, e := lic.StatusAdmin(ctx, g.a)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Explicit synthetic Registry SQL action fixture", DeploymentID: status.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), GraceUntil: now.Add(25 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	raw, e := licensewire.Issue(claims, private)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = lic.Import(ctx, g.a, licensing.ImportInput{Artifact: string(raw), Reason: "Signed synthetic Registry action License"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	h := wire.Handshake{ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile, Capabilities: wire.Capabilities(wire.DockerInferenceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "explicit-synthetic-registry-actions", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: now, AgentVersion: wire.AgentVersion}}
	join, e := n.MintForProfile(ctx, g.a, f.Workspace, "Synthetic Registry action node", h.Profile(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.node, e = n.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	h.Inference = &wire.InferenceObservation{RegistryImageDelivery: true, GPUs: []wire.NVIDIAGPU{{UUID: g.gpu, Name: "Explicit synthetic GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: now}}, ObservedAt: now}
	if pinned {
		h.Inference.ProbeErrorCode = wire.InferenceImageMissing
	}
	if _, e = n.Heartbeat(ctx, g.node.NodeID, g.node.Credential, h); e != nil {
		t.Fatal(e)
	}
	g.handshake = h
	manifest, _ := json.Marshal(wire.ApprovedModel())
	g.exec(t, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Explicit synthetic approved metadata',$4,$5,$6,'ready',$7)`, g.model, f.Tenant, f.Workspace, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, f.User)
	d.RegistryActions = g.s // Actual authority owner, with no transport in this fixture.
	input := deployments.CreateRequest{NodeID: g.node.NodeID, ModelID: g.model, GPUUUID: g.gpu, Reason: "Actual SQL intent with synthetic target metadata"}
	if pinned {
		recipe := wire.ApprovedInferenceRecipe()
		g.exec(t, `INSERT INTO image_registry_pins(id,tenant_id,workspace_id,asset_id,source_archive_sha256,source_manifest_sha256,backend_identity,state,manifest_digest,config_digest,archive_sha256,archive_bytes,inventory,inventory_sha256,created_by,actor_epoch) VALUES($1,$2,$3,'explicit-synthetic-identity',$4,$5,$6,'ready',$7,$8,$9,1,'{}'::jsonb,$10,$11,1)`, g.pin, f.Tenant, f.Workspace, strings.Repeat("1", 64), strings.Repeat("2", 64), "sha256:"+strings.Repeat("3", 64), recipe.ImageManifestDigest, recipe.ImageConfigDigest, strings.Repeat("4", 64), strings.Repeat("5", 64), f.User)
		g.deployment, e = d.CreateRegistryPinned(ctx, g.a, input, uuid.NewString(), uuid.NewString(), g.pin, lic.CheckWithin, func(ctx context.Context, q transaction.Executor, n nodes.Node, task nodes.Task, c wire.InferenceCommand, replay bool) error {
			return g.s.bindDeployment(ctx, q, g.a, g.pin, n, task, c, replay)
		})
	} else {
		g.deployment, e = d.Create(ctx, g.a, input, uuid.NewString(), uuid.NewString())
	}
	if e != nil {
		t.Fatal("actual deployment fixture creation", e)
	}
	g.complete(t, "running")
	return g
}

func (g registryActionStory) exec(t *testing.T, text string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, transaction.SQL(transaction.PostgreSQL, text, args...)); e != nil {
		t.Fatal("owned fixture mutation", e)
	}
}
func (g registryActionStory) session(t *testing.T, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = g.s.identity.IssueSessionWithin(ctx, q, g.f.User, 1, audience, "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := g.s.identity.Authenticate(g.ctx, issued.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (g *registryActionStory) complete(t *testing.T, state string) {
	t.Helper()
	claimed, e := g.s.nodes.Claim(g.ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	if e != nil || claimed.Lease == nil || claimed.Lease.Inference == nil {
		t.Fatal("actual synthetic-target claim", e)
	}
	l := claimed.Lease
	c := l.Inference
	o := &wire.DeploymentObservation{DeploymentID: c.DeploymentID, ModelID: c.ModelID, CatalogDigest: c.CatalogDigest, GPUUUID: c.GPUUUID, Revision: c.Revision, State: state, Managed: true, ContainerID: strings.Repeat("a", 64), ImageID: wire.ApprovedInferenceRecipe().ImageConfigDigest}
	if state == "running" {
		o.Ready = true
		o.Verification = &wire.GenerationVerification{Model: wire.InferenceModelName, PromptSHA256: wire.InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 10, OutputTokens: 1, ObservedAt: time.Now().UTC()}
	}
	observation := *g.handshake.Inference
	observation.ProbeErrorCode = ""
	observation.Deployment = o
	observation.ObservedAt = time.Now().UTC()
	for index := range observation.GPUs {
		observation.GPUs[index].ObservedAt = observation.ObservedAt
	}
	result := wire.Result{LeaseToken: l.LeaseToken, Outcome: "success", Inference: &observation}
	if state, e := g.s.nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, l.TaskID, result, uuid.NewString()); e != nil || state != "succeeded" {
		t.Fatal("actual synthetic-target completion", e, state)
	}
	g.deployment, e = g.d.Get(g.ctx, g.a, "", g.deployment.ID)
	if e != nil {
		t.Fatal(e)
	}
}
func (g registryActionStory) snapshot(t *testing.T) string {
	t.Helper()
	counts := map[string]int{}
	for _, table := range []string{"deployments", "node_tasks", "gpu_leases", "image_registry_grants", "audit_events"} {
		var count int
		if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, "SELECT count(*) FROM "+table+" WHERE tenant_id=$1", g.f.Tenant)).Scan(&count); e != nil {
			t.Fatal(e)
		}
		counts[table] = count
	}
	var revision, gpuRevision int64
	var last, state string
	var live int
	if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT d.revision,d.last_task_id::text,g.revision,g.state FROM deployments d JOIN gpu_leases g ON g.deployment_id=d.id WHERE d.id=$1`, g.deployment.ID)).Scan(&revision, &last, &gpuRevision, &state); e != nil {
		t.Fatal(e)
	}
	if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT count(*) FROM image_registry_grants WHERE deployment_id=$1 AND revoked_at IS NULL`, g.deployment.ID)).Scan(&live); e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal([]any{counts, revision, last, gpuRevision, state, live})
	return string(b)
}
func (g registryActionStory) action(action string, key string) (deployments.Deployment, error) {
	return g.d.Action(g.ctx, g.a, g.deployment.ID, deployments.ActionRequest{Action: action, Revision: g.deployment.Revision, Reason: "Actual Registry action transaction"}, key, uuid.NewString())
}

type registryActionFailureAfterBind struct{ *Service }

func (s registryActionFailureAfterBind) BindActionWithin(ctx context.Context, q transaction.Executor, a identity.Auth, n nodes.Node, task nodes.Task, c wire.InferenceCommand, replay bool, reason, request string) error {
	if e := s.Service.BindActionWithin(ctx, q, a, n, task, c, replay, reason, request); e != nil {
		return e
	}
	return failure("REGISTRY_RECIPE_MISMATCH")
}

func TestActualRegistryDeploymentActions(t *testing.T) {
	for _, name := range []string{"ordinary_unmarked", "retry_pin_and_current_actor", "concurrent_same_key", "replay_no_effects", "replay_changed_session", "replay_missing_grant", "replay_expired_grant", "foreign_workspace_refusal", "rollback_after_bound_effects", "released_reference", "missing_reference", "portal_refusal", "stop_then_retry", "physical_recovery_refusal", "disabled_owner_state"} {
		t.Run(name, func(t *testing.T) {
			g := registryActionSetup(t, name != "ordinary_unmarked")
			key := uuid.NewString()
			switch name {
			case "replay_expired_grant":
				g.s.config.GrantSeconds = 1
			case "foreign_workspace_refusal":
				n, readErr := g.s.nodes.Get(g.ctx, g.a, "", g.node.NodeID)
				if readErr != nil {
					t.Fatal(readErr)
				}
				n.WorkspaceID = uuid.NewString()
				before := g.snapshot(t)
				readErr = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					_, e := g.s.PrepareActionWithin(ctx, q, g.a, n, g.deployment, wire.InferenceCommand{Action: "deploy", DeploymentID: g.deployment.ID, Revision: g.deployment.Revision + 1})
					return e
				})
				if readErr == nil || readErr.Error() != "REGISTRY_OWNER_REQUIRED" || g.snapshot(t) != before {
					t.Fatal("foreign workspace callback gained effects", readErr)
				}
				return
			case "released_reference":
				g.exec(t, `UPDATE image_registry_deployment_refs SET released_at=clock_timestamp() WHERE deployment_id=$1`, g.deployment.ID)
			case "missing_reference":
				g.exec(t, `DELETE FROM image_registry_grants WHERE deployment_id=$1`, g.deployment.ID)
				g.exec(t, `DELETE FROM image_registry_deployment_refs WHERE deployment_id=$1`, g.deployment.ID)
			case "portal_refusal":
				g.a = g.session(t, "portal")
			case "rollback_after_bound_effects":
				g.d.RegistryActions = registryActionFailureAfterBind{g.s}
			case "physical_recovery_refusal":
				g.exec(t, `UPDATE gpu_leases SET state='recovery_required' WHERE deployment_id=$1`, g.deployment.ID)
			case "disabled_owner_state":
				if e := RequireEmptyWhenDisabled(g.ctx, g.f.Store); e == nil || e.Error() != "REGISTRY_OWNER_REQUIRED" {
					t.Fatal("populated owner disabled", e)
				}
				g.d.RegistryActions = nil
			case "stop_then_retry":
				stopped, e := g.action("stop", key)
				if e != nil {
					t.Fatal(e)
				}
				g.deployment = stopped
				var live int
				var marker bool
				if e = g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT count(*) FROM image_registry_grants WHERE deployment_id=$1 AND revoked_at IS NULL`, g.deployment.ID)).Scan(&live); e != nil || live != 0 {
					t.Fatal("stop retained grant", e, live)
				}
				if e = g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT payload ? 'image_registry_pin_id' FROM node_tasks WHERE id=$1`, *stopped.LastTaskID)).Scan(&marker); e != nil || marker {
					t.Fatal("stop gained load intent", e)
				}
				g.complete(t, "stopped")
				key = uuid.NewString()
			}
			before := g.snapshot(t)
			if name == "concurrent_same_key" {
				var tasks, grants int
				if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT (SELECT count(*) FROM node_tasks),(SELECT count(*) FROM image_registry_grants)`)).Scan(&tasks, &grants); e != nil {
					t.Fatal(e)
				}
				type result struct {
					deployment deployments.Deployment
					err        error
				}
				results := make(chan result, 2)
				for i := 0; i < 2; i++ {
					go func() { d, e := g.action("deploy", key); results <- result{d, e} }()
				}
				first, second := <-results, <-results
				if first.err != nil || second.err != nil || first.deployment.LastTaskID == nil || second.deployment.LastTaskID == nil || *first.deployment.LastTaskID != *second.deployment.LastTaskID || first.deployment.Revision != second.deployment.Revision {
					t.Fatal("concurrent idempotency diverged", first.err, second.err)
				}
				var afterTasks, afterGrants int
				if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT (SELECT count(*) FROM node_tasks),(SELECT count(*) FROM image_registry_grants)`)).Scan(&afterTasks, &afterGrants); e != nil || afterTasks != tasks+1 || afterGrants != grants+1 {
					t.Fatal("concurrent action duplicated effects", e)
				}
			}
			out, e := g.action("deploy", key)
			refusals := map[string]string{"released_reference": "REGISTRY_REFERENCE_RELEASED", "missing_reference": "REGISTRY_REFERENCE_MISSING", "portal_refusal": "ACCESS_DENIED", "rollback_after_bound_effects": "REGISTRY_RECIPE_MISMATCH", "physical_recovery_refusal": "INFERENCE_RECOVERY_REQUIRED", "disabled_owner_state": "REGISTRY_OWNER_REQUIRED"}
			if want, refusal := refusals[name]; refusal {
				if e == nil || e.Error() != want || g.snapshot(t) != before {
					t.Fatal("refusal/rollback effects changed", e, want)
				}
				return
			}
			if e != nil || out.LastTaskID == nil {
				t.Fatal("actual Registry action refused", e)
			}
			var marker *string
			if e = g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT payload->>'image_registry_pin_id' FROM node_tasks WHERE id=$1`, *out.LastTaskID)).Scan(&marker); e != nil {
				t.Fatal(e)
			}
			if name == "ordinary_unmarked" {
				if marker != nil {
					t.Fatal("ordinary action gained pin")
				}
				if e = RequireEmptyWhenDisabled(g.ctx, g.f.Store); e != nil {
					t.Fatal("empty owner required configuration", e)
				}
				return
			}
			if marker == nil || *marker != g.pin {
				t.Fatal("retry pin lost")
			}
			var correct bool
			if e = g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT revision=$1 AND created_by=$2 AND actor_epoch=$3 AND created_session_id=$4 AND created_token_hash=$5 FROM image_registry_grants WHERE deployment_id=$6 AND task_id=$7 AND revoked_at IS NULL`, out.Revision, g.a.Me.User.ID, g.a.Epoch, g.a.SessionID, g.a.TokenHash, g.deployment.ID, *out.LastTaskID)).Scan(&correct); e != nil || !correct {
				t.Fatal("new task actor binding lost", e)
			}
			var live int
			if e = g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT count(*) FROM image_registry_grants WHERE deployment_id=$1 AND revoked_at IS NULL`, g.deployment.ID)).Scan(&live); e != nil || live != 1 {
				t.Fatal("superseded grant retained authority", e, live)
			}
			var expiry time.Time
			if e = g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT expires_at FROM image_registry_grants WHERE task_id=$1 AND revoked_at IS NULL`, *out.LastTaskID)).Scan(&expiry); e != nil {
				t.Fatal(e)
			}
			if name == "replay_no_effects" || name == "replay_changed_session" || name == "replay_missing_grant" || name == "replay_expired_grant" {
				if name == "replay_changed_session" {
					g.a = g.session(t, "admin")
				}
				if name == "replay_missing_grant" {
					g.exec(t, `DELETE FROM image_registry_grants WHERE task_id=$1`, *out.LastTaskID)
				}
				if name == "replay_expired_grant" {
					select {
					case <-g.ctx.Done():
						t.Fatal("fixture deadline before bounded expiry")
					case <-time.After(1100 * time.Millisecond):
					}
				}
				before = g.snapshot(t)
				replay, repeatErr := g.action("deploy", key)
				if name == "replay_missing_grant" {
					if repeatErr == nil || repeatErr.Error() != "REGISTRY_REFERENCE_MISSING" {
						t.Fatal("missing grant repaired", repeatErr)
					}
				} else if name == "replay_expired_grant" {
					if repeatErr == nil || repeatErr.Error() != "REGISTRY_GRANT_REVOKED" {
						t.Fatal("expired grant renewed by replay", repeatErr)
					}
				} else if name == "replay_changed_session" {
					if repeatErr == nil || repeatErr.Error() != "REGISTRY_GRANT_REVOKED" {
						t.Fatal("new session repaired old grant", repeatErr)
					}
				} else if repeatErr != nil || replay.LastTaskID == nil || *replay.LastTaskID != *out.LastTaskID {
					t.Fatal("idempotent action changed", repeatErr)
				}
				if name != "replay_missing_grant" {
					var afterExpiry time.Time
					if e = g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.PostgreSQL, `SELECT expires_at FROM image_registry_grants WHERE task_id=$1 AND revoked_at IS NULL`, *out.LastTaskID)).Scan(&afterExpiry); e != nil || !afterExpiry.Equal(expiry) {
						t.Fatal("replay extended grant", e)
					}
				}
				if g.snapshot(t) != before {
					t.Fatal("replay created effects")
				}
			}
		})
	}
}
