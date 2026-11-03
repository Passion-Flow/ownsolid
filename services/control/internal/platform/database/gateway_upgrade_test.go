package database_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"reflect"
	"strings"
	"testing"
	"time"
)

func m06Snapshot(t *testing.T, p *pgxpool.Pool) map[string]string {
	t.Helper()
	out := map[string]string{}
	tables := []string{"schema_migrations", "tenants", "workspaces", "users", "password_credentials", "workspace_memberships", "installation_state", "sessions", "invitations", "password_reset_tokens", "mail_deliveries", "audit_events", "rate_limit_buckets", "idempotency_records", "platform_license_state", "platform_license_artifacts", "workspace_notes", "node_join_tokens", "managed_nodes", "node_tasks", "managed_components", "component_log_snapshots", "model_versions", "model_imports", "model_import_chunks", "inference_nodes", "deployments", "gpu_leases"}
	for _, table := range tables {
		where := ""
		if table == "schema_migrations" {
			where = " WHERE version<=11"
		}
		var raw string
		if e := p.QueryRow(t.Context(), "SELECT COALESCE(string_agg(v::text,E'\\n' ORDER BY v::text),'') FROM (SELECT to_jsonb(t) AS v FROM "+table+" t"+where+") rows").Scan(&raw); e != nil {
			t.Fatal(table, e)
		}
		out[table] = fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	}
	return out
}
func TestM07DeliveredM06UpgradeAndIsolatedRestore(t *testing.T) {
	s := fixture.NewM06(t)
	ctx := t.Context()
	password := fixture.Bootstrap(t, s)
	admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	portal := fixture.Login(t, s, "admin@example.invalid", password, "portal")
	w := &workspaces.Service{DB: s.DB, Identity: s}
	note, e := w.CreateNote(ctx, portal, portal.Me.Workspace.ID, "Preserved M02 note", "Synthetic populated data", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), "pending@example.invalid", "en", 0, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if e = s.RequestReset(ctx, "admin@example.invalid", "portal", "", uuid.NewString()); e != nil {
		t.Fatal(e)
	}

	// Preserve a real enrolled discovery node, pending token and running discovery task.
	originalNodes := &nodes.Service{DB: s.DB, Identity: s, Workspaces: w}
	originalToken, e := originalNodes.Mint(ctx, admin, admin.Me.Workspace.ID, "M03 retained node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	originalFacts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-m03", LogicalCPUs: 2, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 2048, DiskAvailableBytes: 1024, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	originalHandshake := wire.Handshake{ProtocolVersion: wire.Version, Capabilities: wire.Capabilities(wire.ReadOnlyProfile), Facts: originalFacts}
	originalNode, e := originalNodes.Enroll(ctx, wire.Enrollment{JoinToken: originalToken.Token, Handshake: originalHandshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = originalNodes.Mint(ctx, admin, admin.Me.Workspace.ID, "M03 pending token", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	originalTask, e := originalNodes.CreateTask(ctx, admin, originalNode.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	originalClaim, e := originalNodes.Claim(ctx, originalNode.NodeID, originalNode.Credential, uuid.NewString())
	if e != nil || originalClaim.Lease == nil {
		t.Fatal(e)
	}
	// A pending M04 lifecycle task and current component row coexist with the running discovery job.
	oldComponent := &components.Service{Nodes: originalNodes}
	originalNodes.Components = oldComponent
	join, e := originalNodes.MintForProfile(ctx, admin, admin.Me.Workspace.ID, "M04 pending component", wire.SystemdComponentsProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	oldNode, e := originalNodes.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: wire.Handshake{ProtocolVersion: wire.ManagementVersion, ExecutionProfile: wire.SystemdComponentsProfile, Capabilities: wire.Capabilities(wire.SystemdComponentsProfile), Facts: originalFacts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = oldComponent.Request(ctx, admin, oldNode.NodeID, "start", uuid.NewString(), "Preserved pending M04 component job", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	baseline := m06Snapshot(t, s.DB)
	equal := func(p *pgxpool.Pool) {
		for k, v := range m06Snapshot(t, p) {
			if v != baseline[k] {
				t.Fatal("M04 row changed", k)
			}
		}
	}
	if database.Check(ctx, s.DB, "gateway") == nil {
		t.Fatal("unupgraded accepted")
	}
	lock, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = lock.Exec(ctx, "LOCK TABLE managed_nodes IN ACCESS EXCLUSIVE MODE"); e != nil {
		t.Fatal(e)
	}
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	e = database.Migrate(bounded, s.DB, "gateway")
	cancel()
	_ = lock.Rollback(ctx)
	if e == nil {
		t.Fatal("interruption accepted")
	}
	equal(s.DB)
	if _, e = s.DB.Exec(ctx, "CREATE TABLE gateway_instances(id integer)"); e != nil {
		t.Fatal(e)
	}
	if database.Migrate(ctx, s.DB, "gateway") == nil {
		t.Fatal("partial conflict accepted")
	}
	if _, e = s.DB.Exec(ctx, "DROP TABLE gateway_instances"); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, s.DB, "models"); e != nil {
		t.Fatal(e)
	}
	equal(s.DB)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- database.Migrate(ctx, s.DB, "gateway") }()
	}
	for i := 0; i < 2; i++ {
		if e = <-results; e != nil {
			t.Fatal(e)
		}
	}
	if e = database.Migrate(ctx, s.DB, "gateway"); e != nil {
		t.Fatal(e)
	}
	equal(s.DB)
	var tables, versions int
	if e = s.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),(SELECT count(*) FROM schema_migrations)").Scan(&tables, &versions); e != nil || tables != 35 || versions != 12 {
		t.Fatal(tables, versions, e)
	}
	fresh := fixture.NewM07(t)
	if !reflect.DeepEqual(gatewayCatalog(t, s.DB), gatewayCatalog(t, fresh.DB)) {
		t.Fatal("fresh/upgraded catalog definitions differ")
	}
	for _, profile := range []string{"account", "workspaces", "nodes", "models", "platform"} {
		if database.Check(ctx, s.DB, profile) == nil {
			t.Fatal("wrong catalog", profile)
		}
	}
	profile := "gateway"
	s.Config.Server.ApplicationProfile = &profile
	s.Config.Models.ArtifactRoot = t.TempDir()
	s.Mail.Config = s.Config
	n := &nodes.Service{DB: s.DB, Identity: s, Workspaces: w}
	if _, e = n.Complete(ctx, originalNode.NodeID, originalNode.Credential, originalTask.ID, wire.Result{LeaseToken: originalClaim.Lease.LeaseToken, Outcome: "success", Facts: &originalFacts}, uuid.NewString()); e != nil {
		t.Fatal("preserved live M03 task", e)
	}

	token, e := n.MintForProfile(ctx, admin, admin.Me.Workspace.ID, "Restored management node", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-restore", LogicalCPUs: 2, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 2048, DiskAvailableBytes: 1024, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	enrolled, e := n.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: facts}}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = n.Mint(ctx, admin, admin.Me.Workspace.ID, "Pending restore token", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = n.CreateTask(ctx, admin, enrolled.NodeID, wire.CollectFacts, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = n.Claim(ctx, enrolled.NodeID, enrolled.Credential, uuid.NewString()); e != nil {
		t.Fatal(e)
	}

	c := &components.Service{Nodes: n}
	n.Components = c
	observation := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "unknown", ErrorCode: "COMPONENT_SYSTEMD_UNAVAILABLE", ObservedAt: time.Now().UTC()}
	if _, e = n.Heartbeat(ctx, enrolled.NodeID, enrolled.Credential, wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: facts, Component: &observation}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE managed_components SET version='1.12.0',desired_version='1.12.1',desired_config='{"log_level":"error","max_scrape_requests":3}',applied_config=$2,previous_version='1.12.1' WHERE node_id=$1`, enrolled.NodeID, []byte(`{"log_level":"warn","max_scrape_requests":2}`)); e != nil {
		t.Fatal(e)
	}
	var correlated string
	if e = s.DB.QueryRow(ctx, "SELECT id::text FROM node_tasks WHERE node_id=$1 LIMIT 1", enrolled.NodeID).Scan(&correlated); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `INSERT INTO component_log_snapshots(tenant_id,workspace_id,node_id,component,task_id,snapshot) VALUES($1,$2,$3,'node_exporter',$4,$5)`, admin.Me.Workspace.TenantID, admin.Me.Workspace.ID, enrolled.NodeID, correlated, []byte(`{"source":"systemd_journal","available":true,"observed_at":"2026-10-25T00:00:00Z","since":"2026-10-24T23:59:00Z","entries":[]}`)); e != nil {
		t.Fatal(e)
	}
	var retained string
	if e = s.DB.QueryRow(ctx, "SELECT md5(row_to_json(c)::text) FROM managed_components c WHERE node_id=$1", enrolled.NodeID).Scan(&retained); e != nil {
		t.Fatal(e)
	}
	var retainedLogs string
	if e = s.DB.QueryRow(ctx, "SELECT md5(row_to_json(c)::text) FROM component_log_snapshots c WHERE node_id=$1", enrolled.NodeID).Scan(&retainedLogs); e != nil {
		t.Fatal(e)
	}
	owned := fixture.OwnedPostgres(t, s.DB)
	archive := owned.DumpSchema(t, ctx)
	restored := owned.RestoreFresh(t, ctx, s.DB, archive, true)
	if e = database.Check(ctx, restored, "gateway"); e != nil {
		t.Fatal(e)
	}
	clone := *s
	clone.DB = restored
	mail := *s.Mail
	mail.DB = restored
	clone.Mail = &mail
	if e = recovery.InvalidateRestored(ctx, &clone, "Synthetic isolated M04 restore"); e != nil {
		t.Fatal(e)
	}
	var after, afterLogs string
	if e = restored.QueryRow(ctx, "SELECT md5(row_to_json(c)::text) FROM managed_components c WHERE node_id=$1", enrolled.NodeID).Scan(&after); e != nil || after != retained {
		t.Fatal("component content changed on restore", e)
	}
	if e = restored.QueryRow(ctx, "SELECT md5(row_to_json(c)::text) FROM component_log_snapshots c WHERE node_id=$1", enrolled.NodeID).Scan(&afterLogs); e != nil || afterLogs != retainedLogs {
		t.Fatal("log content changed on restore", e)
	}
	var live int
	if e = restored.QueryRow(ctx, "SELECT (SELECT count(*) FROM managed_nodes WHERE credential_hash IS NOT NULL)+(SELECT count(*) FROM node_join_tokens WHERE consumed_at IS NULL AND revoked_at IS NULL)+(SELECT count(*) FROM node_tasks WHERE state IN ('pending','running'))+(SELECT count(*) FROM sessions WHERE revoked_at IS NULL)").Scan(&live); e != nil || live != 0 {
		t.Fatal("restored authority", live, e)
	}
	renewed := fixture.Login(t, &clone, "admin@example.invalid", password, "portal")
	rw := workspaces.Service{DB: restored, Identity: &clone}
	got, e := rw.GetNote(ctx, renewed, renewed.Me.Workspace.ID, note.ID)
	if e != nil || got.Text != note.Text {
		t.Fatal("note recovery", e)
	}
	rn := nodes.Service{DB: restored, Identity: &clone, Workspaces: &rw}
	if _, e = rn.List(ctx, renewed, renewed.Me.Workspace.ID, url.Values{}); e != nil {
		t.Fatal(e)
	}

	recoveredAdmin := fixture.Login(t, &clone, "admin@example.invalid", password, "admin")
	newToken, e := rn.MintForProfile(ctx, recoveredAdmin, recoveredAdmin.Me.Workspace.ID, "Explicit restored re-enrollment", wire.SystemdMaintenanceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = rn.Enroll(ctx, wire.Enrollment{JoinToken: newToken.Token, Handshake: wire.Handshake{ProtocolVersion: wire.MaintenanceVersion, ExecutionProfile: wire.SystemdMaintenanceProfile, Capabilities: wire.Capabilities(wire.SystemdMaintenanceProfile), Facts: facts}}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = rn.Heartbeat(ctx, enrolled.NodeID, enrolled.Credential, originalHandshake); e == nil {
		t.Fatal("restored old credential accepted")
	}
	var kept int
	if e = restored.QueryRow(ctx, "SELECT count(*) FROM managed_components WHERE node_id=$1", enrolled.NodeID).Scan(&kept); e != nil || kept != 1 {
		t.Fatal("component evidence lost on restore", e)
	}
	t.Log("35 tables / 12 migrations; exact populated M06 preservation, interrupted/conflicting/concurrent/repeated upgrade and isolated logical restore passed")
}

// Compare real columns/defaults, constraints, indexes, triggers and function bodies;
// normalize only the two generated isolated schema names, never semantic definitions.
func gatewayCatalog(t *testing.T, p *pgxpool.Pool) []string {
	t.Helper()
	query := `WITH definitions AS (
 SELECT 'column' kind,c.relname owner,a.attnum::text identity,concat_ws(':',a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull::text,COALESCE(pg_get_expr(d.adbin,d.adrelid),'')) body
 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
 WHERE n.nspname=current_schema() AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT 'constraint',c.relname,k.conname,pg_get_constraintdef(k.oid) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema()
 UNION ALL SELECT 'index',c.relname,x.relname,pg_get_indexdef(i.indexrelid) FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_class x ON x.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema()
 UNION ALL SELECT 'trigger',c.relname,g.tgname,pg_get_triggerdef(g.oid) FROM pg_trigger g JOIN pg_class c ON c.oid=g.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND NOT g.tgisinternal
 UNION ALL SELECT 'function',p.proname,pg_get_function_identity_arguments(p.oid),pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema()
 ) SELECT kind,owner,identity,replace(body,current_schema(),'OWNED_SCHEMA') FROM definitions ORDER BY 1,2,3`
	rows, e := p.Query(t.Context(), query)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var kind, owner, id, body string
		if e = rows.Scan(&kind, &owner, &id, &body); e != nil {
			t.Fatal(e)
		}
		out = append(out, strings.Join([]string{kind, owner, id, body}, "\t"))
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	return out
}
