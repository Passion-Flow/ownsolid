package components_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"strings"
	"testing"
	"time"
)

type maintenanceScenario struct {
	service       *components.Service
	admin, portal identity.Auth
	node          wire.Enrolled
	handshake     wire.Handshake
}

func maintenanceSetup(t *testing.T) maintenanceScenario {
	t.Helper()
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	a := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	portal := fixture.Login(t, account, "admin@example.invalid", password, "portal")
	n := &nodes.Service{DB: account.DB, Identity: account, Workspaces: &workspaces.Service{DB: account.DB, Identity: account}}
	s := &components.Service{Nodes: n, LogStore: &memoryLogStore{}, CatalogDirectory: filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/components/node-exporter")}
	n.Components = s
	join, e := n.MintForProfile(context.Background(), a, a.Me.Workspace.ID, "Synthetic maintenance node", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	h := wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-maintenance-control", LogicalCPUs: 1, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 4096, DiskAvailableBytes: 2048, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	node, e := n.Enroll(context.Background(), wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return maintenanceScenario{s, a, portal, node, h}
}

// These observations are explicit control-contract fixtures, not systemd/hardware evidence.
func maintenanceObservation(version string, config *wire.ComponentConfig) wire.ComponentObservation {
	return wire.ComponentObservation{Component: wire.NodeExporter, Version: version, State: "running", Managed: true, ObservedAt: time.Now().UTC(), Config: config, Metrics: &wire.ComponentMetrics{CPUSecondsTotal: 100, CPUIdleSecondsTotal: 80, MemoryTotalBytes: 1024, MemoryAvailableBytes: 512, FilesystemTotalBytes: 4096, FilesystemAvailableBytes: 2048}}
}
func completeMaintenance(t *testing.T, f maintenanceScenario, task nodes.Task, o wire.ComponentObservation, logs *wire.ComponentLogs) {
	t.Helper()
	n := f.service.Nodes
	claim, e := n.Claim(context.Background(), f.node.NodeID, f.node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.TaskID != task.ID {
		t.Fatal("typed maintenance claim missing", e)
	}
	result := wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o, Logs: logs}
	if o.ErrorCode != "" {
		result.Outcome = "failure"
		result.ErrorCode = o.ErrorCode
	}
	if _, e = n.Complete(context.Background(), f.node.NodeID, f.node.Credential, task.ID, result, uuid.NewString()); e != nil {
		t.Fatal("typed maintenance completion failed", e)
	}
}
func maintenanceInstalled(t *testing.T, f maintenanceScenario, configuration wire.ComponentConfig) {
	t.Helper()
	task, e := f.service.RequestDetailed(context.Background(), f.admin, f.node.NodeID, components.Request{Action: "install", Version: "1.12.0", Config: &configuration, Reason: "Synthetic older baseline"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	completeMaintenance(t, f, task, maintenanceObservation("1.12.0", &configuration), nil)
}
func TestM05ControlIntentFingerprintSurvivesStateChangeAndMissingAsset(t *testing.T) {
	f := maintenanceSetup(t)
	s := f.service
	ctx := context.Background()
	configuration := wire.DefaultComponentConfig()
	input := components.Request{Action: "install", Version: "1.12.0", Config: &configuration, Reason: "Synthetic fingerprint"}
	key := uuid.NewString()
	task, e := s.RequestDetailed(ctx, f.admin, f.node.NodeID, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	completeMaintenance(t, f, task, maintenanceObservation("1.12.0", &configuration), nil)
	original := s.CatalogDirectory
	s.CatalogDirectory = "/unavailable-synthetic-catalog"
	again, e := s.RequestDetailed(ctx, f.admin, f.node.NodeID, input, key, uuid.NewString())
	s.CatalogDirectory = original
	if e != nil || again.ID != task.ID {
		t.Fatal("committed request depended on current state/assets", e)
	}
	changed := input
	newConfig := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	changed.Config = &newConfig
	_, e = s.RequestDetailed(ctx, f.admin, f.node.NodeID, changed, key, uuid.NewString())
	expect(t, e, "IDEMPOTENCY_CONFLICT")
	upgrade, e := s.RequestDetailed(ctx, f.admin, f.node.NodeID, components.Request{Action: "upgrade", Version: "1.12.1", Reason: "Synthetic forward upgrade"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := s.Nodes.Claim(ctx, f.node.NodeID, f.node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal(e)
	}
	command := claim.Lease.Component
	if command == nil || command.Version != "1.12.1" || command.ExpectedVersion != "1.12.0" || command.Config == nil || *command.Config != configuration {
		t.Fatal("upgrade did not bind last verified configuration")
	}
	o := maintenanceObservation("1.12.1", &configuration)
	o.PreviousVersion = "1.12.0"
	if _, e = s.Nodes.Complete(ctx, f.node.NodeID, f.node.Credential, upgrade.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	view, e := s.Get(ctx, f.portal, f.portal.Me.Workspace.ID, f.node.NodeID)
	if e != nil || view.Version != "1.12.1" || view.AppliedConfig == nil || view.PreviousVersion == nil || *view.PreviousVersion != "1.12.0" {
		t.Fatal("maintenance state did not persist", e)
	}
	legacy := setupM04(t)
	_, e = legacy.service.RequestDetailed(ctx, legacy.admin, legacy.enrolled.NodeID, components.Request{Action: "upgrade", Version: "1.12.1", Reason: "Denied legacy escalation"}, uuid.NewString(), uuid.NewString())
	expect(t, e, "AGENT_OPERATION_UNSUPPORTED")
}
func TestM05LatestLogsAreAdminScopedAndAuditAtomic(t *testing.T) {
	f := maintenanceSetup(t)
	s := f.service
	n := s.Nodes
	ctx := context.Background()
	configuration := wire.DefaultComponentConfig()
	maintenanceInstalled(t, f, configuration)
	task, e := s.Request(ctx, f.admin, f.node.NodeID, "logs", uuid.NewString(), "Synthetic log retrieval", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := n.Claim(ctx, f.node.NodeID, f.node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	logs := wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute), Entries: []wire.ComponentLogEntry{{Timestamp: now, Priority: 6, Message: "SYNTHETIC_LOG_SENTINEL <script>text</script>"}}}
	o := maintenanceObservation("1.12.0", &configuration)
	result := wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o, Logs: &logs}
	if _, e = n.DB.Exec(ctx, `CREATE FUNCTION reject_log_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='node.task_succeeded' THEN RAISE EXCEPTION 'synthetic log audit failure';END IF;RETURN NEW;END $$;CREATE TRIGGER reject_logs BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_log_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = n.Complete(ctx, f.node.NodeID, f.node.Credential, task.ID, result, uuid.NewString()); e == nil {
		t.Fatal("log audit failure ignored")
	}
	view, e := s.Logs(ctx, f.admin, f.node.NodeID)
	if e != nil || view.Snapshot != nil {
		t.Fatal("log snapshot survived audit rollback", e)
	}
	if _, e = n.DB.Exec(ctx, "DROP TRIGGER reject_logs ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if _, e = n.Complete(ctx, f.node.NodeID, f.node.Credential, task.ID, result, uuid.NewString()); e != nil {
		t.Fatal("rolled-back lease could not retry", e)
	}
	view, e = s.Logs(ctx, f.admin, f.node.NodeID)
	if e != nil || view.Snapshot == nil || view.TaskID == nil || *view.TaskID != task.ID || view.Stale {
		t.Fatal("latest scoped snapshot missing", e)
	}
	second, e := s.Request(ctx, f.admin, f.node.NodeID, "logs", uuid.NewString(), "Synthetic latest bounded replacement", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	newer := logs
	newer.Entries = []wire.ComponentLogEntry{{Timestamp: now, Priority: 6, Message: "SYNTHETIC_LOG_SENTINEL_SECOND"}}
	completeMaintenance(t, f, second, o, &newer)
	// Reconciliation of an older acknowledged task cannot replace the latest snapshot.
	if _, e = n.Complete(ctx, f.node.NodeID, f.node.Credential, task.ID, result, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	latest, e := s.Logs(ctx, f.admin, f.node.NodeID)
	if e != nil || latest.TaskID == nil || *latest.TaskID != second.ID || latest.Snapshot == nil || latest.Snapshot.Entries[0].Message != "SYNTHETIC_LOG_SENTINEL_SECOND" {
		t.Fatal("old replay replaced latest logs", e)
	}
	var count int
	for _, table := range []string{"audit_events", "node_tasks"} {
		if e = n.DB.QueryRow(ctx, "SELECT count(*) FROM "+table+" t WHERE to_jsonb(t)::text LIKE '%SYNTHETIC_LOG_SENTINEL%'").Scan(&count); e != nil || count != 0 {
			t.Fatal("log message retained outside bounded snapshot", table, e)
		}
	}
	_, e = s.Logs(ctx, f.portal, f.node.NodeID)
	expect(t, e, "ACCESS_DENIED")
	state, e := s.Get(ctx, f.portal, f.portal.Me.Workspace.ID, f.node.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(state)
	if strings.Contains(string(body), "SYNTHETIC_LOG_SENTINEL") {
		t.Fatal("raw logs leaked through portal state")
	}
	remove, e := s.Request(ctx, f.admin, f.node.NodeID, "remove", uuid.NewString(), "Synthetic explicit retention", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	absent := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed", ObservedAt: time.Now().UTC()}
	completeMaintenance(t, f, remove, absent, nil)
	view, e = s.Logs(ctx, f.admin, f.node.NodeID)
	if e != nil || view.Snapshot != nil {
		t.Fatal("fetched snapshot did not follow explicit removal policy", e)
	}
}
func TestLogsReportAnUnavailableStoreAndRecoverWhenItReturns(t *testing.T) {
	f := maintenanceSetup(t)
	ctx := context.Background()
	store := f.service.LogStore.(*memoryLogStore)
	store.failing(true)
	_, e := f.service.Logs(ctx, f.admin, f.node.NodeID)
	expect(t, e, "STORAGE_UNAVAILABLE")
	store.failing(false)
	if view, e := f.service.Logs(ctx, f.admin, f.node.NodeID); e != nil || view.Snapshot != nil || !view.Stale {
		t.Fatal("logs did not recover with the store", e)
	}
	unconfigured := *f.service
	unconfigured.LogStore = nil
	_, e = unconfigured.Logs(ctx, f.admin, f.node.NodeID)
	expect(t, e, "STORAGE_UNAVAILABLE")
}

func TestLogsSkipRecordsWhoseTaskDidNotSucceed(t *testing.T) {
	f := maintenanceSetup(t)
	ctx := context.Background()
	store := f.service.LogStore.(*memoryLogStore)
	scope := telemetrystore.Scope{NodeID: f.node.NodeID, Component: wire.NodeExporter}
	if e := f.service.Nodes.DB.QueryRow(ctx, "SELECT tenant_id::text,workspace_id::text FROM managed_nodes WHERE id=$1", f.node.NodeID).Scan(&scope.TenantID, &scope.WorkspaceID); e != nil {
		t.Fatal(e)
	}
	for _, task := range []string{"not-a-uuid", uuid.NewString()} {
		store.Enqueue(telemetrystore.Record{Scope: scope, Time: time.Now(), TaskID: task, Body: []byte(`{}`)})
	}
	if view, e := f.service.Logs(ctx, f.admin, f.node.NodeID); e != nil || view.Snapshot != nil {
		t.Fatal("a record without a succeeded task was shown", e)
	}
}

func TestM05UnknownObservationRetainsVerifiedMetadataForRecovery(t *testing.T) {
	f := maintenanceSetup(t)
	s := f.service
	ctx := context.Background()
	configuration := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	maintenanceInstalled(t, f, configuration)
	before, e := s.Get(ctx, f.admin, "", f.node.NodeID)
	if e != nil || before.LastVerifiedAt == nil {
		t.Fatal(e)
	}
	unknown := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "unknown", ObservedAt: time.Now().UTC(), ErrorCode: "COMPONENT_CANCELLED"}
	h := f.handshake
	h.Component = &unknown
	if _, e = s.Nodes.Heartbeat(ctx, f.node.NodeID, f.node.Credential, h); e != nil {
		t.Fatal(e)
	}
	after, e := s.Get(ctx, f.admin, "", f.node.NodeID)
	if e != nil || after.Version != "1.12.0" || after.AppliedConfig == nil || *after.AppliedConfig != configuration || after.LastVerifiedAt == nil || !after.LastVerifiedAt.Equal(*before.LastVerifiedAt) || after.Observation.State != "unknown" {
		t.Fatal("unknown probe fabricated new verified state", e)
	}
	task, e := s.RequestDetailed(ctx, f.admin, f.node.NodeID, components.Request{Action: "upgrade", Version: "1.12.1", Reason: "Synthetic recoverable retry"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := s.Nodes.Claim(ctx, f.node.NodeID, f.node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil || claim.Lease.Component.ExpectedVersion != "1.12.0" || claim.Lease.Component.Config == nil || *claim.Lease.Component.Config != configuration {
		t.Fatal("recovery retry reset verified settings", e)
	}
	o := maintenanceObservation("1.12.0", &configuration)
	o.ErrorCode = "COMPONENT_UPDATE_ROLLED_BACK"
	o.MaintenanceError = o.ErrorCode
	o.MaintenanceCause = "COMPONENT_SERVICE_FAILED"
	if _, e = s.Nodes.Complete(ctx, f.node.NodeID, f.node.Credential, task.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "failure", ErrorCode: o.ErrorCode, Component: &o}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	view, e := s.Get(ctx, f.admin, "", f.node.NodeID)
	if e != nil || view.DesiredVersion != "1.12.1" || view.Version != "1.12.0" || view.Observation.MaintenanceError != "COMPONENT_UPDATE_ROLLED_BACK" {
		t.Fatal("rollback was presented as successful upgrade", e)
	}
}

func TestM05MaintenanceScopeBindingsAndStoredAuthorityRemainEnforced(t *testing.T) {
	f := maintenanceSetup(t)
	ctx := context.Background()
	s := f.service
	configuration := wire.DefaultComponentConfig()
	maintenanceInstalled(t, f, configuration)
	changed := f.handshake
	changed.ExecutionProfile = wire.SystemdComponentsProfile
	changed.ProtocolVersion = wire.ManagementVersion
	changed.Capabilities = wire.Capabilities(wire.SystemdComponentsProfile)
	if _, e := s.Nodes.Heartbeat(ctx, f.node.NodeID, f.node.Credential, changed); e == nil {
		t.Fatal("credential profile changed through heartbeat")
	}
	other, e := s.Nodes.Workspaces.Create(ctx, f.admin, "Different maintenance scope", f.admin.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	join, e := s.Nodes.MintForProfile(ctx, f.admin, other.ID, "Other maintenance node", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	node, e := s.Nodes.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: f.handshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Get(ctx, f.portal, other.ID, f.node.NodeID); e == nil {
		t.Fatal("node read through wrong Workspace")
	}
	task, e := s.Request(ctx, f.admin, f.node.NodeID, "logs", uuid.NewString(), "Synthetic scoped task", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := s.Nodes.Claim(ctx, f.node.NodeID, f.node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	logs := wire.ComponentLogs{Source: "systemd_journal", Available: true, ObservedAt: now, Since: now.Add(-time.Minute), Entries: []wire.ComponentLogEntry{}}
	o := maintenanceObservation("1.12.0", &configuration)
	if _, e = s.Nodes.Complete(ctx, node.NodeID, node.Credential, task.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o, Logs: &logs}, uuid.NewString()); e == nil {
		t.Fatal("another node completed maintenance task")
	}
	if _, e = s.Nodes.DB.Exec(ctx, `UPDATE managed_nodes SET capabilities=$1 WHERE id=$2`, []byte(`["collect_facts.v1"]`), f.node.NodeID); e == nil {
		t.Fatal("database admitted capability tampering")
	}

}

func TestM05MinimalDisabledRecoveryAndExplicitEnable(t *testing.T) {
	f := maintenanceSetup(t)
	ctx := t.Context()
	s := f.service
	n := s.Nodes
	defaults := wire.DefaultComponentConfig()
	maintenanceInstalled(t, f, defaults)
	desired := wire.ComponentConfig{LogLevel: "warn", MaxScrapeRequests: 2}
	job, e := s.RequestDetailed(ctx, f.admin, f.node.NodeID, components.Request{Action: "configure", Config: &desired, Reason: "Synthetic interrupted configuration"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	claim, e := n.Claim(ctx, f.node.NodeID, f.node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatal(e)
	}
	current, e := n.Get(ctx, f.admin, "", f.node.NodeID)
	if e != nil {
		t.Fatal(e)
	}
	disabled, e := n.Update(ctx, f.admin, current.ID, current.Name, "disabled", current.Version, "Synthetic recovery fence", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	o := maintenanceObservation("1.12.0", &defaults)
	o.MaintenanceError = "COMPONENT_UPDATE_ROLLED_BACK"
	o.MaintenanceCause = "COMPONENT_INTERRUPTED"
	h := f.handshake
	h.Component = &o
	if d, e := n.Heartbeat(ctx, f.node.NodeID, f.node.Credential, h); e != nil || d.Status != "disabled" {
		t.Fatal(e)
	}
	view, e := s.Get(ctx, f.portal, f.portal.Me.Workspace.ID, f.node.NodeID)
	if e != nil || !view.Stale || view.ManagementAvailable || view.MaintenanceAvailable || view.AppliedConfig == nil || *view.AppliedConfig != defaults || view.DesiredConfig == nil || *view.DesiredConfig != desired || view.LastTaskID == nil || *view.LastTaskID != job.ID {
		t.Fatal("passive recovery changed intent/authority", e)
	}
	if _, e = n.Claim(ctx, f.node.NodeID, f.node.Credential, uuid.NewString()); e == nil {
		t.Fatal("disabled claim")
	}
	if _, e = n.Renew(ctx, f.node.NodeID, f.node.Credential, job.ID, claim.Lease.LeaseToken); e == nil {
		t.Fatal("disabled renewal")
	}
	if _, e = n.Complete(ctx, f.node.NodeID, f.node.Credential, job.ID, wire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Component: &o}, uuid.NewString()); e == nil {
		t.Fatal("disabled completion")
	}
	enabled, e := n.Update(ctx, f.admin, disabled.ID, disabled.Name, "active", disabled.Version, "Explicit enable after inspection", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = n.Heartbeat(ctx, f.node.NodeID, f.node.Credential, h); e != nil {
		t.Fatal(e)
	}
	if view, e = s.Get(ctx, f.portal, f.portal.Me.Workspace.ID, f.node.NodeID); e != nil || view.Stale || !view.MaintenanceAvailable {
		t.Fatal(e)
	}
	if _, e = n.Update(ctx, f.admin, enabled.ID, enabled.Name, "removed", enabled.Version, "Synthetic retire", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = n.Heartbeat(ctx, f.node.NodeID, f.node.Credential, h); e == nil {
		t.Fatal("removed credential recovery")
	}
}
