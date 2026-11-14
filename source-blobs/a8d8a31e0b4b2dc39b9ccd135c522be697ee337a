//go:build repository_integration && machine_ingress_integration

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// These roots use real selected SQL, production composition and workers. HTML,
// signed License claims and node facts are explicitly synthetic fixture inputs;
// no React, GPU, physical agent or artifact-transfer result is asserted here.
type miCompositionResponse struct {
	status  int
	body    []byte
	doc     map[string]any
	cookies []*http.Cookie
	request string
}

func miCompositionAddress(t *testing.T) string {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	if e := listener.Close(); e != nil {
		t.Fatal(e)
	}
	return address
}

func miCompositionConfigure(t *testing.T, x *serverCompositionStory, dedicated bool) *http.Client {
	t.Helper()
	certificate, key := x.c.Server.TLSCertificateFile, x.c.Server.TLSPrivateKeyFile
	_, port, e := net.SplitHostPort(x.c.Server.ListenAddress)
	if e != nil {
		t.Fatal(e)
	}
	scheme := "https"
	x.c.Server.TransportMode = config.TransportDirectHTTPS
	if dedicated {
		scheme = "http"
		x.c.Server.TransportMode = config.TransportHTTP
		x.c.Server.TLSCertificateFile, x.c.Server.TLSPrivateKeyFile = "", ""
		address := miCompositionAddress(t)
		if address == x.c.Server.ListenAddress {
			t.Fatal("fixture selected the same released port twice")
		}
		x.c.MachineServer = &config.MachineServerConfiguration{ListenAddress: address, Origin: "https://" + address,
			TLSCertificateFile: certificate, TLSPrivateKeyFile: key}
	}
	x.c.Server.PortalOrigin, x.c.Server.AdminOrigin = scheme+"://portal.example.invalid:"+port, scheme+"://admin.example.invalid:"+port
	if e := x.c.Validate(); e != nil {
		t.Fatal("explicit actual composition configuration", e)
	}
	x.owner.Config, x.mail.Config = x.c, x.c
	rootPEM, e := os.ReadFile(certificate)
	roots := x509.NewCertPool()
	if e != nil || !roots.AppendCertsFromPEM(rootPEM) {
		t.Fatal("explicit private synthetic CA unavailable")
	}
	dials := map[string]string{}
	for _, origin := range []string{x.c.Server.PortalOrigin, x.c.Server.AdminOrigin} {
		u, e := url.Parse(origin)
		if e != nil {
			t.Fatal(e)
		}
		dials[u.Host] = x.c.Server.ListenAddress
	}
	if x.c.MachineServer != nil {
		u, e := url.Parse(x.c.MachineServer.Origin)
		if e != nil {
			t.Fatal(e)
		}
		dials[u.Host] = x.c.MachineServer.ListenAddress
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true,
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, requested string) (net.Conn, error) {
			target, admitted := dials[requested]
			if !admitted || network != "tcp" {
				return nil, errors.New("MACHINE_COMPOSITION_DIAL_REFUSED")
			}
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", target)
		}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("MACHINE_COMPOSITION_REDIRECT_REFUSED") }}
}

func miCompositionCall(t *testing.T, x *serverCompositionStory, client *http.Client, origin, method, path string, input any, headers map[string]string, cookies []*http.Cookie) miCompositionResponse {
	t.Helper()
	var raw []byte
	var e error
	if input != nil {
		raw, e = json.Marshal(input)
		if e != nil {
			t.Fatal("synthetic request encoding")
		}
	}
	r, e := http.NewRequestWithContext(x.ctx, method, origin+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal("explicit composition request")
	}
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	// The URL itself selects Host and DNS SNI. No IP URL/DNS Host override or
	// verifier bypass can manufacture the configured entrance's TLS identity.
	response, e := client.Do(r)
	if e != nil {
		t.Fatal("actual normally verified composition transport", e)
	}
	defer response.Body.Close()
	body, e := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if e != nil || len(body) > 1<<20 {
		t.Fatal("bounded actual composition response")
	}
	out := miCompositionResponse{status: response.StatusCode, body: body, cookies: response.Cookies(), request: response.Header.Get("X-Request-ID")}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") || strings.HasPrefix(response.Header.Get("Content-Type"), "application/problem+json") {
		if json.Unmarshal(body, &out.doc) != nil {
			t.Fatal("actual composition JSON response")
		}
	}
	if _, e := uuid.Parse(out.request); e != nil || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("actual dispatcher provenance missing")
	}
	return out
}

