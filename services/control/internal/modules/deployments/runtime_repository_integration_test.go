//go:build repository_integration

package deployments

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	nodehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

// All GPU/container/model/derived-file catalog observations are declared synthetic
// metadata. Sessions, signed License, enrollment, SQL, owner callbacks and audits
// are actual. These fixtures never accept fake file bytes or claim GPU execution.
type deploymentRuntimeStory struct {
	f             repositoryfixture.Fixture
	ctx           context.Context
	s             *Service
	admin, portal identity.Auth
	node          wire.Enrolled
	handshake     wire.Handshake
	model, gpu    string
}

func deploymentRuntimeSetup(t *testing.T) deploymentRuntimeStory {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	a := &identity.Service{Storage: f.Store, HMACKey: make([]byte, 32)}
	if _, e := rand.Read(a.HMACKey); e != nil {
		t.Fatal(e)
	}
	if f.PostgreSQL != nil {
		a.Storage = nil
		a.DB = f.PostgreSQL
	}
	a.Config.Server.AdminOrigin = "https://admin.example.invalid"
	a.Config.Server.PortalOrigin = "https://portal.example.invalid"
	w := &workspaces.Service{DB: f.PostgreSQL, Identity: a}
	n := &nodes.Service{DB: f.PostgreSQL, Identity: a, Workspaces: w}
	modelRoot := t.TempDir()
	// The model store requires an explicitly private fixture root.
	if e := os.Chmod(modelRoot, 0700); e != nil {
		t.Fatal("private model fixture root", e)
	}
	info, e := os.Lstat(modelRoot)
	if e != nil || !info.IsDir() {
		t.Fatal("actual model fixture root", e)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		t.Fatal("actual model fixture owner")
	}
	t.Logf("M29_MODEL_ROOT path=%q mode=%04o uid=%d expected_uid=%d device=%d", modelRoot, info.Mode().Perm(), owner.Uid, os.Geteuid(), owner.Dev)
	m := &models.Service{DB: f.PostgreSQL, Identity: a, Workspaces: w, RootDirectory: modelRoot}
	s := &Service{Nodes: n, Models: m}
	n.Inference = s
	g := deploymentRuntimeStory{f: f, ctx: ctx, s: s, model: uuid.NewString(), gpu: "GPU-" + uuid.NewString()}
	g.exec(t, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,'owner')", f.Tenant, f.Workspace, f.User)
	g.admin = g.session(t, "admin")
	g.portal = g.session(t, "portal")
	private, trust, e := licensewire.GenerateKey("synthetic-deployment-runtime-root")
	if e != nil {
		t.Fatal(e)
	}
	lic, e := licensing.New(a, trust, licensewire.DefaultPolicy(), func() time.Time { return time.Now().UTC() })
	if e != nil {
		t.Fatal(e)
	}
	a.BusinessAdmission = lic
	state, e := lic.StatusAdmin(ctx, g.admin)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Explicit synthetic deployment SQL fixture", DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), GraceUntil: now.Add(25 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	raw, e := licensewire.Issue(claims, private)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = lic.Import(ctx, g.admin, licensing.ImportInput{Artifact: string(raw), Reason: "Actual signed synthetic deployment license"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	g.handshake = wire.Handshake{ProtocolVersion: wire.InferenceVersion, ExecutionProfile: wire.DockerInferenceProfile, Capabilities: wire.Capabilities(wire.DockerInferenceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-deployment-runtime", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: now, AgentVersion: wire.AgentVersion}}
	g.node = g.enroll(t, f.Workspace, g.handshake)
	manifest, _ := json.Marshal(wire.ApprovedModel())
	g.exec(t, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic approved metadata',$4,$5,$6,'ready',$7)`, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Synthetic approved metadata',?,?,CAST(? AS JSON),'ready',?)`, g.model, f.Tenant, f.Workspace, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, f.User)
	g.handshake.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{{UUID: g.gpu, Name: "Synthetic physical GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: now}}, ObservedAt: now}
	g.heartbeat(t, g.node, g.handshake)
	return g
}
func (g deploymentRuntimeStory) session(t *testing.T, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = g.s.Nodes.Identity.IssueSessionWithin(ctx, q, g.f.User, 1, audience, "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := g.s.Nodes.Identity.Authenticate(g.ctx, issued.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (g deploymentRuntimeStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("constrained synthetic fixture mutation", e)
	}
}
func (g deploymentRuntimeStory) enroll(t *testing.T, workspace string, h wire.Handshake) wire.Enrolled {
	t.Helper()
	token, e := g.s.Nodes.MintForProfile(g.ctx, g.admin, workspace, "Synthetic runtime "+uuid.NewString(), h.Profile(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	observed := h
	h.Inference = nil
	h.Component = nil
	n, e := g.s.Nodes.Enroll(g.ctx, wire.Enrollment{JoinToken: token.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if observed.Inference != nil || observed.Component != nil {
		g.heartbeat(t, n, observed)
	}
	return n
}
func (g deploymentRuntimeStory) heartbeat(t *testing.T, n wire.Enrolled, h wire.Handshake) {
	t.Helper()
	if _, e := g.s.Nodes.Heartbeat(g.ctx, n.NodeID, n.Credential, h); e != nil {
		t.Fatal("actual selected heartbeat", e)
	}
}
func (g deploymentRuntimeStory) input() CreateRequest {
	return CreateRequest{NodeID: g.node.NodeID, ModelID: g.model, GPUUUID: g.gpu, Reason: "Actual selected inference intent; synthetic target"}
}
func (g deploymentRuntimeStory) create(t *testing.T) Deployment {
	t.Helper()
	d, e := g.s.Create(g.ctx, g.admin, g.input(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("actual deployment owner", e)
	}
	return d
}
func (g deploymentRuntimeStory) claim(t *testing.T) wire.Lease {
	t.Helper()
	c, e := g.s.Nodes.Claim(g.ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	if e != nil || c.Lease == nil || c.Lease.Inference == nil {
		t.Fatal("actual inference claim", e)
	}
	// The agent acknowledges a lease before it starts (ADR 0011).
	if _, e = g.s.Nodes.Acknowledge(g.ctx, g.node.NodeID, g.node.Credential, c.Lease.TaskID, wire.TaskAcknowledgementRequest{Sequence: c.Lease.Sequence, LeaseToken: c.Lease.LeaseToken}, uuid.NewString()); e != nil {
		t.Fatal("actual acknowledgement", e)
	}
	return *c.Lease
}
func deploymentRuntimeResult(l wire.Lease, state string) wire.Result {
	c := l.Inference
	o := &wire.DeploymentObservation{DeploymentID: c.DeploymentID, ModelID: c.ModelID, CatalogDigest: c.CatalogDigest, GPUUUID: c.GPUUUID, Revision: c.Revision, State: state}
	if c.Derived != nil {
		o.ModelKind = wire.DerivedModelKind
		o.ServingRecipeSHA256 = c.Derived.ServingRecipeSHA256
	}
	if state == "running" || state == "stopped" {
		o.Managed = true
		o.ContainerID = strings.Repeat("a", 64)
		o.ImageID = wire.ApprovedInferenceRecipe().ImageConfigDigest
	}
	if state == "running" {
		o.Ready = true
		o.Verification = &wire.GenerationVerification{Model: wire.InferenceModelName, PromptSHA256: wire.InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 10, OutputTokens: 1, ObservedAt: time.Now().UTC()}
	}
	return wire.Result{LeaseToken: l.LeaseToken, Outcome: "success", Inference: &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, Deployment: o, ObservedAt: time.Now().UTC()}}
}
func (g deploymentRuntimeStory) complete(t *testing.T, l wire.Lease, state string) {
	t.Helper()
	if out, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, l.TaskID, deploymentRuntimeResult(l, state), uuid.NewString()); e != nil || out != "succeeded" {
		t.Fatal("actual inference completion", out, e)
	}
}
func (g deploymentRuntimeStory) action(t *testing.T, d Deployment, action string) Deployment {
	t.Helper()
	v, e := g.s.Action(g.ctx, g.admin, d.ID, ActionRequest{Action: action, Revision: d.Revision, Reason: "Actual selected " + action}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func (g deploymentRuntimeStory) count(t *testing.T, table string) int {
	t.Helper()
	switch table {
	case "deployments", "gpu_leases", "node_tasks", "audit_events":
	default:
		t.Fatal("unowned count")
	}
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT count(*) FROM "+table+" WHERE tenant_id=$1", "SELECT count(*) FROM "+table+" WHERE tenant_id=?", g.f.Tenant)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func deploymentRuntimeCode(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("wanted %s got %v", code, e)
	}
}
func (g deploymentRuntimeStory) wrapped(store transaction.Store) deploymentRuntimeStory {
	a := *g.s.Nodes.Identity
	a.DB = nil
	a.Storage = store
	w := *g.s.Nodes.Workspaces
	w.DB = nil
	w.Identity = &a
	n := *g.s.Nodes
	n.DB = nil
	n.Identity = &a
	n.Workspaces = &w
	m := *g.s.Models
	m.DB = nil
	m.Identity = &a
	m.Workspaces = &w
	s := &Service{Nodes: &n, Models: &m}
	n.Inference = s
	g.s = s
	return g
}

type deploymentRuntimeFaultStore struct {
	transaction.Store
	failExec                   int
	unknownWrites, unknownRead bool
	hits                       int
}
type deploymentRuntimeFaultExecutor struct {
	transaction.Executor
	count int
	fail  int
}

func (q *deploymentRuntimeFaultExecutor) Exec(ctx context.Context, s transaction.Statement) (int64, error) {
	q.count++
	if q.count == q.fail {
		return 0, &transaction.Error{Kind: transaction.Unavailable}
	}
	return q.Executor.Exec(ctx, s)
}
func (s *deploymentRuntimeFaultStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	var writes int
	e := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		x := &deploymentRuntimeFaultExecutor{Executor: q, fail: s.failExec}
		e := fn(ctx, x)
		writes = x.count
		if s.failExec > 0 && x.count == s.failExec {
			s.hits++
		}
		return e
	})
	if e != nil {
		return e
	}
	if s.unknownRead || s.unknownWrites && writes >= 5 {
		s.hits++
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}

func TestActualPairedDeploymentRuntime(t *testing.T) {
	for _, name := range []string{"create_replay_atomic_scope", "current_actor_and_portal_rejection", "cross_workspace_read_and_model_binding", "physical_gpu_exclusion_across_nodes", "claim_renew_reclaim_and_stale_lease", "result_replay_and_failed_hold", "stop_remove_confirmed_release", "old_future_foreign_observations", "deadline_and_attempt_sweep", "restore_fences_without_release", "rollback_and_unknown_zero_projection", "read_cancel_and_unknown_zero_projection", "typed_nondefault_policy", "derived_epoch_before_after_callback", "inference_http_manifest_and_missing_files", "selected_component_dependency_failclosed", "parallel_physical_gpu_race", "profile_capability_and_credential_revocation"} {
		t.Run(name, func(t *testing.T) {
			g := deploymentRuntimeSetup(t)
			switch name {
			case "create_replay_atomic_scope":
				before := g.count(t, "audit_events")
				key := uuid.NewString()
				d, e := g.s.Create(g.ctx, g.admin, g.input(), key, uuid.NewString())
				if e != nil || d.LastTaskID == nil || d.ResourceState != "held" {
					t.Fatal(d, e)
				}
				again, e := g.s.Create(g.ctx, g.admin, g.input(), key, uuid.NewString())
				if e != nil || again.ID != d.ID || g.count(t, "deployments") != 1 || g.count(t, "gpu_leases") != 1 || g.count(t, "node_tasks") != 1 || g.count(t, "audit_events") != before+1 {
					t.Fatal("atomic exact replay", again, e)
				}
				changed := g.input()
				changed.Reason = "Changed immutable intent"
				out, e := g.s.Create(g.ctx, g.admin, changed, key, uuid.NewString())
				deploymentRuntimeCode(t, e, "IDEMPOTENCY_CONFLICT")
				if out.ID != "" {
					t.Fatal("conflict projection")
				}
			case "current_actor_and_portal_rejection":
				if out, e := g.s.Create(g.ctx, g.portal, g.input(), uuid.NewString(), uuid.NewString()); e == nil || out.ID != "" {
					t.Fatal("portal mutation accepted", e)
				}
				g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
				if out, e := g.s.Create(g.ctx, g.admin, g.input(), uuid.NewString(), uuid.NewString()); e == nil || out.ID != "" || g.count(t, "deployments") != 0 {
					t.Fatal("stale admin accepted", e)
				}
			case "cross_workspace_read_and_model_binding":
				d := g.create(t)
				other := uuid.NewString()
				g.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other synthetic Workspace','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Other synthetic Workspace','active')", other, g.f.Tenant)
				if out, e := g.s.Get(g.ctx, g.portal, other, d.ID); e == nil || out.ID != "" {
					t.Fatal("foreign Workspace read", e)
				}
				foreign := uuid.NewString()
				manifest, _ := json.Marshal(wire.ApprovedModel())
				g.exec(t, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Other model',$4,$5,$6,'ready',$7)`, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Other model',?,?,CAST(? AS JSON),'ready',?)`, foreign, g.f.Tenant, other, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, g.f.User)
				input := g.input()
				input.ModelID = foreign
				if out, e := g.s.Create(g.ctx, g.admin, input, uuid.NewString(), uuid.NewString()); e == nil || out.ID != "" {
					t.Fatal("cross Workspace model bound", e)
				}
				page, e := g.s.ListFiltered(g.ctx, g.portal, g.f.Workspace, g.model, true)
				if e != nil || len(page) != 1 || page[0].ID != d.ID {
					t.Fatal("scoped list", e)
				}
			case "physical_gpu_exclusion_across_nodes":
				g.create(t)
				other := g.enroll(t, g.f.Workspace, g.handshake)
				input := g.input()
				input.NodeID = other.NodeID
				if out, e := g.s.Create(g.ctx, g.admin, input, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "INFERENCE_RESOURCE_BUSY" || out.ID != "" {
					t.Fatal("physical UUID reused", e)
				}
				if g.count(t, "deployments") != 1 || g.count(t, "gpu_leases") != 1 || g.count(t, "node_tasks") != 1 {
					t.Fatal("conflicting physical intent partially committed")
				}
			case "claim_renew_reclaim_and_stale_lease":
				g.create(t)
				first := g.claim(t)
				if c, e := g.s.Nodes.Claim(g.ctx, g.node.NodeID, g.node.Credential, uuid.NewString()); e != nil || c.Lease != nil {
					t.Fatal("unexpired lease redelivered", e)
				}
				renewed, e := g.s.Nodes.Renew(g.ctx, g.node.NodeID, g.node.Credential, first.TaskID, first.LeaseToken)
				if e != nil || renewed.ValidForMilliseconds <= 0 || renewed.ValidForMilliseconds > 30000 {
					t.Fatal("actual renewal", e)
				}
				g.exec(t, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE node_tasks SET lease_expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", first.TaskID)
				second := g.claim(t)
				if second.TaskID != first.TaskID || second.Attempt != 2 || second.LeaseToken == first.LeaseToken {
					t.Fatal("expired lease not reclaimed")
				}
				if out, e := g.s.Nodes.Renew(g.ctx, g.node.NodeID, g.node.Credential, first.TaskID, first.LeaseToken); e == nil || !out.LeaseExpiresAt.IsZero() {
					t.Fatal("old physical lease renewed", e)
				}
			case "result_replay_and_failed_hold":
				d := g.create(t)
				lease := g.claim(t)
				result := deploymentRuntimeResult(lease, "failed")
				result.Outcome = "failure"
				result.ErrorCode = "INFERENCE_OPERATION_FAILED"
				result.Inference.ErrorCode = result.ErrorCode
				before := g.count(t, "audit_events")
				for range 2 {
					if status, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, lease.TaskID, result, uuid.NewString()); e != nil || status != "failed" {
						t.Fatal(status, e)
					}
				}
				value, e := g.s.Get(g.ctx, g.admin, "", d.ID)
				if e != nil || value.ResourceState != "held" || g.count(t, "audit_events") != before+1 {
					t.Fatal("failure released resource or duplicated audit", e)
				}
				result.ErrorCode = "INFERENCE_LOAD_TIMEOUT"
				result.Inference.ErrorCode = result.ErrorCode
				if out, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, lease.TaskID, result, uuid.NewString()); e == nil || out != "" {
					t.Fatal("changed result replay", e)
				}
			case "stop_remove_confirmed_release":
				d := g.create(t)
				g.complete(t, g.claim(t), "running")
				d = g.action(t, d, "stop")
				if d.ResourceState != "held" {
					t.Fatal("intent alone released GPU")
				}
				g.complete(t, g.claim(t), "stopped")
				d, e := g.s.Get(g.ctx, g.admin, "", d.ID)
				if e != nil || d.ResourceState != "released" {
					t.Fatal("confirmed stop not released", e)
				}
				d = g.action(t, d, "remove")
				g.complete(t, g.claim(t), "not_installed")
				d, e = g.s.Get(g.ctx, g.admin, "", d.ID)
				if e != nil || d.RemovedAt == nil || d.ResourceState != "released" {
					t.Fatal("confirmed removal missing", e)
				}
			case "old_future_foreign_observations":
				d := g.create(t)
				old := g.claim(t)
				g.complete(t, old, "running")
				d = g.action(t, d, "stop")
				h := g.handshake
				h.Inference = deploymentRuntimeResult(old, "running").Inference
				g.heartbeat(t, g.node, h)
				value, e := g.s.Get(g.ctx, g.admin, "", d.ID)
				if e != nil || !value.Stale || value.Revision != 2 {
					t.Fatal("old observation promoted current revision", e)
				}
				h.Inference.Deployment.Revision = 3
				if out, e := g.s.Nodes.Heartbeat(g.ctx, g.node.NodeID, g.node.Credential, h); e == nil || out.Status != "" {
					t.Fatal("future observation accepted", e)
				}
				h.Inference.Deployment.Revision = 2
				h.Inference.Deployment.DeploymentID = uuid.NewString()
				if out, e := g.s.Nodes.Heartbeat(g.ctx, g.node.NodeID, g.node.Credential, h); e == nil || out.Status != "" {
					t.Fatal("foreign deployment observation accepted", e)
				}
			case "deadline_and_attempt_sweep":
				d := g.create(t)
				l := g.claim(t)
				p := g.s.Nodes.Identity.Config.Policy()
				p.Nodes.MaximumTaskAttempts = 1
				p.Nodes.TaskSweepBatch = 1
				g.s.Nodes.Identity.Config.Runtime = &p
				g.exec(t, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE node_tasks SET lease_expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", l.TaskID)
				if e := g.s.Nodes.Sweep(g.ctx); e != nil {
					t.Fatal(e)
				}
				value, e := g.s.Get(g.ctx, g.admin, "", d.ID)
				if e != nil || value.TaskState == nil || *value.TaskState != "failed" || value.TaskErrorCode == nil || *value.TaskErrorCode != "TASK_ATTEMPTS_EXHAUSTED" || value.ResourceState != "held" {
					t.Fatal("attempt sweep", e)
				}
				task, e := g.s.Discover(g.ctx, g.admin, g.node.NodeID, "Actual deadline sweep", uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				g.exec(t, "UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE node_tasks SET deadline_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", task.ID)
				if e = g.s.Nodes.Sweep(g.ctx); e != nil {
					t.Fatal(e)
				}
				var code string
				if e = g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT error_code FROM node_tasks WHERE id=$1", "SELECT error_code FROM node_tasks WHERE id=?", task.ID)).Scan(&code); e != nil || code != "TASK_DEADLINE_EXCEEDED" {
					t.Fatal(code, e)
				}
			case "restore_fences_without_release":
				d := g.create(t)
				g.claim(t)
				if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					if e := nodes.InvalidateAuthoritiesWithin(ctx, q); e != nil {
						return e
					}
					return InvalidateAuthoritiesWithin(ctx, q)
				}); e != nil {
					t.Fatal(e)
				}
				value, e := g.s.Get(g.ctx, g.admin, "", d.ID)
				if e != nil || value.ResourceState != "recovery_required" || value.ObservedState != "unknown" || value.TaskState == nil || *value.TaskState != "cancelled" {
					t.Fatal("restore ownership fence", e)
				}
				if c, e := g.s.Nodes.Claim(g.ctx, g.node.NodeID, g.node.Credential, uuid.NewString()); e == nil || c.Lease != nil {
					t.Fatal("restored credential retained", e)
				}
			case "rollback_and_unknown_zero_projection":
				baseline := g.count(t, "audit_events")
				fault := &deploymentRuntimeFaultStore{Store: g.f.Store, failExec: 3}
				wrapped := g.wrapped(fault)
				if out, e := wrapped.s.Create(g.ctx, g.admin, g.input(), uuid.NewString(), uuid.NewString()); e == nil || out.ID != "" || fault.hits == 0 {
					t.Fatal("owner write fault not injected", e)
				}
				for _, table := range []string{"deployments", "gpu_leases", "node_tasks"} {
					if g.count(t, table) != 0 {
						t.Fatal("partial graph after rollback", table)
					}
				}
				if g.count(t, "audit_events") != baseline {
					t.Fatal("audit outside rollback")
				}
				key := uuid.NewString()
				unknown := &deploymentRuntimeFaultStore{Store: g.f.Store, unknownWrites: true}
				wrapped = g.wrapped(unknown)
				out, e := wrapped.s.Create(g.ctx, g.admin, g.input(), key, uuid.NewString())
				deploymentRuntimeCode(t, e, string(transaction.CommitUnknown))
				if out.ID != "" || g.count(t, "deployments") != 1 {
					t.Fatal("unknown commit projection or no actual committed intent")
				}
				again, e := g.s.Create(g.ctx, g.admin, g.input(), key, uuid.NewString())
				if e != nil || again.ID == "" || g.count(t, "deployments") != 1 {
					t.Fatal("exact retained intent not reconciled", e)
				}
			case "read_cancel_and_unknown_zero_projection":
				d := g.create(t)
				wrapped := g.wrapped(&deploymentRuntimeFaultStore{Store: g.f.Store, unknownRead: true})
				if out, e := wrapped.s.Get(g.ctx, g.admin, "", d.ID); e == nil || out.ID != "" {
					t.Fatal("unknown private read projected", e)
				}
				ctx, cancel := context.WithCancel(g.ctx)
				cancel()
				if out, e := g.s.List(ctx, g.portal, g.f.Workspace); e == nil || out != nil {
					t.Fatal("cancelled page projected", e)
				}
			case "typed_nondefault_policy":
				p := g.s.Nodes.Identity.Config.Policy()
				p.Nodes.TaskSeconds = 120
				p.Nodes.InferenceDeploySeconds = 240
				p.Nodes.TaskLeaseSeconds = 17
				p.Nodes.MaximumTaskAttempts = 2
				p.Nodes.TaskSweepBatch = 7
				p.UI.LookupPageSize = 1
				if e := p.Validate(); e != nil {
					t.Fatal(e)
				}
				g.s.Nodes.Identity.Config.Runtime = &p
				d := g.create(t)
				l := g.claim(t)
				if l.LeaseExpiresAt.Sub(time.Now().UTC()) > 18*time.Second {
					t.Fatal("lease override ignored")
				}
				var seconds float64
				statement := g.f.Statement("SELECT extract(epoch FROM (deadline_at-created_at)) FROM node_tasks WHERE id=$1", "SELECT TIMESTAMPDIFF(MICROSECOND,created_at,deadline_at)/1000000.0 FROM node_tasks WHERE id=?", l.TaskID)
				if e := g.f.Store.QueryRow(g.ctx, statement).Scan(&seconds); e != nil || seconds < 239 || seconds > 241 {
					t.Fatal("deployment deadline override ignored", seconds, e)
				}
				page, e := g.s.List(g.ctx, g.portal, g.f.Workspace)
				if e != nil || len(page) != 1 || page[0].ID != d.ID {
					t.Fatal("lookup bound", e)
				}
			case "derived_epoch_before_after_callback":
				g.derivedEpochCase(t)
			case "inference_http_manifest_and_missing_files":
				g.httpLeaseCase(t)
			case "selected_component_dependency_failclosed":
				// No selected component owner is installed in this dedicated inference fixture.
				h := g.handshake
				h.ProtocolVersion = wire.ManagementVersion
				h.ExecutionProfile = wire.SystemdComponentsProfile
				h.Capabilities = wire.Capabilities(h.ExecutionProfile)
				h.Inference = nil
				n := g.enroll(t, g.f.Workspace, h)
				h.Component = &wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, Managed: false, State: "not_installed", ObservedAt: time.Now().UTC()}
				if e := h.Validate(); e != nil {
					t.Fatal("invalid component fixture", e)
				}
				out, e := g.s.Nodes.Heartbeat(g.ctx, n.NodeID, n.Credential, h)
				deploymentRuntimeCode(t, e, "COMPONENT_PROFILE_UNAVAILABLE")
				if out.Status != "" {
					t.Fatal("missing component owner projected status")
				}
			case "parallel_physical_gpu_race":
				other := g.enroll(t, g.f.Workspace, g.handshake)
				inputs := []CreateRequest{g.input(), g.input()}
				inputs[1].NodeID = other.NodeID
				var wg sync.WaitGroup
				results := make(chan error, 2)
				for _, input := range inputs {
					wg.Add(1)
					go func(input CreateRequest) {
						defer wg.Done()
						_, e := g.s.Create(g.ctx, g.admin, input, uuid.NewString(), uuid.NewString())
						results <- e
					}(input)
				}
				wg.Wait()
				close(results)
				passed := 0
				for e := range results {
					if e == nil {
						passed++
					} else if e.Error() != "INFERENCE_RESOURCE_BUSY" {
						t.Fatal(e)
					}
				}
				if passed != 1 || g.count(t, "deployments") != 1 || g.count(t, "gpu_leases") != 1 {
					t.Fatal("concurrent physical ownership split")
				}
			case "profile_capability_and_credential_revocation":
				d := g.create(t)
				n, e := g.s.Nodes.Get(g.ctx, g.admin, "", g.node.NodeID)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = g.s.Nodes.Update(g.ctx, g.admin, n.ID, n.Name, "removed", n.Version, "Actual revoked machine authority", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				if c, e := g.s.Nodes.Claim(g.ctx, g.node.NodeID, g.node.Credential, uuid.NewString()); e == nil || c.Lease != nil {
					t.Fatal("removed credential claimed", e)
				}
				value, e := g.s.Get(g.ctx, g.admin, "", d.ID)
				if e != nil || value.ResourceState != "recovery_required" {
					t.Fatal("node removal released physical ownership", e)
				}
				h := g.handshake
				h.Capabilities = []string{}
				if out, e := g.s.Nodes.Heartbeat(g.ctx, g.node.NodeID, g.node.Credential, h); e == nil || out.Status != "" {
					t.Fatal("profile downgrade accepted", e)
				}
			default:
				t.Fatal("unregistered exact leaf")
			}
		})
	}
}

