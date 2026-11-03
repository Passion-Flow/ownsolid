package http_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

type licenseClock struct {
	mu    sync.Mutex
	value time.Time
}

func TestM19PublicRuntimeNondefaultDeadlineAndProtectedTrustInitialization(t *testing.T) {
	s := fixture.NewUnlicensed(t)
	policy := s.Config.Policy()
	policy.HTTP.RequestSeconds = 47
	s.Config.Runtime = &policy
	h := transport.New(s)
	if e := h.LicenseInitializationError(); e != nil {
		t.Fatal(e)
	}
	server := accountTLSServer(t, h)
	c := newClient(t, server, "admin", nil)
	value := c.request("GET", "/runtime-config.json", nil, 200, "", nil)
	if value["request_timeout_milliseconds"] != float64(47000) {
		t.Fatal("nondefault owning HTTP policy not projected", value)
	}
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), s.Config.Licensing.TrustFile) || strings.Contains(string(raw), "Licensing") || strings.Contains(string(raw), "trust_file") {
		t.Fatal("private configuration leaked into browser")
	}
	if e := os.Chmod(s.Config.Licensing.TrustFile, 0644); e != nil {
		t.Fatal(e)
	}
	broken := transport.New(s)
	if broken.LicenseInitializationError() == nil {
		t.Fatal("serving constructor accepted unsafe trust")
	}
	if e := s.RequireNewBusiness(context.Background()); e == nil || e.Error() != "LICENSE_CONFIGURATION_REQUIRED" {
		t.Fatal("constructor left an allowing gate", e)
	}
}

func (c *licenseClock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.value }
func (c *licenseClock) set(v time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.value = v }

func licenseClients(t *testing.T, clock *licenseClock) (*identity.Service, *transport.Handler, *client, *client) {
	t.Helper()
	s := fixture.NewUnlicensed(t)
	password := fixture.Bootstrap(t, s)
	h := transport.New(s)
	if e := h.LicenseInitializationError(); e != nil {
		t.Fatal(e)
	}
	if clock != nil {
		trust, e := s.Config.LicenseTrust()
		if e != nil {
			t.Fatal(e)
		}
		service, e := licensing.New(s, trust, s.Config.LicensePolicy(), clock.now)
		if e != nil {
			t.Fatal(e)
		}
		h.Licensing.Service = service
		s.BusinessAdmission = service
	}
	server := accountTLSServer(t, h)
	ss := schemas(t)
	admin, portal := newClient(t, server, "admin", ss), newClient(t, server, "portal", ss)
	for _, c := range []*client{admin, portal} {
		c.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
	}
	return s, h, admin, portal
}
func licenseClaims(t *testing.T, s *identity.Service, base time.Time) wire.Claims {
	t.Helper()
	var deployment string
	if e := s.DB.QueryRow(context.Background(), "SELECT deployment_id::text FROM platform_license_state WHERE id=1").Scan(&deployment); e != nil {
		t.Fatal(e)
	}
	stamp := func(d time.Duration) string { return base.Add(d).UTC().Truncate(time.Second).Format(time.RFC3339) }
	return wire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic HTTP customer", DeploymentID: deployment, IssuedAt: stamp(-time.Minute), NotBefore: stamp(-time.Minute), ExpiresAt: stamp(time.Minute), GraceUntil: stamp(2 * time.Minute), Scope: wire.WholePlatformScope}
}
func licenseInput(t *testing.T, s *identity.Service, c wire.Claims, head string) licensing.ImportInput {
	t.Helper()
	raw, e := wire.Issue(c, fixture.LicensePrivateKey(t, s))
	if e != nil {
		t.Fatal(e)
	}
	return licensing.ImportInput{Artifact: string(raw), Reason: "Synthetic HTTP import", ExpectedCurrentLicenseID: head}
}
func licenseDiagnostic(t *testing.T, c *client) map[string]any {
	t.Helper()
	r, e := http.NewRequest("GET", c.s.URL+"/api/v1/admin/license/diagnostics", nil)
	if e != nil {
		t.Fatal(e)
	}
	r.Host = "admin.example.invalid"
	for _, v := range c.cookies {
		r.AddCookie(v)
	}
	response, e := c.http.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(response.Body)
	if e != nil || response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/json" || response.Header.Get("Content-Disposition") != "attachment; filename=license-diagnostics.json" || response.Header.Get("X-Artifact-SHA256") != wire.Digest(raw) {
		t.Fatal("actual diagnostics download", response.StatusCode, e)
	}
	for _, private := range []string{"artifact\"", "signature", "seed", "trust_file", "credential", "session", ".test-key"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private diagnostic content", private)
		}
	}
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil || len(doc) != 2 || doc["format_version"] != float64(1) {
		t.Fatal("diagnostic shape")
	}
	return doc
}