func miCompositionStatus(t *testing.T, response miCompositionResponse, status int, code string) {
	t.Helper()
	if response.status != status || code != "" && response.doc["code"] != code {
		// Never print successful enrollment bodies, cookies or one-time secrets.
		t.Fatalf("actual composition status=%d expected=%d code=%v", response.status, status, response.doc["code"])
	}
}

func miCompositionData(t *testing.T, response miCompositionResponse) map[string]any {
	t.Helper()
	data, ok := response.doc["data"].(map[string]any)
	if !ok {
		t.Fatal("actual composition data envelope")
	}
	return data
}

func miCompositionCookies(previous, issued []*http.Cookie) []*http.Cookie {
	byName := map[string]*http.Cookie{}
	for _, cookie := range append(append([]*http.Cookie(nil), previous...), issued...) {
		if cookie.MaxAge < 0 {
			delete(byName, cookie.Name)
		} else {
			byName[cookie.Name] = cookie
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]*http.Cookie, 0, len(names))
	for _, name := range names {
		result = append(result, byName[name])
	}
	return result
}

func miCompositionLicense(t *testing.T, x *serverCompositionStory) {
	t.Helper()
	a := x.session(t)
	state, e := x.h.Licensing.Service.StatusAdmin(x.ctx, a)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic machine composition acceptance", DeploymentID: state.DeploymentID,
		IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339),
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}
	raw, e := licensewire.Issue(claims, x.key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := x.h.Licensing.Service.Import(x.ctx, a, licensing.ImportInput{Artifact: string(raw), Reason: "Synthetic signed machine composition fixture"}, uuid.NewString()); e != nil {
		t.Fatal("actual License import", e)
	}
}

func miCompositionPassword(t *testing.T, x *serverCompositionStory) string {
	t.Helper()
	password := "Synthetic machine composition passphrase " + uuid.NewString()
	phc, e := x.owner.Passwords.Hash(x.ctx, password)
	if e != nil {
		t.Fatal("actual bounded password hash", e)
	}
	x.exec(t, "INSERT INTO password_credentials(user_id,password_phc) VALUES($1,$2)", "INSERT INTO password_credentials(user_id,password_phc) VALUES(?,?)", x.f.User, phc)
	return password
}

type miCompositionRun struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
	once   sync.Once
}

func miCompositionStart(t *testing.T, x *serverCompositionStory, client *http.Client) *miCompositionRun {
	t.Helper()
	ctx, cancel := context.WithCancel(x.ctx)
	run := &miCompositionRun{cancel: cancel, done: make(chan struct{})}
	selected := x.d
	go func() {
		defer close(run.done)
		startup, stop := context.WithTimeout(ctx, config.Seconds(x.c.Policy().HTTP.StartupSeconds))
		defer stop()
		run.err = serveSelected(ctx, startup, x.c, selected, x.owner, x.mail)
	}()
	t.Cleanup(func() { miCompositionStop(t, x, run) })
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-run.done:
			t.Fatal("production composition ended before ready", run.err)
		case <-deadline.C:
			t.Fatal("production composition readiness deadline")
		case <-ticker.C:
			r, e := http.NewRequestWithContext(x.ctx, http.MethodGet, x.c.Server.PortalOrigin+"/health/ready", nil)
			if e != nil {
				t.Fatal(e)
			}
			response, e := client.Do(r)
			if response == nil || e != nil {
				if response != nil {
					response.Body.Close()
				}
				continue
			}
			raw, readError := io.ReadAll(io.LimitReader(response.Body, 4097))
			response.Body.Close()
			var health struct {
				Data struct {
					Status string `json:"status"`
				} `json:"data"`
			}
			if response.StatusCode == http.StatusOK && readError == nil && len(raw) <= 4096 && json.Unmarshal(raw, &health) == nil && health.Data.Status == "ok" && response.Header.Get("Cache-Control") == "no-store" {
				for _, address := range miCompositionAddresses(x) {
					t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", address)
				}
				t.Log("MACHINE_INGRESS_COMPOSITION_READY")
				return run
			}
		}
	}
}

