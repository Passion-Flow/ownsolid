//go:build repository_integration && machine_ingress_integration

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentca"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SQL, runtime roles, installation ownership, TLS and workers are actual.
// HTML and node observations remain the existing explicitly synthetic fixtures.
func entranceCompositionClient(t *testing.T, x *serverCompositionStory, forceAddress string) *http.Client {
	t.Helper()
	certificate := x.c.MachineServer.TLSCertificateFile
	raw, err := os.ReadFile(certificate)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(raw) {
		t.Fatal("synthetic local CA unavailable")
	}
	dials := map[string]string{}
	for _, entry := range []struct{ origin, address string }{{x.c.Server.AdminOrigin, x.c.Server.ListenAddress}, {x.c.Server.PortalOrigin, x.c.Server.PortalListenAddress}, {x.c.MachineServer.Origin, x.c.MachineServer.ListenAddress}} {
		origin, err := url.Parse(entry.origin)
		if err != nil {
			t.Fatal(err)
		}
		dials[origin.Host] = entry.address
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			target, allowed := dials[address]
			if !allowed || network != "tcp" {
				return nil, errors.New("ENTRANCE_COMPOSITION_DIAL_REFUSED")
			}
			if forceAddress != "" {
				target = forceAddress
			}
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, target)
		}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("ENTRANCE_COMPOSITION_REDIRECT_REFUSED") }}
}

// Construct the canonical public URL before the actual HTTP request. The
// production router continues to reject cell-relative and foreign-cell paths.
func entranceCompositionCall(t *testing.T, x *serverCompositionStory, client *http.Client, origin, method, path string, input any, headers map[string]string, cookies []*http.Cookie) miCompositionResponse {
	t.Helper()
	return miCompositionCall(t, x, client, origin, method, cellroute.Public(path, wire.LocalScope()), input, headers, cookies)
}

func entranceCompositionHeartbeat(t *testing.T, x *serverCompositionStory, client *http.Client, origin string, node wire.Enrolled, handshake wire.Handshake) {
	t.Helper()
	handshake.Facts.Hostname = "synthetic-split-current-heartbeat"
	handshake.Facts.ObservedAt = time.Now().UTC()
	response := entranceCompositionCall(t, x, client, origin, http.MethodPost, "/agent/v1/nodes/"+node.NodeID+"/heartbeat", handshake, map[string]string{"Authorization": "Bearer " + node.Credential}, nil)
	miCompositionStatus(t, response, http.StatusOK, "")
	data := miCompositionData(t, response)
	serverTime, ok := data["server_time"].(string)
	committed, err := time.Parse(time.RFC3339Nano, serverTime)
	if !ok || err != nil || data["status"] != "active" {
		t.Fatal("actual scoped heartbeat disposition or committed time")
	}
	var facts []byte
	var storedTime time.Time
	if err := x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT facts,last_heartbeat_at FROM managed_nodes WHERE id=$1", "SELECT facts,last_heartbeat_at FROM managed_nodes WHERE id=?", node.NodeID)).Scan(&facts, &storedTime); err != nil {
		t.Fatal("actual scoped heartbeat persistence", err)
	}
	var stored wire.Facts
	if json.Unmarshal(facts, &stored) != nil || stored.Hostname != handshake.Facts.Hostname || !storedTime.Equal(committed) {
		t.Fatal("heartbeat facts and exact returned time did not commit")
	}
}

