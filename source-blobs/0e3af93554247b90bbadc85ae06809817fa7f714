//go:build repository_integration && machine_ingress_integration && transport_integration

package http_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// This root owns one actual migrated schema, one original dispatcher graph and
// production listeners. Synthetic facts describe a protocol observation only;
// no physical agent, CPU command, engine inference or artifact bytes are faked.
func TestTransportActualHTTPWithIndependentMachineHTTPS(t *testing.T) {
	owner := fixture.New(t)
	transportConfigure(owner, config.TransportHTTP)
	owner.Config.Server.ListenAddress = machineSQLAddress(t)
	_, browserPort, e := net.SplitHostPort(owner.Config.Server.ListenAddress)
	if e != nil {
		t.Fatal(e)
	}
	owner.Config.Server.PortalOrigin = "http://portal.example.invalid:" + browserPort
	owner.Config.Server.AdminOrigin = "http://admin.example.invalid:" + browserPort
	machineAddress := machineSQLAddress(t)
	if machineAddress == owner.Config.Server.ListenAddress {
		t.Fatal("owned ephemeral socket identities collided")
	}
	_, machinePort, e := net.SplitHostPort(machineAddress)
	if e != nil {
		t.Fatal(e)
	}
	certificate, key, roots := machineSQLMaterial(t)
	owner.Config.MachineServer = &config.MachineServerConfiguration{
		ListenAddress: machineAddress, Origin: "https://machine.example.invalid:" + machinePort,
		TLSCertificateFile: certificate, TLSPrivateKeyFile: key,
	}
	owner.Mail.Config = owner.Config
	password := fixture.Bootstrap(t, owner)
	originalHandler := transport.New(owner)
	if e := originalHandler.InitializationError(); e != nil {
		t.Fatal("original dispatcher initialization", e)
	}
	if e := originalHandler.Licensing.Service.VerifyStored(context.Background()); e != nil {
		t.Fatal("actual signed fixture License", e)
	}
	// These domain owners start no background worker in this HTTP/API root.
	// Their cleanup still joins before the fixture's registered schema cleanup.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), transportHTTPTimeout)
		defer cancel()
		if e := originalHandler.Notifications.Service.Stop(ctx); e != nil {
			t.Error("notification owner shutdown", e)
		}
		if originalHandler.SSO != nil && originalHandler.SSO.Service != nil {
			originalHandler.SSO.Service.Close()
		}
	})
	group, done := machineSQLOpen(t, owner, originalHandler)
	stop := func() {
		if group == nil {
			return
		}
		closeError := group.Close()
		var serveError error
		select {
		case serveError = <-done:
		case <-time.After(transportHTTPTimeout):
			t.Fatal("dual listener Serve did not join")
		}
		group = nil
		machineSQLReleased(t, owner.Config.Server.ListenAddress, machineAddress)
		if closeError != nil || serveError != nil {
			t.Fatal("dual listener close/Serve join failed", closeError, serveError)
		}
	}
	t.Cleanup(stop)
	admin := machineSQLBrowser(t, owner.Config.Server.AdminOrigin, owner.Config.Server.ListenAddress)
	admin.schemas["MachineProblem"] = machineSQLProblemSchema(t)
	portal := machineSQLBrowser(t, owner.Config.Server.PortalOrigin, owner.Config.Server.ListenAddress)
	machine := machineSQLClient(t, owner.Config.MachineServer.Origin, machineAddress, roots, "")
	call := func(path string, body any, credential string, status int, code string, headers http.Header, host string, selected *http.Client) map[string]any {
		t.Helper()
		return machineSQLCall(t, selected, owner.Config.MachineServer.Origin, path, body, credential, status, code, headers, host, admin)
	}
	refuse := func(path string, body any, credential string, status int, code string, headers http.Header, host string, selected *http.Client) {
		t.Helper()
		before := machineSQLSnapshot(t, owner)
		call(path, body, credential, status, code, headers, host, selected)
		if machineSQLSnapshot(t, owner) != before {
			t.Fatal("refused request mutated node/token/task/system audit state")
		}
	}

	t.Log("step browser_login_and_scoped_join")
	for _, browser := range []*transportBrowser{admin, portal} {
		public := browser.call("GET", "/runtime-config.json", nil, 200, "", nil)["transport"].(map[string]any)
		if public["mode"] != "http" || public["plaintext"] != true {
			t.Fatal("actual browser entrance did not retain explicit HTTP policy")
		}
		browser.write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")
		browser.call("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	}
	me := admin.call("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	user := me["user"].(map[string]any)["id"].(string)
	workspace := admin.write("POST", "/api/v1/admin/workspaces", map[string]any{"name": "Independent machine HTTP Workspace", "owner_user_id": user}, 201, "")
	workspaceID, tenantID := workspace["id"].(string), workspace["tenant_id"].(string)
	portal.call("GET", "/api/v1/workspaces/"+workspaceID, nil, 200, "", nil)
	join := admin.write("POST", nodeJoinHTTPPath, nodeJoinHTTPBody(workspaceID, wire.ReadOnlyProfile), 201, "")
	joinID, joinSecret := join["id"].(string), join["token"].(string)
	if !security.ValidToken(joinSecret) || transportCount(t, owner, "SELECT count(*) FROM node_join_tokens WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND token_hash=$4 AND consumed_at IS NULL", joinID, tenantID, workspaceID, security.Digest("node-join:v1:"+joinSecret)) != 1 {
		t.Fatal("scoped join secret was not durably hashed")
	}
	for _, browser := range []*transportBrowser{admin, portal} {
		if transportCount(t, owner, "SELECT count(*) FROM sessions WHERE token_hash=$1 AND audience=$2 AND user_id=$3 AND revoked_at IS NULL", security.Digest(transportCookie(t, browser)), browser.audience, user) != 1 {
			t.Fatal("actual audience-scoped login was not durable")
		}
	}

	t.Log("step machine_enrollment_heartbeat_and_task_lease")
	enrollment := enrollmentHTTPInput(joinSecret)
	var node wire.Enrolled
	machineSQLDecode(t, call("/agent/v1/enroll", enrollment, "", 201, "", nil, "", machine), &node)
	if !security.ValidToken(node.Credential) || node.ProtocolVersion != wire.Version || transportCount(t, owner, "SELECT count(*) FROM managed_nodes WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND credential_hash=$4 AND status='active'", node.NodeID, tenantID, workspaceID, security.Digest("node-credential:v1:"+node.Credential)) != 1 || transportCount(t, owner, "SELECT count(*) FROM node_join_tokens WHERE id=$1 AND node_id=$2 AND consumed_at IS NOT NULL", joinID, node.NodeID) != 1 {
		t.Fatal("actual enrollment scope/credential/consumption mismatch")
	}
	base := "/agent/v1/sites/default/cells/default/nodes/" + node.NodeID
	var heartbeat wire.Disposition
	machineSQLDecode(t, call(base+"/heartbeat", enrollment.Handshake, node.Credential, 200, "", nil, "", machine), &heartbeat)
	if heartbeat.Status != "active" || heartbeat.ServerTime.IsZero() || transportCount(t, owner, "SELECT count(*) FROM managed_nodes WHERE id=$1 AND last_heartbeat_at IS NOT NULL", node.NodeID) != 1 {
		t.Fatal("actual heartbeat was not persisted")
	}
	task := admin.write("POST", "/api/v1/admin/nodes/"+node.NodeID+"/tasks", map[string]any{"operation": wire.CollectFacts}, 202, "")
	taskID := task["id"].(string)
	var claim wire.Claim
	machineSQLDecode(t, call(base+"/tasks/claims", struct{}{}, node.Credential, 200, "", nil, "", machine), &claim)
	if claim.Lease == nil || claim.Lease.TaskID != taskID || claim.Lease.Operation != wire.CollectFacts || claim.Lease.Attempt != 1 || !security.ValidToken(claim.Lease.LeaseToken) || transportCount(t, owner, "SELECT count(*) FROM node_tasks WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND node_id=$4 AND state='running' AND attempt=1 AND lease_token_hash=$5", taskID, tenantID, workspaceID, node.NodeID, security.Digest("node-lease:v1:"+claim.Lease.LeaseToken)) != 1 {
		t.Fatal("actual scoped task lease was not committed")
	}
	lease := claim.Lease.LeaseToken

	t.Log("step transport_and_route_refusals")
	for _, path := range []string{"/", "/api/v1/me", "/api/v1/auth/login", "/runtime-config.json", "/health/live", "/assets/application.js"} {
		refuse(path, struct{}{}, node.Credential, 404, "RESOURCE_NOT_FOUND", nil, "", machine)
	}
	for _, browser := range []*transportBrowser{admin, portal} {
		for _, path := range []string{"/agent/v1/enroll", "/gateway/v1/admissions"} {
			before := machineSQLSnapshot(t, owner)
			transportProblem(t, browser.call("POST", path, enrollment, 404, "Problem", nil), "RESOURCE_NOT_FOUND")
			if machineSQLSnapshot(t, owner) != before {
				t.Fatal("browser route fallback changed machine state")
			}
		}
	}
	for _, name := range []string{"Origin", "Cookie", "Forwarded", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Real-IP"} {
		refuse(base+"/heartbeat", enrollment.Handshake, node.Credential, 403, "ORIGIN_REJECTED", http.Header{name: []string{"synthetic-browser-or-proxy-value"}}, "", machine)
	}
	refuse(base+"/heartbeat", enrollment.Handshake, node.Credential, 421, "MISDIRECTED_REQUEST", nil, "foreign.example.invalid:"+machinePort, machine)
	wrongSNI := machineSQLClient(t, owner.Config.MachineServer.Origin, machineAddress, roots, "admin.example.invalid")
	// Both names are actual SANs, so normal TLS verification succeeds before
	// the production request resolver refuses mismatched SNI and authority.
	refuse(base+"/heartbeat", enrollment.Handshake, node.Credential, 421, "MISDIRECTED_REQUEST", nil, "", wrongSNI)
	for _, selected := range []*http.Client{
		machineSQLClient(t, owner.Config.MachineServer.Origin, machineAddress, x509.NewCertPool(), ""),
		machineSQLClient(t, owner.Config.MachineServer.Origin, machineAddress, roots, "uncovered.example.invalid"),
	} {
		before := machineSQLSnapshot(t, owner)
		response, e := selected.Get(owner.Config.MachineServer.Origin + "/api/v1/me")
		if response != nil {
			response.Body.Close()
		}
		if e == nil || machineSQLSnapshot(t, owner) != before {
			t.Fatal("untrusted CA/name accepted or changed SQL state")
		}
	}
	beforeCleartext := machineSQLSnapshot(t, owner)
	response, e := machine.Get("http" + strings.TrimPrefix(owner.Config.MachineServer.Origin, "https") + "/api/v1/me")
	if e != nil {
		t.Fatal("owned cleartext refusal transport", e)
	}
	_, readError := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	closeError := response.Body.Close()
	if readError != nil || closeError != nil || response.StatusCode != http.StatusBadRequest || response.TLS != nil || machineSQLSnapshot(t, owner) != beforeCleartext {
		t.Fatal("TLS socket did not refuse cleartext before dispatch")
	}

	t.Log("step credential_lease_and_foreign_scope_refusals")
	refuse(base+"/heartbeat", enrollment.Handshake, "", 401, "AGENT_UNAUTHORIZED", nil, "", machine)
	refuse(base+"/heartbeat", enrollment.Handshake, security.Token(), 401, "AGENT_UNAUTHORIZED", nil, "", machine)
	refuse(base+"/heartbeat", enrollment.Handshake, "", 401, "AGENT_UNAUTHORIZED", http.Header{"Authorization": []string{"Bearer " + node.Credential, "Bearer " + node.Credential}}, "", machine)
	refuse(base+"/tasks/"+taskID+"/renewals", wire.LeaseRenewal{LeaseToken: security.Token()}, node.Credential, 409, "AGENT_LEASE_STALE", nil, "", machine)
	refuse(base+"/tasks/"+taskID+"/artifact", wire.LeaseRenewal{LeaseToken: security.Token()}, node.Credential, 409, "AGENT_LEASE_STALE", nil, "", machine)
	foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
	machineSQLExec(t, owner, "INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign machine tenant','active')", foreignTenant)
	machineSQLExec(t, owner, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic foreign machine Workspace','active')", foreignWorkspace, foreignTenant)
	beforeForeign := machineSQLSnapshot(t, owner)
	transportProblem(t, admin.write("POST", nodeJoinHTTPPath, nodeJoinHTTPBody(foreignWorkspace, wire.ReadOnlyProfile), 404, "Problem"), "RESOURCE_NOT_FOUND")
	if machineSQLSnapshot(t, owner) != beforeForeign || transportCount(t, owner, "SELECT count(*) FROM node_join_tokens WHERE workspace_id=$1", foreignWorkspace) != 0 {
		t.Fatal("foreign existing Workspace acquired node authority")
	}

	t.Log("step same_database_listener_restart_and_completion")
	call(base+"/tasks/"+taskID+"/renewals", wire.LeaseRenewal{LeaseToken: lease}, node.Credential, 200, "", nil, "", machine)
	beforeRestart := machineSQLSnapshot(t, owner)
	stop()
	group, done = machineSQLOpen(t, owner, originalHandler)
	if machineSQLSnapshot(t, owner) != beforeRestart {
		t.Fatal("listener restart rewrote durable machine state")
	}
	admin.call("GET", "/api/v1/me", nil, 200, "MeResponse", nil)
	portal.call("GET", "/api/v1/workspaces/"+workspaceID, nil, 200, "", nil)
	machineSQLDecode(t, call(base+"/heartbeat", enrollment.Handshake, node.Credential, 200, "", nil, "", machine), &heartbeat)
	call(base+"/tasks/"+taskID+"/renewals", wire.LeaseRenewal{LeaseToken: lease}, node.Credential, 200, "", nil, "", machine)
	result := wire.Result{LeaseToken: lease, Outcome: "success", Facts: &enrollment.Facts}
	if out := call(base+"/tasks/"+taskID+"/results", result, node.Credential, 200, "", nil, "", machine); out["state"] != "succeeded" {
		t.Fatal("actual result did not finish the task")
	}
	completed := machineSQLSnapshot(t, owner)
	if out := call(base+"/tasks/"+taskID+"/results", result, node.Credential, 200, "", nil, "", machine); out["state"] != "succeeded" || machineSQLSnapshot(t, owner) != completed {
		t.Fatal("same completed result did not retain its immutable receipt")
	}
	if transportCount(t, owner, "SELECT count(*) FROM node_tasks WHERE id=$1 AND state='succeeded' AND lease_token_hash IS NULL AND completed_lease_hash=$2 AND result_hash IS NOT NULL AND completed_at IS NOT NULL", taskID, security.Digest("node-lease:v1:"+lease)) != 1 {
		t.Fatal("terminal task receipt was not durable")
	}
	for action, audience := range map[string]string{"node.join_token_created": "admin", "node.enrolled": "system", "node.task_requested": "admin", "node.task_claimed": "system", "node.task_succeeded": "system"} {
		if transportCount(t, owner, "SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND action=$3 AND audience=$4 AND outcome='success'", tenantID, workspaceID, action, audience) != 1 {
			t.Fatal("actual scoped operation audit absent or duplicated", action)
		}
	}

	t.Log("step current_session_and_node_revocation")
	pending := admin.write("POST", nodeJoinHTTPPath, nodeJoinHTTPBody(workspaceID, wire.ReadOnlyProfile), 201, "")
	current := admin.call("GET", "/api/v1/admin/nodes/"+node.NodeID, nil, 200, "", nil)
	admin.write("PATCH", "/api/v1/admin/nodes/"+node.NodeID, map[string]any{"name": current["name"], "status": "removed", "version": current["version"], "reason": "Explicit isolated machine credential revocation"}, 200, "")
	refuse(base+"/heartbeat", enrollment.Handshake, node.Credential, 401, "AGENT_UNAUTHORIZED", nil, "", machine)
	if transportCount(t, owner, "SELECT count(*) FROM managed_nodes WHERE id=$1 AND status='removed' AND credential_hash IS NULL", node.NodeID) != 1 {
		t.Fatal("real node removal retained a credential")
	}
	oldSession := transportCookie(t, admin)
	admin.write("POST", "/api/v1/auth/logout", nil, 204, "")
	transportCleared(t, admin)
	admin.cookies[admin.cookieName("session")] = &http.Cookie{Name: admin.cookieName("session"), Value: oldSession}
	transportProblem(t, admin.call("GET", "/api/v1/me", nil, 401, "Problem", nil), "AUTH_SESSION_EXPIRED")
	if transportCount(t, owner, "SELECT count(*) FROM sessions WHERE token_hash=$1 AND revoked_at IS NOT NULL", security.Digest(oldSession)) != 1 {
		t.Fatal("real logout did not revoke durable session authority")
	}
	admin.write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "LoginResponse")

	t.Log("step missing_license_refuses_new_admission")
	// Only this disposable schema loses its signed synthetic fixture history.
	// This tests actual missing-License admission, not simulated wall-clock expiry.
	machineSQLExec(t, owner, "DELETE FROM platform_license_artifacts")
	state := admin.call("GET", "/api/v1/admin/license", nil, 200, "", nil)
	if state["state"] != "missing" || state["new_business_allowed"] != false {
		t.Fatal("actual missing License did not close new admission")
	}
	beforeLicense := machineSQLSnapshot(t, owner)
	transportProblem(t, admin.write("POST", nodeJoinHTTPPath, nodeJoinHTTPBody(workspaceID, wire.ReadOnlyProfile), 403, "Problem"), "LICENSE_NEW_BUSINESS_UNAVAILABLE")
	if machineSQLSnapshot(t, owner) != beforeLicense {
		t.Fatal("missing License created a node/token/task or success audit")
	}
	if revoked := admin.write("POST", nodeJoinHTTPPath+"/"+pending["id"].(string)+"/revocations", map[string]any{"reason": "Unlicensed recovery of isolated pending token"}, 200, ""); revoked["status"] != "revoked" {
		t.Fatal("missing License blocked an authorized recovery revocation")
	}
	stop()
	t.Log("actual HTTP browser + verified machine TLS, scoped SQL enrollment/heartbeat/task lease/result/replay/revocation and same-database listener restart; positive registry/model/training/preparation/component file transfer remains pending its real immutable-input fixture; no agent execution, engine inference, full-process restart or browser UI qualification")
}

func machineSQLAddress(t *testing.T) string {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	if e = listener.Close(); e != nil {
		t.Fatal(e)
	}
	t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", address)
	return address
}

func machineSQLMaterial(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	public, private, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	leaf := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated machine ingress SQL fixture"}, DNSNames: []string{"machine.example.invalid", "admin.example.invalid"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, leaf, leaf, public, private)
	if e != nil {
		t.Fatal(e)
	}
	keyDER, e := x509.MarshalPKCS8PrivateKey(private)
	if e != nil {
		t.Fatal(e)
	}
	certificate, key := filepath.Join(root, "machine-certificate.pem"), filepath.Join(root, "machine-key.pem")
	if e = os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); e != nil {
		t.Fatal(e)
	}
	parsed, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return certificate, key, roots
}

func machineSQLOpen(t *testing.T, owner *identity.Service, handler *transport.Handler) (*httpserver.ListenerGroup, <-chan error) {
	t.Helper()
	material, e := owner.Config.MachineTLSConfig()
	if e != nil {
		t.Fatal("actual machine material validation", e)
	}
	group, e := httpserver.ListenConfigured(owner.Config.Server, nil, handler, owner.Config.MachineServer, material, handler.MachineHandler(), owner.Config.Policy().HTTP)
	if e != nil {
		t.Fatal("production prebound dual listeners", e)
	}
	done := make(chan error, 1)
	go func() { done <- group.Serve(context.Background()) }()
	return group, done
}

func machineSQLReleased(t *testing.T, addresses ...string) {
	t.Helper()
	for _, address := range addresses {
		listener, e := net.Listen("tcp", address)
		if e != nil {
			t.Fatal("owned socket retained after bounded join", e)
		}
		if e = listener.Close(); e != nil {
			t.Fatal(e)
		}
	}
}

func machineSQLClient(t *testing.T, origin, address string, roots *x509.CertPool, serverName string) *http.Client {
	t.Helper()
	declared, e := url.Parse(origin)
	if e != nil {
		t.Fatal(e)
	}
	dial := &net.Dialer{Timeout: transportHTTPTimeout}
	clientTransport := &http.Transport{Proxy: nil, DisableKeepAlives: true,
		TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: serverName, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
			if network != "tcp" || target != declared.Host {
				return nil, &net.AddrError{Err: "undeclared isolated authority", Addr: target}
			}
			return dial.DialContext(ctx, network, address)
		}}
	t.Cleanup(clientTransport.CloseIdleConnections)
	return &http.Client{Transport: clientTransport, Timeout: transportHTTPTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func machineSQLBrowser(t *testing.T, origin, address string) *transportBrowser {
	t.Helper()
	declared, e := url.Parse(origin)
	if e != nil {
		t.Fatal(e)
	}
	audience := strings.TrimSuffix(declared.Hostname(), ".example.invalid")
	if audience != "admin" && audience != "portal" || declared.Scheme != "http" {
		t.Fatal("exact declared browser origin required")
	}
	return &transportBrowser{t: t, client: machineSQLClient(t, origin, address, nil, ""), origin: origin, audience: audience, secure: false, cookies: map[string]*http.Cookie{}, schemas: schemas(t)}
}

func machineSQLProblemSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join("../../../../../../api/openapi/v1/nodes/openapi.json"))
	if e != nil {
		t.Fatal("declared Nodes HTTP contract unavailable", e)
	}
	var contract map[string]any
	if e = json.Unmarshal(raw, &contract); e != nil {
		t.Fatal(e)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	const identifier = "https://projectone.invalid/machine-nodes"
	if e = compiler.AddResource(identifier, map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "components": contract["components"]}); e != nil {
		t.Fatal(e)
	}
	schema, e := compiler.Compile(identifier + "#/components/schemas/Problem")
	if e != nil {
		t.Fatal("actual Nodes Problem schema compile", e)
	}
	return schema
}