func miCompositionStop(t *testing.T, x *serverCompositionStory, run *miCompositionRun) {
	t.Helper()
	run.once.Do(func() {
		run.cancel()
		select {
		case <-run.done:
			if run.err != nil {
				t.Error("production composition stop", run.err)
			}
		case <-time.After(15 * time.Second):
			t.Error("production composition did not join")
			return
		}
		miCompositionJoined(t, x)
		miCompositionReleased(t, miCompositionAddresses(x)...)
	})
}

func miCompositionAddresses(x *serverCompositionStory) []string {
	addresses := []string{x.c.Server.ListenAddress}
	if x.c.MachineServer != nil {
		addresses = append(addresses, x.c.MachineServer.ListenAddress)
	}
	return addresses
}

func miCompositionJoined(t *testing.T, x *serverCompositionStory) {
	t.Helper()
	if x.d.workers == nil {
		t.Fatal("production worker ownership was not installed")
	}
	select {
	case <-x.d.workers:
		t.Log("MACHINE_INGRESS_COMPOSITION_WORKERS_JOINED")
	default:
		t.Fatal("production workers remain unjoined")
	}
}

func miCompositionReleased(t *testing.T, addresses ...string) {
	t.Helper()
	for _, address := range addresses {
		listener, e := net.Listen("tcp", address)
		if e != nil {
			t.Fatal("owned composition socket not released", e)
		}
		if e := listener.Close(); e != nil {
			t.Fatal(e)
		}
		t.Logf("MACHINE_INGRESS_COMPOSITION_SOCKET_RELEASED=%s", address)
	}
}

func miCompositionReopen(t *testing.T, x *serverCompositionStory) {
	t.Helper()
	if e := x.d.Close(); e != nil {
		t.Fatal("joined selected owner did not release installation lease", e)
	}
	passwords, hmac := x.owner.Passwords, x.owner.HMACKey
	d, e := openSelectedDatabase(x.ctx, x.c, "serve")
	if e != nil {
		t.Fatal("same database installation lease not recoverable", e)
	}
	x.d = d
	x.owner = d.identity(x.c)
	x.owner.Passwords, x.owner.HMACKey = passwords, hmac
	x.mail.DB, x.mail.Storage, x.mail.Config = d.postgres, nil, x.c
	if d.mysql != nil {
		x.mail.Storage = d.store
	}
	x.owner.Mail = x.mail
	x.h = identityhttp.New(x.owner)
	if e := x.h.InitializationError(); e != nil || d.Verify(x.ctx) != nil {
		t.Fatal("same database actual owner reconstruction", e)
	}
	t.Log("MACHINE_INGRESS_COMPOSITION_INSTALLATION_LEASE_REACQUIRED")
}

func miCompositionLogin(t *testing.T, x *serverCompositionStory, client *http.Client, origin, audience, password string) []*http.Cookie {
	t.Helper()
	csrf := miCompositionCall(t, x, client, origin, http.MethodGet, "/api/v1/auth/csrf", nil, nil, nil)
	miCompositionStatus(t, csrf, http.StatusOK, "")
	token, ok := miCompositionData(t, csrf)["token"].(string)
	if !ok || token == "" {
		t.Fatal("actual CSRF token missing")
	}
	login := miCompositionCall(t, x, client, origin, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "synthetic@example.invalid", "password": password}, map[string]string{"Origin": origin, "X-CSRF-Token": token}, csrf.cookies)
	miCompositionStatus(t, login, http.StatusOK, "")
	cookies := miCompositionCookies(csrf.cookies, login.cookies)
	wanted := "__Host-" + audience + "_session"
	secure := x.c.Server.EffectiveTransportMode() != config.TransportHTTP
	if !secure {
		wanted = "projectone_http_" + audience + "_session"
	}
	found := false
	for _, cookie := range cookies {
		if cookie.Name == wanted {
			found = true
			if cookie.Secure != secure || !cookie.HttpOnly || cookie.Domain != "" || cookie.Path != "/" {
				t.Fatal("actual composition session cookie policy")
			}
		}
	}
	if !found {
		t.Fatal("actual composition session cookie missing")
	}
	miCompositionStatus(t, miCompositionCall(t, x, client, origin, http.MethodGet, "/api/v1/me", nil, nil, cookies), http.StatusOK, "")
	return cookies
}

