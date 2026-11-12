package identity_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func TestWorkspaceAuditAssistantProjection(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	s := identityfixture.NewAtVersion(t, 52)
	password := identityfixture.Password()
	const adminEmail = "admin@example.invalid"
	if err := s.Bootstrap(ctx, adminEmail, "Audit Administrator", password, "Audit installation", "Default"); err != nil {
		t.Fatal(err)
	}
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	var tenant, adminID, sibling string
	if err := s.DB.QueryRow(ctx, `SELECT tenant_id::text,primary_admin_user_id::text,default_workspace_id::text
		FROM installation_state WHERE id=1`).Scan(&tenant, &adminID, &sibling); err != nil {
		t.Fatal(err)
	}
	workspace, foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString(), uuid.NewString()
	subject := uuid.NewString()
	const subjectEmail = "subject@example.invalid"
	exec("INSERT INTO tenants(id,name,status) VALUES($1,'Foreign','active')", foreignTenant)
	exec(`INSERT INTO workspaces(id,tenant_id,name,status)
		VALUES($1,$2,'Audit workspace','active'),($3,$4,'Foreign workspace','active')`,
		workspace, tenant, foreignWorkspace, foreignTenant)
	exec(`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status)
		VALUES($1,$2,$2,'Audit Subject','en','active')`, subject, subjectEmail)
	exec(`INSERT INTO password_credentials(user_id,password_phc)
		SELECT $1,password_phc FROM password_credentials WHERE user_id=$2`, subject, adminID)
	exec(`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role)
		VALUES($1,$2,$3,'member'),($1,$2,$4,'member'),($1,$5,$6,'member')`, subject, tenant, workspace, sibling, foreignTenant, foreignWorkspace)

	const exact = `{"actor_kind":"product_assistant","actor_id":"ownsolid.product-assistant"}`
	const action = "product_assistant.list_applications"
	stamp := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	insert := func(id, tenantID, workspaceID, audience, action, metadata string, actor, user any) string {
		t.Helper()
		request := uuid.NewString()
		exec(`INSERT INTO audit_events(id,occurred_at,tenant_id,workspace_id,audience,action,outcome,request_id,actor_user_id,subject_user_id,metadata)
			VALUES($1,$2,$3,$4,$5,$6,'success',$7,$8,$9,$10::jsonb)`,
			id, stamp, tenantID, workspaceID, audience, action, request, actor, user, metadata)
		return request
	}
	unchained := insert(uuid.NewString(), tenant, workspace, "portal", action, exact, nil, subject)
	chainMigration, err := migrations.Files.ReadFile("postgresql/000053_audit_hash_chain.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	checksum := sha256.Sum256(chainMigration)
	exec(string(chainMigration))
	exec("INSERT INTO schema_migrations(version,checksum) VALUES(53,$1)", hex.EncodeToString(checksum[:]))
	v1 := insert(uuid.NewString(), tenant, workspace, "portal", action, exact, nil, subject)
	if err := database.Migrate(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	identityfixture.SeedLicense(t, s)
	_, adminToken, _, err := s.Login(ctx, adminEmail, password, "admin", "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	admin, err := s.Authenticate(ctx, adminToken, "admin")
	if err != nil {
		t.Fatal(err)
	}
	portal := identityfixture.Login(t, s, subjectEmail, password, "portal")

	store, err := s.Repository()
	if err != nil {
		t.Fatal(err)
	}
	appendAssistant := func(workspaceID string) (string, string) {
		t.Helper()
		event := audit.ProductAssistantEvent{
			SubjectUserID: subject, TenantID: tenant, WorkspaceID: workspaceID, Audience: "portal",
			RequestID: uuid.NewString(), ToolID: "list_applications", Outcome: "success",
		}
		err := store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			return audit.WriteProductAssistantWithin(ctx, tx, event)
		})
		if err != nil {
			t.Fatal(err)
		}
		var id string
		if err := s.DB.QueryRow(ctx, "SELECT id::text FROM audit_events WHERE request_id=$1", event.RequestID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return event.RequestID, id
	}
	genuine, genuineID := appendAssistant(workspace)
	secondGenuine, _ := appendAssistant(workspace)
	// The partitioned table permits the same ID at a different timestamp.
	human := insert(genuineID, tenant, workspace, "admin", action, exact, adminID, subject)
	ordinary := insert(uuid.NewString(), tenant, workspace, "system", "system.retention", `{"detail":"private-fixture-metadata"}`, nil, nil)
	extra := insert(uuid.NewString(), tenant, workspace, "portal", action, strings.TrimSuffix(exact, "}")+`,"detail":"private-fixture-metadata"}`, nil, subject)
	wrongKind := insert(uuid.NewString(), tenant, workspace, "portal", action, `{"actor_kind":["product_assistant"],"actor_id":"ownsolid.product-assistant"}`, nil, subject)
	wrongID := insert(uuid.NewString(), tenant, workspace, "portal", action, `{"actor_kind":"product_assistant","actor_id":"ownsolid.other"}`, nil, subject)
	noSubject := insert(uuid.NewString(), tenant, workspace, "portal", action, exact, nil, nil)
	wrongAudience := insert(uuid.NewString(), tenant, workspace, "system", action, exact, nil, subject)
	wrongAction := insert(uuid.NewString(), tenant, workspace, "portal", "product_assistant.not a tool", exact, nil, subject)
	unrelated := insert(uuid.NewString(), tenant, workspace, "portal", "system.retention", exact, nil, subject)
	altered := insert(uuid.NewString(), tenant, workspace, "portal", action, exact, nil, subject)
	exec("UPDATE audit_events SET hash=decode(repeat('00',32),'hex') WHERE request_id=$1", altered)
	siblingEvent, _ := appendAssistant(sibling)
	foreign := insert(uuid.NewString(), foreignTenant, foreignWorkspace, "portal", action, exact, nil, subject)
	exec("UPDATE workspaces SET status='disabled' WHERE id=$1", workspace)

	restricted := auditListReader(t, ctx, s)
	list := func(workspaceID string, filters url.Values) identity.WorkspaceAuditList {
		t.Helper()
		page, err := restricted.ListWorkspaceAuditForActor(ctx, admin, workspaceID, filters)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := list(workspace, nil)
	key := func(item map[string]any) identity.AuditEventKey {
		t.Helper()
		return identity.AuditEventKey{ID: item["id"].(string), OccurredAt: item["occurred_at"].(string)}
	}
	items := make(map[string]map[string]any)
	for _, item := range page.Items {
		items[item["request_id"].(string)] = item
	}
	wantOrdinary := []string{unchained, v1, human, ordinary, extra, wrongKind, wrongID, noSubject, wrongAudience, wrongAction, unrelated, altered}
	if len(items) != len(wantOrdinary)+2 || page.NextCursor != nil || len(page.AssistantActors) != 2 {
		t.Fatalf("unexpected scoped result: %d items, %d attributions, cursor %v", len(items), len(page.AssistantActors), page.NextCursor != nil)
	}
	for _, request := range wantOrdinary {
		item, found := items[request]
		if !found {
			t.Fatalf("ordinary event %s missing", request)
		}
		if _, attributed := page.AssistantActors[key(item)]; attributed {
			t.Fatalf("ordinary event %s received assistant attribution", request)
		}
	}
	assistant, found := items[genuine]
	if !found || assistant["actor_user_id"] != nil || assistant["actor_email"] != nil ||
		assistant["subject_user_id"] != subject || assistant["subject_email"] != subjectEmail ||
		page.AssistantActors[key(assistant)] != (identity.AuditAssistantActor{Kind: "product_assistant", ID: "ownsolid.product-assistant"}) {
		t.Fatal("the built-in assistant and its distinct user subject were not preserved")
	}
	if item := items[human]; item["actor_user_id"] != adminID || item["actor_email"] != adminEmail ||
		item["subject_user_id"] != subject || item["subject_email"] != subjectEmail {
		t.Fatal("human actor or independent subject lost")
	}
	other := list(sibling, nil)
	if len(other.AssistantActors) != 1 {
		t.Fatal("sibling workspace attribution mixed scopes")
	}
	for _, item := range other.Items {
		if _, hasAssistant := other.AssistantActors[key(item)]; hasAssistant && item["request_id"] != siblingEvent {
			t.Fatal("sibling workspace attributed a different event")
		}
	}
	if other := list(sibling, url.Values{"request_id": {genuine}}); len(other.Items) != 0 || len(other.AssistantActors) != 0 {
		t.Fatal("a request filter bypassed the workspace scope")
	}

	t.Run("authorization and scope", func(t *testing.T) {
		forged := identityfixture.Login(t, s, adminEmail, password, "portal")
		forged.Me.Audience = "admin"
		foreignClaim := admin
		foreignClaim.Me.Workspace = &identity.Workspace{ID: foreignWorkspace, TenantID: foreignTenant, Role: "owner"}
		for _, tc := range []struct {
			name, workspaceID, code string
			actor                   identity.Auth
			query                   url.Values
		}{
			{"absent session", workspace, "AUTH_SESSION_EXPIRED", identity.Auth{}, nil},
			{"Portal session", workspace, "ACCESS_DENIED", portal, nil},
			{"forged audience", workspace, "AUTH_SESSION_EXPIRED", forged, nil},
			{"foreign workspace", foreignWorkspace, "RESOURCE_NOT_FOUND", admin, nil},
			{"foreign context claim", foreignWorkspace, "RESOURCE_NOT_FOUND", foreignClaim, nil},
			{"unknown workspace", uuid.NewString(), "RESOURCE_NOT_FOUND", admin, nil},
			{"invalid workspace", "foreign", "VALIDATION_FAILED", admin, nil},
			{"tenant query injection", workspace, "VALIDATION_FAILED", admin, url.Values{"tenant_id": {foreignTenant}}},
			{"workspace query injection", workspace, "VALIDATION_FAILED", admin, url.Values{"workspace_id": {foreignWorkspace}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				result, err := restricted.ListWorkspaceAuditForActor(ctx, tc.actor, tc.workspaceID, tc.query)
				if err == nil || err.Error() != tc.code || len(result.Items) != 0 || len(result.AssistantActors) != 0 {
					t.Fatalf("expected %s and no events, got %v", tc.code, err)
				}
			})
		}
	})

	t.Run("pagination and legacy contract", func(t *testing.T) {
		first := list(workspace, url.Values{"limit": {"1"}, "category": {"product_assistant"}})
		if first.NextCursor == nil || len(first.Items) != 1 || len(first.AssistantActors) != 1 {
			t.Fatal("unexpected first page or attribution from the lookahead row")
		}
		query := url.Values{"limit": {"1"}, "category": {"product_assistant"}, "cursor": {*first.NextCursor}}
		next := list(workspace, query)
		if len(next.Items) != 1 || len(next.AssistantActors) != 1 {
			t.Fatal("scoped cursor did not resume within the same workspace")
		}
		firstRequest, nextRequest := first.Items[0]["request_id"], next.Items[0]["request_id"]
		if firstRequest == nextRequest || (firstRequest != genuine && nextRequest != genuine) ||
			(firstRequest != secondGenuine && nextRequest != secondGenuine) {
			t.Fatal("scoped pages lost or repeated an assistant event")
		}
		if _, err := restricted.ListWorkspaceAuditForActor(ctx, admin, sibling, query); err == nil || err.Error() != "INVALID_CURSOR" {
			t.Fatalf("cursor crossed workspace: %v", err)
		}
		if _, err := restricted.ListForActor(ctx, admin, "audit-events", query); err == nil || err.Error() != "INVALID_CURSOR" {
			t.Fatalf("scoped cursor accepted by legacy list: %v", err)
		}
		legacy, err := restricted.ListForActor(ctx, admin, "audit-events", url.Values{"limit": {"1"}})
		if err != nil || legacy.NextCursor == nil {
			t.Fatalf("legacy pagination unavailable: %v", err)
		}
		if _, err := restricted.ListWorkspaceAuditForActor(ctx, admin, workspace, url.Values{"cursor": {*legacy.NextCursor}}); err == nil || err.Error() != "INVALID_CURSOR" {
			t.Fatalf("legacy cursor accepted by scoped list: %v", err)
		}
		for _, request := range append(wantOrdinary, genuine, secondGenuine) {
			query := url.Values{"request_id": {request}}
			old, err := restricted.ListForActor(ctx, admin, "audit-events", query)
			if err != nil || len(old.Items) != 1 || len(old.Items[0]) != 13 {
				t.Fatalf("legacy Admin list shape changed: %v", err)
			}
			auditJSON(t, list(workspace, query), old)
		}
		for _, scope := range []string{"tenant_id", "workspace_id"} {
			if _, err := restricted.ListForActor(ctx, admin, "audit-events", url.Values{scope: {foreignTenant}}); err == nil || err.Error() != "VALIDATION_FAILED" {
				t.Fatalf("legacy list accepted client-provided scope %s: %v", scope, err)
			}
		}
		for _, tc := range []struct {
			actor identity.Auth
			code  string
		}{{identity.Auth{}, "AUTH_SESSION_EXPIRED"}, {portal, "ACCESS_DENIED"}} {
			if _, err := restricted.ListForActor(ctx, tc.actor, "audit-events", nil); err == nil || err.Error() != tc.code {
				t.Fatalf("legacy list authorization changed: %v", err)
			}
		}
		query = url.Values{"request_id": {foreign}}
		old, err := restricted.ListForActor(ctx, admin, "audit-events", query)
		if err != nil || len(old.Items) != 1 {
			t.Fatalf("legacy installation-wide list changed: %v", err)
		}
		trusted, err := restricted.List(ctx, "audit-events", query)
		if err != nil {
			t.Fatal(err)
		}
		auditJSON(t, trusted, old)
	})
	if err := s.Logout(ctx, adminToken, "admin", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := restricted.ListWorkspaceAuditForActor(ctx, admin, workspace, nil); err == nil || err.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatalf("revoked session accepted: %v", err)
	}
	if _, err := restricted.ListForActor(ctx, admin, "audit-events", nil); err == nil || err.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatalf("legacy audit accepted a revoked session: %v", err)
	}
}

func auditJSON(t *testing.T, projected any, legacy identity.List) {
	t.Helper()
	raw, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	old, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, old) || bytes.Contains(raw, []byte("private-fixture-metadata")) ||
		bytes.Contains(raw, []byte(`"metadata"`)) || bytes.Contains(raw, []byte(`"actor_kind"`)) ||
		bytes.Contains(raw, []byte(`"actor_id"`)) {
		t.Fatal("repository data escaped the legacy JSON contract")
	}
}

func auditListReader(t *testing.T, ctx context.Context, s *identity.Service) *identity.Service {
	t.Helper()
	var schema string
	if err := s.DB.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	role := pgx.Identifier{"audit_list_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	exec := func(statement string) {
		t.Helper()
		if _, err := s.DB.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE ROLE " + role + " NOLOGIN")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, statement := range []string{"DROP OWNED BY " + role, "DROP ROLE " + role} {
			if _, err := s.DB.Exec(ctx, statement); err != nil {
				t.Error(err)
			}
		}
	})
	exec("GRANT USAGE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + role)
	exec("GRANT SELECT ON installation_state, workspaces TO " + role)
	// Serving can append access events without reading or mutating the private chain head.
	exec("GRANT SELECT, INSERT ON audit_events TO " + role)
	// Audit insert defaults resolve their local cell using the caller's privileges.
	exec("GRANT SELECT(id,site_id,is_local) ON cells TO " + role)
	exec("GRANT SELECT, UPDATE ON users, sessions TO " + role)
	config := s.DB.Config()
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+role)
		return err
	}
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	var canReadHead bool
	if err := db.QueryRow(ctx, "SELECT has_table_privilege('audit_chain_head','SELECT') OR has_any_column_privilege('audit_chain_head','SELECT')").Scan(&canReadHead); err != nil || canReadHead {
		t.Fatalf("expected a reader without chain head privileges: %v", err)
	}
	reader := *s
	reader.DB = db
	return &reader
}
