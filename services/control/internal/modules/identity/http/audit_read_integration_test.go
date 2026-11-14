package http_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

const auditReadPath = "/api/v1/admin/audit-events"

type auditReadFixture struct {
	service                                          *identity.Service
	server                                           *httptest.Server
	admin, portal                                    *client
	password, tenant, workspace, actor, seed, hidden string
}

func newAuditReadFixture(t *testing.T) *auditReadFixture {
	t.Helper()
	s, server, portal, admin, password := setup(t)
	f := &auditReadFixture{service: s, server: server, portal: portal, admin: admin, password: password, seed: uuid.NewString(), hidden: security.Token()}
	admin.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	if err := s.DB.QueryRow(t.Context(), "SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&f.tenant, &f.workspace, &f.actor); err != nil {
		t.Fatal(err)
	}
	metadata, _ := json.Marshal(map[string]string{"private_source": f.hidden})
	if _, err := s.DB.Exec(t.Context(), `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,reason,request_id,metadata,identifier_digest)
 VALUES($1,clock_timestamp(),$2,$3,$4,'admin','system.retention','success','Approved operator reason',$5,$6::jsonb,$7)`, uuid.NewString(), f.actor, f.tenant, f.workspace, f.seed, string(metadata), security.Digest(f.hidden)); err != nil {
		t.Fatal(err)
	}
	return f
}

type auditReadObservation struct {
	status  int
	headers http.Header
	body    map[string]any
	raw     []byte
}

func requestAuditRead(ctx context.Context, c *client, path string, headers map[string]string) (auditReadObservation, error) {
	var out auditReadObservation
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.s.URL+cellroute.Public(path, wire.LocalScope()), nil)
	if err != nil {
		return out, err
	}
	request.Host = c.audience + ".example.invalid"
	request.Header.Set("Origin", "https://"+request.Host)
	for _, cookie := range c.cookies {
		request.AddCookie(cookie)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return out, err
	}
	defer response.Body.Close()
	if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" {
		return out, errors.New("audit read transport boundary failed")
	}
	for _, cookie := range response.Cookies() {
		if response.StatusCode != http.StatusUnauthorized || cookie.MaxAge >= 0 || cookie.Value != "" || !cookie.Secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" {
			return out, errors.New("audit read issued an unexpected credential cookie")
		}
	}
	out.status, out.headers = response.StatusCode, response.Header.Clone()
	out.raw, err = io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(out.raw) > 1<<20 || json.Unmarshal(out.raw, &out.body) != nil {
		return auditReadObservation{}, errors.New("audit read fixture response invalid")
	}
	return out, nil
}

func (f *auditReadFixture) count(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.service.DB.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE action='audit.events_read'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *auditReadFixture) checkEvent(t *testing.T, observation auditReadObservation, reason string) {
	t.Helper()
	var actor, tenant, workspace, audience, outcome, target, storedReason, metadata string
	var subject, targetID sql.NullString
	err := f.service.DB.QueryRow(t.Context(), `SELECT actor_user_id::text,subject_user_id::text,tenant_id::text,workspace_id::text,audience,outcome,target_type,target_id::text,reason,metadata::text
 FROM audit_events WHERE action='audit.events_read' AND request_id=$1`, observation.headers.Get("X-Request-ID")).Scan(&actor, &subject, &tenant, &workspace, &audience, &outcome, &target, &targetID, &storedReason, &metadata)
	if err != nil || actor != f.actor || subject.Valid || tenant != f.tenant || workspace != f.workspace || audience != "admin" || outcome != "success" || target != "installation" || targetID.Valid || storedReason != reason || metadata != "{}" || strings.Contains(storedReason+metadata, f.hidden) {
		t.Fatal("audit access event lost current actor, scope or bounded selection", err)
	}
}

