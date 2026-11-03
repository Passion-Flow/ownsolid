package database_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
	"time"
)

func m02Snapshot(t *testing.T, p *pgxpool.Pool) map[string]string {
	t.Helper()
	out := map[string]string{}
	tables := []string{"schema_migrations", "tenants", "workspaces", "users", "password_credentials", "workspace_memberships", "installation_state", "sessions", "invitations", "password_reset_tokens", "mail_deliveries", "audit_events", "rate_limit_buckets", "idempotency_records", "platform_license_state", "platform_license_artifacts", "workspace_notes"}
	for _, table := range tables {
		where := ""
		if table == "schema_migrations" {
			where = " WHERE version<=6"
		}
		var raw string
		if e := p.QueryRow(t.Context(), "SELECT COALESCE(string_agg(v::text,E'\\n' ORDER BY v::text),'') FROM (SELECT to_jsonb(t) AS v FROM "+table+" t"+where+") rows").Scan(&raw); e != nil {
			t.Fatal(table, e)
		}
		out[table] = fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	}
	return out
}
func TestM03DeliveredM02UpgradeAndIsolatedRestore(t *testing.T) {
	s := fixture.NewM02(t)
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
	baseline := m02Snapshot(t, s.DB)
	equal := func(p *pgxpool.Pool) {
		for k, v := range m02Snapshot(t, p) {
			if v != baseline[k] {
				t.Fatal("M02 row changed", k)
			}
		}
	}
	if database.Check(ctx, s.DB, "nodes") == nil {
		t.Fatal("unupgraded accepted")
	}
	lock, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = lock.Exec(ctx, "LOCK TABLE workspaces IN ACCESS EXCLUSIVE MODE"); e != nil {
		t.Fatal(e)
	}
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	e = database.Migrate(bounded, s.DB, "nodes")
	cancel()
	_ = lock.Rollback(ctx)
	if e == nil {
		t.Fatal("interruption accepted")
	}
	equal(s.DB)
	if _, e = s.DB.Exec(ctx, "CREATE TABLE managed_nodes(id integer)"); e != nil {
		t.Fatal(e)
	}
	if database.Migrate(ctx, s.DB, "nodes") == nil {
		t.Fatal("partial conflict accepted")
	}
	if _, e = s.DB.Exec(ctx, "DROP TABLE managed_nodes"); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, s.DB, "workspaces"); e != nil {
		t.Fatal(e)
	}
	equal(s.DB)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- database.Migrate(ctx, s.DB, "nodes") }()
	}
	for i := 0; i < 2; i++ {
		if e = <-results; e != nil {
			t.Fatal(e)
		}
	}
	if e = database.Migrate(ctx, s.DB, "nodes"); e != nil {
		t.Fatal(e)
	}
	equal(s.DB)
	var tables, versions int
	if e = s.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),(SELECT count(*) FROM schema_migrations)").Scan(&tables, &versions); e != nil || tables != 20 || versions != 8 {
		t.Fatal(tables, versions, e)
	}
	for _, profile := range []string{"account", "workspaces", "platform"} {
		if database.Check(ctx, s.DB, profile) == nil {
			t.Fatal("wrong catalog", profile)
		}
	}
	profile := "nodes"
	s.Config.Server.ApplicationProfile = &profile
	s.Mail.Config = s.Config
	n := &nodes.Service{DB: s.DB, Identity: s, Workspaces: w}
	token, e := n.Mint(ctx, admin, admin.Me.Workspace.ID, "Restored discovery node", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	facts := wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-restore", LogicalCPUs: 2, MemoryTotalBytes: 1024, DiskPath: "/", DiskTotalBytes: 2048, DiskAvailableBytes: 1024, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}
	enrolled, e := n.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: wire.Handshake{ProtocolVersion: wire.Version, Capabilities: []string{wire.CollectFacts}, Facts: facts}}, uuid.NewString())
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
	owned := fixture.OwnedPostgres(t, s.DB)
	archive := owned.DumpSchema(t, ctx)
	restored := owned.RestoreFresh(t, ctx, s.DB, archive, true)
	if e = database.Check(ctx, restored, "nodes"); e != nil {
		t.Fatal(e)
	}
	clone := *s
	clone.DB = restored
	mail := *s.Mail
	mail.DB = restored
	clone.Mail = &mail
	if e = recovery.InvalidateRestored(ctx, &clone, "Synthetic isolated M03 restore"); e != nil {
		t.Fatal(e)
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
	t.Log("20 tables / 8 migrations; exact populated M02 preservation, interrupted/conflicting/concurrent/repeated upgrade and isolated logical restore passed")
}