func miCompositionNode(t *testing.T, x *serverCompositionStory, client *http.Client, origin string, adminCookies []*http.Cookie) (wire.Enrolled, wire.Handshake) {
	t.Helper()
	csrf := miCompositionCall(t, x, client, x.c.Server.AdminOrigin, http.MethodGet, "/api/v1/auth/csrf", nil, nil, adminCookies)
	miCompositionStatus(t, csrf, http.StatusOK, "")
	adminCookies = miCompositionCookies(adminCookies, csrf.cookies)
	token, ok := miCompositionData(t, csrf)["token"].(string)
	if !ok || token == "" {
		t.Fatal("actual admin CSRF missing")
	}
	join := miCompositionCall(t, x, client, x.c.Server.AdminOrigin, http.MethodPost, "/api/v1/admin/node-join-tokens",
		map[string]string{"workspace_id": x.f.Workspace, "node_name": "Synthetic production composition node", "execution_profile": wire.ReadOnlyProfile},
		map[string]string{"Origin": x.c.Server.AdminOrigin, "X-CSRF-Token": token}, adminCookies)
	miCompositionStatus(t, join, http.StatusCreated, "")
	joinToken, ok := miCompositionData(t, join)["token"].(string)
	if !ok || !security.ValidToken(joinToken) {
		t.Fatal("actual scoped join token missing")
	}
	handshake := wire.Handshake{ProtocolVersion: wire.Version, ExecutionProfile: wire.ReadOnlyProfile, Capabilities: wire.Capabilities(wire.ReadOnlyProfile),
		Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-composition-node", LogicalCPUs: 1,
			MemoryTotalBytes: 1 << 30, DiskPath: "/", DiskTotalBytes: 4 << 30, DiskAvailableBytes: 2 << 30, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	enrollment := miCompositionCall(t, x, client, origin, http.MethodPost, "/agent/v1/enroll", wire.Enrollment{JoinToken: joinToken, Handshake: handshake}, nil, nil)
	miCompositionStatus(t, enrollment, http.StatusCreated, "")
	data, e := json.Marshal(miCompositionData(t, enrollment))
	var node wire.Enrolled
	if e != nil || json.Unmarshal(data, &node) != nil || !security.ValidToken(node.Credential) {
		t.Fatal("actual machine enrollment envelope")
	}
	if _, e := uuid.Parse(node.NodeID); e != nil {
		t.Fatal("actual machine identity missing")
	}
	var tenant, workspace, state string
	var audits int
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT tenant_id::text,workspace_id::text,status FROM managed_nodes WHERE id=$1", "SELECT tenant_id,workspace_id,status FROM managed_nodes WHERE id=?", node.NodeID)).Scan(&tenant, &workspace, &state); e != nil || tenant != x.f.Tenant || workspace != x.f.Workspace || state != "active" {
		t.Fatal("actual enrolled SQL scope", e)
	}
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT count(*) FROM audit_events WHERE action='node.enrolled' AND target_id=$1 AND request_id=$2", "SELECT count(*) FROM audit_events WHERE action='node.enrolled' AND target_id=? AND request_id=?", node.NodeID, enrollment.request)).Scan(&audits); e != nil || audits != 1 {
		t.Fatal("actual enrollment audit correlation", e)
	}
	return node, handshake
}