func TestAdminAuditReadAccountingHTTPS(t *testing.T) {
	f := newAuditReadFixture(t)
	empty, err := requestAuditRead(t.Context(), f.admin, auditReadPath+"?category=audit&limit=1", nil)
	if err != nil || empty.status != 200 || len(empty.body["data"].(map[string]any)["items"].([]any)) != 0 {
		t.Fatal("an empty audit view included its own access event", err)
	}
	f.checkEvent(t, empty, "installation_audit_read;limit=1;returned=0;assistant=false")
	for _, preferred := range []bool{false, true} {
		spoofed := uuid.NewString()
		headers := map[string]string{"X-Request-ID": spoofed}
		if preferred {
			headers["X-Ownsolid-Audit-Actor"] = "true"
		}
		out, err := requestAuditRead(t.Context(), f.admin, auditReadPath+"?limit=2&request_id="+f.seed, headers)
		if err != nil || out.status != 200 || out.headers.Get("X-Request-ID") == spoofed || f.admin.schemas["AuditListResponse"].Validate(out.body) != nil || strings.Contains(string(out.raw), f.hidden) {
			t.Fatal("actual audit read did not preserve its safe correlated representation", err)
		}
		data := out.body["data"].(map[string]any)
		items := data["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["reason"] != "Approved operator reason" || len(items[0].(map[string]any)) != 13 {
			t.Fatal("audit read changed the existing allowed operator fields")
		}
		reason := "installation_audit_read;limit=2;returned=1;assistant=false"
		if preferred {
			reason = "installation_audit_read;limit=2;returned=1;assistant=true"
		}
		f.checkEvent(t, out, reason)
	}
	if f.count(t) != 3 {
		t.Fatal("a representation preference changed access accounting")
	}
}

