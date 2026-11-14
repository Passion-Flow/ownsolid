//go:build repository_integration

package middleware_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/json"
	"encoding/pem"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	license "github.com/Passion-Flow/ownsolid/pkg/license/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/middleware"
	middlewarehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/middleware/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/middleware/testschema"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
)

// The exact owning candidate DDL, not a toy replacement or active catalog claim.
//
//go:embed schema/postgresql/*.sql schema/mysql/*.sql
var httpCandidateSchema embed.FS

type httpFixture struct {
	f                     repositoryfixture.Fixture
	ctx                   context.Context
	accounts              *identity.Service
	service               *middleware.Service
	handler               *identityhttp.Handler
	portal, admin, viewer httpActor
}
type httpActor struct {
	audience, session string
	auth              identity.Auth
}
type envelope struct {
	Data json.RawMessage `json:"data"`
	Code string          `json:"code"`
}

func (p httpFixture) exec(t *testing.T, pg, my string, args ...any) {
	t.Helper()
	if _, e := p.f.Store.Exec(p.ctx, p.f.Statement(pg, my, args...)); e != nil {
		t.Fatal("actual selected HTTP fixture SQL failed", e)
	}
}
func (p httpFixture) count(t *testing.T, pg, my string, args ...any) int {
	t.Helper()
	var n int
	if p.f.Store.QueryRow(p.ctx, p.f.Statement(pg, my, args...)).Scan(&n) != nil {
		t.Fatal("actual HTTP fixture readback failed")
	}
	return n
}
func setupHTTP(t *testing.T) httpFixture {
	t.Helper()
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	p := httpFixture{f: f, ctx: ctx}
	testschema.Prepare(t, ctx, f, httpCandidateSchema)
	var e error

	accounts := &identity.Service{Storage: f.Store, HMACKey: make([]byte, 32)}
	if _, e = rand.Read(accounts.HMACKey); e != nil {
		t.Fatal(e)
	}
	// Named test-only origins select the existing real handler's two audiences.
	accounts.Config.Server.PortalOrigin = "https://portal.m26.example.invalid"
	accounts.Config.Server.AdminOrigin = "https://admin.m26.example.invalid"
	p.accounts = accounts
	p.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'owner')", f.User, f.Tenant, f.Workspace)
	issue := func(user, audience string) httpActor {
		var session identity.IssuedSession
		if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
			var e error
			session, e = accounts.IssueSessionWithin(ctx, q, user, 1, audience, "", uuid.NewString(), nil)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		a, e := accounts.Authenticate(ctx, session.Token, audience)
		if e != nil {
			t.Fatal(e)
		}
		return httpActor{audience: audience, session: session.Token, auth: a}
	}
	p.portal, p.admin = issue(f.User, "portal"), issue(f.User, "admin")
	viewer := uuid.NewString()
	email := "synthetic-" + viewer + "@example.invalid"
	p.exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,$2,$3,'Synthetic HTTP viewer','en','active')", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES(?,?,?,'Synthetic HTTP viewer','en','active')", viewer, email, email)
	p.exec(t, "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'viewer')", "INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES(?,?,?,'viewer')", viewer, f.Tenant, f.Workspace)
	p.viewer = issue(viewer, "portal")
	key, trust, e := license.GenerateKey("synthetic-m26-http-license")
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Add(5 * time.Second)
	licenses, e := licensing.New(accounts, trust, license.DefaultPolicy(), func() time.Time { return now })
	if e != nil {
		t.Fatal(e)
	}
	accounts.BusinessAdmission = licenses
	status, e := licenses.StatusAdmin(ctx, p.admin.auth)
	if e != nil {
		t.Fatal(e)
	}
	stamp := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	artifact, e := license.Issue(license.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic M26 HTTP fixture", DeploymentID: status.DeploymentID, IssuedAt: stamp(-time.Second), NotBefore: stamp(-time.Second), ExpiresAt: stamp(time.Hour), GraceUntil: stamp(2 * time.Hour), Scope: license.WholePlatformScope}, key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = licenses.Import(ctx, p.admin.auth, licensing.ImportInput{Artifact: string(artifact), Reason: "Synthetic signed HTTP fixture"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private external test root required")
	}
	private, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic unavailable HTTP metadata CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &private.PublicKey, private)
	if e != nil {
		t.Fatal(e)
	}
	passwordFile, caFile := filepath.Join(root, "password"), filepath.Join(root, "ca.pem")
	if os.WriteFile(passwordFile, []byte(uuid.NewString()), 0600) != nil || os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600) != nil {
		t.Fatal("private explicit metadata inputs required")
	}
	configuration := middleware.Configuration{FormatVersion: 1, Policy: middleware.DefaultPolicy(), Profiles: []middleware.Profile{{Key: "synthetic_external", Kind: middleware.CustomerExternal, Host: "127.0.0.1", Port: 1, Database: "synthetic", User: "readonly", PasswordFile: passwordFile, CAFile: caFile, ExpectedMajor: 18}}}
	second := configuration.Profiles[0]
	second.Key = "synthetic_admin"
	configuration.Profiles = append(configuration.Profiles, second)
	p.service, e = middleware.New(accounts, &workspaces.Service{Identity: accounts}, configuration)
	if e != nil {
		t.Fatal(e)
	}
	p.handler = identityhttp.New(accounts)
	// New constructs ordinary owners; this test wires only the approved actual
	// M26 request seam. It does not claim whole server readiness qualification.
	p.handler.Middleware = &middlewarehttp.Handler{Service: p.service}
	accounts.BusinessAdmission = licenses
	return p
}
func (p httpFixture) origin(a httpActor) string {
	if a.audience == "admin" {
		return p.accounts.Config.Server.AdminOrigin
	}
	return p.accounts.Config.Server.PortalOrigin
}
func (p httpFixture) base(a httpActor) string {
	prefix := "/api/v1/"
	if a.audience == "admin" {
		prefix += "admin/"
	}
	return prefix + "workspaces/" + p.f.Workspace + "/middleware"
}
func (p httpFixture) call(t *testing.T, a httpActor, method, path string, input any, csrf, origin bool) (*httptest.ResponseRecorder, envelope) {
	t.Helper()
	var contextCookie *http.Cookie
	var token string
	if method != "GET" && csrf {
		r := httptest.NewRequest("GET", p.origin(a)+"/api/v1/auth/csrf", nil).WithContext(p.ctx)
		r.AddCookie(&http.Cookie{Name: "__Host-" + a.audience + "_session", Value: a.session})
		w := httptest.NewRecorder()
		p.handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal("ordinary actual CSRF acquisition failed", w.Code)
		}
		var out envelope
		var data struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || json.Unmarshal(out.Data, &data) != nil || data.Token == "" {
			t.Fatal("actual CSRF response missing")
		}
		token = data.Token
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == "__Host-"+a.audience+"_csrf_context" {
				contextCookie = cookie
			}
		}
		if contextCookie == nil {
			t.Fatal("ordinary protected CSRF cookie missing")
		}
	}
	var body []byte
	if input != nil {
		var e error
		body, e = json.Marshal(input)
		if e != nil {
			t.Fatal(e)
		}
	}
	r := httptest.NewRequest(method, p.origin(a)+cellroute.Public(path, wire.LocalScope()), bytes.NewReader(body)).WithContext(p.ctx)
	r.AddCookie(&http.Cookie{Name: "__Host-" + a.audience + "_session", Value: a.session})
	if contextCookie != nil {
		r.AddCookie(contextCookie)
		r.Header.Set("X-CSRF-Token", token)
	}
	if origin {
		r.Header.Set("Origin", p.origin(a))
	} else {
		r.Header.Set("Origin", "https://foreign.example.invalid")
	}
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.handler.ServeHTTP(w, r)
	var out envelope
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("actual handler response not JSON")
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" {
		t.Fatal("actual request headers missing")
	}
	return w, out
}
func (p httpFixture) register(t *testing.T, a httpActor) (middleware.Instance, string) {
	t.Helper()
	key := uuid.NewString()
	profile := "synthetic_external"
	if a.audience == "admin" {
		profile = "synthetic_admin"
	}
	w, out := p.call(t, a, "POST", p.base(a), map[string]any{"profile_key": profile, "name": "Synthetic HTTP maintenance \u4e16\u754c", "idempotency_key": key}, true, true)
	var v middleware.Instance
	if w.Code != 201 || out.Code != "" || json.Unmarshal(out.Data, &v) != nil || v.ID == "" {
		t.Fatal("ordinary actual HTTP registration failed", w.Code, out.Code)
	}
	return v, key
}
func denied(t *testing.T, w *httptest.ResponseRecorder, out envelope, code string) {
	t.Helper()
	if w.Code < 400 || out.Code != code || len(out.Data) != 0 && string(out.Data) != "null" {
		t.Fatal("HTTP refusal code or zero projection failed", w.Code, out.Code)
	}
}

