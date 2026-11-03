package database_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/recovery"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// Hash all delivered account rows in memory, never writing credentials/mail to logs.
func accountSnapshot(t *testing.T, p *pgxpool.Pool) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, table := range []string{"schema_migrations", "tenants", "workspaces", "users", "password_credentials", "workspace_memberships", "installation_state", "sessions", "invitations", "password_reset_tokens", "mail_deliveries", "audit_events", "rate_limit_buckets", "idempotency_records", "platform_license_state", "platform_license_artifacts"} {
		where := ""
		if table == "schema_migrations" {
			where = " WHERE version<=4"
		}
		var raw string
		statement := "SELECT COALESCE(string_agg(v::text,E'\\n' ORDER BY v::text),'') FROM (SELECT to_jsonb(t)-'version'-'updated_at'-'authority_id' AS v FROM " + table + " t" + where + ") rows"
		// Migration versions and account optimistic versions are part of the retained data.
		if table != "workspaces" && table != "workspace_memberships" {
			statement = "SELECT COALESCE(string_agg(v::text,E'\\n' ORDER BY v::text),'') FROM (SELECT to_jsonb(t) AS v FROM " + table + " t" + where + ") rows"
		}
		if e := p.QueryRow(t.Context(), statement).Scan(&raw); e != nil {
			t.Fatal("snapshot", table, e)
		}
		sum := sha256.Sum256([]byte(raw))
		result[table] = hex.EncodeToString(sum[:])
	}
	return result
}
func assertAccountSnapshot(t *testing.T, p *pgxpool.Pool, expected map[string]string) {
	t.Helper()
	for k, v := range accountSnapshot(t, p) {
		if expected[k] != v {
			t.Fatal("account state changed", k)
		}
	}
}

func TestM02DeliveredM01ForwardUpgradeAndRestore(t *testing.T) {
	s := fixture.NewAccount(t)
	ctx := t.Context()
	password := fixture.Bootstrap(t, s)
	admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	for _, email := range []string{"alice@example.invalid", "pending@example.invalid"} {
		invitation, e := s.Invite(ctx, admin, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		if email == "alice@example.invalid" {
			if e = s.Register(ctx, fixture.InvitationToken(t, s, invitation.ID), "Existing account", password, uuid.NewString()); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := s.RequestReset(ctx, "alice@example.invalid", "portal", "", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	baseline := accountSnapshot(t, s.DB)
	if e := database.Check(ctx, s.DB, "workspaces"); e == nil {
		t.Fatal("unupgraded schema served")
	}
	// An interrupted operator waiting on real DDL locks leaves the four-entry ledger intact.
	lock, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = lock.Exec(ctx, "LOCK TABLE workspaces IN ACCESS SHARE MODE"); e != nil {
		t.Fatal(e)
	}
	limited, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	e = database.Migrate(limited, s.DB, "workspaces")
	cancel()
	_ = lock.Rollback(ctx)
	if e == nil {
		t.Fatal("interrupted upgrade succeeded")
	}
	assertAccountSnapshot(t, s.DB, baseline)
	// DDL failure after the first ALTER must roll back all schema and journal changes.
	if _, e = s.DB.Exec(ctx, "CREATE TABLE workspace_notes(id integer)"); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(ctx, s.DB, "workspaces"); e == nil {
		t.Fatal("conflicting upgrade succeeded")
	}
	if _, e = s.DB.Exec(ctx, "DROP TABLE workspace_notes"); e != nil {
		t.Fatal(e)
	}
	if e = database.Check(ctx, s.DB, "account"); e != nil {
		t.Fatal("old installation not recoverable", e)
	}
	assertAccountSnapshot(t, s.DB, baseline)
	// Concurrent/repeated operator execution is serialized by the existing migration lock.
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- database.Migrate(ctx, s.DB, "workspaces") }()
	}
	for i := 0; i < 2; i++ {
		if e = <-results; e != nil {
			t.Fatal(e)
		}
	}
	if e = database.Migrate(ctx, s.DB, "workspaces"); e != nil {
		t.Fatal(e)
	}
	assertAccountSnapshot(t, s.DB, baseline)
	if e = database.Check(ctx, s.DB, "workspaces"); e != nil {
		t.Fatal(e)
	}
	for _, profile := range []string{"account", "platform"} {
		if database.Check(ctx, s.DB, profile) == nil {
			t.Fatal("wrong ledger accepted", profile)
		}
	}
	var tables, versions int
	if e = s.DB.QueryRow(ctx, "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()),(SELECT count(*) FROM schema_migrations)").Scan(&tables, &versions); e != nil || tables != 17 || versions != 6 {
		t.Fatal("minimal schema", tables, versions, e)
	}
	profile := "workspaces"
	s.Config.Server.ApplicationProfile = &profile
	s.Mail.Config = s.Config
	w := &workspaces.Service{DB: s.DB, Identity: s}
	// Existing sessions, user IDs, license and default membership work without bootstrap.
	a := fixture.Login(t, s, "alice@example.invalid", password, "portal")
	if _, e = w.List(ctx, a, url.Values{}); e != nil {
		t.Fatal(e)
	}
	created, e := w.Create(ctx, admin, "Upgraded Workspace", a.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	note, e := w.CreateNote(ctx, a, created.ID, "Retained note", "Synthetic persisted text", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	owned := fixture.OwnedPostgres(t, s.DB)
	archive := owned.DumpSchema(t, ctx)
	restored := owned.RestoreFresh(t, ctx, s.DB, archive, true)
	if e = database.Check(ctx, restored, "workspaces"); e != nil {
		t.Fatal(e)
	}
	assertAccountSnapshot(t, restored, accountSnapshot(t, s.DB))
	clone := *s
	clone.DB = restored
	mail := *s.Mail
	mail.DB = restored
	clone.Mail = &mail
	if e = recovery.InvalidateRestored(ctx, &clone, "Synthetic isolated restore"); e != nil {
		t.Fatal(e)
	}
	var liveSessions int
	if e = restored.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE revoked_at IS NULL").Scan(&liveSessions); e != nil || liveSessions != 0 {
		t.Fatal("restore retained authority", e)
	}
	renewed := fixture.Login(t, &clone, "alice@example.invalid", password, "portal")
	rw := &workspaces.Service{DB: restored, Identity: &clone}
	actual, e := rw.GetNote(ctx, renewed, created.ID, note.ID)
	if e != nil || actual.Text != note.Text {
		t.Fatal("restored note", e)
	}
	t.Log("populated delivered M01: exact 16-table data and ledger prefix preserved; interrupted/failed/concurrent/repeated upgrade; M01 login; 17-table M02; logical dump/fresh restore and authority invalidation passed")
}