func miCompositionHeartbeat(t *testing.T, x *serverCompositionStory, client *http.Client, origin string, node wire.Enrolled, handshake wire.Handshake) {
	t.Helper()
	handshake.Facts.Hostname = "synthetic-composition-current-heartbeat"
	handshake.Facts.ObservedAt = time.Now().UTC()
	response := miCompositionCall(t, x, client, origin, http.MethodPost, "/agent/v1/nodes/"+node.NodeID+"/heartbeat", handshake, map[string]string{"Authorization": "Bearer " + node.Credential}, nil)
	miCompositionStatus(t, response, http.StatusOK, "")
	if miCompositionData(t, response)["status"] != "active" {
		t.Fatal("actual heartbeat disposition")
	}
	var facts []byte
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT facts FROM managed_nodes WHERE id=$1", "SELECT facts FROM managed_nodes WHERE id=?", node.NodeID)).Scan(&facts); e != nil {
		t.Fatal("actual heartbeat persisted facts", e)
	}
	var stored wire.Facts
	if json.Unmarshal(facts, &stored) != nil || stored.Hostname != handshake.Facts.Hostname {
		t.Fatal("heartbeat did not persist through original production dispatcher")
	}
}

func miCompositionLeaseHeld(t *testing.T, x *serverCompositionStory) {
	t.Helper()
	d, e := openSelectedDatabase(x.ctx, x.c, "serve")
	if d != nil {
		_ = d.Close()
	}
	if e == nil || e.Error() != "INSTALLATION_PROCESS_ALREADY_RUNNING" {
		t.Fatal("concurrent installation owner admitted", e)
	}
}

func miCompositionBusinessCounts(t *testing.T, x *serverCompositionStory) [4]int {
	t.Helper()
	var counts [4]int
	statements := []string{"SELECT count(*) FROM managed_nodes", "SELECT count(*) FROM node_tasks", "SELECT count(*) FROM mail_deliveries", "SELECT count(*) FROM audit_events"}
	for i, statement := range statements {
		if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(statement, statement)).Scan(&counts[i]); e != nil {
			t.Fatal("actual composition business snapshot", e)
		}
	}
	return counts
}

func TestActualMachineDualIngressServerComposition(t *testing.T) {
	x := serverCompositionSetup(t, false)
	client := miCompositionConfigure(t, x, true)
	miCompositionLicense(t, x)
	password := miCompositionPassword(t, x)
	run := miCompositionStart(t, x, client)
	miCompositionLeaseHeld(t, x)
	entrances := []struct{ audience, origin string }{{"portal", x.c.Server.PortalOrigin}, {"admin", x.c.Server.AdminOrigin}}
	cookies := map[string][]*http.Cookie{}
	for _, entrance := range entrances {
		page := miCompositionCall(t, x, client, entrance.origin, http.MethodGet, "/", nil, nil, nil)
		miCompositionStatus(t, page, http.StatusOK, "")
		if !bytes.Contains(page.body, []byte("Synthetic "+entrance.audience+" routing fixture")) {
			t.Fatal("synthetic HTML routed to wrong entrance")
		}
		miCompositionStatus(t, miCompositionCall(t, x, client, entrance.origin, http.MethodGet, "/health/ready", nil, nil, nil), http.StatusOK, "")
		cookies[entrance.audience] = miCompositionLogin(t, x, client, entrance.origin, entrance.audience, password)
	}
	node, handshake := miCompositionNode(t, x, client, x.c.MachineServer.Origin, cookies["admin"])
	miCompositionHeartbeat(t, x, client, x.c.MachineServer.Origin, node, handshake)
	path := "/agent/v1/nodes/" + node.NodeID + "/heartbeat"
	miCompositionStatus(t, miCompositionCall(t, x, client, x.c.MachineServer.Origin, http.MethodPost, path, handshake, nil, nil), http.StatusUnauthorized, "AGENT_UNAUTHORIZED")
	for _, entrance := range entrances {
		miCompositionStatus(t, miCompositionCall(t, x, client, entrance.origin, http.MethodPost, path, handshake, map[string]string{"Authorization": "Bearer " + node.Credential}, nil), http.StatusNotFound, "RESOURCE_NOT_FOUND")
	}
	for _, kind := range []string{"live", "ready"} {
		response := miCompositionCall(t, x, client, x.c.MachineServer.Origin, http.MethodGet, "/health/"+kind, nil, nil, nil)
		miCompositionStatus(t, response, http.StatusOK, "")
		if miCompositionData(t, response)["status"] != "ok" {
			t.Fatal("actual machine health owner response")
		}
	}
	for _, path := range []string{"/", "/api/v1/me", "/runtime-config.json"} {
		miCompositionStatus(t, miCompositionCall(t, x, client, x.c.MachineServer.Origin, http.MethodGet, path, nil, nil, nil), http.StatusNotFound, "RESOURCE_NOT_FOUND")
	}
	miCompositionStatus(t, miCompositionCall(t, x, client, x.c.MachineServer.Origin, http.MethodPost, path, handshake,
		map[string]string{"Authorization": "Bearer " + node.Credential, "Origin": x.c.Server.AdminOrigin}, nil), http.StatusForbidden, "ORIGIN_REJECTED")
	miCompositionStop(t, x, run)
	miCompositionReopen(t, x)
	run = miCompositionStart(t, x, client)
	for _, entrance := range entrances {
		miCompositionStatus(t, miCompositionCall(t, x, client, entrance.origin, http.MethodGet, "/api/v1/me", nil, nil, cookies[entrance.audience]), http.StatusOK, "")
	}
	miCompositionHeartbeat(t, x, client, x.c.MachineServer.Origin, node, handshake)
	miCompositionLeaseHeld(t, x)
	miCompositionStop(t, x, run)
	miCompositionReopen(t, x)
	if miCompositionBusinessCounts(t, x)[2] != 0 {
		t.Fatal("synthetic machine flow unexpectedly queued external mail")
	}
}

