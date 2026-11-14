package http_test

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

const managedSessionsPath = "/api/v1/me/sessions"

func ownSession(t *testing.T, c *client) string {
	t.Helper()
	data := c.request(http.MethodGet, managedSessionsPath, nil, 200, "", nil)
	var id string
	for _, raw := range data["items"].([]any) {
		item := raw.(map[string]any)
		if item["current"] != true {
			continue
		}
		if id != "" {
			t.Fatal("multiple current sessions")
		}
		id = item["id"].(string)
	}
	if id == "" {
		t.Fatal("current session missing")
	}
	return id
}

func TestBrowserSessionManagementHTTPS(t *testing.T) {
	s, server, portal, admin, password := setup(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	const adminEmail = "admin@example.invalid"
	login := func(c *client, email, password string) string {
		t.Helper()
		c.mutate(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": email, "password": password}, 200, "LoginResponse")
		return ownSession(t, c)
	}
	adminID := login(admin, adminEmail, password)
	portalID := login(portal, adminEmail, password)
	portalPeer := newClient(t, server, "portal", portal.schemas)
	portalPeerID := login(portalPeer, adminEmail, password)
	pool, err := pgxpool.NewWithConfig(ctx, s.DB.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	replica := *s
	replica.DB, replica.Sources = pool, nil
	replicaServer := accountTLSServer(t, transport.New(&replica))
	remote := newClient(t, replicaServer, "admin", admin.schemas)
	remoteID := login(remote, adminEmail, password)
	remoteToken := remote.cookies["__Host-admin_session"].Value
	captured, err := replica.Authenticate(ctx, remoteToken, "admin")
	if err != nil || captured.SessionID != remoteID {
		t.Fatalf("second control handler did not issue the expected session: %v", err)
	}

	rotating := newClient(t, server, "admin", admin.schemas)
	rotatedID := login(rotating, adminEmail, password)
	rotated := newClient(t, server, "admin", admin.schemas)
	rotated.cookies = maps.Clone(rotating.cookies)
	absoluteID := login(rotating, adminEmail, password)
	idle := newClient(t, server, "admin", admin.schemas)
	idleID := login(idle, adminEmail, password)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.DB.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE sessions SET created_at=clock_timestamp()-interval '2 days',
		last_seen_at=clock_timestamp()-interval '1 second',absolute_expires_at=clock_timestamp()-interval '1 microsecond' WHERE id=$1`, absoluteID)
	exec(`UPDATE sessions SET created_at=clock_timestamp()-interval '2 days',
		last_seen_at=clock_timestamp()-$2::bigint*interval '1 second',absolute_expires_at=clock_timestamp()+interval '1 hour' WHERE id=$1`,
		idleID, int64(s.Idle("admin")/time.Second)+1)
	for _, expired := range []*client{rotated, rotating, idle} {
		problemCode(t, expired.request(http.MethodGet, managedSessionsPath, nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	}

	const foreignEmail = "foreign-session@example.invalid"
	foreign := newClient(t, server, "portal", portal.schemas)
	foreignPassword := fixture.Password()
	invite := admin.mutate(http.MethodPost, "/api/v1/admin/invitations", map[string]any{"email": foreignEmail, "locale": "en"}, 201, "InvitationResponse")
	foreign.mutate(http.MethodPost, "/api/v1/auth/register", map[string]any{
		"token": fixture.InvitationToken(t, s, invite["id"].(string)), "display_name": "Foreign member", "password": foreignPassword,
	}, 201, "RegisterResponse")
	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	exec("INSERT INTO tenants(id,name,status) VALUES($1,'Foreign tenant','active')", foreignTenant)
	exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign workspace','active')", foreignWorkspace, foreignTenant)
	exec("DELETE FROM workspace_memberships WHERE user_id=(SELECT id FROM users WHERE email_canonical=$1)", foreignEmail)
	exec(`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role)
		SELECT id,$2,$3,'member' FROM users WHERE email_canonical=$1`, foreignEmail, foreignTenant, foreignWorkspace)
	foreignID := login(foreign, foreignEmail, foreignPassword)

	requirePage := func(c *client, path, wantID string, current bool) map[string]any {
		t.Helper()
		page := c.request(http.MethodGet, path, nil, 200, "", map[string]string{"X-Ownsolid-Background": "true"})
		items := page["items"].([]any)
		if len(page) != 2 || len(items) != 1 {
			t.Fatalf("expected one scoped session, got %d", len(items))
		}
		item := items[0].(map[string]any)
		if len(item) != 6 || item["id"] != wantID || item["current"] != current || item["authentication_method"] != "password" {
			t.Fatal("session scope, attribution or safe fields changed")
		}
		for _, field := range []string{"created_at", "last_seen_at", "absolute_expires_at"} {
			if _, err := time.Parse(time.RFC3339Nano, item[field].(string)); err != nil {
				t.Fatal("invalid session timestamp", field)
			}
		}
		return page
	}
	readFirst := requirePage(admin, managedSessionsPath+"?limit=1", remoteID, false)
	cursor, ok := readFirst["next_cursor"].(string)
	if !ok || cursor == "" {
		t.Fatal("expected a cursor for the second Admin session")
	}
	pagedPath := managedSessionsPath + "?limit=1&cursor=" + url.QueryEscape(cursor)
	if page := requirePage(admin, pagedPath, adminID, true); page["next_cursor"] != nil {
		t.Fatal("revoked or expired sessions appeared in the Admin list", rotatedID, idleID, absoluteID)
	}
	portalPage := requirePage(portal, managedSessionsPath+"?limit=1", portalPeerID, false)
	portalCursor, ok := portalPage["next_cursor"].(string)
	if !ok || portalCursor == "" {
		t.Fatal("expected a cursor for the second Portal session")
	}
	requirePage(foreign, managedSessionsPath, foreignID, true)
	problemCode(t, portal.request(http.MethodGet, pagedPath, nil, 400, "Problem", nil), "INVALID_CURSOR")
	problemCode(t, foreign.request(http.MethodGet, managedSessionsPath+"?cursor="+url.QueryEscape(portalCursor), nil, 400, "Problem", nil), "INVALID_CURSOR")
	portal.mutate(http.MethodDelete, managedSessionsPath+"/"+portalPeerID, nil, 204, "")
	problemCode(t, portalPeer.request(http.MethodGet, managedSessionsPath, nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	requirePage(portal, managedSessionsPath, portalID, true)
	problemCode(t, portal.request(http.MethodGet, managedSessionsPath+"?tenant_id="+foreignTenant, nil, 400, "Problem", nil), "VALIDATION_FAILED")

	for _, source := range []*client{admin, portal} {
		destination := "portal"
		if source == portal {
			destination = "admin"
		}
		replay := newClient(t, server, destination, admin.schemas)
		sourceName, targetName := "__Host-"+source.audience+"_session", "__Host-"+destination+"_session"
		replay.cookies[targetName] = &http.Cookie{Name: targetName, Value: source.cookies[sourceName].Value}
		problemCode(t, replay.request(http.MethodGet, managedSessionsPath, nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	}
	for _, id := range []string{adminID, foreignID, uuid.NewString()} {
		problemCode(t, portal.mutate(http.MethodDelete, managedSessionsPath+"/"+id, nil, 404, "Problem"), "RESOURCE_NOT_FOUND")
	}
	requirePage(foreign, managedSessionsPath, foreignID, true)

	target := managedSessionsPath + "/" + remoteID
	admin.refresh()
	problemCode(t, admin.request(http.MethodDelete, target, nil, 403, "Problem", map[string]string{"Origin": ""}), "ORIGIN_REJECTED")
	problemCode(t, admin.request(http.MethodDelete, target, nil, 403, "Problem", map[string]string{"X-CSRF-Token": ""}), "CSRF_INVALID")
	problemCode(t, admin.request(http.MethodDelete, target+"?tenant_id="+foreignTenant, nil, 400, "Problem", nil), "VALIDATION_FAILED")
	problemCode(t, admin.request(http.MethodDelete, target, map[string]any{}, 400, "Problem", nil), "VALIDATION_FAILED")
	requirePage(remote, managedSessionsPath+"?limit=1", remoteID, true)

	// Two concurrent DELETEs use independent requests with the same live caller.
	results := make(chan error, 2)
	for range 2 {
		request, err := http.NewRequestWithContext(ctx, http.MethodDelete, server.URL+cellroute.Public(target, wire.LocalScope()), nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "admin.example.invalid"
		request.Header.Set("Origin", "https://"+request.Host)
		request.Header.Set("X-CSRF-Token", admin.csrf)
		for _, cookie := range admin.cookies {
			request.AddCookie(cookie)
		}
		go func() {
			response, err := admin.http.Do(request)
			if err == nil {
				defer response.Body.Close()
				body, readErr := io.ReadAll(response.Body)
				if readErr != nil || response.StatusCode != http.StatusNoContent || len(body) != 0 || len(response.Cookies()) != 0 {
					err = errors.New("expected a bodyless revoke without cookie changes")
				}
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	admin.mutate(http.MethodDelete, target, nil, 204, "")
	var auditCount int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_user_id=$1 AND subject_user_id=$1
		AND target_id=$2 AND target_type='session' AND audience='admin' AND action='auth.logout' AND reason='session_revoked'`,
		captured.Me.User.ID, remoteID).Scan(&auditCount)
	if err != nil || auditCount != 1 {
		t.Fatalf("expected one durable revocation audit, got %d: %v", auditCount, err)
	}
	if err := replica.RevokeBrowserSession(ctx, captured, adminID, uuid.NewString()); err == nil || err.Error() != "AUTH_SESSION_EXPIRED" {
		t.Fatalf("a captured actor kept write authority after revocation: %v", err)
	}
	for _, dst := range []*client{remote, newClient(t, server, "admin", admin.schemas)} {
		name := "__Host-admin_session"
		dst.cookies[name] = &http.Cookie{Name: name, Value: remoteToken}
		problemCode(t, dst.request(http.MethodGet, "/api/v1/me/session", nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	}
	requirePage(admin, managedSessionsPath, adminID, true)
	requirePage(portal, managedSessionsPath, portalID, true)

	admin.refresh()
	stale := newClient(t, replicaServer, "admin", admin.schemas)
	stale.cookies = maps.Clone(admin.cookies)
	admin.request(http.MethodDelete, managedSessionsPath+"/"+adminID, nil, 204, "", nil)
	if len(admin.cookies) != 0 {
		t.Fatal("ending the current session did not clear the entrance's identity cookies")
	}
	problemCode(t, stale.request(http.MethodGet, managedSessionsPath, nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	requirePage(portal, managedSessionsPath, portalID, true)
	requirePage(foreign, managedSessionsPath, foreignID, true)
}

func TestBrowserSessionRevocationWithoutLicense(t *testing.T) {
	s, _, admin, portal := licenseClients(t, nil)
	if err := s.RequireNewBusiness(t.Context()); err == nil {
		t.Fatal("expected the unlicensed fixture to refuse new business")
	}
	id := ownSession(t, admin)
	admin.mutate(http.MethodDelete, managedSessionsPath+"/"+id, nil, 204, "")
	if len(admin.cookies) != 0 {
		t.Fatal("unlicensed current session was not cleared")
	}
	ownSession(t, portal)
}