func TestActualPairedMiddlewareHTTP(t *testing.T) {
	for _, leaf := range []string{"portal_and_admin_registration_persist", "ordinary_csrf_origin_zero_write", "foreign_audience_session_refused", "viewer_current_membership_denied", "strict_body_external_false_restore_zero_job", "original_intent_and_revision_fences", "queued_revocation_terminal_zero_output"} {
		t.Run(leaf, func(t *testing.T) {
			p := setupHTTP(t)
			switch leaf {
			case "portal_and_admin_registration_persist":
				for _, a := range []httpActor{p.portal, p.admin} {
					v, _ := p.register(t, a)
					w, out := p.call(t, a, "GET", p.base(a)+"/"+v.ID, nil, false, true)
					var got middleware.Instance
					if w.Code != 200 || json.Unmarshal(out.Data, &got) != nil || got.ID != v.ID {
						t.Fatal("actual audience scoped read failed")
					}
					if p.count(t, "SELECT count(*) FROM audit_events WHERE action='middleware.registered' AND target_id=$1", "SELECT count(*) FROM audit_events WHERE action='middleware.registered' AND target_id=?", v.ID) != 1 {
						t.Fatal("actual HTTP mutation/audit atomicity failed")
					}
				}
				w, out := p.call(t, p.portal, "POST", p.base(p.portal), map[string]any{"profile_key": "synthetic_external", "name": "Explicit duplicate profile", "idempotency_key": uuid.NewString()}, true, true)
				denied(t, w, out, "MIDDLEWARE_PROFILE_ALREADY_REGISTERED")
				if w.Code != 409 || p.count(t, "SELECT count(*) FROM middleware_instances", "SELECT count(*) FROM middleware_instances") != 2 || p.count(t, "SELECT count(*) FROM audit_events WHERE action='middleware.registered'", "SELECT count(*) FROM audit_events WHERE action='middleware.registered'") != 2 {
					t.Fatal("duplicate profile changed actual rows/audit or status")
				}
			case "ordinary_csrf_origin_zero_write":
				input := map[string]any{"profile_key": "synthetic_external", "name": "Refused", "idempotency_key": uuid.NewString()}
				w, out := p.call(t, p.portal, "POST", p.base(p.portal), input, true, false)
				denied(t, w, out, "ORIGIN_REJECTED")
				w, out = p.call(t, p.portal, "POST", p.base(p.portal), input, false, true)
				denied(t, w, out, "CSRF_INVALID")
				if p.count(t, "SELECT count(*) FROM middleware_instances", "SELECT count(*) FROM middleware_instances") != 0 {
					t.Fatal("ordinary authority refusal wrote registration")
				}
			case "foreign_audience_session_refused":
				foreign := p.portal
				foreign.audience = "admin"
				w, out := p.call(t, foreign, "GET", p.base(foreign)+"/profiles", nil, false, true)
				denied(t, w, out, "AUTH_SESSION_EXPIRED")
				w, out = p.call(t, p.portal, "GET", p.base(p.admin)+"/profiles", nil, false, true)
				denied(t, w, out, "RESOURCE_NOT_FOUND")
			case "viewer_current_membership_denied":
				v, _ := p.register(t, p.portal)
				w, out := p.call(t, p.viewer, "GET", p.base(p.viewer)+"/"+v.ID, nil, false, true)
				if w.Code != 200 {
					t.Fatal("actual viewer read denied", out.Code)
				}
				w, out = p.call(t, p.viewer, "POST", p.base(p.viewer), map[string]any{"profile_key": "synthetic_external", "name": "Refused viewer", "idempotency_key": uuid.NewString()}, true, true)
				denied(t, w, out, "ACCESS_DENIED")
				p.exec(t, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", "DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?", p.f.Workspace, p.viewer.auth.Me.User.ID)
				w, out = p.call(t, p.viewer, "GET", p.base(p.viewer)+"/"+v.ID, nil, false, true)
				denied(t, w, out, "RESOURCE_NOT_FOUND")
			case "strict_body_external_false_restore_zero_job":
				w, out := p.call(t, p.portal, "POST", p.base(p.portal), map[string]any{"profile_key": "synthetic_external", "name": "Bad URL override", "idempotency_key": uuid.NewString(), "host": "foreign.invalid"}, true, true)
				denied(t, w, out, "VALIDATION_FAILED")
				v, _ := p.register(t, p.portal)
				for _, kind := range []string{"backup", "isolated_restore"} {
					input := map[string]any{"kind": kind, "reason": "Explicit denied synthetic HTTP action", "idempotency_key": uuid.NewString(), "version": v.Version}
					code := "MIDDLEWARE_EXTERNAL_MUTATION_REFUSED"
					if kind == "isolated_restore" {
						input["backup_id"] = uuid.NewString()
						input["target_key"] = "isolated"
						input["confirm_isolated"] = false
						code = "MIDDLEWARE_ISOLATED_CONFIRMATION_REQUIRED"
					}
					w, out = p.call(t, p.portal, "POST", p.base(p.portal)+"/"+v.ID+"/operations", input, true, true)
					denied(t, w, out, code)
				}
				if p.count(t, "SELECT count(*) FROM middleware_operations", "SELECT count(*) FROM middleware_operations") != 0 {
					t.Fatal("refused HTTP request wrote operation")
				}
			case "original_intent_and_revision_fences":
				v, key := p.register(t, p.portal)
				w, out := p.call(t, p.portal, "GET", p.base(p.portal)+"/registration-intents/"+key, nil, false, true)
				var original middleware.Instance
				if w.Code != 200 || json.Unmarshal(out.Data, &original) != nil || original.ID != v.ID {
					t.Fatal("actual original HTTP intent missing")
				}
				change := map[string]any{"name": v.Name, "state": "archived", "version": v.Version, "refresh_inputs": false, "reason": "Explicit synthetic HTTP archive"}
				w, out = p.call(t, p.portal, "PATCH", p.base(p.portal)+"/"+v.ID, change, true, true)
				if w.Code != 200 {
					t.Fatal("actual HTTP archive failed", out.Code)
				}
				w, out = p.call(t, p.portal, "PATCH", p.base(p.portal)+"/"+v.ID, change, true, true)
				denied(t, w, out, "VERSION_CONFLICT")
			case "queued_revocation_terminal_zero_output":
				v, _ := p.register(t, p.portal)
				input := map[string]any{"kind": "health", "reason": "Explicit synthetic HTTP queued authority fence", "idempotency_key": uuid.NewString(), "version": v.Version}
				w, out := p.call(t, p.portal, "POST", p.base(p.portal)+"/"+v.ID+"/operations", input, true, true)
				var job middleware.Operation
				if w.Code != 202 || json.Unmarshal(out.Data, &job) != nil || job.ID == "" {
					t.Fatal("actual HTTP queue admission failed", out.Code)
				}
				p.exec(t, "DELETE FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", "DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?", p.f.Workspace, p.f.User)
				if e := p.service.Pass(p.ctx); e != nil {
					t.Fatal("actual queued current-authority pass failed", e)
				}
				w, out = p.call(t, p.admin, "GET", p.base(p.admin)+"/operations/"+job.ID, nil, false, true)
				var terminal middleware.Operation
				if w.Code != 200 || json.Unmarshal(out.Data, &terminal) != nil || terminal.State != "failed" || len(terminal.Result) != 0 {
					t.Fatal("HTTP terminal lost-authority output fence failed", out.Code)
				}
			}
		})
	}
}
