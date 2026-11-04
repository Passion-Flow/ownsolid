//go:build repository_integration

// Package runtimeownerclosure_test owns real SQL acceptance, not hardware proof.
package runtimeownerclosure_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlmigrate"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	base "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/knowledge-workflow-portal"
	plugin "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/plugin-tool"
	closure "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/runtime-owner-closure"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/logstorefixture"
)

// Reused explicit schema/authority fixture arrangements from the existing paired
// component/distribution consumers. Only wire node/model/physical envelopes are
// synthetic; sessions, signatures, owning transactions and public calls are real.
type pairedComponentStory struct {
	f             repositoryfixture.Fixture
	ctx           context.Context
	owner         *identity.Service
	s             *components.Service
	admin, portal identity.Auth
	node          agent.Enrolled
	handshake     agent.Handshake
}

func pairedComponentSetup(t *testing.T) pairedComponentStory {
	t.Helper()
	f := rclFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: f.Store, HMACKey: make([]byte, 32)}
	if _, e := rand.Read(owner.HMACKey); e != nil {
		t.Fatal(e)
	}
	if f.PostgreSQL != nil {
		owner.Storage = nil
		owner.DB = f.PostgreSQL
	}
	owner.Config.Server.AdminOrigin = "https://admin.example.invalid"
	owner.Config.Server.PortalOrigin = "https://portal.example.invalid"
	n := &nodes.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: &workspaces.Service{DB: f.PostgreSQL, Identity: owner}}
	s := &components.Service{Nodes: n, LogStore: &logstorefixture.Memory{}}
	n.Components = s
	g := pairedComponentStory{f: f, ctx: ctx, owner: owner, s: s}
	g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", f.User, f.Tenant, f.Workspace)
	g.admin = g.session(t, "admin")
	g.portal = g.session(t, "portal")
	join, e := n.MintForProfile(ctx, g.admin, f.Workspace, "Synthetic paired component node", agent.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.handshake = agent.Handshake{ProtocolVersion: agent.MaintenanceVersion, ExecutionProfile: agent.SystemdMaintenanceProfile, Capabilities: agent.Capabilities(agent.SystemdMaintenanceProfile), Facts: agent.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-component-sql", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: agent.AgentVersion}}
	g.node, e = n.Enroll(ctx, agent.Enrollment{JoinToken: join.Token, Handshake: g.handshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return g
}
func (g pairedComponentStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual component fixture", e)
	}
}
func (g pairedComponentStory) count(t *testing.T, pg, my string, args ...any) int {
	t.Helper()
	var count int
	if e := g.f.Store.QueryRow(g.ctx, g.f.Statement(pg, my, args...)).Scan(&count); e != nil {
		t.Fatal(e)
	}
	return count
}
func (g pairedComponentStory) session(t *testing.T, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = g.owner.IssueSessionWithin(ctx, q, g.f.User, 1, audience, "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := g.owner.Authenticate(g.ctx, issued.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (g pairedComponentStory) request(t *testing.T, action string) nodes.Task {
	t.Helper()
	out, e := g.s.Request(g.ctx, g.admin, g.node.NodeID, action, uuid.NewString(), "Explicit synthetic component intent", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func (g pairedComponentStory) claim(t *testing.T, task nodes.Task) agent.Claim {
	t.Helper()
	out, e := g.s.Nodes.Claim(g.ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	if e != nil || out.Lease == nil || out.Lease.TaskID != task.ID || out.Lease.Component == nil {
		t.Fatal("actual component lease", e)
	}
	return out
}
func (g pairedComponentStory) observe(t *testing.T, o agent.ComponentObservation) {
	t.Helper()
	h := g.handshake
	h.Component = &o
	if _, e := g.s.Nodes.Heartbeat(g.ctx, g.node.NodeID, g.node.Credential, h); e != nil {
		t.Fatal(e)
	}
}
func (g pairedComponentStory) installed(t *testing.T) {
	t.Helper()
	c := agent.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	g.observe(t, maintenanceObservation("1.12.0", &c))
}
func (g pairedComponentStory) complete(t *testing.T, task nodes.Task, o agent.ComponentObservation, logs *agent.ComponentLogs) {
	t.Helper()
	claim := g.claim(t, task)
	result := agent.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o, Logs: logs}
	if o.ErrorCode != "" {
		result.Outcome = "failure"
		result.ErrorCode = o.ErrorCode
	}
	if _, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, result, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
}
func pairedComponentCode(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatalf("want %s got %v", code, e)
	}
}
func (g pairedComponentStory) auditFailure(t *testing.T, action string) func() {
	t.Helper()
	g.exec(t, "CREATE FUNCTION paired_component_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='"+action+"' THEN RAISE EXCEPTION 'explicit component audit failure'; END IF; RETURN NEW; END;$$", "CREATE TRIGGER paired_component_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='"+action+"' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='explicit component audit failure'; END IF; END")
	if g.f.Store.Dialect() == transaction.PostgreSQL {
		g.exec(t, "CREATE TRIGGER paired_component_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION paired_component_audit_failure()", "")
	}
	dropped := false
	drop := func() {
		if !dropped {
			g.exec(t, "DROP TRIGGER paired_component_audit_failure ON audit_events", "DROP TRIGGER paired_component_audit_failure")
			dropped = true
		}
	}
	t.Cleanup(drop)
	return drop
}

type pairedComponentOutcomeStore struct {
	transaction.Store
	calls, at int
}

func (s *pairedComponentOutcomeStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls++
	e := s.Store.Within(ctx, ro, fn)
	if e == nil && s.calls == s.at {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}
func (g pairedComponentStory) wrapped(store transaction.Store) *components.Service {
	owner := *g.owner
	owner.DB = nil
	owner.Storage = store
	n := &nodes.Service{Identity: &owner, Workspaces: &workspaces.Service{Identity: &owner}}
	s := &components.Service{Nodes: n, LogStore: g.s.LogStore, ArchiveFile: g.s.ArchiveFile, CatalogDirectory: g.s.CatalogDirectory}
	n.Components = s
	return s
}
func pairedComponentLogs() agent.ComponentLogs {
	now := time.Now().UTC()
	return agent.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute), Entries: []agent.ComponentLogEntry{{Timestamp: now, Priority: 6, Message: "SYNTHETIC_COMPONENT_LOG_SENTINEL <script>text</script>"}}}
}

type pairedDistributionStory struct {
	f                            repositoryfixture.Fixture
	ctx                          context.Context
	owner                        *identity.Service
	s                            *distribution.Service
	admin, portal                identity.Auth
	node, model, deployment, gpu string
	license                      *licensing.Service
	trust                        licensewire.Trust
	gateway                      distribution.Provisioned
	alias                        distribution.Alias
	key                          distribution.MintedKey
}

func pairedDistributionSetup(t *testing.T) pairedDistributionStory {
	t.Helper()
	f := rclFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	owner := &identity.Service{Storage: f.Store, HMACKey: make([]byte, 32)}
	if _, e := rand.Read(owner.HMACKey); e != nil {
		t.Fatal(e)
	}
	if f.PostgreSQL != nil {
		owner.Storage = nil
		owner.DB = f.PostgreSQL
	}
	owner.Config.Server.AdminOrigin = "https://admin.example.invalid"
	owner.Config.Server.PortalOrigin = "https://portal.example.invalid"
	w := &workspaces.Service{DB: f.PostgreSQL, Identity: owner}
	n := &nodes.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: w}
	m := &models.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: w}
	d := &deployments.Service{Nodes: n, Models: m}
	n.Inference = d
	s := &distribution.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: w, Nodes: n, Deployments: d}
	g := pairedDistributionStory{f: f, ctx: ctx, owner: owner, s: s, model: uuid.NewString(), deployment: uuid.NewString(), gpu: "GPU-" + uuid.NewString()}
	g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", f.User, f.Tenant, f.Workspace)
	g.admin = g.session(t, "admin")
	g.portal = g.session(t, "portal")
	private, trust, e := licensewire.GenerateKey("synthetic-distribution-paired-root")
	if e != nil {
		t.Fatal(e)
	}
	g.trust = trust
	g.license, e = licensing.New(owner, trust, licensewire.DefaultPolicy(), func() time.Time { return time.Now().UTC() })
	if e != nil {
		t.Fatal(e)
	}
	owner.BusinessAdmission = g.license
	state, e := g.license.StatusAdmin(ctx, g.admin)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Explicit synthetic distribution SQL fixture", DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339), GraceUntil: now.Add(25 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	raw, e := licensewire.Issue(claims, private)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = g.license.Import(ctx, g.admin, licensing.ImportInput{Artifact: string(raw), Reason: "Actual signed synthetic distribution license"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	token, e := n.MintForProfile(ctx, g.admin, f.Workspace, "Synthetic distribution target", agent.DockerInferenceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := agent.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-distribution", LogicalCPUs: 4, MemoryTotalBytes: 16 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: agent.AgentVersion}
	enrolled, e := n.Enroll(ctx, agent.Enrollment{JoinToken: token.Token, Handshake: agent.Handshake{ProtocolVersion: agent.InferenceVersion, ExecutionProfile: agent.DockerInferenceProfile, Capabilities: agent.Capabilities(agent.DockerInferenceProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.node = enrolled.NodeID
	manifest, _ := json.Marshal(agent.ApprovedModel())
	g.exec(t, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,'Synthetic scoped directory model','qwen3-0.6b',$4,$5::jsonb,'ready',$6)`, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES(?,?,?,'Synthetic scoped directory model','qwen3-0.6b',?,CAST(? AS JSON),'ready',?)`, g.model, f.Tenant, f.Workspace, agent.ModelCatalogDigest(), manifest, f.User)
	// Constrained synthetic ready deployment/GPU/verification envelope. Actual
	// authorization, SQL, signatures and sessions run; no physical inference runs.
	observation := g.observation(1, true)
	if e := (agent.InferenceObservation{GPUs: []agent.NVIDIAGPU{}, Deployment: &observation, ObservedAt: time.Now().UTC()}).Validate(); e != nil {
		t.Fatal("constrained synthetic ready observation", e)
	}
	encoded, _ := json.Marshal(observation)
	g.exec(t, `INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,observed_state,revision,observation,observed_at,created_by) VALUES($1,$2,$3,$4,$5,$6,'running','running',1,$7::jsonb,clock_timestamp(),$8)`, `INSERT INTO deployments(id,tenant_id,workspace_id,node_id,model_id,gpu_uuid,desired_state,observed_state,revision,observation,observed_at,created_by) VALUES(?,?,?,?,?,?,'running','running',1,CAST(? AS JSON),UTC_TIMESTAMP(6),?)`, g.deployment, f.Tenant, f.Workspace, g.node, g.model, g.gpu, encoded, f.User)
	g.exec(t, `INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) VALUES($1,$2,$3,$4,$5,$1,1,'held')`, `INSERT INTO gpu_leases(id,tenant_id,workspace_id,node_id,gpu_uuid,deployment_id,revision,state) VALUES(?,?,?,?,?,?,1,'held')`, g.leaseArgs()...)
	return g
}
func (g pairedDistributionStory) leaseArgs() []any {
	if g.f.Store.Dialect() == transaction.MySQL {
		return []any{g.deployment, g.f.Tenant, g.f.Workspace, g.node, g.gpu, g.deployment}
	}
	return []any{g.deployment, g.f.Tenant, g.f.Workspace, g.node, g.gpu}
}
func (g pairedDistributionStory) sql(pg, my string, args ...any) transaction.Statement {
	return g.f.Statement(pg, my, args...)
}
func (g pairedDistributionStory) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, g.sql(pg, my, args...)); e != nil {
		t.Fatal("actual constrained distribution fixture", e)
	}
}
func (g pairedDistributionStory) count(t *testing.T, pg, my string, args ...any) int {
	t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, g.sql(pg, my, args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func (g pairedDistributionStory) session(t *testing.T, audience string) identity.Auth {
	return g.sessionAs(t, g.f.User, audience)
}
func (g pairedDistributionStory) sessionAs(t *testing.T, user, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = g.owner.IssueSessionWithin(ctx, q, user, 1, audience, "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := g.owner.Authenticate(g.ctx, issued.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (g pairedDistributionStory) provisionInput() distribution.ProvisionRequest {
	return distribution.ProvisionRequest{WorkspaceID: g.f.Workspace, NodeID: g.node, Name: "Synthetic gateway \u4e16\u754c", ClientOrigin: "https://gateway.example.invalid", Reason: "Actual scoped gateway intent"}
}
func (g pairedDistributionStory) observation(revision int64, ready bool) agent.DeploymentObservation {
	o := agent.DeploymentObservation{DeploymentID: g.deployment, ModelID: g.model, CatalogDigest: agent.ModelCatalogDigest(), GPUUUID: g.gpu, Revision: revision, Managed: true, State: "running", Ready: ready, ContainerID: strings.Repeat("a", 64), ImageID: agent.ApprovedInferenceRecipe().ImageConfigDigest}
	if ready {
		o.Verification = &agent.GenerationVerification{Model: agent.InferenceModelName, PromptSHA256: agent.InferencePromptDigest(), ResponseSHA256: strings.Repeat("c", 64), InputTokens: 1, OutputTokens: 1, ObservedAt: time.Now().UTC()}
	} else {
		o.State = "stopped"
	}
	return o
}
func (g pairedDistributionStory) access(t *testing.T) pairedDistributionStory {
	t.Helper()
	var e error
	g.gateway, e = g.s.Provision(g.ctx, g.admin, g.provisionInput(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.alias, e = g.s.Publish(g.ctx, g.admin, distribution.PublishRequest{WorkspaceID: g.f.Workspace, GatewayID: g.gateway.Gateway.ID, Name: "scoped-model", DeploymentID: g.deployment, DeploymentRevision: 1, Reason: "Actual exact target publication"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.key, e = g.s.MintKey(g.ctx, g.portal, g.keyInput(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return g
}
func (g pairedDistributionStory) keyInput() distribution.KeyRequest {
	return distribution.KeyRequest{WorkspaceID: g.f.Workspace, AliasID: g.alias.ID, Label: "Synthetic API key", TTLSeconds: 300, Reason: "Actual caller key"}
}
func (g pairedDistributionStory) admission() wire.AdmissionRequest {
	return wire.AdmissionRequest{RequestID: uuid.NewString(), APIKey: *g.key.Secret, Model: g.alias.Name, MaximumOutputTokens: 16}
}
func pairedDistributionSuccess(id string) wire.Outcome {
	return wire.Outcome{RequestID: id, Status: "succeeded", Usage: &wire.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}, ElapsedMilliseconds: 1, OutputStarted: true}
}
func pairedDistributionCode(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || e.Error() != code {
		t.Fatal("expected", code, "received", e)
	}
}
func (g pairedDistributionStory) auditFailure(t *testing.T, action string) func() {
	t.Helper()
	g.exec(t, "CREATE FUNCTION paired_distribution_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='"+action+"' THEN RAISE EXCEPTION 'explicit distribution audit failure'; END IF; RETURN NEW; END;$$", "CREATE TRIGGER paired_distribution_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='"+action+"' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='explicit distribution audit failure'; END IF; END")
	if g.f.Store.Dialect() == transaction.PostgreSQL {
		g.exec(t, "CREATE TRIGGER paired_distribution_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION paired_distribution_audit_failure()", "")
	}
	dropped := false
	drop := func() {
		if !dropped {
			g.exec(t, "DROP TRIGGER paired_distribution_audit_failure ON audit_events", "DROP TRIGGER paired_distribution_audit_failure")
			dropped = true
		}
	}
	t.Cleanup(drop)
	return drop
}

type pairedDistributionOutcomeStore struct {
	transaction.Store
	calls, at int
}

func (s *pairedDistributionOutcomeStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	s.calls++
	e := s.Store.Within(ctx, ro, fn)
	if e == nil && s.calls == s.at {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}
func (g pairedDistributionStory) wrapped(store transaction.Store) *distribution.Service {
	owner := *g.owner
	owner.DB = nil
	owner.Storage = store
	w := &workspaces.Service{Identity: &owner}
	n := &nodes.Service{Identity: &owner, Workspaces: w}
	m := &models.Service{Identity: &owner, Workspaces: w}
	d := &deployments.Service{Nodes: n, Models: m}
	n.Inference = d
	return &distribution.Service{Identity: &owner, Workspaces: w, Nodes: n, Deployments: d}
}
func (g pairedDistributionStory) held(t *testing.T, id string, want bool) {
	t.Helper()
	var held bool
	if e := g.f.Store.QueryRow(g.ctx, g.sql("SELECT capacity_held FROM gateway_requests WHERE id=$1", "SELECT capacity_held FROM gateway_requests WHERE id=?", id)).Scan(&held); e != nil || held != want {
		t.Fatal("actual capacity", held, e)
	}
}
func (g pairedDistributionStory) fence(id string) wire.FenceReceipt {
	digest := sha256.Sum256([]byte(g.owner.Config.Server.AdminOrigin))
	now := uint64(time.Now().UnixMilli())
	return wire.FenceReceipt{SchemaVersion: 1, ChallengeID: id, GatewayID: g.gateway.Gateway.ID, NodeID: g.node, ControlOriginSHA256: hex.EncodeToString(digest[:]), StateFenceSHA256: strings.Repeat("d", 64), FencedAtUnixMilliseconds: now, CheckedAtUnixMilliseconds: now, ExclusiveStateLockConfirmed: true, PersistentFenceConfirmed: true, GatewayVersion: "0.7.0"}
}
func (g pairedDistributionStory) stopEnvelope(t *testing.T, revision int64) int64 {
	t.Helper()
	task := uuid.NewString()
	next := revision + 1
	o := g.observation(next, false)
	if e := (agent.InferenceObservation{GPUs: []agent.NVIDIAGPU{}, Deployment: &o, ObservedAt: time.Now().UTC()}).Validate(); e != nil {
		t.Fatal("constrained synthetic stop observation", e)
	}
	raw, _ := json.Marshal(o)
	g.exec(t, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,created_at,completed_at,payload) VALUES($1,$2,$3,$4,$5,'succeeded',$6,$7,clock_timestamp()+interval '1 minute',clock_timestamp(),clock_timestamp(),'{"action":"stop"}'::jsonb)`, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at,created_at,completed_at,payload) VALUES(?,?,?,?,?,'succeeded',?,?,TIMESTAMPADD(MINUTE,1,UTC_TIMESTAMP(6)),UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),CAST('{"action":"stop"}' AS JSON))`, task, g.f.Tenant, g.f.Workspace, g.node, agent.InferenceOperation("stop"), uuid.NewString(), g.f.User)
	g.exec(t, `UPDATE deployments SET desired_state='stopped',observed_state='stopped',revision=$1,observation=$2::jsonb,observed_at=clock_timestamp(),last_task_id=$3 WHERE id=$4`, `UPDATE deployments SET desired_state='stopped',observed_state='stopped',revision=?,observation=CAST(? AS JSON),observed_at=UTC_TIMESTAMP(6),last_task_id=? WHERE id=?`, next, raw, task, g.deployment)
	g.exec(t, "UPDATE gpu_leases SET state='released',revision=$1 WHERE deployment_id=$2", "UPDATE gpu_leases SET state='released',revision=? WHERE deployment_id=?", next, g.deployment)
	return next
}

func maintenanceObservation(version string, config *agent.ComponentConfig) agent.ComponentObservation {
	return agent.ComponentObservation{Component: agent.NodeExporter, Version: version, State: "running", Managed: true, ObservedAt: time.Now().UTC(), Config: config, Metrics: &agent.ComponentMetrics{CPUSecondsTotal: 100, CPUIdleSecondsTotal: 80, MemoryTotalBytes: 1024, MemoryAvailableBytes: 512, FilesystemTotalBytes: 4096, FilesystemAvailableBytes: 2048}}
}

// Preinstalled mode is exclusive to separately orchestrated retained-graph/DML
// gates, after an actual203 migration. It never claims raw setup as migration.
func rclFixture(t *testing.T) repositoryfixture.Fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	mode := os.Getenv("PROJECT_ONE_RCL_PREINSTALLED_SCHEMA")
	if mode == "false" {
		mode = ""
	}
	if mode != "" && mode != "true" {
		t.Fatal("RCL_EXPLICIT_PREINSTALLED_MODE_REQUIRED")
	}
	var f repositoryfixture.Fixture
	if mode == "" {
		f = repositoryfixture.New(t)
	} else {
		if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "mysql" {
			t.Fatal("RCL_PREINSTALLED_MYSQL_ONLY")
		}
		c, e := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
		if e != nil || c.Host != "127.0.0.1" || !strings.HasPrefix(c.Database, "m20_repository_") || !strings.HasSuffix(c.Database, "_test") {
			t.Fatal("RCL_ISOLATED_PREINSTALLED_CONFIGURATION_REQUIRED")
		}
		k, _, e := mysqlstore.Open(t.Context(), c)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if e := k.Close(); e != nil {
				t.Error(e)
			}
		})
		store, e := transaction.FromMySQL(k)
		if e != nil {
			t.Fatal(e)
		}
		var count, applied int
		if e = store.QueryRow(t.Context(), transaction.SQL(transaction.MySQL, "SELECT COUNT(*),SUM(state='applied') FROM schema_migrations")).Scan(&count, &applied); e != nil || count != 203 || applied != 203 {
			t.Fatal("RCL_ACTUAL203_APPLIED_JOURNAL_REQUIRED", e)
		}
		f = repositoryfixture.Fixture{Store: store, Tenant: uuid.NewString(), Workspace: uuid.NewString(), User: uuid.NewString()}
		if e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			for _, s := range []transaction.Statement{
				f.Statement("", "INSERT INTO tenants(id,name,status) VALUES(?,'RCL actual isolated tenant','active')", f.Tenant),
				f.Statement("", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'RCL actual Workspace','active')", f.Workspace, f.Tenant),
				f.Statement("", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'RCL actual actor','en','active',true)", f.User, f.User+"@example.invalid", f.User+"@example.invalid"),
				f.Statement("", "UPDATE installation_state SET bootstrap_complete=true,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=UTC_TIMESTAMP(6) WHERE id=1", f.Tenant, f.Workspace, f.User),
			} {
				if _, e := q.Exec(ctx, s); e != nil {
					return e
				}
			}
			return nil
		}); e != nil {
			t.Fatal("RCL actual DML seed", e)
		}
	}
	if f.Store.Dialect() != transaction.MySQL || mode != "" {
		return f
	}
	catalog, e := mysqlmigrate.DefaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	if catalog.Steps() != 149 && catalog.Steps() != 194 && catalog.Steps() != 203 {
		t.Fatal("RCL_EXACT_REVIEWED_ACTIVE_PREFIX_REQUIRED")
	}
	type declaration struct {
		Step   int    `json:"step"`
		File   string `json:"file"`
		Source string `json:"source_sha256"`
	}
	for _, files := range []fs.FS{base.Files, plugin.Files, closure.Files} {
		raw, e := fs.ReadFile(files, "catalog.json")
		var v struct {
			Steps []declaration `json:"steps"`
		}
		if e != nil || json.Unmarshal(raw, &v) != nil {
			t.Fatal("RCL_ACTUAL_CANDIDATE_CATALOG_REQUIRED")
		}
		for _, entry := range v.Steps {
			raw, e := fs.ReadFile(files, entry.File)
			sum := sha256.Sum256(raw)
			if e != nil || hex.EncodeToString(sum[:]) != entry.Source {
				t.Fatal("RCL_ACTUAL_CANDIDATE_SOURCE_CHANGED")
			}
			if entry.Step <= catalog.Steps() {
				continue
			}
			if _, e = f.Store.Exec(t.Context(), transaction.SQL(transaction.MySQL, string(raw))); e != nil {
				t.Fatal("RCL actual candidate setup", entry.Step, e)
			}
		}
	}
	t.Log("RCL schema route: actual active journal prefix plus exact inactive candidate DDL setup; no migration qualification")
	return f
}

func refusal(t *testing.T, f repositoryfixture.Fixture, pg, my string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		_, e := q.Exec(ctx, f.Statement(pg, my, args...))
		return e
	})
	var constraint *transaction.Error
	if !errors.As(e, &constraint) || !(constraint.Kind == transaction.ForeignKeyConflict || constraint.Kind == transaction.CheckConflict || constraint.SQLState == "P0001" || constraint.Number == 1644) {
		t.Fatal("RCL_REAL_CONSTRAINT_NOT_SYNTAX_OR_CONNECTION_REFUSAL_REQUIRED", e)
	}
}

// Existing foreign node/task/Workspace rows make a bare-ID FK insufficient.
func (g pairedComponentStory) foreignTask(t *testing.T) (string, nodes.Task) {
	t.Helper()
	workspace, e := g.s.Nodes.Workspaces.Create(g.ctx, g.admin, "RCL actual other Workspace", g.f.User, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	join, e := g.s.Nodes.MintForProfile(g.ctx, g.admin, workspace.ID, "RCL actual other node", agent.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	enrolled, e := g.s.Nodes.Enroll(g.ctx, agent.Enrollment{JoinToken: join.Token, Handshake: g.handshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	task, e := g.s.Request(g.ctx, g.admin, enrolled.NodeID, "start", uuid.NewString(), "RCL real other node task", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return workspace.ID, task
}
