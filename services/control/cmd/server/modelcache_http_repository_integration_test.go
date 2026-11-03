//go:build repository_integration && valkey_http_integration

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/modelcache"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// The parent fixture owns real Valkey stop/join/restart. This test owns actual
// serveSelected HTTPS and SQL; no cache, HTTP response or transaction is faked.
// Static pages and model bytes are synthetic metadata-only fixtures, not UI or
// artifact-import qualification. Login issuance is an existing privileged fixture.
type modelCacheHTTPInput struct {
	Format              string `json:"format"`
	Configuration       string `json:"configuration"`
	ConfigurationSHA256 string `json:"configuration_sha256"`
	PhaseDirectory      string `json:"phase_directory"`
	Nonce               string `json:"nonce"`
	WaitSeconds         int    `json:"wait_seconds"`
}

func modelCacheHTTPPrivate(t *testing.T, path string) []byte {
	t.Helper()
	canonical, e := filepath.EvalSymlinks(path)
	parent, pe := os.Stat(filepath.Dir(path))
	if e != nil || canonical != path || !filepath.IsAbs(path) || pe != nil || parent.Mode().Perm() != 0700 {
		t.Fatal("MODELCACHE_HTTP_PRIVATE_INPUT_REQUIRED")
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		t.Fatal("MODELCACHE_HTTP_PRIVATE_INPUT_UNREADABLE")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < 1 || info.Size() > 65536 {
		t.Fatal("MODELCACHE_HTTP_PRIVATE_INPUT_MODE_INVALID")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	owner, parentOK := parent.Sys().(*syscall.Stat_t)
	if !ok || !parentOK || int(stat.Uid) != os.Geteuid() || int(owner.Uid) != os.Geteuid() || stat.Nlink != 1 {
		t.Fatal("MODELCACHE_HTTP_PRIVATE_INPUT_OWNER_INVALID")
	}
	raw, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || int64(len(raw)) != info.Size() {
		t.Fatal("MODELCACHE_HTTP_PRIVATE_INPUT_CHANGED")
	}
	return raw
}

func modelCacheHTTPInputForTest(t *testing.T) modelCacheHTTPInput {
	t.Helper()
	path := os.Getenv("PROJECT_ONE_MODELCACHE_HTTP_INPUT")
	var input modelCacheHTTPInput
	d := json.NewDecoder(bytes.NewReader(modelCacheHTTPPrivate(t, path)))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF || input.Format != "projectone.modelcache-http.input.v1" ||
		!regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(input.Nonce) || input.WaitSeconds < 10 || input.WaitSeconds > 200 {
		t.Fatal("MODELCACHE_HTTP_INPUT_INVALID")
	}
	root := filepath.Dir(path)
	phaseRoot, phaseError := filepath.EvalSymlinks(input.PhaseDirectory)
	phaseInfo, phaseStatError := os.Stat(input.PhaseDirectory)
	state, e := filepath.EvalSymlinks(os.Getenv("PROJECT_ONE_STATE_ROOT"))
	if e != nil || filepath.Dir(root) != filepath.Join(state, "evidence/m20") ||
		!regexp.MustCompile(`^modelcache-http-[0-9]{8}-[0-9]{6}-[a-f0-9]{12}$`).MatchString(filepath.Base(root)) ||
		path != filepath.Join(root, "http-input.json") || input.Configuration != filepath.Join(root, "engine/modelcache-configuration.json") ||
		input.PhaseDirectory != filepath.Join(root, "phases") || phaseError != nil || phaseRoot != input.PhaseDirectory ||
		phaseStatError != nil || !phaseInfo.IsDir() || phaseInfo.Mode().Perm() != 0700 {
		t.Fatal("MODELCACHE_HTTP_EXACT_OWNED_INPUT_REQUIRED")
	}
	raw := modelCacheHTTPPrivate(t, input.Configuration)
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != input.ConfigurationSHA256 {
		t.Fatal("MODELCACHE_HTTP_CONFIGURATION_CHANGED")
	}
	return input
}

func modelCacheHTTPPhase(t *testing.T, x *serverCompositionStory, input modelCacheHTTPInput, sequence int, phase, want string) {
	t.Helper()
	root, e := os.OpenRoot(input.PhaseDirectory)
	if e != nil {
		t.Fatal("MODELCACHE_HTTP_PHASE_ROOT_REQUIRED")
	}
	defer root.Close()
	name := phase + ".request.json"
	raw, e := json.Marshal(map[string]any{"format": "projectone.modelcache-http.phase.v1", "sequence": sequence,
		"phase": phase, "nonce": input.Nonce, "pid": os.Getpid(), "uid": os.Geteuid(), "https_listener": x.c.Server.ListenAddress})
	if e != nil {
		t.Fatal(e)
	}
	f, e := root.OpenFile(name+".pending", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal("MODELCACHE_HTTP_NEW_PHASE_REQUIRED")
	}
	_, e = f.Write(raw)
	if e != nil || f.Sync() != nil || f.Close() != nil {
		t.Fatal("MODELCACHE_HTTP_PHASE_WRITE_FAILED")
	}
	if e = root.Rename(name+".pending", name); e != nil {
		t.Fatal("MODELCACHE_HTTP_PHASE_PUBLISH_FAILED")
	}
	deadline := time.NewTimer(time.Duration(input.WaitSeconds) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, e := root.Stat(phase + ".ack.json"); e == nil {
			var ack struct {
				Format   string `json:"format"`
				Sequence int    `json:"sequence"`
				Phase    string `json:"phase"`
				Nonce    string `json:"nonce"`
				State    string `json:"state"`
				RunID    string `json:"run_id"`
			}
			d := json.NewDecoder(bytes.NewReader(modelCacheHTTPPrivate(t, filepath.Join(input.PhaseDirectory, phase+".ack.json"))))
			d.DisallowUnknownFields()
			if d.Decode(&ack) != nil || d.Decode(new(any)) != io.EOF || ack.Format != "projectone.modelcache-http.ack.v1" ||
				ack.Sequence != sequence || ack.Phase != phase || ack.Nonce != input.Nonce || ack.State != want ||
				!regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(ack.RunID) {
				t.Fatal("MODELCACHE_HTTP_PHASE_ACK_INVALID")
			}
			t.Log("MODELCACHE_HTTP_PHASE " + phase + " " + want + " " + ack.RunID)
			return
		} else if !errors.Is(e, os.ErrNotExist) {
			t.Fatal("MODELCACHE_HTTP_PHASE_READ_FAILED")
		}
		select {
		case <-x.ctx.Done():
			t.Fatal(x.ctx.Err())
		case <-deadline.C:
			t.Fatal("MODELCACHE_HTTP_PHASE_TIMEOUT")
		case <-ticker.C:
		}
	}
}

func modelCacheHTTPSession(t *testing.T, x *serverCompositionStory, audience string) (identity.IssuedSession, []*http.Cookie) {
	t.Helper()
	var issued identity.IssuedSession
	if e := x.d.store.Within(x.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = x.owner.IssueSessionWithin(ctx, q, x.f.User, 1, audience, "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal("MODELCACHE_HTTP_REAL_SESSION_REQUIRED", e)
	}
	return issued, []*http.Cookie{{Name: "__Host-" + audience + "_session", Value: issued.Token, Secure: true, HttpOnly: true, Path: "/"}}
}

func modelCacheHTTPGet(t *testing.T, x *serverCompositionStory, label, audience, path string, cookies []*http.Cookie, status int, code string) map[string]any {
	t.Helper()
	r, e := http.NewRequestWithContext(x.ctx, http.MethodGet, x.origin(t, audience)+path, nil)
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Origin", x.origin(t, audience))
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	response, e := x.client.Do(r)
	if e != nil {
		t.Fatal("MODELCACHE_HTTP_REAL_VERIFIED_HTTPS_FAILED", e)
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var out map[string]any
	if e != nil || len(raw) >= 1<<20 || json.Unmarshal(raw, &out) != nil || response.TLS == nil ||
		len(response.TLS.VerifiedChains) == 0 || response.Header.Get("Cache-Control") != "no-store" ||
		response.StatusCode != status {
		t.Fatal("MODELCACHE_HTTP_RESPONSE_CONTRACT_FAILED", label, response.StatusCode)
	}
	requestID := response.Header.Get("X-Request-ID")
	if id, e := uuid.Parse(requestID); e != nil || id == uuid.Nil {
		t.Fatal("MODELCACHE_HTTP_REQUEST_ID_REQUIRED")
	}
	if code != "" {
		if out["code"] != code || out["request_id"] != requestID || out["data"] != nil || out["items"] != nil {
			t.Fatal("MODELCACHE_HTTP_DENIAL_MUST_NOT_PUBLISH", label, out["code"])
		}
	}
	sum := sha256.Sum256(raw)
	evidence, _ := json.Marshal(map[string]any{"label": label, "status": response.StatusCode, "code": code,
		"request_id": requestID, "body_sha256": hex.EncodeToString(sum[:]), "verified_tls": true, "no_store": true})
	t.Log("MODELCACHE_HTTP_RESPONSE " + string(evidence))
	return out
}

func modelCacheHTTPList(t *testing.T, x *serverCompositionStory, label, audience, workspace, id, name string, cookies []*http.Cookie) {
	t.Helper()
	path := "/api/v1/workspaces/" + workspace + "/models"
	if audience == "admin" {
		path = "/api/v1/admin/models?workspace_id=" + workspace
	}
	out := modelCacheHTTPGet(t, x, label, audience, path, cookies, 200, "")
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatal("MODELCACHE_HTTP_DATA_REQUIRED")
	}
	items, ok := data["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatal("MODELCACHE_HTTP_EXACT_SCOPE_LIST_REQUIRED", label)
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["id"] != id || item["name"] != name || item["workspace_id"] != workspace {
		t.Fatal("MODELCACHE_HTTP_STALE_OR_FOREIGN_MODEL", label)
	}
}

func TestActualModelCacheHTTPSOutage(t *testing.T) {
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "postgresql" {
		t.Fatal("MODELCACHE_HTTP_ACTUAL_POSTGRES_REQUIRED")
	}
	input := modelCacheHTTPInputForTest(t)
	loaded, e := config.Load(input.Configuration)
	if e != nil || !loaded.ModelCache.Enabled || !loaded.ModelCache.TLS || loaded.ModelCache.TTLSeconds != 1 ||
		!strings.HasPrefix(loaded.ModelCache.Namespace, "m23_test_") || !strings.HasPrefix(loaded.ModelCache.Address, "127.0.0.1:") {
		t.Fatal("MODELCACHE_HTTP_ACTUAL_PRIVATE_VALKEY_REQUIRED")
	}
	x := serverCompositionSetup(t, false)
	x.c.ModelCache = loaded.ModelCache
	// This fixed test expectation keeps warm keys alive across the bounded outage
	// handshake. It does not alter a product default or bypass typed validation.
	x.c.ModelCache.TTLSeconds = 60
	x.owner.Config, x.mail.Config = x.c, x.c
	a := x.session(t)
	state, e := x.h.Licensing.Service.StatusAdmin(x.ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic model-cache HTTPS fixture",
		DeploymentID: state.DeploymentID, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	signed, e := licensewire.Issue(claims, x.key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = x.h.Licensing.Service.Import(x.ctx, a, licensing.ImportInput{Artifact: string(signed), Reason: "Actual HTTPS cache outage fixture"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	stop := x.serve(t)
	defer stop()
	// Seed only metadata after startup's real stored-artifact validation. This test
	// qualifies directory reads and cache authority, never imported model bytes.
	ownModel, otherWorkspace, otherModel, foreignTenant, foreignWorkspace, foreignModel := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	x.exec(t, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')`, "", x.f.User, x.f.Tenant, x.f.Workspace)
	x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other owned tenant scope','active')`, "", otherWorkspace, x.f.Tenant)
	x.exec(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign isolated tenant','active')`, "", foreignTenant)
	x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign isolated Workspace','active')`, "", foreignWorkspace, foreignTenant)
	manifest, e := json.Marshal(wire.ApprovedModel())
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range []struct{ id, tenant, workspace, name string }{
		{ownModel, x.f.Tenant, x.f.Workspace, "Initial owned model"},
		{otherModel, x.f.Tenant, otherWorkspace, "Other Workspace model"},
		{foreignModel, foreignTenant, foreignWorkspace, "Foreign tenant model"},
	} {
		x.exec(t, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,'ready',$8)`, "", row.id, row.tenant, row.workspace, row.name, wire.ApprovedModel().ModelKey, wire.ModelCatalogDigest(), manifest, x.f.User)
	}
	oldPortal, portal := modelCacheHTTPSession(t, x, "portal")
	_, admin := modelCacheHTTPSession(t, x, "admin")
	portalPath := "/api/v1/workspaces/" + x.f.Workspace + "/models"
	denyScopes := func(stage string, portal []*http.Cookie) {
		modelCacheHTTPGet(t, x, stage+"-portal-other-workspace", "portal", "/api/v1/workspaces/"+otherWorkspace+"/models", portal, 404, "RESOURCE_NOT_FOUND")
		modelCacheHTTPGet(t, x, stage+"-portal-foreign-tenant", "portal", "/api/v1/workspaces/"+foreignWorkspace+"/models", portal, 404, "RESOURCE_NOT_FOUND")
		modelCacheHTTPGet(t, x, stage+"-admin-foreign-tenant", "admin", "/api/v1/admin/models?workspace_id="+foreignWorkspace, admin, 404, "RESOURCE_NOT_FOUND")
	}
	modelCacheHTTPPhase(t, x, input, 1, "ready", "running")
	for _, label := range []string{"warm-portal-cold", "warm-portal-hit"} {
		modelCacheHTTPList(t, x, label, "portal", x.f.Workspace, ownModel, "Initial owned model", portal)
	}
	modelCacheHTTPList(t, x, "warm-admin-hit", "admin", x.f.Workspace, ownModel, "Initial owned model", admin)
	for _, label := range []string{"warm-other-cold", "warm-other-hit"} {
		modelCacheHTTPList(t, x, label, "admin", otherWorkspace, otherModel, "Other Workspace model", admin)
	}
	denyScopes("warm", portal)
	modelCacheHTTPGet(t, x, "warm-anonymous", "portal", portalPath, nil, 401, "AUTH_REQUIRED")
	modelCacheHTTPPhase(t, x, input, 2, "stop", "stopped")
	connection, dialError := net.DialTimeout("tcp", x.c.ModelCache.Address, time.Second)
	if dialError == nil {
		connection.Close()
		t.Fatal("MODELCACHE_HTTP_REAL_CACHE_MUST_BE_STOPPED")
	}
	cache, e := modelcache.New(x.c.ModelCache)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = cache.Get(x.ctx, "owned-probe"); !errors.Is(e, modelcache.ErrUnavailable) {
		t.Fatal("MODELCACHE_HTTP_REAL_DISCONNECT_REQUIRED")
	}
	x.exec(t, `UPDATE model_versions SET name='Updated during actual outage' WHERE id=$1`, "", ownModel)
	modelCacheHTTPList(t, x, "outage-portal-authoritative", "portal", x.f.Workspace, ownModel, "Updated during actual outage", portal)
	modelCacheHTTPList(t, x, "outage-admin-authoritative", "admin", x.f.Workspace, ownModel, "Updated during actual outage", admin)
	modelCacheHTTPList(t, x, "outage-other-scope", "admin", otherWorkspace, otherModel, "Other Workspace model", admin)
	denyScopes("outage", portal)
	x.exec(t, `UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='Synthetic actual outage' WHERE id=$1`, "", oldPortal.SessionID)
	modelCacheHTTPGet(t, x, "outage-revoked-session", "portal", portalPath, portal, 401, "AUTH_SESSION_EXPIRED")
	_, freshPortal := modelCacheHTTPSession(t, x, "portal")
	x.exec(t, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, "", x.f.User, x.f.Workspace)
	modelCacheHTTPGet(t, x, "outage-removed-member", "portal", portalPath, freshPortal, 404, "RESOURCE_NOT_FOUND")
	modelCacheHTTPList(t, x, "outage-current-admin", "admin", x.f.Workspace, ownModel, "Updated during actual outage", admin)
	x.exec(t, `INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'owner')`, "", x.f.User, x.f.Tenant, x.f.Workspace)
	modelCacheHTTPPhase(t, x, input, 3, "restart", "running")
	for _, label := range []string{"recovered-portal-cold", "recovered-portal-hit"} {
		modelCacheHTTPList(t, x, label, "portal", x.f.Workspace, ownModel, "Updated during actual outage", freshPortal)
	}
	for _, label := range []string{"recovered-other-cold", "recovered-other-hit"} {
		modelCacheHTTPList(t, x, label, "admin", otherWorkspace, otherModel, "Other Workspace model", admin)
	}
	x.exec(t, `UPDATE model_versions SET name='Updated after actual recovery' WHERE id=$1`, "", ownModel)
	modelCacheHTTPList(t, x, "recovered-portal-mutation", "portal", x.f.Workspace, ownModel, "Updated after actual recovery", freshPortal)
	modelCacheHTTPList(t, x, "recovered-admin-hit", "admin", x.f.Workspace, ownModel, "Updated after actual recovery", admin)
	denyScopes("recovered", freshPortal)
	modelCacheHTTPGet(t, x, "recovered-revoked-session", "portal", portalPath, portal, 401, "AUTH_SESSION_EXPIRED")
	x.exec(t, `DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, "", x.f.User, x.f.Workspace)
	modelCacheHTTPGet(t, x, "recovered-removed-member", "portal", portalPath, freshPortal, 404, "RESOURCE_NOT_FOUND")
	x.exec(t, `UPDATE users SET is_platform_admin=false WHERE id=$1`, "", x.f.User)
	modelCacheHTTPGet(t, x, "recovered-demoted-admin", "admin", "/api/v1/admin/models?workspace_id="+x.f.Workspace, admin, 401, "AUTH_SESSION_EXPIRED")
	modelCacheHTTPPhase(t, x, input, 4, "complete", "stopped")
	stop()
	connection, e = net.DialTimeout("tcp", x.c.Server.ListenAddress, time.Second)
	if e == nil {
		connection.Close()
		t.Fatal("MODELCACHE_HTTP_LISTENER_NOT_JOINED")
	}
	t.Log("MODELCACHE_HTTP_HTTPS_JOINED " + x.c.Server.ListenAddress)
}