func miCompositionFailure(t *testing.T, x *serverCompositionStory) (error, string) {
	t.Helper()
	// These roots are deliberately serial. Capture the actual composition's fixed
	// startup marker without adding a production hook or changing process code.
	previous := os.Stdout
	reader, writer, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	os.Stdout = writer
	defer func() { os.Stdout = previous; writer.Close(); reader.Close() }()
	output := make(chan []byte, 1)
	go func() { raw, _ := io.ReadAll(io.LimitReader(reader, 65537)); output <- raw }()
	ctx, cancel := context.WithTimeout(x.ctx, 10*time.Second)
	defer cancel()
	startup, stop := context.WithTimeout(ctx, config.Seconds(x.c.Policy().HTTP.StartupSeconds))
	defer stop()
	result := serveSelected(ctx, startup, x.c, x.d, x.owner, x.mail)
	os.Stdout = previous
	writer.Close()
	raw := <-output
	if len(raw) > 65536 {
		t.Fatal("bounded composition startup diagnostic exceeded")
	}
	return result, string(raw)
}

func TestActualMachineIngressBindFailureBeforeWorkers(t *testing.T) {
	x := serverCompositionSetup(t, false)
	_ = miCompositionConfigure(t, x, true)
	miCompositionLicense(t, x)
	canary := security.Digest("synthetic-machine-before-workers:" + uuid.NewString())
	old := time.Now().UTC().Add(-30 * 24 * time.Hour)
	x.exec(t, "INSERT INTO rate_limit_buckets(bucket_key,window_started_at,attempt_count,expires_at) VALUES($1,$2,1,$3)", "INSERT INTO rate_limit_buckets(bucket_key,window_started_at,attempt_count,expires_at) VALUES(?,?,1,?)", canary, old, old.Add(time.Second))
	before := miCompositionBusinessCounts(t, x)
	foreign, e := net.Listen("tcp", x.c.MachineServer.ListenAddress)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { foreign.Close() })
	t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", x.c.Server.ListenAddress)
	t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", x.c.MachineServer.ListenAddress)
	failure, output := miCompositionFailure(t, x)
	if failure == nil || failure.Error() != "SERVER_LISTEN_FAILED" || strings.Contains(output, "CONTROL_STARTED") {
		t.Fatal("second bind failure was not preserved before startup", failure)
	}
	miCompositionJoined(t, x)
	miCompositionReleased(t, x.c.Server.ListenAddress)
	var count int
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT count(*) FROM rate_limit_buckets WHERE bucket_key=$1", "SELECT count(*) FROM rate_limit_buckets WHERE bucket_key=?", canary)).Scan(&count); e != nil || count != 1 {
		t.Fatal("failed bind entered startup maintenance", e)
	}
	if miCompositionBusinessCounts(t, x) != before {
		t.Fatal("failed bind changed business/admission state")
	}
	if e := foreign.Close(); e != nil {
		t.Fatal(e)
	}
	miCompositionReleased(t, x.c.MachineServer.ListenAddress)
	miCompositionReopen(t, x)
	// A separate known-prestart failure uses the same one fixture and same SQL
	// state, not a retry of an uncertain business operation or an altered receipt.
	x.c.MachineServer.TLSCertificateFile = x.private(t, "invalid-machine-certificate.pem", []byte("synthetic invalid certificate\n"))
	x.owner.Config, x.mail.Config = x.c, x.c
	failure, output = miCompositionFailure(t, x)
	if failure == nil || failure.Error() != "TLS_KEYPAIR_INVALID" || strings.Contains(output, "CONTROL_STARTED") {
		t.Fatal("invalid machine certificate reached startup", failure)
	}
	miCompositionJoined(t, x)
	miCompositionReleased(t, miCompositionAddresses(x)...)
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT count(*) FROM rate_limit_buckets WHERE bucket_key=$1", "SELECT count(*) FROM rate_limit_buckets WHERE bucket_key=?", canary)).Scan(&count); e != nil || count != 1 {
		t.Fatal("invalid certificate entered startup maintenance", e)
	}
	if miCompositionBusinessCounts(t, x) != before {
		t.Fatal("invalid certificate changed business/admission state")
	}
	miCompositionReopen(t, x)
}