func TestM19ActualHTTPImportAuthorityDiagnosticsAndWallClock(t *testing.T) {
	s, _, admin, portal := licenseClients(t, nil)
	state := admin.request("GET", "/api/v1/admin/license", nil, 200, "", nil)
	if state["state"] != "missing" {
		t.Fatal(state)
	}
	admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": "blocked@example.invalid", "locale": "en"}, 403, "")
	var count int
	if e := s.DB.QueryRow(context.Background(), "SELECT count(*) FROM invitations").Scan(&count); e != nil || count != 0 {
		t.Fatal("denied write changed data", e, count)
	}
	portal.request("GET", "/api/v1/admin/license", nil, 404, "", nil)
	claims := licenseClaims(t, s, time.Now())
	input := licenseInput(t, s, claims, "")
	admin.request("POST", "/api/v1/admin/license", input, 403, "", map[string]string{"X-CSRF-Token": "invalid"})
	for _, raw := range []string{`{}`, `{"artifact":null,"reason":"Synthetic","expected_current_license_id":""}`, `{"artifact":"x","reason":"Synthetic","expected_current_license_id":"","unknown":true}`, `{"artifact":"x","artifact":"y","reason":"Synthetic","expected_current_license_id":""}`, `{"Artifact":"x","reason":"Synthetic","expected_current_license_id":""}`} {
		admin.mutate("POST", "/api/v1/admin/license", []byte(raw), 400, "")
	}
	invalid := input
	invalid.Artifact = "invalid artifact"
	admin.mutate("POST", "/api/v1/admin/license", invalid, 422, "")
	imported := admin.mutate("POST", "/api/v1/admin/license", input, 200, "")
	if imported["imported_license_id"] != claims.LicenseID || imported["state"].(map[string]any)["state"] != "active" {
		t.Fatal(imported)
	}
	if repeated := admin.mutate("POST", "/api/v1/admin/license", input, 200, ""); repeated["repeated"] != true {
		t.Fatal(repeated)
	}

	padded := input
	padded.Artifact = strings.Repeat("\n", wire.MaximumArtifactBytes-len(input.Artifact)) + input.Artifact
	if v := admin.mutate("POST", "/api/v1/admin/license", padded, 200, ""); v["repeated"] != true {
		t.Fatal("bounded large artifact did not normalize to original", v)
	}
	view := portal.request("GET", "/api/v1/license/status", nil, 200, "", nil)
	if len(view) != 3 || view["state"] != "active" || view["new_business_allowed"] != true {
		t.Fatal("portal secret-free state", view)
	}
	admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": "licensed@example.invalid", "locale": "en"}, 201, "InvitationResponse")
	doc := licenseDiagnostic(t, admin)
	if doc["state"].(map[string]any)["deployment_id"] != claims.DeploymentID {
		t.Fatal(doc)
	}
	// Reconstruct the actual serving component over the same persisted database.
	reopened := transport.New(s)
	if e := reopened.LicenseInitializationError(); e != nil {
		t.Fatal(e)
	}
	server := accountTLSServer(t, reopened)
	again := newClient(t, server, "admin", admin.schemas)
	again.cookies = admin.cookies
	if v := again.request("GET", "/api/v1/admin/license", nil, 200, "", nil); v["history_head_license_id"] != claims.LicenseID || v["accepted_licenses"] != float64(1) {
		t.Fatal("restart lost actual history", v)
	}
}

