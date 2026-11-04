//go:build cgo && odbc_manager && repository_integration && dameng_repository_integration

package components_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/logstorefixture"
)

// This profile requires a separately provisioned, empty, disposable native DM
// schema. It never selects PostgreSQL/MySQL, installs software, runs DDL or drops
// retained evidence. A missing prerequisite fails, rather than skipping a gate.
func nativeComponentSetup(t *testing.T) pairedComponentStory {
	t.Helper()
	path := os.Getenv("PROJECT_ONE_DAMENG_NODES_CONFIGURATION")
	if path == "" {
		t.Fatal("NATIVE_NODES_PRIVATE_DISPOSABLE_CONFIGURATION_REQUIRED")
	}
	c, e := dameng.LoadConnectionConfiguration(path, "")
	if e != nil || !regexp.MustCompile(`^PO_NODES_[A-F0-9]{16}_TEST$`).MatchString(c.Schema) {
		t.Fatal("NATIVE_NODES_PRIVATE_DISPOSABLE_CONFIGURATION_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	t.Cleanup(cancel)
	kernel, _, e := dameng.Open(ctx, c)
	if kernel != nil {
		t.Cleanup(func() {
			// Close remains owned even after failed Open. A second bounded drain
			// joins retained cleanup; it never retries business SQL or frees handles.
			for attempt := 0; attempt < 2; attempt++ {
				closing, stop := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
				status, err := kernel.Close(closing)
				stop()
				if err == nil && status.State == "closed" && status.PhysicalOwners == 0 && status.CleanupJobs == 0 {
					return
				}
			}
			t.Error("NATIVE_NODES_OWNER_DRAIN_INCOMPLETE: retain fixture and cleanup diagnostics")
		})
	}
	if e != nil {
		t.Fatal(e)
	}
	store, e := transaction.FromDameng(kernel)
	if e != nil || store.Dialect() != transaction.Dameng {
		t.Fatal("NATIVE_NODES_TRANSACTION_REQUIRED", e)
	}
	f := repositoryfixture.Fixture{Store: store, Tenant: uuid.NewString(), Workspace: uuid.NewString(), User: uuid.NewString()}
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var ready int
		if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT count(*) FROM installation_state WHERE id=1 AND bootstrap_complete=0 AND tenant_id IS NULL AND default_workspace_id IS NULL AND primary_admin_user_id IS NULL`)).Scan(&ready); e != nil {
			return e
		}
		if ready != 1 {
			return fmt.Errorf("NATIVE_NODES_FRESH_FOUNDATION_REQUIRED")
		}
		// The table names are a fixed schema contract, never configuration input.
		for _, table := range []string{"tenants", "workspaces", "users", "sessions", "workspace_memberships", "audit_events", "managed_nodes", "node_join_tokens", "node_tasks", "managed_components", "component_log_snapshots"} {
			var count int
			if e := q.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT count(*) FROM "+table)).Scan(&count); e != nil {
				return e
			}
			if count != 0 {
				return fmt.Errorf("NATIVE_NODES_EMPTY_SCHEMA_REQUIRED")
			}
		}
		for _, statement := range []transaction.Statement{
			transaction.SQL(transaction.Dameng, `INSERT INTO tenants(id,name,status) VALUES(?,?,'active')`, f.Tenant, "Synthetic native tenant"),
			transaction.SQL(transaction.Dameng, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,?,'active')`, f.Workspace, f.Tenant, "Synthetic native workspace"),
			transaction.SQL(transaction.Dameng, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,?,'en','active',1)`, f.User, "synthetic-native@example.invalid", "synthetic-native@example.invalid", "Synthetic native actor"),
			transaction.SQL(transaction.Dameng, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES(?,?,?,'owner',?)`, f.User, f.Tenant, f.Workspace, uuid.NewString()),
			transaction.SQL(transaction.Dameng, `UPDATE installation_state SET bootstrap_complete=1,tenant_id=?,default_workspace_id=?,primary_admin_user_id=?,bootstrapped_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE id=1 AND bootstrap_complete=0`, f.Tenant, f.Workspace, f.User),
		} {
			changed, e := q.Exec(ctx, statement)
			if e != nil {
				return e
			}
			if changed != 1 {
				return fmt.Errorf("NATIVE_NODES_SEED_ROWCOUNT_INVALID")
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	owner := &identity.Service{Storage: store, HMACKey: make([]byte, 32)}
	if _, e = rand.Read(owner.HMACKey); e != nil {
		t.Fatal(e)
	}
	owner.Config.Server.AdminOrigin = "https://admin.example.invalid"
	owner.Config.Server.PortalOrigin = "https://portal.example.invalid"
	n := &nodes.Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
	s := &components.Service{Nodes: n, LogStore: &logstorefixture.Memory{}}
	n.Components = s
	g := pairedComponentStory{f: f, ctx: ctx, owner: owner, s: s}
	g.admin, g.portal = g.session(t, "admin"), g.session(t, "portal")
	g.handshake = wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-native-node", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	join, e := n.MintForProfile(ctx, g.admin, f.Workspace, "Synthetic native component node", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.node, e = n.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: g.handshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return g
}

func nativeComponentCount(t *testing.T, g pairedComponentStory, statement string, args ...any) int {
	t.Helper()
	var out int
	if e := g.f.Store.QueryRow(g.ctx, transaction.SQL(transaction.Dameng, statement, args...)).Scan(&out); e != nil {
		t.Fatal(e)
	}
	return out
}

func TestActualNativeDamengNodeComponentRecovery(t *testing.T) {
	g := nativeComponentSetup(t)
	// Include the generic discovery path; facts remain synthetic wire input.
	discovery, e := g.s.Nodes.CreateTask(g.ctx, g.admin, g.node.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	discoveryClaim, e := g.s.Nodes.Claim(g.ctx, g.node.NodeID, g.node.Credential, uuid.NewString())
	if e != nil || discoveryClaim.Lease == nil || discoveryClaim.Lease.TaskID != discovery.ID {
		t.Fatal("native discovery claim", e)
	}
	if out, e := g.s.Nodes.Complete(g.ctx, g.node.NodeID, g.node.Credential, discovery.ID, wire.Result{LeaseToken: discoveryClaim.Lease.LeaseToken, Outcome: "success", Facts: &g.handshake.Facts}, uuid.NewString()); e != nil || out != "succeeded" {
		t.Fatal("native discovery result", e)
	}
	g.installed(t)
	if _, e := g.s.Get(g.ctx, g.portal, uuid.NewString(), g.node.NodeID); e == nil {
		t.Fatal("foreign Workspace accepted")
	}
	if _, e := g.s.Logs(g.ctx, g.portal, g.node.NodeID); e == nil {
		t.Fatal("Portal read Admin logs")
	}
	key := uuid.NewString()
	task, e := g.s.Request(g.ctx, g.admin, g.node.NodeID, "restart", key, "Native exact intent", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	again, e := g.s.Request(g.ctx, g.admin, g.node.NodeID, "restart", key, "Native exact intent", uuid.NewString())
	if e != nil || again.ID != task.ID {
		t.Fatal("native request replay", e)
	}
	if _, e = g.s.Request(g.ctx, g.admin, g.node.NodeID, "stop", key, "Native exact intent", uuid.NewString()); e == nil {
		t.Fatal("conflicting native intent accepted")
	}
	claim := g.claim(t, task)
	if _, e = g.s.Nodes.Renew(g.ctx, g.node.NodeID, g.node.Credential, task.ID, claim.Lease.LeaseToken); e != nil {
		t.Fatal(e)
	}
	n, e := g.s.Nodes.Get(g.ctx, g.admin, "", g.node.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	disabled, e := g.s.Nodes.Update(g.ctx, g.admin, n.ID, n.Name, "disabled", n.Version, "Native interrupted maintenance", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	restored := wire.DefaultComponentConfig()
	o := maintenanceObservation("1.12.0", &restored)
	o.MaintenanceError, o.MaintenanceCause = "COMPONENT_UPDATE_ROLLED_BACK", "COMPONENT_INTERRUPTED"
	h := g.handshake
	h.Component = &o
	if out, e := g.s.Nodes.Heartbeat(g.ctx, n.ID, g.node.Credential, h); e != nil || out.Status != "disabled" {
		t.Fatal("native passive recovery", e)
	}
	view, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, n.ID)
	desired := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	if e != nil || !view.Stale || view.ManagementAvailable || view.AppliedConfig == nil || *view.AppliedConfig != restored || view.DesiredConfig == nil || *view.DesiredConfig != desired || view.LastTaskID == nil || *view.LastTaskID != task.ID {
		t.Fatal("native passive recovery lost state/intent", e)
	}
	if out, e := g.s.Nodes.Claim(g.ctx, n.ID, g.node.Credential, uuid.NewString()); e == nil || !reflect.DeepEqual(out, wire.Claim{}) {
		t.Fatal("disabled native claim", e)
	}
	if out, e := g.s.Nodes.Renew(g.ctx, n.ID, g.node.Credential, task.ID, claim.Lease.LeaseToken); e == nil || !reflect.DeepEqual(out, wire.RenewedLease{}) {
		t.Fatal("disabled native renewal", e)
	}
	result := wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o}
	if out, e := g.s.Nodes.Complete(g.ctx, n.ID, g.node.Credential, task.ID, result, uuid.NewString()); e == nil || out != "" {
		t.Fatal("cancelled native result", e)
	}
	active, e := g.s.Nodes.Update(g.ctx, g.admin, n.ID, n.Name, "active", disabled.Version, "Native explicit recovery", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g.observe(t, o)
	fresh := g.request(t, "observe")
	freshClaim := g.claim(t, fresh)
	result.LeaseToken = freshClaim.Lease.LeaseToken
	if out, e := g.s.Nodes.Complete(g.ctx, n.ID, g.node.Credential, fresh.ID, result, uuid.NewString()); e != nil || out != "succeeded" {
		t.Fatal("native fresh result", e)
	}
	before := nativeComponentCount(t, g, "SELECT count(*) FROM audit_events")
	if out, e := g.s.Nodes.Complete(g.ctx, n.ID, g.node.Credential, fresh.ID, result, uuid.NewString()); e != nil || out != "succeeded" {
		t.Fatal("native exact result replay", e)
	}
	if nativeComponentCount(t, g, "SELECT count(*) FROM audit_events") != before {
		t.Fatal("native replay duplicated audit")
	}
	expired := g.request(t, "observe")
	expiredClaim := g.claim(t, expired)
	// The isolated fixture moves only its exact task's deadline; no sleep,
	// global clock change or implicit uncertain mutation retry is involved.
	changed, e := g.f.Store.Exec(g.ctx, transaction.SQL(transaction.Dameng, `UPDATE node_tasks SET deadline_at=DATEADD(SECOND,-1,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))) WHERE tenant_id=? AND workspace_id=? AND node_id=? AND id=?`, g.f.Tenant, g.f.Workspace, n.ID, expired.ID))
	if e != nil || changed != 1 {
		t.Fatal("native expiry fixture", e)
	}
	result.LeaseToken = expiredClaim.Lease.LeaseToken
	if out, e := g.s.Nodes.Complete(g.ctx, n.ID, g.node.Credential, expired.ID, result, uuid.NewString()); e == nil || out != "" {
		t.Fatal("expired native result accepted", e)
	}
	if out, e := g.s.Nodes.Claim(g.ctx, n.ID, g.node.Credential, uuid.NewString()); e != nil || out.Lease != nil {
		t.Fatal("native expiry reconciliation", e)
	}
	if nativeComponentCount(t, g, `SELECT count(*) FROM node_tasks WHERE id=? AND state='failed' AND error_code='TASK_DEADLINE_EXCEEDED' AND lease_token_hash IS NULL AND lease_expires_at IS NULL`, expired.ID) != 1 {
		t.Fatal("native expiry did not clear execution authority")
	}
	// Exercise both native snapshot insert and update with real leased results.
	for i := 0; i < 2; i++ {
		g.complete(t, g.request(t, "logs"), o, ptrNativeLogs())
	}
	logs, e := g.s.Logs(g.ctx, g.admin, n.ID)
	if e != nil || logs.Snapshot == nil || logs.Stale {
		t.Fatal("native logs persistence", e)
	}
	page, e := g.s.Nodes.Tasks(g.ctx, g.admin, "", n.ID, url.Values{"limit": {"1"}})
	if e != nil || page.NextCursor == nil {
		t.Fatal("native pagination first page", e)
	}
	second, e := g.s.Nodes.Tasks(g.ctx, g.admin, "", n.ID, url.Values{"limit": {"1"}, "cursor": {*page.NextCursor}})
	if e != nil || len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID {
		t.Fatal("native pagination continuation", e)
	}
	if _, e = g.s.Nodes.Update(g.ctx, g.admin, n.ID, n.Name, "removed", active.Version, "Native identity retirement", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if out, e := g.s.Nodes.Heartbeat(g.ctx, n.ID, g.node.Credential, h); e == nil || !reflect.DeepEqual(out, wire.Disposition{}) {
		t.Fatal("removed native credential accepted", e)
	}
	join, e := g.s.Nodes.MintForProfile(g.ctx, g.admin, g.f.Workspace, n.Name, wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	replacement, e := g.s.Nodes.Enroll(g.ctx, wire.Enrollment{JoinToken: join.Token, Handshake: g.handshake}, uuid.NewString())
	if e != nil || replacement.NodeID == n.ID || replacement.Credential == g.node.Credential {
		t.Fatal("native identity reused", e)
	}

	// Explicit outcome double after a real native commit; this does not claim an
	// actual lost ODBC COMMIT acknowledgement or inject a physical network fault.
	join, e = g.s.Nodes.MintForProfile(g.ctx, g.admin, g.f.Workspace, "Synthetic native orphan", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	hook := &pairedComponentOutcomeStore{Store: g.f.Store, at: 1}
	uncertain := g.wrapped(hook)
	out, e := uncertain.Nodes.Enroll(g.ctx, wire.Enrollment{JoinToken: join.Token, Handshake: g.handshake}, uuid.NewString())
	pairedComponentCode(t, e, string(transaction.CommitUnknown))
	if !reflect.DeepEqual(out, wire.Enrolled{}) || hook.calls != 1 {
		t.Fatal("unknown native enrollment disclosed secret or replayed")
	}
	tokens, e := g.s.Nodes.Tokens(g.ctx, g.admin, url.Values{})
	if e != nil {
		t.Fatal(e)
	}
	orphan := ""
	for _, token := range tokens.Items {
		if token.ID == join.ID && token.Status == "consumed" && token.NodeID != nil {
			orphan = *token.NodeID
		}
	}
	if orphan == "" {
		t.Fatal("native orphan reconciliation missing")
	}
	current, e := g.s.Nodes.Get(g.ctx, g.admin, "", orphan)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = g.s.Nodes.Update(g.ctx, g.admin, current.ID, current.Name, "removed", current.Version, "Native explicit orphan removal", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
}

func ptrNativeLogs() *wire.ComponentLogs { value := pairedComponentLogs(); return &value }