func machineSQLCall(t *testing.T, client *http.Client, origin, path string, body any, credential string, status int, code string, headers http.Header, host string, browser *transportBrowser) map[string]any {
	t.Helper()
	raw, e := json.Marshal(body)
	if e != nil {
		t.Fatal(e)
	}
	request, e := http.NewRequest("POST", origin+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	if host != "" {
		request.Host = host
	}
	for name, values := range headers {
		request.Header[name] = append([]string(nil), values...)
	}
	response, e := client.Do(request)
	if e != nil {
		t.Fatal("actual private-CA machine request failed", e)
	}
	defer response.Body.Close()
	raw, e = io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if e != nil || len(raw) == 1<<20 {
		t.Fatal("bounded machine JSON response invalid", e)
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || response.StatusCode != status {
		t.Fatalf("machine %s expected %d observed %d (%v)", path, status, response.StatusCode, value["code"])
	}
	declared, _ := url.Parse(origin)
	expectedName := declared.Hostname()
	if override := client.Transport.(*http.Transport).TLSClientConfig.ServerName; override != "" {
		expectedName = override
	}
	if response.TLS == nil || len(response.TLS.VerifiedChains) == 0 || response.TLS.ServerName != expectedName || len(response.Cookies()) != 0 || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Request-ID") == "" {
		t.Fatal("machine response lost verified TLS or its cookie-free security envelope")
	}
	if status >= 400 {
		if response.Header.Get("Content-Type") != "application/problem+json" || value["code"] != code || value["request_id"] != response.Header.Get("X-Request-ID") || browser.schemas["MachineProblem"].Validate(value) != nil {
			t.Fatal("machine refusal lost its exact redacted correlated Problem")
		}
		return value
	}
	data, ok := value["data"].(map[string]any)
	if !ok {
		t.Fatal("machine success did not use the original dispatcher envelope")
	}
	return data
}

func machineSQLDecode(t *testing.T, value map[string]any, output any) {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil || json.Unmarshal(raw, output) != nil {
		t.Fatal("actual protocol response decode failed")
	}
}

func machineSQLSnapshot(t *testing.T, owner *identity.Service) [sha256.Size]byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), transportHTTPTimeout)
	defer cancel()
	var raw string
	if e := owner.DB.QueryRow(ctx, `SELECT jsonb_build_object('nodes',(SELECT jsonb_agg(to_jsonb(n) ORDER BY n.id) FROM managed_nodes n),'joins',(SELECT jsonb_agg(to_jsonb(j) ORDER BY j.id) FROM node_join_tokens j),'tasks',(SELECT jsonb_agg(to_jsonb(t) ORDER BY t.id) FROM node_tasks t),'audits',(SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM audit_events a WHERE action LIKE 'node.%'))::text`).Scan(&raw); e != nil {
		t.Fatal("actual node state readback", e)
	}
	return sha256.Sum256([]byte(raw))
}

func machineSQLExec(t *testing.T, owner *identity.Service, statement string, values ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), transportHTTPTimeout)
	defer cancel()
	if _, e := owner.DB.Exec(ctx, statement, values...); e != nil {
		t.Fatal("isolated fixture setup mutation", e)
	}
}