func (g deploymentRuntimeStory) derivedEpochCase(t *testing.T) {
	t.Helper()
	h := g.handshake
	h.ProtocolVersion = wire.ModelLifecycleVersion
	h.ExecutionProfile = wire.DockerModelLifecycleProfile
	h.Capabilities = wire.Capabilities(h.ExecutionProfile)
	h.Inference = nil
	n := g.enroll(t, g.f.Workspace, h)
	id, model := uuid.NewString(), uuid.NewString()
	// Exact synthetic catalog only: no derived files are published or accepted.
	catalog := lifecycle.DerivedCatalog{SchemaVersion: 1, Kind: wire.DerivedModelKind, Scope: lifecycle.Scope{TenantID: g.f.Tenant, WorkspaceID: g.f.Workspace, PreparationID: id}, ModelID: model, BaseModelID: id, BaseCatalogSHA256: wire.ModelCatalogDigest(), AttemptID: id, ArtifactID: model, ReportSHA256: strings.Repeat("a", 64), RecipeSHA256: lifecycle.RecipeDigest(), BaseRepository: lifecycle.ApprovedBaseRepository, BaseRevision: lifecycle.ApprovedBaseRevision, License: lifecycle.ApprovedBaseLicense, Format: "safetensors", Architecture: lifecycle.ApprovedBaseArchitecture, ModelType: lifecycle.ApprovedBaseModelType, Precision: "BF16", MaximumContextTokens: 2048, Files: lifecycle.ApprovedBaseFiles()}
	for i, v := range catalog.Files {
		if v.Path == "model.safetensors" {
			catalog.Files[i].SizeBytes = 128
			catalog.Files[i].SHA256 = strings.Repeat("b", 64)
		}
	}
	sha, e := catalog.Digest()
	if e != nil {
		t.Fatal(e)
	}
	command := wire.InferenceCommand{Action: "deploy", DeploymentID: id, ModelID: model, CatalogDigest: sha, GPUUUID: "GPU-" + id, Revision: 1, Derived: &wire.DerivedInferenceIdentity{SchemaVersion: 1, Catalog: catalog, ServingRecipeSHA256: wire.DerivedServingRecipeDigest()}}
	var task nodes.Task
	if e = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		task, e = g.s.Nodes.CreateInferenceTaskBoundWithin(ctx, q, g.admin, n.NodeID, wire.DerivedInferenceOperation, uuid.NewString(), "Synthetic typed derived node delegation only", uuid.NewString(), bytes.Repeat([]byte{1}, 32), func(context.Context, transaction.Executor, nodes.Node) (wire.InferenceCommand, error) {
			return command, nil
		}, func(context.Context, transaction.Executor, nodes.Node, nodes.Task, wire.InferenceCommand) error {
			return nil
		})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	claim, e := g.s.Nodes.Claim(g.ctx, n.NodeID, n.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.Inference == nil || claim.Lease.Inference.Derived == nil {
		t.Fatal("actual derived typed claim", e)
	}
	before, e := g.s.Nodes.Get(g.ctx, g.admin, "", n.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	e = g.s.Nodes.WithLeaseExecutor(g.ctx, n.NodeID, n.Credential, task.ID, claim.Lease.LeaseToken, func(ctx context.Context, q transaction.Executor, node nodes.Node, _ nodes.Task) error {
		if _, e := q.Exec(ctx, g.f.Statement("UPDATE managed_nodes SET name='Synthetic callback must roll back' WHERE id=$1", "UPDATE managed_nodes SET name='Synthetic callback must roll back' WHERE id=?", node.ID)); e != nil {
			return e
		}
		// A separate real connection commits revocation while the authority callback holds node scope.
		_, e := g.f.Store.Exec(ctx, g.f.Statement("UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User))
		return e
	})
	deploymentRuntimeCode(t, e, "ACCESS_DENIED")
	var actual string
	if e = g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT name FROM managed_nodes WHERE id=$1", "SELECT name FROM managed_nodes WHERE id=?", n.NodeID)).Scan(&actual); e != nil || actual != before.Name {
		t.Fatal("callback mutation escaped epoch rollback", e)
	}
	if out, e := g.s.Nodes.Renew(g.ctx, n.NodeID, n.Credential, task.ID, claim.Lease.LeaseToken); e == nil || !out.LeaseExpiresAt.IsZero() {
		t.Fatal("revoked derived creator renewed", e)
	}
}
func (g deploymentRuntimeStory) httpLeaseCase(t *testing.T) {
	t.Helper()
	g.create(t)
	lease := g.claim(t)
	h := &nodehttp.Handler{Service: g.s.Nodes}
	h.ModelManifestWithin = func(ctx context.Context, q transaction.Executor, n nodes.Node, task nodes.Task) (wire.ModelCatalog, error) {
		c, e := g.s.Nodes.ReadInferenceCommandWithin(ctx, q, n, task)
		if e != nil {
			return wire.ModelCatalog{}, e
		}
		if c.Action != "deploy" || c.Derived != nil {
			return wire.ModelCatalog{}, errors.New("AGENT_OPERATION_UNSUPPORTED")
		}
		if e = g.s.Models.ReadyWithin(ctx, q, n.TenantID, n.WorkspaceID, c.ModelID); e != nil {
			return wire.ModelCatalog{}, e
		}
		return wire.ApprovedModel(), nil
	}
	h.ModelFileWithin = func(ctx context.Context, q transaction.Executor, n nodes.Node, task nodes.Task, input wire.ModelFileRequest) (httpserver.Download, error) {
		c, e := g.s.Nodes.ReadInferenceCommandWithin(ctx, q, n, task)
		if e != nil {
			return httpserver.Download{}, e
		}
		return g.s.Models.OpenTransferWithin(ctx, q, n.TenantID, n.WorkspaceID, c.ModelID, input)
	}
	call := func(action string, input any) (any, int, error) {
		raw, _ := json.Marshal(input)
		r := httptest.NewRequest("POST", "https://control.example.invalid/agent/v1/nodes/"+g.node.NodeID+"/tasks/"+lease.TaskID+"/"+action, bytes.NewReader(raw))
		r = r.WithContext(g.ctx)
		r.RemoteAddr = "127.0.0.1:20123"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+g.node.Credential)
		return h.Machine(r, uuid.NewString())
	}
	out, status, e := call("model-manifest", wire.LeaseRenewal{LeaseToken: lease.LeaseToken})
	if e != nil || status != 200 || !reflect.DeepEqual(out, wire.ApprovedModel()) {
		t.Fatal("actual selected HTTP manifest callback", e)
	}
	file := wire.ApprovedModel().Files[0]
	request := wire.ModelFileRequest{LeaseToken: lease.LeaseToken, File: file.Path, Length: min(wire.ModelTransferChunkBytes, file.SizeBytes)}
	out, _, e = call("model-files", request)
	if e == nil || out != nil || e.Error() != "MODEL_FILE_INVALID" {
		t.Fatal("missing real model file accepted or projected", e)
	}
	g.exec(t, "UPDATE node_tasks SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE node_tasks SET lease_expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", lease.TaskID)
	out, _, e = call("model-manifest", wire.LeaseRenewal{LeaseToken: lease.LeaseToken})
	if e == nil || out != nil {
		t.Fatal("stale lease HTTP projected catalog", e)
	}
}