func TestActualLegacySharedHTTPSMachineComposition(t *testing.T) {
	x := serverCompositionSetup(t, false)
	client := miCompositionConfigure(t, x, false)
	miCompositionLicense(t, x)
	password := miCompositionPassword(t, x)
	run := miCompositionStart(t, x, client)
	miCompositionLeaseHeld(t, x)
	admin := miCompositionLogin(t, x, client, x.c.Server.AdminOrigin, "admin", password)
	portal := miCompositionLogin(t, x, client, x.c.Server.PortalOrigin, "portal", password)
	node, handshake := miCompositionNode(t, x, client, x.c.Server.AdminOrigin, admin)
	miCompositionHeartbeat(t, x, client, x.c.Server.AdminOrigin, node, handshake)
	path := "/agent/v1/nodes/" + node.NodeID + "/heartbeat"
	miCompositionStatus(t, miCompositionCall(t, x, client, x.c.Server.PortalOrigin, http.MethodPost, path, handshake,
		map[string]string{"Authorization": "Bearer " + node.Credential}, nil), http.StatusNotFound, "RESOURCE_NOT_FOUND")
	miCompositionStop(t, x, run)
	miCompositionReopen(t, x)
	run = miCompositionStart(t, x, client)
	miCompositionStatus(t, miCompositionCall(t, x, client, x.c.Server.AdminOrigin, http.MethodGet, "/health/ready", nil, nil, nil), http.StatusOK, "")
	miCompositionStatus(t, miCompositionCall(t, x, client, x.c.Server.AdminOrigin, http.MethodGet, "/api/v1/me", nil, nil, admin), http.StatusOK, "")
	miCompositionStatus(t, miCompositionCall(t, x, client, x.c.Server.PortalOrigin, http.MethodGet, "/api/v1/me", nil, nil, portal), http.StatusOK, "")
	miCompositionHeartbeat(t, x, client, x.c.Server.AdminOrigin, node, handshake)
	miCompositionStop(t, x, run)
	miCompositionReopen(t, x)
}
