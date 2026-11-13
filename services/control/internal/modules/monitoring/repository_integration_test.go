//go:build repository_integration && monitoring_integration

package monitoring_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	gateway "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	monitoringhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
)

type story struct {
	cardinalitySource *monitoring.SeriesCardinalityQuerySource
	t                 *testing.T
	f                 repositoryfixture.Fixture
	ctx               context.Context
	i                 *identity.Service
	w                 *workspaces.Service
	n                 *nodes.Service
	c                 *components.Service
	d                 *distribution.Service
	l                 *licensing.Service
	s                 *monitoring.Service
	m                 *monitoringmetrics.Recorder
	admin, portal     identity.Auth
	adminToken        string
	offset            atomic.Int64
	config            monitoring.Config
	gateway           distribution.Provisioned
	alias             distribution.Alias
	key               distribution.MintedKey
}

func setup(t *testing.T, licensed bool) *story {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	g := &story{t: t, f: f, ctx: ctx, config: monitoring.Defaults()}
	g.i = &identity.Service{Storage: f.Store, HMACKey: make([]byte, 32)}
	if _, e := rand.Read(g.i.HMACKey); e != nil {
		t.Fatal(e)
	}
	g.i.Config.Server.AdminOrigin = "https://admin.example.invalid"
	g.i.Config.Server.PortalOrigin = "https://portal.example.invalid"
	g.w = &workspaces.Service{Identity: g.i}
	g.n = &nodes.Service{Identity: g.i, Workspaces: g.w}
	g.c = &components.Service{Nodes: g.n}
	g.n.Components = g.c
	model := &models.Service{Identity: g.i, Workspaces: g.w}
	deployment := &deployments.Service{Nodes: g.n, Models: model}
	g.n.Inference = deployment
	g.d = &distribution.Service{Identity: g.i, Workspaces: g.w, Nodes: g.n, Deployments: deployment}
	g.exec(`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')`, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')`, f.User, f.Tenant, f.Workspace)
	g.admin, g.adminToken = g.session("admin")
	g.portal, _ = g.session("portal")
	key, trust, e := licensewire.GenerateKey("explicit-m30-synthetic-sql-license")
	if e != nil {
		t.Fatal(e)
	}
	g.l, e = licensing.New(g.i, trust, licensewire.DefaultPolicy(), func() time.Time { return time.Now().UTC().Add(time.Duration(g.offset.Load())) })
	if e != nil {
		t.Fatal(e)
	}
	g.i.BusinessAdmission = g.l
	if licensed {
		state, e := g.l.StatusAdmin(ctx, g.admin)
		if e != nil {
			t.Fatal(e)
		}
		now := time.Now().UTC().Truncate(time.Second)
		claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Explicit M30 isolated SQL fixture", DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
		raw, e := licensewire.Issue(claims, key)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = g.l.Import(ctx, g.admin, licensing.ImportInput{Artifact: string(raw), Reason: "Actual signed synthetic whole-platform license"}, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	g.m, e = monitoringmetrics.New(g.config.HTTP, nil)
	if e != nil {
		t.Fatal(e)
	}
	g.rebuild()
	return g
}
func (g *story) rebuild() {
	g.t.Helper()
	var e error
	g.s, e = monitoring.NewWithSeriesCardinalitySource(g.i, g.w, g.l, g.c, g.d, g.m, g.config, g.cardinalitySource)
	if e != nil {
		g.t.Fatal(e)
	}
}
func (g *story) exec(pg, my string, args ...any) {
	g.t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		g.t.Fatal("actual constrained monitoring fixture", e)
	}
}
func (g *story) count(sql string, args ...any) int {
	g.t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(g.f.Store.Dialect(), sql, args...)).Scan(&n); e != nil {
		g.t.Fatal(e)
	}
	return n
}
func (g *story) session(audience string) (identity.Auth, string) {
	g.t.Helper()
	var issued identity.IssuedSession
	e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = g.i.IssueSessionWithin(ctx, q, g.f.User, 1, audience, "", uuid.NewString(), nil)
		return e
	})
	if e != nil {
		g.t.Fatal(e)
	}
	a, e := g.i.Authenticate(g.ctx, issued.Token, audience)
	if e != nil {
		g.t.Fatal(e)
	}
	return a, issued.Token
}
func (g *story) get() monitoring.Snapshot {
	g.t.Helper()
	out, e := g.s.Get(g.ctx, g.admin, g.f.Workspace, "", uuid.NewString())
	if e != nil {
		g.t.Fatal(e)
	}
	return out
}
func (g *story) denied(a identity.Auth, workspace string) {
	g.t.Helper()
	out, e := g.s.Get(g.ctx, a, workspace, "", uuid.NewString())
	if e == nil || !reflect.DeepEqual(out, monitoring.Snapshot{}) {
		g.t.Fatal("denied or failed read exposed projection", e)
	}
}
func (g *story) enroll(profile, name string) (agent.Enrolled, agent.Handshake) {
	g.t.Helper()
	join, e := g.n.MintForProfile(g.ctx, g.admin, g.f.Workspace, name+" "+uuid.NewString(), profile, uuid.NewString())
	if e != nil {
		g.t.Fatal(e)
	}
	protocol := agent.InferenceVersion
	if profile == agent.SystemdComponentsProfile {
		protocol = agent.ManagementVersion
	}
	h := agent.Handshake{ProtocolVersion: protocol, ExecutionProfile: profile, Capabilities: agent.Capabilities(profile), Facts: agent.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "explicit-M30-node-envelope", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: agent.AgentVersion}}
	enrolled, e := g.n.Enroll(g.ctx, agent.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		g.t.Fatal(e)
	}
	return enrolled, h
}
func (g *story) component() agent.Enrolled {
	g.t.Helper()
	n, h := g.enroll(agent.SystemdComponentsProfile, "Explicit M30 node observation")
	h.Component = &agent.ComponentObservation{Component: agent.NodeExporter, Version: agent.NodeExporterVersion, Managed: true, State: "running", ObservedAt: time.Now().UTC(), Metrics: &agent.ComponentMetrics{CPUSecondsTotal: 100, CPUIdleSecondsTotal: 40, MemoryTotalBytes: 4096, MemoryAvailableBytes: 2048, FilesystemTotalBytes: 8192, FilesystemAvailableBytes: 4096}}
	if h.Component.ValidateForProfile(h.ExecutionProfile) != nil {
		g.t.Fatal("explicit node metric envelope invalid")
	}
	if _, e := g.n.Heartbeat(g.ctx, n.NodeID, n.Credential, h); e != nil {
		g.t.Fatal(e)
	}
	return n
}
func (g *story) access() {
	g.t.Helper()
	n, _ := g.enroll(agent.DockerInferenceProfile, "Explicit M30 gateway physical envelope")
	modelID, deploymentID, gpu := uuid.NewString(), uuid.NewString(), "GPU-"+uuid.NewString()
	manifest, _ := json.Marshal(agent.ApprovedModel())
	g.exec(`INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Explicit M30 model envelope','qwen3-0.6b',$4,$5::jsonb,'ready',$6)`, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Explicit M30 model envelope','qwen3-0.6b',?,CAST(? AS JSON),'ready',?)`, modelID, g.f.Tenant, g.f.Workspace, agent.ModelCatalogDigest(), manifest, g.f.User)
	o := agent.DeploymentObservation{DeploymentID: deploymentID, ModelID: modelID, CatalogDigest: agent.ModelCatalogDigest(), GPUUUID: gpu, Revision: 1, Managed: true, State: "running", Ready: true, ContainerID: strings.Repeat("a", 64), ImageID: agent.ApprovedInferenceRecipe().ImageConfigDigest, Verification: &agent.GenerationVerification{Model: agent.InferenceModelName, PromptSHA256: agent.InferencePromptDigest(), ResponseSHA256: strings.Repeat("c", 64), InputTokens: 1, OutputTokens: 1, ObservedAt: time.Now().UTC()}}
	if e := (agent.InferenceObservation{GPUs: []agent.NVIDIAGPU{}, Deployment: &o, ObservedAt: time.Now().UTC()}).Validate(); e != nil {
		g.t.Fatal(e)
	}
	raw, _ := json.Marshal(o)
	// Only the node/model/physical envelope is explicit synthetic input. Actual
	// schema/FKs/owner APIs/session/license/admission/report/usage run unchanged.
	g.exec(`INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,observed_state,revision,observation,observed_at,created_by) VALUES($1,$2,$3,$4,$5,$6,'running','running',1,$7::jsonb,clock_timestamp(),$8)`, `INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,observed_state,revision,observation,observed_at,created_by) VALUES(?,?,?,?,?,?,'running','running',1,CAST(? AS JSON),UTC_TIMESTAMP(6),?)`, deploymentID, g.f.Tenant, g.f.Workspace, n.NodeID, modelID, gpu, raw, g.f.User)
	if g.f.Store.Dialect() == transaction.MySQL {
		g.exec("", `INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) VALUES(?,?,?,?,?,?,1,'held')`, deploymentID, g.f.Tenant, g.f.Workspace, n.NodeID, gpu, deploymentID)
	} else {
		g.exec(`INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) VALUES($1,$2,$3,$4,$5,$1,1,'held')`, "", deploymentID, g.f.Tenant, g.f.Workspace, n.NodeID, gpu)
	}
	var e error
	g.gateway, e = g.d.Provision(g.ctx, g.admin, distribution.ProvisionRequest{WorkspaceID: g.f.Workspace, NodeID: n.NodeID, Name: "M30 actual gateway authority", ClientOrigin: "https://gateway.example.invalid", Reason: "Actual monitoring fixture provision"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		g.t.Fatal(e)
	}
	g.alias, e = g.d.Publish(g.ctx, g.admin, distribution.PublishRequest{WorkspaceID: g.f.Workspace, GatewayID: g.gateway.Gateway.ID, Name: "m30-model", DeploymentID: deploymentID, DeploymentRevision: 1, Reason: "Actual monitoring fixture publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		g.t.Fatal(e)
	}
	g.key, e = g.d.MintKey(g.ctx, g.portal, distribution.KeyRequest{WorkspaceID: g.f.Workspace, AliasID: g.alias.ID, Label: "M30 actual key", TTLSeconds: 300, Reason: "Actual monitoring fixture admission"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		g.t.Fatal(e)
	}
}
func (g *story) report(milliseconds uint64, status string) string {
	g.t.Helper()
	id := uuid.NewString()
	_, e := g.d.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, gateway.AdmissionRequest{RequestID: id, APIKey: *g.key.Secret, Model: g.alias.Name, MaximumOutputTokens: 16}, uuid.NewString())
	if e != nil {
		g.t.Fatal(e)
	}
	o := gateway.Outcome{RequestID: id, Status: status, ElapsedMilliseconds: milliseconds}
	if status == "succeeded" {
		o.Usage = &gateway.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}
		o.OutputStarted = true
	} else {
		code := "gateway_interrupted"
		o.ErrorCode = &code
	}
	if _, e = g.d.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, o, uuid.NewString()); e != nil {
		g.t.Fatal(e)
	}
	return id
}

// These instruments retain actual selected SQL and actual database commit. They
// alter only ACK delivery or add a real failing constraint inside the same tx.
type faultStore struct {
	transaction.Store
	armed   atomic.Bool
	unknown bool
	joined  bool
}

// The real selected store commits its audit first; only delivery of its known
// success races cancellation. This never rolls back or replays that committed read.
type cancelledDeliveryStore struct {
	transaction.Store
	cancel context.CancelFunc
}

func (f *cancelledDeliveryStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	e := f.Store.Within(ctx, ro, fn)
	if e == nil {
		f.cancel()
	}
	return e
}

func (f *faultStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	injected := false
	e := f.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		if e := fn(ctx, q); e != nil {
			return e
		}
		if f.armed.CompareAndSwap(true, false) {
			injected = true
			if !f.unknown {
				_, e := q.Exec(ctx, transaction.SQL(q.Dialect(), `INSERT INTO audit_events(id,audience,action,outcome,request_id) VALUES('invalid','admin','monitoring.constraint','invalid','invalid')`))
				return e
			}
		}
		return nil
	})
	if e == nil && injected && f.unknown {
		if f.joined {
			return errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitRolledBack}, fmt.Errorf("private dropped acknowledgement: %w", &transaction.Error{Kind: transaction.CommitUnknown}))
		}
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}
func TestActualPairedMonitoringRuntime(t *testing.T) {
	t.Run("current_admin_missing_expired_license_and_atomic_audit", func(t *testing.T) {
		g := setup(t, false)
		before := g.count("SELECT COUNT(*) FROM audit_events")
		out := g.get()
		if out.License.State != licensing.Missing || out.License.NewBusinessAllowed || out.Gateway.Sampled != 0 || len(out.Nodes) != 0 || out.Process.Epoch == "" || g.count("SELECT COUNT(*) FROM audit_events") != before+1 {
			t.Fatal("actual missing-license diagnostics", out)
		}
		g.denied(g.portal, g.f.Workspace)
	})
	t.Run("real_outcome_usage_quantiles_and_truncation", func(t *testing.T) {
		g := setup(t, true)
		g.access()
		for _, ms := range []uint64{3, 19, 7} {
			g.report(ms, "succeeded")
		}
		out := g.get()
		if out.Gateway.Reported != 3 || out.Gateway.P95Milliseconds == nil || *out.Gateway.P95Milliseconds != 19 || out.Gateway.UsageKnown != 3 || out.Gateway.TotalTokens != 27 || out.Gateway.HeldInSample != 0 {
			t.Fatal(out.Gateway)
		}
		g.config.MaximumGatewayRows = 2
		g.rebuild()
		out = g.get()
		if out.Gateway.Sampled != 2 || !out.Gateway.Truncated || out.Gateway.UsageKnown != 2 || out.Gateway.OldestAdmission == nil {
			t.Fatal(out.Gateway)
		}
	})
	t.Run("node_observations_staleness_and_canonical_keysets", func(t *testing.T) {
		g := setup(t, true)
		n := g.component()
		other := g.component()
		g.config.NodePageSize = 1
		g.rebuild()
		out := g.get()
		if len(out.Nodes) != 1 || out.NextNodeCursor == nil || out.Nodes[0].Metrics == nil || out.Nodes[0].Stale {
			t.Fatal(out)
		}
		next, e := g.s.Get(g.ctx, g.admin, g.f.Workspace, *out.NextNodeCursor, uuid.NewString())
		if e != nil || len(next.Nodes) != 1 || next.Nodes[0].NodeID == out.Nodes[0].NodeID || next.NextNodeCursor != nil {
			t.Fatal(next, e)
		}
		g.exec(`UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-interval '1 hour' WHERE id=$1`, `UPDATE managed_nodes SET last_heartbeat_at=TIMESTAMPADD(HOUR,-1,UTC_TIMESTAMP(6)) WHERE id=?`, n.NodeID)
		g.config.NodePageSize = 25
		g.rebuild()
		for _, v := range g.get().Nodes {
			if v.NodeID == n.NodeID && !v.Stale {
				t.Fatal("offline became fresh")
			}
		}
		// Historical metrics on a real removed node remain stale; monitoring
		// must neither revive execution nor turn absent values into healthy zero.
		current, e := g.n.Get(g.ctx, g.admin, g.f.Workspace, n.NodeID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = g.n.Update(g.ctx, g.admin, n.NodeID, current.Name, "removed", current.Version, "Actual fixture node removal", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		for _, v := range g.get().Nodes {
			if v.NodeID == n.NodeID && (v.Status != "removed" || !v.Stale) {
				t.Fatal("removed node revived")
			}
		}
		// The real JSON/schema row accepts shape, while approved-wire runtime
		// validation must reject impossible resource facts rather than project it.
		g.exec(`UPDATE managed_components SET observation=jsonb_set(observation,'{metrics,memory_available_bytes}','8192'::jsonb) WHERE node_id=$1`, `UPDATE managed_components SET observation=JSON_SET(observation,'$.metrics.memory_available_bytes',8192) WHERE node_id=?`, other.NodeID)
		g.denied(g.admin, g.f.Workspace)
	})
	t.Run("session_epoch_workspace_foreign_and_portal_denials", func(t *testing.T) {
		g := setup(t, true)
		g.denied(g.portal, g.f.Workspace)
		tenant, workspace := uuid.NewString(), uuid.NewString()
		g.exec(`INSERT INTO tenants(id,name,status) VALUES($1,'Foreign tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Foreign tenant','active')`, tenant)
		g.exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign scope','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Foreign scope','active')`, workspace, tenant)
		g.denied(g.admin, workspace)
		// A previously authenticated Admin projection is not current authority.
		// Every revocation is read from the real selected transaction, then reset
		// only inside this disposable fixture for the next independent denial.
		g.exec(`UPDATE users SET is_platform_admin=false WHERE id=$1`, `UPDATE users SET is_platform_admin=false WHERE id=?`, g.f.User)
		g.denied(g.admin, g.f.Workspace)
		g.exec(`UPDATE users SET is_platform_admin=true WHERE id=$1`, `UPDATE users SET is_platform_admin=true WHERE id=?`, g.f.User)
		g.exec(`UPDATE users SET status='disabled' WHERE id=$1`, `UPDATE users SET status='disabled' WHERE id=?`, g.f.User)
		g.denied(g.admin, g.f.Workspace)
		g.exec(`UPDATE users SET status='active' WHERE id=$1`, `UPDATE users SET status='active' WHERE id=?`, g.f.User)
		g.exec(`UPDATE workspaces SET status='disabled' WHERE id=$1`, `UPDATE workspaces SET status='disabled' WHERE id=?`, g.f.Workspace)
		// Directory inspection remains available to Admin; the monitoring owner
		// applies its stricter active-scope contract without changing that owner.
		disabled, e := g.w.Get(g.ctx, g.admin, g.f.Workspace, false)
		if e != nil || disabled.Status != "disabled" {
			t.Fatal("ordinary Admin directory inspection changed", e)
		}
		beforeDisabledRead := g.count("SELECT COUNT(*) FROM audit_events")
		out, e := g.s.Get(g.ctx, g.admin, g.f.Workspace, "", uuid.NewString())
		if e == nil || e.Error() != "ACCESS_DENIED" || !reflect.DeepEqual(out, monitoring.Snapshot{}) || g.count("SELECT COUNT(*) FROM audit_events") != beforeDisabledRead {
			t.Fatal("disabled Workspace monitoring exposed projection or appended audit", e)
		}
		g.exec(`UPDATE workspaces SET status='active' WHERE id=$1`, `UPDATE workspaces SET status='active' WHERE id=?`, g.f.Workspace)
		recovered := g.get()
		if recovered.WorkspaceID != g.f.Workspace || g.count("SELECT COUNT(*) FROM audit_events") != beforeDisabledRead+1 {
			t.Fatal("explicit active Workspace read did not recover with one audit")
		}
		g.exec(`UPDATE tenants SET status='disabled' WHERE id=$1`, `UPDATE tenants SET status='disabled' WHERE id=?`, g.f.Tenant)
		g.denied(g.admin, g.f.Workspace)
		g.exec(`UPDATE tenants SET status='active' WHERE id=$1`, `UPDATE tenants SET status='active' WHERE id=?`, g.f.Tenant)
		g.exec(`DELETE FROM sessions WHERE id=$1`, `DELETE FROM sessions WHERE id=?`, g.admin.SessionID)
		g.denied(g.admin, g.f.Workspace)
		g.admin, _ = g.session("admin")
		g.exec(`UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1`, `UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?`, g.f.User)
		g.denied(g.admin, g.f.Workspace)
	})
	t.Run("real_audit_rollback_committed_ack_unknown_zero", func(t *testing.T) {
		g := setup(t, true)
		before := g.count("SELECT COUNT(*) FROM audit_events")
		var beforeClock time.Time
		if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(g.f.Store.Dialect(), "SELECT observed_at FROM platform_license_state WHERE id=1")).Scan(&beforeClock); e != nil {
			t.Fatal(e)
		}
		f := &faultStore{Store: g.f.Store}
		g.i.Storage = f
		f.armed.Store(true)
		out, e := g.s.Get(g.ctx, g.admin, g.f.Workspace, "", uuid.NewString())
		if e == nil || !reflect.DeepEqual(out, monitoring.Snapshot{}) || g.count("SELECT COUNT(*) FROM audit_events") != before {
			t.Fatal("actual rollback or zero", e)
		}
		var afterClock time.Time
		if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(g.f.Store.Dialect(), "SELECT observed_at FROM platform_license_state WHERE id=1")).Scan(&afterClock); e != nil || !afterClock.Equal(beforeClock) {
			t.Fatal("trusted-clock update escaped actual audit rollback", e)
		}
		f.unknown = true
		f.armed.Store(true)
		out, e = g.s.Get(g.ctx, g.admin, g.f.Workspace, "", uuid.NewString())
		if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(out, monitoring.Snapshot{}) || g.count("SELECT COUNT(*) FROM audit_events") != before+1 {
			t.Fatal("actual commit ACK unknown", e)
		}
		f.joined = true
		f.armed.Store(true)
		out, e = g.s.Get(g.ctx, g.admin, g.f.Workspace, "", uuid.NewString())
		if e == nil || e.Error() != string(transaction.CommitUnknown) || !reflect.DeepEqual(out, monitoring.Snapshot{}) || g.count("SELECT COUNT(*) FROM audit_events") != before+2 {
			t.Fatal("joined actual commit ACK unknown lost precedence", e)
		}
		g.get()
		if g.count("SELECT COUNT(*) FROM audit_events") != before+3 {
			t.Fatal("read repeated mutation unexpectedly")
		}
		ctx, cancel := context.WithCancel(g.ctx)
		defer cancel()
		g.i.Storage = &cancelledDeliveryStore{Store: g.f.Store, cancel: cancel}
		out, e = g.s.Get(ctx, g.admin, g.f.Workspace, "", uuid.NewString())
		if e == nil || e.Error() != string(transaction.Cancelled) || !reflect.DeepEqual(out, monitoring.Snapshot{}) || g.count("SELECT COUNT(*) FROM audit_events") != before+4 {
			t.Fatal("known committed cancelled delivery exposed projection or replayed audit", e)
		}
		g.i.Storage = g.f.Store
		g.get()
		if g.count("SELECT COUNT(*) FROM audit_events") != before+5 {
			t.Fatal("explicit later recovery did not use one new request")
		}
	})
	t.Run("uncertain_gateway_holds_are_never_released_by_diagnostics", func(t *testing.T) {
		g := setup(t, true)
		g.access()
		g.report(71, "incomplete")
		tasks, leases := g.count("SELECT COUNT(*) FROM node_tasks"), g.count("SELECT COUNT(*) FROM gpu_leases WHERE state='held'")
		for i := 0; i < 2; i++ {
			out := g.get()
			if out.Gateway.HeldInSample != 1 || out.Gateway.UsageKnown != 0 || out.Gateway.UsageUnknown != 1 || out.Gateway.ErrorRate == nil || *out.Gateway.ErrorRate != 1 {
				t.Fatal(out.Gateway)
			}
		}
		if g.count("SELECT COUNT(*) FROM node_tasks") != tasks || g.count("SELECT COUNT(*) FROM gpu_leases WHERE state='held'") != leases || g.count("SELECT COUNT(*) FROM gateway_requests WHERE capacity_held") != 1 {
			t.Fatal("diagnostics changed physical/task authority")
		}
		g.offset.Store(int64(3 * time.Hour))
		out := g.get()
		if out.License.State != licensing.Expired || out.License.NewBusinessAllowed || out.Gateway.HeldInSample != 1 {
			t.Fatal(out)
		}
	})
	t.Run("actual_connection_lock_cancellation_and_recovery", func(t *testing.T) {
		g := setup(t, true)
		locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
				var id string
				if e := q.QueryRow(ctx, g.f.Statement(`SELECT id::text FROM users WHERE id=$1 FOR UPDATE`, `SELECT id FROM users WHERE id=? FOR UPDATE`, g.f.User)).Scan(&id); e != nil {
					return e
				}
				close(locked)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
		select {
		case <-locked:
		case e := <-done:
			t.Fatal(e)
		case <-g.ctx.Done():
			t.Fatal(g.ctx.Err())
		}
		ctx, cancel := context.WithTimeout(g.ctx, 100*time.Millisecond)
		out, e := g.s.Get(ctx, g.admin, g.f.Workspace, "", uuid.NewString())
		cancel()
		close(release)
		if e == nil || !reflect.DeepEqual(out, monitoring.Snapshot{}) {
			t.Fatal("blocked read escaped deadline", e)
		}
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-g.ctx.Done():
			t.Fatal("owned connection did not join")
		}
		g.get()
	})
	t.Run("actual_authenticated_http_bounded_workload_and_no_secret_projection", func(t *testing.T) {
		g := setup(t, true)
		handler := &monitoringhttp.Handler{Service: g.s}
		for _, query := range []string{"workspace_id=" + g.f.Workspace + "&unknown=false", "workspace_id=" + g.f.Workspace + "&workspace_id=" + g.f.Workspace, "workspace_id=" + g.f.Workspace + "&node_cursor=", "workspace_id=bad"} {
			request := httptest.NewRequest("GET", monitoringhttp.Path+"?"+query, nil)
			if value, _, e := handler.Handle(request, g.admin, uuid.NewString()); e == nil || value != nil {
				t.Fatal("query parser exposed projection")
			}
		}
		if value, _, e := handler.Handle(httptest.NewRequest("GET", monitoringhttp.Path+"?workspace_id="+g.f.Workspace, nil), g.portal, uuid.NewString()); e == nil || value != nil {
			t.Fatal("Portal HTTP access")
		}
		server := httptest.NewUnstartedServer(g.m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a, e := g.i.Authenticate(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "admin")
			if e != nil {
				http.Error(w, "ACCESS_DENIED", 401)
				return
			}
			out, status, e := handler.Handle(r, a, uuid.NewString())
			if e != nil {
				http.Error(w, e.Error(), 503)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(out)
		})))
		server.EnableHTTP2 = true
		server.StartTLS()
		defer server.Close()
		client := server.Client()
		client.Timeout = 5 * time.Second
		var wg sync.WaitGroup
		failures := make(chan error, 2)
		for worker := 0; worker < 2; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 3; i++ {
					r, e := http.NewRequestWithContext(g.ctx, "GET", server.URL+monitoringhttp.Path+"?workspace_id="+g.f.Workspace, nil)
					if e != nil {
						failures <- e
						return
					}
					r.Header.Set("Authorization", "Bearer "+g.adminToken)
					resp, e := client.Do(r)
					if e != nil {
						failures <- e
						return
					}
					raw, e := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
					resp.Body.Close()
					if e != nil {
						failures <- e
						return
					}
					if resp.StatusCode != 200 || resp.ProtoMajor != 2 || strings.Contains(string(raw), g.adminToken) || strings.Contains(string(raw), "token_hash") {
						t.Error("actual authenticated HTTP/status/redaction")
					}
				}
			}()
		}
		wg.Wait()
		close(failures)
		for e := range failures {
			t.Fatal(e)
		}
		if snapshot := g.m.Snapshot(); snapshot.Inflight != 0 || len(snapshot.Series) != 1 || snapshot.Series[0].Count != 6 {
			t.Fatal(snapshot)
		}
	})
}
