//go:build repository_integration

package components_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	componenthttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/components/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	nodehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/logstorefixture"
)

type pairedComponentStory struct {
	f             repositoryfixture.Fixture
	ctx           context.Context
	owner         *identity.Service
	s             *components.Service
	admin, portal identity.Auth
	node          wire.Enrolled
	handshake     wire.Handshake
}

func pairedComponentSetup(t *testing.T) pairedComponentStory {
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
	n := &nodes.Service{DB: f.PostgreSQL, Identity: owner, Workspaces: &workspaces.Service{DB: f.PostgreSQL, Identity: owner}}
	s := &components.Service{Nodes: n, LogStore: &logstorefixture.Memory{}}
	n.Components = s
	g := pairedComponentStory{f: f, ctx: ctx, owner: owner, s: s}
	g.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", f.User, f.Tenant, f.Workspace)
	g.admin = g.session(t, "admin")
	g.portal = g.session(t, "portal")
	join, e := n.MintForProfile(ctx, g.admin, f.Workspace, "Synthetic paired component node", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.handshake = wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-component-sql", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	g.node, e = n.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: g.handshake}, uuid.NewString())
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
func (g pairedComponentStory) claim(t *testing.T, task nodes.Task) wire.Claim {
	t.Helper()
	out, e := g.s.Nodes.Claim(g.ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	if e != nil || out.Lease == nil || out.Lease.TaskID != task.ID || out.Lease.Component == nil {
		t.Fatal("actual component lease", e)
	}
	return out
}
func (g pairedComponentStory) observe(t *testing.T, o wire.ComponentObservation) {
	t.Helper()
	h := g.handshake
	h.Component = &o
	if _, e := g.s.Nodes.Heartbeat(g.ctx, g.node.NodeID, g.node.Credential, h); e != nil {
		t.Fatal(e)
	}
}
func (g pairedComponentStory) installed(t *testing.T) {
	t.Helper()
	c := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	g.observe(t, maintenanceObservation("1.12.0", &c))
}
func (g pairedComponentStory) complete(t *testing.T, task nodes.Task, o wire.ComponentObservation, logs *wire.ComponentLogs) {
	t.Helper()
	claim := g.claim(t, task)
	result := wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o, Logs: logs}
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
func pairedComponentLogs() wire.ComponentLogs {
	now := time.Now().UTC()
	return wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute), Entries: []wire.ComponentLogEntry{{Timestamp: now, Priority: 6, Message: "SYNTHETIC_COMPONENT_LOG_SENTINEL <script>text</script>"}}}
}

// Every subtest gets one fresh complete product schema. Enrolled Linux facts,
// observations and journals are constrained synthetic wire envelopes; no systemd
// service or physical installation is represented as executed by these tests.
func TestActualPairedComponentRuntime(t *testing.T) {
	t.Run("current_scoped_state_UTC_and_private_Admin_logs", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		out, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, g.node.NodeID)
		if e != nil || out.Version != "1.12.0" || out.Stale || out.ReceivedAt == nil || out.ReceivedAt.Location() != time.UTC || out.LastVerifiedAt == nil {
			t.Fatal("actual scoped state", e)
		}
		if _, e = g.s.Get(g.ctx, g.portal, uuid.NewString(), g.node.NodeID); e == nil {
			t.Fatal("foreign scope exposed")
		}
		_, e = g.s.Logs(g.ctx, g.portal, g.node.NodeID)
		pairedComponentCode(t, e, "ACCESS_DENIED")
	})
	t.Run("exact_intent_replay_conflict_and_single_inflight", func(t *testing.T) {
		g := pairedComponentSetup(t)
		key := uuid.NewString()
		before := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
		task, e := g.s.Request(g.ctx, g.admin, g.node.NodeID, "start", key, "Initial exact intent", uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		again, e := g.s.Request(g.ctx, g.admin, g.node.NodeID, "start", key, "Initial exact intent", uuid.NewString())
		if e != nil || again.ID != task.ID {
			t.Fatal("replay", e)
		}
		if n := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events"); n != before+1 {
			t.Fatal("replay mutated audit", n, before)
		}
		_, e = g.s.Request(g.ctx, g.admin, g.node.NodeID, "stop", key, "Initial exact intent", uuid.NewString())
		pairedComponentCode(t, e, "IDEMPOTENCY_CONFLICT")
		out, e := g.s.Request(g.ctx, g.admin, g.node.NodeID, "stop", uuid.NewString(), "Busy refusal", uuid.NewString())
		pairedComponentCode(t, e, "NODE_BUSY")
		if !reflect.DeepEqual(out, nodes.Task{}) {
			t.Fatal("busy task exposed")
		}
	})
	t.Run("configure_command_actual_result_and_retained_applied_state", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		c := wire.ComponentConfig{LogLevel: "debug", MaxScrapeRequests: 3}
		task, e := g.s.RequestDetailed(g.ctx, g.admin, g.node.NodeID, components.Request{Action: "configure", Config: &c, Reason: "Typed configure"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		claim := g.claim(t, task)
		command := claim.Lease.Component
		if command.Version != "1.12.0" || command.ExpectedVersion != "1.12.0" || command.Config == nil || *command.Config != c {
			t.Fatal("actual command lost verified state")
		}
		o := maintenanceObservation("1.12.0", &c)
		if _, e = g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o}, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		out, e := g.s.Get(g.ctx, g.admin, "", g.node.NodeID)
		if e != nil || out.AppliedConfig == nil || *out.AppliedConfig != c || out.DesiredConfig == nil || *out.DesiredConfig != c {
			t.Fatal("applied configuration", e)
		}
	})
	t.Run("mismatched_result_version_config_and_logs_never_settle", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		task := g.request(t, "restart")
		claim := g.claim(t, task)
		c := wire.DefaultComponentConfig()
		o := maintenanceObservation(wire.NodeExporterVersion, &c)
		result := wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o}
		out, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, result, uuid.NewString())
		pairedComponentCode(t, e, "VALIDATION_FAILED")
		if out != "" {
			t.Fatal("failed result acknowledged")
		}
		c = wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
		o = maintenanceObservation("1.12.0", &c)
		result.Component = &o
		logs := pairedComponentLogs()
		result.Logs = &logs
		_, e = g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, result, uuid.NewString())
		pairedComponentCode(t, e, "VALIDATION_FAILED")
		result.Logs = nil
		if _, e = g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, result, uuid.NewString()); e != nil {
			t.Fatal("valid current lease did not recover", e)
		}
	})
	t.Run("unknown_observation_preserves_verified_version_configuration", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		before, e := g.s.Get(g.ctx, g.admin, "", g.node.NodeID)
		if e != nil {
			t.Fatal(e)
		}
		g.observe(t, wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "unknown", ObservedAt: time.Now().UTC(), ErrorCode: "COMPONENT_CANCELLED"})
		after, e := g.s.Get(g.ctx, g.admin, "", g.node.NodeID)
		if e != nil || after.Version != before.Version || !reflect.DeepEqual(after.AppliedConfig, before.AppliedConfig) || after.LastVerifiedAt == nil || !after.LastVerifiedAt.Equal(*before.LastVerifiedAt) || after.Observation.State != "unknown" {
			t.Fatal("verified state overwritten", e)
		}
	})
	t.Run("rollback_binds_verified_previous_and_reconciles_desired_settings", func(t *testing.T) {
		g := pairedComponentSetup(t)
		c := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
		o := maintenanceObservation(wire.NodeExporterVersion, &c)
		o.PreviousVersion = "1.12.0"
		g.observe(t, o)
		task := g.request(t, "rollback")
		claim := g.claim(t, task)
		if claim.Lease.Component.Version != "1.12.0" {
			t.Fatal("wrong prior target")
		}
		restored := wire.DefaultComponentConfig()
		o = maintenanceObservation("1.12.0", &restored)
		o.PreviousVersion = wire.NodeExporterVersion
		if _, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o}, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		out, e := g.s.Get(g.ctx, g.admin, "", g.node.NodeID)
		if e != nil || out.DesiredVersion != "1.12.0" || out.DesiredConfig == nil || *out.DesiredConfig != restored {
			t.Fatal("rollback desired state", e)
		}
	})
	t.Run("latest_logs_Admin_only_text_and_owned_remove_retention", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		c := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
		logs := pairedComponentLogs()
		g.complete(t, g.request(t, "logs"), maintenanceObservation("1.12.0", &c), &logs)
		out, e := g.s.Logs(g.ctx, g.admin, g.node.NodeID)
		if e != nil || out.Snapshot == nil || out.Stale || out.TaskID == nil {
			t.Fatal("latest logs", e)
		}
		view, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, g.node.NodeID)
		raw, _ := json.Marshal(view)
		if e != nil || strings.Contains(string(raw), "SYNTHETIC_COMPONENT_LOG_SENTINEL") {
			t.Fatal("logs exposed in portal state", e)
		}
		g.complete(t, g.request(t, "remove"), wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed", ObservedAt: time.Now().UTC()}, nil)
		out, e = g.s.Logs(g.ctx, g.admin, g.node.NodeID)
		if e != nil || out.Snapshot != nil {
			t.Fatal("private removal retention", e)
		}
	})
	t.Run("audit_failure_rolls_back_task_desired_state_and_replays_safely", func(t *testing.T) {
		g := pairedComponentSetup(t)
		before := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
		drop := g.auditFailure(t, "component.start_requested")
		key := uuid.NewString()
		task, e := g.s.Request(g.ctx, g.admin, g.node.NodeID, "start", key, "Atomic request", uuid.NewString())
		if e == nil || !reflect.DeepEqual(task, nodes.Task{}) {
			t.Fatal("failed audit exposed task", e)
		}
		if g.count(t, "SELECT count(*) FROM node_tasks", "SELECT count(*) FROM node_tasks") != 0 || g.count(t, "SELECT count(*) FROM managed_components", "SELECT count(*) FROM managed_components") != 0 || g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != before {
			t.Fatal("request audit rollback incomplete")
		}
		drop()
		if _, e = g.s.Request(g.ctx, g.admin, g.node.NodeID, "start", key, "Atomic request", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("result_audit_failure_rolls_back_logs_and_preserves_current_lease", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		task := g.request(t, "logs")
		claim := g.claim(t, task)
		logs := pairedComponentLogs()
		c := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
		o := maintenanceObservation("1.12.0", &c)
		result := wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o, Logs: &logs}
		drop := g.auditFailure(t, "node.task_succeeded")
		out, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, result, uuid.NewString())
		if e == nil || out != "" {
			t.Fatal("failed audit acknowledged", e)
		}
		if view, e := g.s.Logs(g.ctx, g.admin, g.node.NodeID); e != nil || view.Snapshot != nil {
			t.Fatal("logs survived rollback", e)
		}
		drop()
		if _, e = g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, task.ID, result, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("unknown_commit_zero_task_then_exact_current_intent_recovery", func(t *testing.T) {
		g := pairedComponentSetup(t)
		hook := &pairedComponentOutcomeStore{Store: g.f.Store, at: 2}
		s := g.wrapped(hook)
		key := uuid.NewString()
		task, e := s.Request(g.ctx, g.admin, g.node.NodeID, "start", key, "Unknown actual commit", uuid.NewString())
		pairedComponentCode(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(task, nodes.Task{}) {
			t.Fatal("uncertain task exposed")
		}
		task, e = g.s.Request(g.ctx, g.admin, g.node.NodeID, "start", key, "Unknown actual commit", uuid.NewString())
		if e != nil || task.ID == "" || g.count(t, "SELECT count(*) FROM node_tasks", "SELECT count(*) FROM node_tasks") != 1 {
			t.Fatal("exact reconciliation", e)
		}
	})
	t.Run("read_unknown_commit_zero_state_and_logs", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		s := g.wrapped(&pairedComponentOutcomeStore{Store: g.f.Store, at: 1})
		out, e := s.Get(g.ctx, g.portal, g.f.Workspace, g.node.NodeID)
		pairedComponentCode(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(out, components.State{}) {
			t.Fatal("private uncertain state exposed")
		}
		s = g.wrapped(&pairedComponentOutcomeStore{Store: g.f.Store, at: 1})
		logs, e := s.Logs(g.ctx, g.admin, g.node.NodeID)
		pairedComponentCode(t, e, string(transaction.CommitUnknown))
		if !reflect.DeepEqual(logs, components.LogSnapshot{}) {
			t.Fatal("private uncertain logs exposed")
		}
	})
	t.Run("current_role_membership_and_node_disable_fence_existing_work", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		task := g.request(t, "restart")
		claim := g.claim(t, task)
		current, e := g.s.Nodes.Get(g.ctx, g.admin, "", g.node.NodeID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = g.s.Nodes.Update(g.ctx, g.admin, current.ID, current.Name, "disabled", current.Version, "Explicit machine fence", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
		if _, e = g.s.Nodes.Renew(g.ctx, g.node.NodeID, g.node.Credential, task.ID, claim.Lease.LeaseToken); e == nil {
			t.Fatal("disabled node renewed")
		}
		view, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, g.node.NodeID)
		if e != nil || !view.Stale || view.AppliedConfig == nil {
			t.Fatal("fence erased observed state", e)
		}
		g.exec(t, "UPDATE users SET is_platform_admin=false WHERE id=$1", "UPDATE users SET is_platform_admin=false WHERE id=?", g.f.User)
		if out, e := g.s.Logs(g.ctx, g.admin, g.node.NodeID); e == nil || !reflect.DeepEqual(out, components.LogSnapshot{}) {
			t.Fatal("stale Admin role survived", e)
		}
	})
	t.Run("strict_actual_HTTP_human_and_machine_wire", func(t *testing.T) {
		g := pairedComponentSetup(t)
		handler := &componenthttp.Handler{Service: g.s}
		path := "/api/v1/admin/nodes/" + g.node.NodeID + "/components/node-exporter/actions"
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"action":"start","reason":"HTTP selected task"}`))
		r.Header.Set("Idempotency-Key", uuid.NewString())
		out, status, e := handler.Handle(r, g.admin, uuid.NewString())
		if e != nil || status != 202 || out.(nodes.Task).ID == "" {
			t.Fatal("actual human HTTP", e)
		}
		r = httptest.NewRequest("POST", path, strings.NewReader(`{"action":"start","reason":"HTTP refusal","unknown":true}`))
		r.Header.Set("Idempotency-Key", uuid.NewString())
		if _, _, e = handler.Handle(r, g.admin, uuid.NewString()); e == nil {
			t.Fatal("unknown HTTP property accepted")
		}
		machine := &nodehttp.Handler{Service: g.s.Nodes, ComponentArtifact: g.s.DownloadArtifact}
		r = httptest.NewRequest("POST", "/agent/v1/sites/default/cells/default/nodes/"+g.node.NodeID+"/tasks/"+uuid.NewString()+"/artifact", strings.NewReader(`{"lease_token":"wrong"}`))
		r.Header.Set("Authorization", "Bearer "+g.node.Credential)
		if _, _, e = machine.Machine(r, uuid.NewString()); e == nil {
			t.Fatal("invalid machine lease accepted")
		}
	})
	t.Run("concurrent_new_intents_have_one_actual_inflight_winner", func(t *testing.T) {
		g := pairedComponentSetup(t)
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		errs := []error{}
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				task, e := g.s.Request(g.ctx, g.admin, g.node.NodeID, "start", uuid.NewString(), "Concurrent scoped request", uuid.NewString())
				mu.Lock()
				defer mu.Unlock()
				if e == nil && task.ID != "" {
					wins++
				} else {
					errs = append(errs, e)
				}
			}()
		}
		wg.Wait()
		if wins != 1 || len(errs) != 1 || errs[0] == nil || errs[0].Error() != "NODE_BUSY" || g.count(t, "SELECT count(*) FROM node_tasks WHERE state IN ('pending','running')", "SELECT count(*) FROM node_tasks WHERE state IN ('pending','running')") != 1 {
			t.Fatal("inflight serialization", wins, errs)
		}
	})
	t.Run("profile_config_version_and_missing_approved_package_fail_closed", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		_, e := g.s.RequestDetailed(g.ctx, g.admin, g.node.NodeID, components.Request{Action: "configure", Config: &wire.ComponentConfig{LogLevel: "trace", MaxScrapeRequests: 1}, Reason: "Invalid config"}, uuid.NewString(), uuid.NewString())
		pairedComponentCode(t, e, "COMPONENT_CONFIG_INVALID")
		_, e = g.s.RequestDetailed(g.ctx, g.admin, g.node.NodeID, components.Request{Action: "upgrade", Version: "1.12.1", Reason: "Missing actual archive"}, uuid.NewString(), uuid.NewString())
		pairedComponentCode(t, e, "COMPONENT_ASSET_UNAVAILABLE")
		if g.count(t, "SELECT count(*) FROM node_tasks", "SELECT count(*) FROM node_tasks") != 0 {
			t.Fatal("failed package check persisted task")
		}
		if _, _, e = g.s.OpenArtifactVersion("not-approved"); e == nil {
			t.Fatal("unknown recipe accepted")
		}
	})
	t.Run("borrowed_Store_and_mixed_identity_composition_refused", func(t *testing.T) {
		g := pairedComponentSetup(t)
		e := g.s.ObserveWithin(g.ctx, g.f.Store, nodes.Node{}, wire.ComponentObservation{}, "")
		pairedComponentCode(t, e, "STORAGE_UNAVAILABLE")
		s := *g.s
		n := *g.s.Nodes
		w := *n.Workspaces
		other := *g.owner
		w.Identity = &other
		n.Workspaces = &w
		s.Nodes = &n
		out, e := s.Get(g.ctx, g.admin, "", g.node.NodeID)
		if e == nil || !reflect.DeepEqual(out, components.State{}) {
			t.Fatal("mixed identity composition accepted", e)
		}
	})
	t.Run("deadline_expiry_records_failure_before_new_current_intent", func(t *testing.T) {
		g := pairedComponentSetup(t)
		old := g.request(t, "start")
		g.exec(t, "UPDATE node_tasks SET deadline_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE node_tasks SET deadline_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", old.ID)
		current := g.request(t, "observe")
		if current.ID == old.ID {
			t.Fatal("expired task reused")
		}
		var state string
		var code *string
		if e := g.f.Store.QueryRow(g.ctx, g.f.Statement("SELECT state,error_code FROM node_tasks WHERE id=$1", "SELECT state,error_code FROM node_tasks WHERE id=?", old.ID)).Scan(&state, &code); e != nil || state != "failed" || code == nil || *code != "TASK_DEADLINE_EXCEEDED" {
			t.Fatal("expiry did not settle actual metadata", e)
		}
	})
	t.Run("retained_invalid_configuration_fails_and_old_receipt_becomes_stale", func(t *testing.T) {
		g := pairedComponentSetup(t)
		g.installed(t)
		g.exec(t, "UPDATE managed_nodes SET last_heartbeat_at=clock_timestamp()-interval '121 seconds' WHERE id=$1", "UPDATE managed_nodes SET last_heartbeat_at=TIMESTAMPADD(SECOND,-121,UTC_TIMESTAMP(6)) WHERE id=?", g.node.NodeID)
		out, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, g.node.NodeID)
		if e != nil || !out.Stale || out.AppliedConfig == nil {
			t.Fatal("old observation fabricated freshness", e)
		}
		// Real schema permits a bounded JSON object; owner rejects invalid semantics.
		g.exec(t, `UPDATE managed_components SET applied_config='{"log_level":"trace","max_scrape_requests":1}'::jsonb WHERE node_id=$1`, `UPDATE managed_components SET applied_config=CAST('{"log_level":"trace","max_scrape_requests":1}' AS JSON) WHERE node_id=?`, g.node.NodeID)
		out, e = g.s.Get(g.ctx, g.portal, g.f.Workspace, g.node.NodeID)
		pairedComponentCode(t, e, "STORAGE_UNAVAILABLE")
		if !reflect.DeepEqual(out, components.State{}) {
			t.Fatal("malformed retained state exposed")
		}
	})

}