func TestM19ActualHTTPFutureGraceExpiryRecoveryAndRenewal(t *testing.T) {
	base := time.Now().UTC().Truncate(time.Second).Add(2 * time.Second)
	clock := &licenseClock{value: base}
	s, _, admin, portal := licenseClients(t, clock)
	c := licenseClaims(t, s, base)
	c.NotBefore = base.Add(10 * time.Second).Format(time.RFC3339)
	admin.mutate("POST", "/api/v1/admin/license", licenseInput(t, s, c, ""), 200, "")
	if v := portal.request("GET", "/api/v1/license/status", nil, 200, "", nil); v["state"] != "not_yet_valid" || v["new_business_allowed"] != false {
		t.Fatal(v)
	}
	clock.set(base.Add(11 * time.Second))
	inv := admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": "recoverable@example.invalid", "locale": "en"}, 201, "InvitationResponse")
	clock.set(base.Add(61 * time.Second))
	if v := portal.request("GET", "/api/v1/license/status", nil, 200, "", nil); v["state"] != "grace" || v["warning"] != true || v["new_business_allowed"] != true {
		t.Fatal(v)
	}
	clock.set(base.Add(121 * time.Second))
	admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": "expired@example.invalid", "locale": "en"}, 403, "")
	admin.request("GET", "/api/v1/admin/users", nil, 200, "UserListResponse", nil)
	licenseDiagnostic(t, admin)
	admin.mutate("POST", "/api/v1/admin/invitations/"+inv["id"].(string)+"/revocations", map[string]any{"version": inv["version"], "reason": "Synthetic expired license recovery"}, 200, "InvitationResponse")
	me := portal.request("GET", "/api/v1/me", nil, 200, "MeResponse", nil)["user"].(map[string]any)
	portal.mutate("PATCH", "/api/v1/me", map[string]any{"display_name": "Recovery profile", "locale": "en", "version": me["version"]}, 200, "MeResponse")
	renew := c
	renew.LicenseID = uuid.NewString()
	renew.ReplacesLicenseID = c.LicenseID
	renew.NotBefore = base.Add(110 * time.Second).Format(time.RFC3339)
	renew.ExpiresAt = base.Add(3 * time.Minute).Format(time.RFC3339)
	renew.GraceUntil = base.Add(4 * time.Minute).Format(time.RFC3339)
	admin.mutate("POST", "/api/v1/admin/license", licenseInput(t, s, renew, ""), 409, "")
	admin.mutate("POST", "/api/v1/admin/license", licenseInput(t, s, renew, c.LicenseID), 200, "")
	admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": "renewed@example.invalid", "locale": "en"}, 201, "InvitationResponse")
	clock.set(base.Add(10 * time.Second))
	if v := portal.request("GET", "/api/v1/license/status", nil, 200, "", nil); v["state"] != "clock_suspect" || v["new_business_allowed"] != false {
		t.Fatal(v)
	}
	admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": "rollback@example.invalid", "locale": "en"}, 403, "")
}

func TestM19ActualHTTPOrdinaryActorIsolationAndStaleAdminRefusal(t *testing.T) {
	s, _, admin, _ := licenseClients(t, nil)
	c := licenseClaims(t, s, time.Now())
	admin.mutate("POST", "/api/v1/admin/license", licenseInput(t, s, c, ""), 200, "")
	inv := admin.mutate("POST", "/api/v1/admin/invitations", map[string]any{"email": "ordinary-license@example.invalid", "locale": "en"}, 201, "InvitationResponse")
	token := fixture.InvitationToken(t, s, inv["id"].(string))
	password := fixture.Password()
	ordinary := newClient(t, admin.s, "portal", admin.schemas)
	ordinary.mutate("POST", "/api/v1/auth/register", map[string]any{"token": token, "display_name": "Synthetic ordinary", "password": password}, 201, "RegisterResponse")
	ordinary.mutate("POST", "/api/v1/auth/login", map[string]any{"email": "ordinary-license@example.invalid", "password": password}, 200, "LoginResponse")
	view := ordinary.request("GET", "/api/v1/license/status", nil, 200, "", nil)
	if len(view) != 3 {
		t.Fatal("ordinary customer metadata exposure", view)
	}
	ordinary.request("GET", "/api/v1/admin/license/diagnostics", nil, 404, "", nil)
	ordinary.request("GET", "/api/v1/license/status?workspace_id="+uuid.NewString(), nil, 400, "", nil)
	forged := newClient(t, admin.s, "admin", admin.schemas)
	forged.cookies["__Host-admin_session"] = &http.Cookie{Name: "__Host-admin_session", Value: ordinary.cookies["__Host-portal_session"].Value}
	forged.request("GET", "/api/v1/admin/license", nil, 401, "", nil)
	if _, e := s.DB.Exec(context.Background(), "UPDATE users SET is_platform_admin=false WHERE email_canonical='admin@example.invalid'"); e != nil {
		t.Fatal(e)
	}
	admin.request("GET", "/api/v1/admin/license/diagnostics", nil, 401, "", nil)
	admin.mutate("POST", "/api/v1/admin/license", licenseInput(t, s, c, ""), 401, "")
}