func entranceCompositionNode(t *testing.T, x *serverCompositionStory, client *http.Client, adminCookies []*http.Cookie) (wire.Enrolled, wire.Handshake, *http.Client) {
	t.Helper()
	csrf := entranceCompositionCall(t, x, client, x.c.Server.AdminOrigin, http.MethodGet, "/api/v1/auth/csrf", nil, nil, adminCookies)
	miCompositionStatus(t, csrf, http.StatusOK, "")
	token, ok := miCompositionData(t, csrf)["token"].(string)
	if !ok || token == "" {
		t.Fatal("actual Admin CSRF token missing")
	}
	join := entranceCompositionCall(t, x, client, x.c.Server.AdminOrigin, http.MethodPost, "/api/v1/admin/node-join-tokens", map[string]string{"workspace_id": x.f.Workspace, "node_name": "Synthetic split-entrance node", "execution_profile": wire.ReadOnlyProfile}, map[string]string{"Origin": x.c.Server.AdminOrigin, "X-CSRF-Token": token}, miCompositionCookies(adminCookies, csrf.cookies))
	miCompositionStatus(t, join, http.StatusCreated, "")
	joinToken, ok := miCompositionData(t, join)["token"].(string)
	if !ok || joinToken == "" {
		t.Fatal("actual scoped join token missing")
	}
	key, err := wire.NewCertificateKey()
	if err != nil {
		t.Fatal("synthetic node certificate key", err)
	}
	csr, err := wire.NewCertificateRequest(key)
	if err != nil {
		t.Fatal("synthetic node certificate request", err)
	}
	handshake := wire.Handshake{ProtocolVersion: wire.Version, ExecutionProfile: wire.ReadOnlyProfile, Capabilities: wire.Capabilities(wire.ReadOnlyProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-split-node", LogicalCPUs: 1, MemoryTotalBytes: 1 << 30, DiskPath: "/", DiskTotalBytes: 4 << 30, DiskAvailableBytes: 2 << 30, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
	enrollment := entranceCompositionCall(t, x, client, x.c.MachineServer.Origin, http.MethodPost, "/agent/v1/enroll", wire.Enrollment{JoinToken: joinToken, CertificateRequest: csr, Handshake: handshake}, nil, nil)
	miCompositionStatus(t, enrollment, http.StatusCreated, "")
	raw, err := json.Marshal(miCompositionData(t, enrollment))
	var node wire.Enrolled
	if err != nil || json.Unmarshal(raw, &node) != nil || node.Certificate == "" {
		t.Fatal("actual enrolled certificate missing")
	}
	if _, err := uuid.Parse(node.NodeID); err != nil {
		t.Fatal("actual node identity missing")
	}
	certificate, err := tls.X509KeyPair([]byte(node.Certificate), []byte(key))
	if err != nil {
		t.Fatal("actual enrolled certificate does not match the node key")
	}
	transport := client.Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{certificate}
	t.Cleanup(transport.CloseIdleConnections)
	secured := *client
	secured.Transport = transport
	return node, handshake, &secured
}

func TestActualSplitBrowserThreeIngressServerComposition(t *testing.T) {
	for _, mode := range []config.TransportMode{config.TransportDirectHTTPS, config.TransportHTTP} {
		t.Run(string(mode), func(t *testing.T) {
			t.Setenv("PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA", "m20_repository_"+strings.ReplaceAll(uuid.NewString(), "-", ""))
			x := serverCompositionSetup(t, false)
			// The shared historical story helper grants every table. Current serving
			// admission excludes the private audit head; narrow this fixture before
			// serving rather than weakening production admission or that older helper.
			if x.f.PostgreSQL != nil {
				var role string
				if err := x.d.postgres.QueryRow(x.ctx, "SELECT current_user").Scan(&role); err != nil {
					t.Fatal("fixture runtime principal readback", err)
				}
				schema := x.f.PostgreSQL.Config().ConnConfig.RuntimeParams["search_path"]
				if _, err := x.f.PostgreSQL.Exec(x.ctx, "REVOKE ALL ON "+pgx.Identifier{schema, "audit_chain_head"}.Sanitize()+" FROM "+pgx.Identifier{role}.Sanitize()); err != nil {
					t.Fatal("fixture private audit boundary", err)
				}
				_, err := x.d.postgres.Exec(x.ctx, "SELECT 1 FROM audit_chain_head LIMIT 1")
				var denied *pgconn.PgError
				if !errors.As(err, &denied) || denied.Code != "42501" {
					t.Fatal("runtime fixture retained private audit-head access")
				}
			}
			certificate, key := x.c.Server.TLSCertificateFile, x.c.Server.TLSPrivateKeyFile
			x.c.Server.PortalListenAddress = miCompositionAddress(t)
			machineAddress := miCompositionAddress(t)
			if x.c.Server.PortalListenAddress == x.c.Server.ListenAddress || machineAddress == x.c.Server.ListenAddress || machineAddress == x.c.Server.PortalListenAddress {
				t.Fatal("fixture reused an address")
			}
			x.c.MachineServer = &config.MachineServerConfiguration{ListenAddress: machineAddress, Origin: "https://" + machineAddress, TLSCertificateFile: certificate, TLSPrivateKeyFile: key}
			ca, issuer := agentca.Material(t)
			x.c.AgentIdentity = &config.AgentIdentityConfiguration{CACertificateFile: x.private(t, "agent-ca.pem", ca), CAPrivateKeyFile: x.private(t, "agent-ca-key.pem", issuer)}
			x.c.Server.TransportMode = mode
			scheme := "https"
			if mode == config.TransportHTTP {
				scheme = "http"
				x.c.Server.TLSCertificateFile = ""
				x.c.Server.TLSPrivateKeyFile = ""
			}
			_, adminPort, _ := net.SplitHostPort(x.c.Server.ListenAddress)
			_, portalPort, _ := net.SplitHostPort(x.c.Server.PortalListenAddress)
			x.c.Server.AdminOrigin = scheme + "://admin.example.invalid:" + adminPort
			x.c.Server.PortalOrigin = scheme + "://portal.example.invalid:" + portalPort
			if err := x.c.Validate(); err != nil {
				t.Fatal("explicit split composition configuration", err)
			}
			x.owner.Config, x.mail.Config = x.c, x.c
			client := entranceCompositionClient(t, x, "")
			miCompositionLicense(t, x)
			password := miCompositionPassword(t, x)
			run := miCompositionStart(t, x, client)
			miCompositionLeaseHeld(t, x)
			cookies := map[string][]*http.Cookie{}
			for _, entry := range []struct{ audience, origin string }{{"admin", x.c.Server.AdminOrigin}, {"portal", x.c.Server.PortalOrigin}} {
				response := entranceCompositionCall(t, x, client, entry.origin, http.MethodGet, "/", nil, nil, nil)
				miCompositionStatus(t, response, http.StatusOK, "")
				if !bytes.Contains(response.body, []byte("Synthetic "+entry.audience+" routing fixture")) {
					t.Fatal("split socket served the wrong assets")
				}
				cookies[entry.audience] = miCompositionLogin(t, x, client, entry.origin, entry.audience, password)
			}
			miCompositionStatus(t, entranceCompositionCall(t, x, client, x.c.Server.PortalOrigin, http.MethodGet, "/api/v1/me", nil, nil, cookies["admin"]), http.StatusUnauthorized, "AUTH_REQUIRED")
			miCompositionStatus(t, entranceCompositionCall(t, x, client, x.c.Server.AdminOrigin, http.MethodGet, "/api/v1/me", nil, nil, cookies["portal"]), http.StatusUnauthorized, "AUTH_REQUIRED")
			for _, entry := range []struct{ origin, wrongAddress string }{{x.c.Server.PortalOrigin, x.c.Server.ListenAddress}, {x.c.Server.AdminOrigin, x.c.Server.PortalListenAddress}} {
				wrong := entranceCompositionClient(t, x, entry.wrongAddress)
				for _, path := range []string{"/", "/runtime-config.json", "/health/ready", "/api/v1/me"} {
					miCompositionStatus(t, entranceCompositionCall(t, x, wrong, entry.origin, http.MethodGet, path, nil, nil, nil), http.StatusMisdirectedRequest, "MISDIRECTED_REQUEST")
				}
			}
			node, handshake, machineClient := entranceCompositionNode(t, x, client, cookies["admin"])
			// A public route has exactly one scope. These direct requests bypass
			// only the fixture's canonical URL helper, never server validation.
			relative := "/agent/v1/nodes/" + node.NodeID + "/heartbeat"
			headers := map[string]string{"Authorization": "Bearer " + node.Credential}
			miCompositionStatus(t, miCompositionCall(t, x, machineClient, x.c.MachineServer.Origin, http.MethodPost, relative, handshake, headers, nil), http.StatusNotFound, "RESOURCE_NOT_FOUND")
			foreign := wire.LocalScope()
			foreign.CellID = "foreign"
			miCompositionStatus(t, miCompositionCall(t, x, machineClient, x.c.MachineServer.Origin, http.MethodPost, cellroute.Public(relative, foreign), handshake, headers, nil), http.StatusNotFound, "RESOURCE_NOT_FOUND")

			entranceCompositionHeartbeat(t, x, machineClient, x.c.MachineServer.Origin, node, handshake)
			miCompositionStatus(t, entranceCompositionCall(t, x, client, x.c.MachineServer.Origin, http.MethodPost, "/agent/v1/nodes/"+node.NodeID+"/heartbeat", handshake, map[string]string{"Authorization": "Bearer " + node.Credential}, nil), http.StatusUnauthorized, "AGENT_CERTIFICATE_REQUIRED")
			for _, origin := range []string{x.c.Server.AdminOrigin, x.c.Server.PortalOrigin} {
				miCompositionStatus(t, entranceCompositionCall(t, x, client, origin, http.MethodPost, "/agent/v1/nodes/"+node.NodeID+"/heartbeat", handshake, map[string]string{"Authorization": "Bearer " + node.Credential}, nil), http.StatusNotFound, "RESOURCE_NOT_FOUND")
			}
			miCompositionStop(t, x, run)
			miCompositionReleased(t, x.c.Server.PortalListenAddress)
			miCompositionReopen(t, x)
			run = miCompositionStart(t, x, client)
			for _, entry := range []struct{ audience, origin string }{{"admin", x.c.Server.AdminOrigin}, {"portal", x.c.Server.PortalOrigin}} {
				miCompositionStatus(t, entranceCompositionCall(t, x, client, entry.origin, http.MethodGet, "/api/v1/me", nil, nil, cookies[entry.audience]), http.StatusOK, "")
			}
			entranceCompositionHeartbeat(t, x, machineClient, x.c.MachineServer.Origin, node, handshake)
			miCompositionStop(t, x, run)
			miCompositionReleased(t, x.c.Server.PortalListenAddress)
			miCompositionReopen(t, x)
			t.Log("SPLIT_BROWSER_COMPOSITION_SQL_LOGIN_ISOLATION_RESTART_AND_THREE_SOCKET_CLEANUP_PASSED")
		})
	}
}