func TestAdminAuditReadAppendRefusalHTTPS(t *testing.T) {
	f := newAuditReadFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	store, err := f.service.Repository()
	if err != nil {
		t.Fatal(err)
	}
	request := uuid.NewString()
	if err = store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
		return audit.WriteProductAssistantWithin(ctx, tx, audit.ProductAssistantEvent{SubjectUserID: f.actor, TenantID: f.tenant, WorkspaceID: f.workspace, Audience: "admin", RequestID: request, ToolID: "list_applications", Outcome: "success"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.DB.Exec(t.Context(), "ALTER TABLE audit_events ADD CONSTRAINT fixture_audit_read_refusal CHECK(action<>'audit.events_read') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	path := auditReadPath + "?request_id=" + request
	headers := map[string]string{"X-Ownsolid-Audit-Actor": "true"}
	out, err := requestAuditRead(t.Context(), f.admin, path, headers)
	if err != nil || out.status != 503 || out.body["code"] != "STORAGE_UNAVAILABLE" || out.body["data"] != nil || strings.Contains(string(out.raw), "actor_kind") || f.count(t) != 0 {
		t.Fatal("failed audit append released a page or assistant attribution", err)
	}
	if _, err = f.service.DB.Exec(t.Context(), "ALTER TABLE audit_events DROP CONSTRAINT fixture_audit_read_refusal"); err != nil {
		t.Fatal(err)
	}
	retry, err := requestAuditRead(t.Context(), f.admin, path, headers)
	if err != nil || retry.status != 200 || retry.headers.Get("X-Request-ID") == out.headers.Get("X-Request-ID") || f.count(t) != 1 {
		t.Fatal("explicit read retry did not recover with a new access event", err)
	}
	items := retry.body["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["actor_kind"] != "product_assistant" {
		t.Fatal("repair lost verified assistant attribution")
	}
}

func TestAdminAuditReadAuthorityHTTPS(t *testing.T) {
	f := newAuditReadFixture(t)
	f.portal.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@example.invalid", "password": f.password}, 200, "LoginResponse")
	out, err := requestAuditRead(t.Context(), f.portal, auditReadPath, nil)
	if err != nil || out.status != 404 || out.body["data"] != nil {
		t.Fatal("Portal reached the Admin audit read", err)
	}
	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	if _, err = f.service.DB.Exec(t.Context(), "INSERT INTO tenants(id,name,status) VALUES($1,'Foreign audit read tenant','active')", foreignTenant); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.DB.Exec(t.Context(), "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign audit read workspace','active')", foreignWorkspace, foreignTenant); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"tenant_id=" + foreignTenant, "workspace_id=" + foreignWorkspace, "cursor=invalid"} {
		out, err = requestAuditRead(t.Context(), f.admin, auditReadPath+"?"+query, nil)
		if err != nil || out.status != 400 || out.body["data"] != nil {
			t.Fatal("audit read accepted injected scope or a malformed cursor", err)
		}
	}
	actor, err := f.service.Authenticate(t.Context(), f.admin.cookies["__Host-admin_session"].Value, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if page, e := f.service.ListWorkspaceAuditForActor(t.Context(), actor, foreignWorkspace, nil); e == nil || e.Error() != "RESOURCE_NOT_FOUND" || len(page.Items) != 0 {
		t.Fatal("known foreign workspace bypassed the server tenant check", e)
	}
	if f.count(t) != 0 {
		t.Fatal("a refusal was recorded as a completed diagnostic read")
	}
	memberID, memberEmail := uuid.NewString(), "audit-member-"+uuid.NewString()+"@example.invalid"
	if _, err = f.service.DB.Exec(t.Context(), "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$2,'Audit member','en','active',false)", memberID, memberEmail); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.DB.Exec(t.Context(), "INSERT INTO password_credentials(user_id,password_phc) SELECT $1,password_phc FROM password_credentials WHERE user_id=$2", memberID, f.actor); err != nil {
		t.Fatal(err)
	}
	member := newClient(t, f.server, "portal", f.admin.schemas)
	member.mutate(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": memberEmail, "password": f.password}, 200, "LoginResponse")
	forged := newClient(t, f.server, "admin", f.admin.schemas)
	cookie := *member.cookies["__Host-portal_session"]
	cookie.Name = "__Host-admin_session"
	forged.cookies[cookie.Name] = &cookie
	out, err = requestAuditRead(t.Context(), forged, auditReadPath, nil)
	if err != nil || out.status != 401 || out.body["data"] != nil || f.count(t) != 0 {
		t.Fatal("member cookie acquired Admin diagnostic authority", err)
	}
	stale := newClient(t, f.server, "admin", f.admin.schemas)
	stale.cookies = maps.Clone(f.admin.cookies)
	f.admin.mutate(http.MethodPost, "/api/v1/auth/logout", nil, 204, "")
	out, err = requestAuditRead(t.Context(), stale, auditReadPath, nil)
	if err != nil || out.status != 401 || out.body["code"] != "AUTH_SESSION_EXPIRED" || out.body["data"] != nil || f.count(t) != 0 {
		t.Fatal("revoked session completed an audit read", err)
	}
}

func TestAdminAuditReadConcurrentHTTPS(t *testing.T) {
	f := newAuditReadFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	type result struct {
		observation auditReadObservation
		err         error
	}
	const readers = 4
	completed := make(chan result, readers)
	for range readers {
		go func() {
			out, err := requestAuditRead(ctx, f.admin, auditReadPath+"?limit=1&request_id="+f.seed, nil)
			completed <- result{out, err}
		}()
	}
	seen := make(map[string]bool)
	for range readers {
		select {
		case result := <-completed:
			if result.err != nil || result.observation.status != 200 {
				t.Fatal("bounded concurrent audit read failed", result.err)
			}
			request := result.observation.headers.Get("X-Request-ID")
			if seen[request] {
				t.Fatal("concurrent reads shared an access identity")
			}
			seen[request] = true
			f.checkEvent(t, result.observation, "installation_audit_read;limit=1;returned=1;assistant=false")
		case <-ctx.Done():
			t.Fatal("concurrent audit reads exceeded their fixture deadline")
		}
	}
	if f.count(t) != readers {
		t.Fatal("concurrent reads lost or duplicated an access event")
	}
}

func TestAdminAuditReadCancellationHTTPS(t *testing.T) {
	f := newAuditReadFixture(t)
	lock, err := f.service.DB.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = lock.Rollback(cleanup)
	}()
	if _, err = lock.Exec(t.Context(), "LOCK TABLE audit_events IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		observation auditReadObservation
		err         error
	}
	completed := make(chan result, 1)
	go func() {
		out, err := requestAuditRead(ctx, f.admin, auditReadPath+"?request_id="+f.seed, nil)
		completed <- result{out, err}
	}()
	awaitBlocked := func(wanted bool) {
		t.Helper()
		deadline, stop := context.WithTimeout(t.Context(), 3*time.Second)
		defer stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			var blocked bool
			if err := f.service.DB.QueryRow(deadline, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
 AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE 'SELECT a.id::text,%FROM audit_events a%')`).Scan(&blocked); err != nil {
				t.Fatal("audit read wait observation unavailable", err)
			}
			if blocked == wanted {
				return
			}
			select {
			case <-tick.C:
			case <-deadline.Done():
				t.Fatal("audit read did not reach its expected native wait state")
			}
		}
	}
	awaitBlocked(true)
	cancel()
	select {
	case result := <-completed:
		if !errors.Is(result.err, context.Canceled) || result.observation.body != nil {
			t.Fatal("canceled audit read returned a protected page")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled audit request did not join")
	}
	// Keep the table locked until cancellation has reached the actual SQL read.
	awaitBlocked(false)
	if err = lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.count(t) != 0 {
		t.Fatal("canceled read was recorded as a completed access")
	}
	out, err := requestAuditRead(t.Context(), f.admin, auditReadPath+"?request_id="+f.seed, nil)
	if err != nil || out.status != 200 || f.count(t) != 1 {
		t.Fatal("explicit read after cancellation did not recover", err)
	}
}
