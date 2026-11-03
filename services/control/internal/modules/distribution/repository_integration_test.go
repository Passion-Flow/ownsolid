//go:build repository_integration

package distribution_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	distributionhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

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
	f := repositoryfixture.New(t)
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

func TestActualPairedDistributionRuntime(t *testing.T) {
	t.Run("gateway_one_time_secret_exact_intent_and_atomic_audit", func(t *testing.T) {
		g := pairedDistributionSetup(t)
		input := g.provisionInput()
		key := uuid.NewString()
		drop := g.auditFailure(t, "gateway.instance_created")
		v, e := g.s.Provision(g.ctx, g.admin, input, key, uuid.NewString())
		pairedDistributionCode(t, e, "STORAGE_UNAVAILABLE")
		if !reflect.DeepEqual(v, distribution.Provisioned{}) || g.count(t, "SELECT COUNT(*) FROM gateway_instances", "SELECT COUNT(*) FROM gateway_instances") != 0 {
			t.Fatal("failed secret/state projected")
		}
		drop()
		v, e = g.s.Provision(g.ctx, g.admin, input, key, uuid.NewString())
		if e != nil || v.Secret == nil || !v.SecretAvailable || !v.Created {
			t.Fatal(e)
		}
		replay, e := g.s.Provision(g.ctx, g.admin, input, key, uuid.NewString())
		if e != nil || replay.Gateway.ID != v.Gateway.ID || replay.Secret != nil || replay.SecretAvailable || replay.Created {
			t.Fatal("secret replay", e)
		}
		_, e = g.s.Provision(g.ctx, g.admin, input, uuid.NewString(), uuid.NewString())
		pairedDistributionCode(t, e, "GATEWAY_INSTANCE_EXISTS")
		input.Name = "Changed exact request"
		_, e = g.s.Provision(g.ctx, g.admin, input, key, uuid.NewString())
		pairedDistributionCode(t, e, "IDEMPOTENCY_CONFLICT")
		_, e = g.s.Provision(g.ctx, g.portal, input, uuid.NewString(), uuid.NewString())
		pairedDistributionCode(t, e, "ACCESS_DENIED")
	})
	t.Run("alias_key_UTC_pages_scoped_strict_cursors_and_secretless_history", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		if !g.alias.Available || g.key.Secret == nil {
			t.Fatal("actual access")
		}
		_, e := g.s.MintKey(g.ctx, g.portal, g.keyInput(), uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		p, e := g.s.KeyPage(g.ctx, g.portal, g.f.Workspace, url.Values{"limit": {"1"}})
		if e != nil || len(p.Items) != 1 || p.NextCursor == nil || p.Items[0].CreatedAt.Location() != time.UTC {
			t.Fatal(e)
		}
		next, e := g.s.KeyPage(g.ctx, g.portal, g.f.Workspace, url.Values{"limit": {"1"}, "cursor": {*p.NextCursor}})
		if e != nil || len(next.Items) != 1 || next.Items[0].ID == p.Items[0].ID {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(p)
		if bytes.Contains(raw, []byte(*g.key.Secret)) || bytes.Contains(raw, []byte("secret_hash")) {
			t.Fatal("key metadata leaked")
		}
		for _, values := range []url.Values{{"limit": {""}}, {"cursor": {""}}, {"limit": {"1", "2"}}, {"unknown": {"1"}}} {
			p, e := g.s.KeyPage(g.ctx, g.portal, g.f.Workspace, values)
			if e == nil || len(p.Items) != 0 || p.NextCursor != nil {
				t.Fatal("strict cursor refusal")
			}
		}
		_, e = g.s.Alias(g.ctx, g.portal, uuid.NewString(), g.alias.ID)
		if e == nil {
			t.Fatal("foreign scope")
		}
		history, e := g.s.TargetHistory(g.ctx, g.admin, g.f.Workspace, g.alias.ID, url.Values{})
		if e != nil || len(history.Items) != 1 || history.Items[0].ModelID != g.model {
			t.Fatal(e)
		}
	})
	t.Run("real_signed_license_machine_admission_and_canonical_outcome_replay", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		models, e := g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *g.key.Secret)
		if e != nil || len(models.Models) != 1 || models.Models[0].ID != g.alias.ID {
			t.Fatal(e)
		}
		input := g.admission()
		out, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString())
		if e != nil || out.RequestID != input.RequestID {
			t.Fatal(e)
		}
		g.held(t, input.RequestID, true)
		_, e = g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString())
		pairedDistributionCode(t, e, "GATEWAY_REQUEST_EXISTS")
		outcome := pairedDistributionSuccess(input.RequestID)
		ack, e := g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, outcome, uuid.NewString())
		digest, _ := outcome.Digest()
		if e != nil || ack.CanonicalSHA256 != digest {
			t.Fatal(e)
		}
		g.held(t, input.RequestID, false)
		replay, e := g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, outcome, uuid.NewString())
		if e != nil || replay != ack {
			t.Fatal(e)
		}
		outcome.OutputStarted = false
		_, e = g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, outcome, uuid.NewString())
		pairedDistributionCode(t, e, "GATEWAY_OUTCOME_CONFLICT")
		if g.count(t, "SELECT COUNT(*) FROM audit_events WHERE action='gateway.request_reported' AND target_id=$1", "SELECT COUNT(*) FROM audit_events WHERE action='gateway.request_reported' AND target_id=?", input.RequestID) != 1 {
			t.Fatal("replayed audit")
		}
	})
	t.Run("revoked_key_denies_new_work_existing_admission_still_reports", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		input := g.admission()
		if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e := g.s.RevokeKey(g.ctx, g.portal, g.f.Workspace, g.key.Key.ID, "Explicit current key revocation", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		m, e := g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *g.key.Secret)
		pairedDistributionCode(t, e, "API_KEY_INVALID")
		if !reflect.DeepEqual(m, wire.ModelsAuthorization{}) {
			t.Fatal("revoked key projection")
		}
		if _, e = g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, pairedDistributionSuccess(input.RequestID), uuid.NewString()); e != nil {
			t.Fatal("inflight receipt", e)
		}
	})
	t.Run("current_membership_lifetime_epoch_and_workspace_fences", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		g.exec(t, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", "DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?", g.f.Workspace, g.f.User)
		_, e := g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *g.key.Secret)
		pairedDistributionCode(t, e, "FORBIDDEN")
		g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'viewer')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'viewer')", g.f.User, g.f.Tenant, g.f.Workspace)
		_, e = g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *g.key.Secret)
		pairedDistributionCode(t, e, "API_KEY_INVALID")
		fresh, e := g.s.MintKey(g.ctx, g.portal, g.keyInput(), uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal("new lifetime key", e)
		}
		g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
		_, e = g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *fresh.Secret)
		pairedDistributionCode(t, e, "API_KEY_INVALID")
	})
	t.Run("node_GPU_model_and_current_answer_snapshot_fences", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		recheck := func(snapshot distribution.Alias) error {
			return g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
				return g.s.RecheckAnswerAliasWithin(ctx, q, g.portal, g.f.Workspace, snapshot)
			})
		}
		if e := recheck(g.alias); e != nil {
			t.Fatal(e)
		}
		bad := g.alias
		bad.Revision++
		pairedDistributionCode(t, recheck(bad), "MODEL_ALIAS_UNAVAILABLE")
		g.exec(t, "UPDATE gpu_leases SET revision=revision+1 WHERE deployment_id=$1", "UPDATE gpu_leases SET revision=revision+1 WHERE deployment_id=?", g.deployment)
		_, e := g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *g.key.Secret)
		pairedDistributionCode(t, e, "MODEL_ALIAS_UNAVAILABLE")
		g.exec(t, "UPDATE gpu_leases SET revision=1 WHERE deployment_id=$1", "UPDATE gpu_leases SET revision=1 WHERE deployment_id=?", g.deployment)
		g.exec(t, "UPDATE model_versions SET state='deleted' WHERE id=$1", "UPDATE model_versions SET state='deleted' WHERE id=?", g.model)
		_, e = g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *g.key.Secret)
		if e == nil {
			t.Fatal("deleted model authorized")
		}
	})
	t.Run("transient_operation_key_single_use_and_cleanup_after_epoch_revocation", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		var keyID string
		e := g.s.WithTransientKey(g.ctx, g.portal, g.keyInput(), 3*time.Second, uuid.NewString(), func(raw string) error {
			pieces := strings.Split(raw, ".")
			keyID = pieces[1]
			input := g.admission()
			input.APIKey = raw
			if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString()); e != nil {
				return e
			}
			input.RequestID = uuid.NewString()
			_, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString())
			pairedDistributionCode(t, e, "API_KEY_INVALID")
			g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", g.f.User)
			return nil
		})
		if e != nil {
			t.Fatal(e)
		}
		if g.count(t, "SELECT COUNT(*) FROM api_keys WHERE id=$1 AND revoked_at IS NOT NULL", "SELECT COUNT(*) FROM api_keys WHERE id=? AND revoked_at IS NOT NULL", keyID) != 1 {
			t.Fatal("independent exact reduction")
		}
	})
	t.Run("license_expiry_blocks_new_ordinary_work_but_preserves_receipts", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		input := g.admission()
		if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		expired, e := licensing.New(g.owner, g.trust, licensewire.DefaultPolicy(), func() time.Time { return time.Now().UTC().Add(48 * time.Hour) })
		if e != nil {
			t.Fatal(e)
		}
		g.owner.BusinessAdmission = expired
		out, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, g.admission(), uuid.NewString())
		pairedDistributionCode(t, e, "LICENSE_NEW_BUSINESS_UNAVAILABLE")
		if !reflect.DeepEqual(out, wire.Admission{}) {
			t.Fatal("expired license admission")
		}
		if _, e = g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, pairedDistributionSuccess(input.RequestID), uuid.NewString()); e != nil {
			t.Fatal("admitted receipt", e)
		}
	})
	t.Run("unknown_commit_withholds_human_secrets_admission_and_acknowledgment", func(t *testing.T) {
		g := pairedDistributionSetup(t)
		input := g.provisionInput()
		intent := uuid.NewString()
		unknown := g.wrapped(&pairedDistributionOutcomeStore{Store: g.f.Store, at: 1})
		out, e := unknown.Provision(g.ctx, g.admin, input, intent, uuid.NewString())
		pairedDistributionCode(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(out, distribution.Provisioned{}) {
			t.Fatal("unknown gateway secret")
		}
		replay, e := g.s.Provision(g.ctx, g.admin, input, intent, uuid.NewString())
		if e != nil || replay.Secret != nil || replay.Gateway.ID == "" {
			t.Fatal("exact metadata reconciliation", e)
		}
		// A lost gateway secret is never recovered. Explicitly revoke and use a fresh
		// intent to get a new known credential before exercising the machine owner.
		if _, e = g.s.RevokeGateway(g.ctx, g.admin, g.f.Workspace, replay.Gateway.ID, "Lost response explicit reduction", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		g = g.access(t)
		unknown = g.wrapped(&pairedDistributionOutcomeStore{Store: g.f.Store, at: 2})
		admission := g.admission()
		got, e := unknown.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, admission, uuid.NewString())
		pairedDistributionCode(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(got, wire.Admission{}) {
			t.Fatal("unknown admission projected")
		}
		g.held(t, admission.RequestID, true)
		if _, e = g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, admission, uuid.NewString()); e == nil || e.Error() != "GATEWAY_REQUEST_EXISTS" {
			t.Fatal("unknown admission replayed inference authority", e)
		}
		unknown = g.wrapped(&pairedDistributionOutcomeStore{Store: g.f.Store, at: 2})
		outcome := pairedDistributionSuccess(admission.RequestID)
		ack, e := unknown.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, outcome, uuid.NewString())
		pairedDistributionCode(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(ack, wire.Acknowledgment{}) {
			t.Fatal("unknown acknowledgment")
		}
		ack, e = g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, outcome, uuid.NewString())
		if e != nil || ack.RequestID != admission.RequestID {
			t.Fatal("exact immutable outcome reconciliation", e)
		}
		g.held(t, admission.RequestID, false)
		unknown = g.wrapped(&pairedDistributionOutcomeStore{Store: g.f.Store, at: 1})
		page, e := unknown.RequestPage(g.ctx, g.portal, g.f.Workspace, url.Values{})
		pairedDistributionCode(t, e, string(transaction.CommitUnknown))
		if len(page.Items) != 0 || page.NextCursor != nil {
			t.Fatal("unknown private page")
		}
	})
	t.Run("audit_failure_rolls_back_admission_capacity_and_immutable_outcome", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		input := g.admission()
		drop := g.auditFailure(t, "gateway.request_admitted")
		out, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString())
		pairedDistributionCode(t, e, "STORAGE_UNAVAILABLE")
		if !reflect.DeepEqual(out, wire.Admission{}) || g.count(t, "SELECT COUNT(*) FROM gateway_requests", "SELECT COUNT(*) FROM gateway_requests") != 0 {
			t.Fatal("failed admission persisted")
		}
		drop()
		if _, e = g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		drop = g.auditFailure(t, "gateway.request_reported")
		ack, e := g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, pairedDistributionSuccess(input.RequestID), uuid.NewString())
		pairedDistributionCode(t, e, "STORAGE_UNAVAILABLE")
		if !reflect.DeepEqual(ack, wire.Acknowledgment{}) {
			t.Fatal("failed report ack")
		}
		g.held(t, input.RequestID, true)
		drop()
		if _, e = g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, pairedDistributionSuccess(input.RequestID), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		g.held(t, input.RequestID, false)
	})
	t.Run("alias_pause_drain_retarget_rollback_and_historical_answer_access", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		admit := g.admission()
		if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, admit, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		paused, e := g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, distribution.CutoverRequest{Action: "pause", ExpectedRevision: 1, Reason: "Explicit pause before drain"}, uuid.NewString(), uuid.NewString())
		if e != nil || paused.Alias.DisabledAt == nil {
			t.Fatal(e)
		}
		rev := int64(1)
		in := distribution.CutoverRequest{Action: "retarget", ExpectedRevision: 2, DeploymentID: &g.deployment, DeploymentRevision: &rev, Reason: "Exact ready target after drain"}
		_, e = g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, in, uuid.NewString(), uuid.NewString())
		pairedDistributionCode(t, e, "ALIAS_REQUESTS_UNDRAINED")
		if _, e = g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, pairedDistributionSuccess(admit.RequestID), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		moved, e := g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, in, uuid.NewString(), uuid.NewString())
		if e != nil || moved.AppliedRevision != 3 {
			t.Fatal(e)
		}
		original := int64(1)
		rolled, e := g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, distribution.CutoverRequest{Action: "rollback", ExpectedRevision: 3, DeploymentID: &g.deployment, DeploymentRevision: &rev, SourceRevision: &original, Reason: "Exact immutable prior model identity"}, uuid.NewString(), uuid.NewString())
		if e != nil || rolled.AppliedRevision != 4 {
			t.Fatal(e)
		}
		resume := distribution.CutoverRequest{Action: "resume", ExpectedRevision: 4, Reason: "Reviewed ready target resume"}
		key := uuid.NewString()
		result, e := g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, resume, key, uuid.NewString())
		if e != nil || !result.Alias.Available {
			t.Fatal(e)
		}
		same, e := g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, resume, key, uuid.NewString())
		if e != nil || !same.Replayed || same.AppliedRevision != 5 {
			t.Fatal(e)
		}
		e = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			return g.s.CheckHistoricalAliasAccessWithin(ctx, q, g.portal, g.f.Workspace, g.alias)
		})
		if e != nil {
			t.Fatal("current access preserves historical content", e)
		}
		e = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			return g.s.RecheckAnswerAliasWithin(ctx, q, g.portal, g.f.Workspace, g.alias)
		})
		pairedDistributionCode(t, e, "MODEL_ALIAS_UNAVAILABLE")
		history, e := g.s.TargetHistory(g.ctx, g.admin, g.f.Workspace, g.alias.ID, url.Values{})
		if e != nil || len(history.Items) != 5 {
			t.Fatal("immutable target history", e)
		}
	})
	t.Run("recovery_requires_revocation_exact_fence_and_new_positive_stop", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		input := g.admission()
		if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		_, e := g.s.CreateRecoveryChallenge(g.ctx, g.admin, g.f.Workspace, input.RequestID, "Require actual gateway fence", uuid.NewString(), uuid.NewString())
		pairedDistributionCode(t, e, "GATEWAY_RECOVERY_REQUIRES_REVOCATION")
		if _, e = g.s.RevokeGateway(g.ctx, g.admin, g.f.Workspace, g.gateway.Gateway.ID, "Explicit fencing prerequisite", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		key := uuid.NewString()
		challenge, e := g.s.CreateRecoveryChallenge(g.ctx, g.admin, g.f.Workspace, input.RequestID, "Actual ordered recovery", key, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		replay, e := g.s.CreateRecoveryChallenge(g.ctx, g.admin, g.f.Workspace, input.RequestID, "Actual ordered recovery", key, uuid.NewString())
		if e != nil || replay.ID != challenge.ID {
			t.Fatal(e)
		}
		revision := g.stopEnvelope(t, 1)
		receipt := g.fence(challenge.ID)
		if e = receipt.Validate(); e != nil {
			t.Fatal(e)
		}
		fenced, e := g.s.AcceptRecoveryFence(g.ctx, g.admin, g.f.Workspace, input.RequestID, challenge.ID, receipt, "Explicit synthetic operator fence", uuid.NewString())
		if e != nil || fenced.FenceAcceptedAt == nil {
			t.Fatal(e)
		}
		_, e = g.s.ConfirmRecovery(g.ctx, g.admin, g.f.Workspace, input.RequestID, challenge.ID, revision, "Exact stop after fence", uuid.NewString())
		pairedDistributionCode(t, e, "GATEWAY_BACKEND_STOP_UNCONFIRMED")
		g.held(t, input.RequestID, true)
		revision = g.stopEnvelope(t, revision)
		confirmed, e := g.s.ConfirmRecovery(g.ctx, g.admin, g.f.Workspace, input.RequestID, challenge.ID, revision, "Exact stop after fence", uuid.NewString())
		if e != nil || confirmed.ConfirmedAt == nil {
			t.Fatal("positive stopped reconciliation", e)
		}
		g.held(t, input.RequestID, false)
		if _, e = g.s.ConfirmRecovery(g.ctx, g.admin, g.f.Workspace, input.RequestID, challenge.ID, revision, "Exact stop after fence", uuid.NewString()); e != nil {
			t.Fatal("exact confirmation replay", e)
		}
		if _, e = g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, pairedDistributionSuccess(input.RequestID), uuid.NewString()); e != nil {
			t.Fatal("late metadata only", e)
		}
		g.held(t, input.RequestID, false)
	})
	t.Run("superseded_challenge_and_changed_fence_receipt_are_refused", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		input := g.admission()
		if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e := g.s.RevokeGateway(g.ctx, g.admin, g.f.Workspace, g.gateway.Gateway.ID, "Recovery scope", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		old, e := g.s.CreateRecoveryChallenge(g.ctx, g.admin, g.f.Workspace, input.RequestID, "First challenge", uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		current, e := g.s.CreateRecoveryChallenge(g.ctx, g.admin, g.f.Workspace, input.RequestID, "Reviewed replacement challenge", uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		_, e = g.s.AcceptRecoveryFence(g.ctx, g.admin, g.f.Workspace, input.RequestID, old.ID, g.fence(old.ID), "Superseded receipt", uuid.NewString())
		pairedDistributionCode(t, e, "GATEWAY_RECOVERY_STATE_CONFLICT")
		receipt := g.fence(current.ID)
		if _, e = g.s.AcceptRecoveryFence(g.ctx, g.admin, g.f.Workspace, input.RequestID, current.ID, receipt, "Current exact receipt", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		receipt.StateFenceSHA256 = strings.Repeat("a", 64)
		_, e = g.s.AcceptRecoveryFence(g.ctx, g.admin, g.f.Workspace, input.RequestID, current.ID, receipt, "Changed replay", uuid.NewString())
		pairedDistributionCode(t, e, "GATEWAY_FENCE_RECEIPT_CONFLICT")
		g.held(t, input.RequestID, true)
	})
	t.Run("restore_invalidates_secrets_preserves_unresolved_capacity_and_history", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		input := g.admission()
		if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		sentinel := &transaction.Error{Kind: transaction.Unavailable}
		e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			if e := distribution.InvalidateAuthoritiesWithin(ctx, q); e != nil {
				return e
			}
			return sentinel
		})
		if e == nil {
			t.Fatal("restore rollback")
		}
		if _, e = g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *g.key.Secret); e != nil {
			t.Fatal("rolled-back restore lost authority", e)
		}
		if e = g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
			return distribution.InvalidateAuthoritiesWithin(ctx, q)
		}); e != nil {
			t.Fatal(e)
		}
		_, e = g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *g.key.Secret)
		pairedDistributionCode(t, e, "GATEWAY_UNAUTHORIZED")
		g.held(t, input.RequestID, true)
		if g.count(t, "SELECT COUNT(*) FROM model_alias_target_versions", "SELECT COUNT(*) FROM model_alias_target_versions") != 1 {
			t.Fatal("restore removed immutable history")
		}
		record, e := g.s.Request(g.ctx, g.portal, g.f.Workspace, input.RequestID)
		if e != nil || !record.CapacityHeld {
			t.Fatal("restored safe history", e)
		}
	})
	t.Run("concurrent_admission_exact_request_and_current_revision_winners", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		input := g.admission()
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString())
				errs <- e
			}()
		}
		wg.Wait()
		close(errs)
		success, conflict := 0, 0
		for e := range errs {
			if e == nil {
				success++
			} else if e.Error() == "GATEWAY_REQUEST_EXISTS" {
				conflict++
			} else {
				t.Fatal(e)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatal("exact admission winner", success, conflict)
		}
		errs = make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := g.s.Cutover(g.ctx, g.admin, g.f.Workspace, g.alias.ID, distribution.CutoverRequest{Action: "pause", ExpectedRevision: 1, Reason: "Concurrent current alias pause"}, uuid.NewString(), uuid.NewString())
				errs <- e
			}()
		}
		wg.Wait()
		close(errs)
		success, conflict = 0, 0
		for e := range errs {
			if e == nil {
				success++
			} else if e.Error() == "VERSION_CONFLICT" {
				conflict++
			} else {
				t.Fatal(e)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatal("exact revision winner", success, conflict)
		}
	})
	t.Run("strict_actual_HTTP_wire_headers_token_domains_and_human_scope", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		handler := &distributionhttp.Handler{Service: g.s}
		call := func(body string, mutate func(*http.Request)) (any, int, error) {
			r := httptest.NewRequest(http.MethodPost, "https://control.example.invalid/gateway/v1/instances/"+g.gateway.Gateway.ID+"/models", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+*g.gateway.Secret)
			if mutate != nil {
				mutate(r)
			}
			return handler.Machine(r, uuid.NewString())
		}
		raw, _ := json.Marshal(wire.ModelsRequest{APIKey: *g.key.Secret})
		value, status, e := call(string(raw), nil)
		if e != nil || status != 200 || len(value.(wire.ModelsAuthorization).Models) != 1 {
			t.Fatal("actual HTTP SQL authorization", e)
		}
		for _, header := range []string{"Cookie", "Origin", "X-Tenant-ID", "X-API-Key"} {
			_, _, e = call(string(raw), func(r *http.Request) { r.Header.Set(header, "forbidden") })
			pairedDistributionCode(t, e, "VALIDATION_FAILED")
		}
		_, _, e = call(string(raw), func(r *http.Request) { r.Header.Add("Authorization", "Bearer duplicate") })
		pairedDistributionCode(t, e, "GATEWAY_UNAUTHORIZED")
		_, _, e = call(`{"api_key":"`+*g.key.Secret+`","api_key":"duplicate"}`, nil)
		pairedDistributionCode(t, e, "VALIDATION_FAILED")
		wrong, _ := json.Marshal(wire.ModelsRequest{APIKey: *g.gateway.Secret})
		_, _, e = call(string(wrong), nil)
		pairedDistributionCode(t, e, "API_KEY_INVALID")
		request := httptest.NewRequest(http.MethodGet, "https://portal.example.invalid/api/v1/workspaces/"+g.f.Workspace+"/distribution/keys", nil)
		value, status, e = handler.Human(request, g.portal, uuid.NewString())
		if e != nil || status != 200 || len(value.(distribution.Page[distribution.APIKey]).Items) != 1 {
			t.Fatal("actual private human HTTP", e)
		}
		ctx, cancel := context.WithCancel(g.ctx)
		cancel()
		out, e := g.s.Alias(ctx, g.portal, g.f.Workspace, g.alias.ID)
		if e == nil || !reflect.DeepEqual(out, distribution.Alias{}) {
			t.Fatal("cancelled current alias projected")
		}
	})
	t.Run("uncertain_metadata_keeps_physical_capacity_and_enforces_shared_limit", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		policy := g.owner.Config.Policy()
		policy.Distribution.MaxConcurrentRequests = 1
		g.owner.Config.Runtime = &policy
		input := g.admission()
		if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		code := "gateway_interrupted"
		outcome := wire.Outcome{RequestID: input.RequestID, Status: "incomplete", ErrorCode: &code, ElapsedMilliseconds: 1, OutputStarted: true}
		if e := outcome.Validate(); e != nil {
			t.Fatal(e)
		}
		if _, e := g.s.Report(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, outcome, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		g.held(t, input.RequestID, true)
		out, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, g.admission(), uuid.NewString())
		pairedDistributionCode(t, e, "RATE_LIMITED")
		if !reflect.DeepEqual(out, wire.Admission{}) {
			t.Fatal("capacity admission projected")
		}
		record, e := g.s.Request(g.ctx, g.portal, g.f.Workspace, input.RequestID)
		if e != nil || record.Outcome == nil || record.Outcome.Usage != nil || !record.CapacityHeld {
			t.Fatal("uncertain usage/capacity history", e)
		}
	})

	t.Run("private_key_creator_current_viewer_and_foreign_Workspace_isolation", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		user := uuid.NewString()
		email := user + "@example.invalid"
		if g.f.Store.Dialect() == transaction.MySQL {
			g.exec(t, "", `INSERT INTO users(id,email_display,email_canonical,display_name,locale,status) VALUES(?,?,?,'Synthetic private key viewer','en','active')`, user, email, email)
		} else {
			g.exec(t, `INSERT INTO users(id,email_display,email_canonical,display_name,locale,status) VALUES($1,$2,$2,'Synthetic private key viewer','en','active')`, "", user, email)
		}
		g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'viewer')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'viewer')", user, g.f.Tenant, g.f.Workspace)
		viewer := g.sessionAs(t, user, "portal")
		page, e := g.s.KeyPage(g.ctx, viewer, g.f.Workspace, url.Values{})
		if e != nil || len(page.Items) != 0 {
			t.Fatal("other creator key list", e)
		}
		key, e := g.s.Key(g.ctx, viewer, g.f.Workspace, g.key.Key.ID)
		pairedDistributionCode(t, e, "RESOURCE_NOT_FOUND")
		if !reflect.DeepEqual(key, distribution.APIKey{}) {
			t.Fatal("other creator detail")
		}
		own, e := g.s.MintKey(g.ctx, viewer, g.keyInput(), uuid.NewString(), uuid.NewString())
		if e != nil || own.Secret == nil {
			t.Fatal("current viewer scoped key", e)
		}
		if _, e = g.s.AuthorizeModels(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, *own.Secret); e != nil {
			t.Fatal("current viewer key", e)
		}
		foreign, e := g.s.Workspaces.Create(g.ctx, g.admin, "Synthetic foreign Workspace", user, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		out, e := g.s.Alias(g.ctx, g.portal, foreign.ID, g.alias.ID)
		if e == nil || !reflect.DeepEqual(out, distribution.Alias{}) {
			t.Fatal("foreign alias metadata")
		}
	})
	t.Run("removed_node_recovery_requires_fresh_scoped_operator_receipt", func(t *testing.T) {
		g := pairedDistributionSetup(t).access(t)
		input := g.admission()
		if _, e := g.s.Admit(g.ctx, g.gateway.Gateway.ID, *g.gateway.Secret, input, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e := g.s.RevokeGateway(g.ctx, g.admin, g.f.Workspace, g.gateway.Gateway.ID, "Removed-node fence prerequisite", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		challenge, e := g.s.CreateRecoveryChallenge(g.ctx, g.admin, g.f.Workspace, input.RequestID, "Removed-node ordered recovery", uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		if _, e = g.s.AcceptRecoveryFence(g.ctx, g.admin, g.f.Workspace, input.RequestID, challenge.ID, g.fence(challenge.ID), "Explicit synthetic gateway fence", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		receipt := strings.Repeat("e", 64)
		// Explicit synthetic root-operator removal envelope. The actual current
		// receipt-audit scope/time checks and capacity reconciliation remain real SQL.
		g.exec(t, "UPDATE managed_nodes SET status='removed',credential_hash=NULL,inference_fenced_at=clock_timestamp() WHERE id=$1", "UPDATE managed_nodes SET status='removed',credential_hash=NULL,inference_fenced_at=UTC_TIMESTAMP(6) WHERE id=?", g.node)
		g.exec(t, "UPDATE deployments SET desired_state='removed',observed_state='not_installed',observation=NULL,observed_at=clock_timestamp(),removed_at=clock_timestamp(),recovery_receipt_sha256=$1 WHERE id=$2", "UPDATE deployments SET desired_state='removed',observed_state='not_installed',observation=NULL,observed_at=UTC_TIMESTAMP(6),removed_at=UTC_TIMESTAMP(6),recovery_receipt_sha256=? WHERE id=?", receipt, g.deployment)
		g.exec(t, "UPDATE gpu_leases SET state='released' WHERE deployment_id=$1", "UPDATE gpu_leases SET state='released' WHERE deployment_id=?", g.deployment)
		_, e = g.s.ConfirmRecovery(g.ctx, g.admin, g.f.Workspace, input.RequestID, challenge.ID, 1, "Positive reviewed removal", uuid.NewString())
		pairedDistributionCode(t, e, "GATEWAY_BACKEND_STOP_UNCONFIRMED")
		g.held(t, input.RequestID, true)
		metadata, _ := json.Marshal(map[string]string{"receipt_sha256": receipt})
		g.exec(t, `INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES($1,clock_timestamp(),$2,$3,'operator','inference.removal_reconciled','success','deployment',$4,'Explicit synthetic operator receipt envelope',$5,$6::jsonb)`, `INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(?,UTC_TIMESTAMP(6),?,?,'operator','inference.removal_reconciled','success','deployment',?,'Explicit synthetic operator receipt envelope',?,CAST(? AS JSON))`, uuid.NewString(), g.f.Tenant, g.f.Workspace, g.deployment, uuid.NewString(), metadata)
		confirmed, e := g.s.ConfirmRecovery(g.ctx, g.admin, g.f.Workspace, input.RequestID, challenge.ID, 1, "Positive reviewed removal", uuid.NewString())
		if e != nil || confirmed.ConfirmedAt == nil {
			t.Fatal("actual removed receipt scope", e)
		}
		g.held(t, input.RequestID, false)
	})

}
