package http_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentca"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

const mtlsContract = "../../../../../../api/openapi/v1/nodes/openapi.json"

func agentHandshake() wire.Handshake {
	return wire.Handshake{ProtocolVersion: wire.Version, Capabilities: wire.Capabilities(wire.ReadOnlyProfile), Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "mtls-node", LogicalCPUs: 4, MemoryTotalBytes: 8 << 30, DiskPath: "/", DiskTotalBytes: 100 << 30, DiskAvailableBytes: 50 << 30, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}
}

func pair(t *testing.T, certificate, key string) tls.Certificate {
	t.Helper()
	out, e := tls.X509KeyPair([]byte(certificate), []byte(key))
	if e != nil {
		t.Fatal(e)
	}
	return out
}

func decode[T any](t *testing.T, data map[string]any) T {
	t.Helper()
	raw, e := json.Marshal(data)
	if e != nil {
		t.Fatal(e)
	}
	var out T
	if e := json.Unmarshal(raw, &out); e != nil {
		t.Fatal(e)
	}
	return out
}

func newKey(t *testing.T) (string, string) {
	t.Helper()
	key, e := wire.NewCertificateKey()
	if e != nil {
		t.Fatal(e)
	}
	request, e := wire.NewCertificateRequest(key)
	if e != nil {
		t.Fatal(e)
	}
	return key, request
}

// TestAgentEntranceOverRealMutualTLS drives a node through real TLS client authentication:
// enrollment without a certificate, then every rule of a connection, then a rotation.
func TestAgentEntranceOverRealMutualTLS(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	bundle, caKey := agentca.Material(t)
	authority, e := agentidentity.NewAuthority(bundle, caKey, 720*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	handler := transport.New(s)
	handler.Nodes.Service.Certificates = authority
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(bundle)
	server := httpfixture.NewTLSServerAskingClients(t, handler, roots)
	anonymous := httpfixture.New(t, server, "admin", httpfixture.Schemas(t, mtlsContract))

	mint := func(name string) string {
		token, e := handler.Nodes.Service.Mint(t.Context(), admin, admin.Me.Workspace.ID, name, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		return token.Token
	}
	enroll := func(name, request string) wire.Enrolled {
		return decode[wire.Enrolled](t, anonymous.Call("POST", "/agent/v1/enroll", wire.Enrollment{JoinToken: mint(name), CertificateRequest: request, Handshake: agentHandshake()}, 201, "EnrolledResponse"))
	}
	key, request := newKey(t)
	node := enroll("Mutual TLS", request)
	if node.Certificate == "" {
		t.Fatal("enrollment issued no certificate")
	}
	otherKey, otherRequest := newKey(t)
	other := enroll("Other node", otherRequest)

	base := "/agent/v1/sites/default/cells/default/nodes/" + node.NodeID
	authorized := func(c *httpfixture.Client) *httpfixture.Client {
		c.Headers = map[string]string{"Authorization": "Bearer " + node.Credential}
		return c
	}
	with := authorized(anonymous.WithCertificate(pair(t, node.Certificate, key)))
	heartbeat := agentHandshake()
	with.Call("POST", base+"/heartbeat", heartbeat, 200, "DispositionResponse")

	problem(t, authorized(anonymous.WithCertificate(tls.Certificate{})), "POST", base+"/heartbeat", heartbeat, 401, "AGENT_CERTIFICATE_REQUIRED")
	problem(t, authorized(anonymous.WithCertificate(pair(t, other.Certificate, otherKey))), "POST", base+"/heartbeat", heartbeat, 401, "AGENT_UNAUTHORIZED")

	// A rotation: the old key works until the node confirms from the new one.
	nextKey, nextRequest := newKey(t)
	issued := decode[wire.IssuedCertificate](t, with.Call("POST", base+"/certificates", wire.CertificateRenewal{CertificateRequest: nextRequest}, 200, "IssuedCertificateResponse"))
	if _, e := wire.CheckIssuedCertificate(issued.Certificate, nextKey, node.NodeID); e != nil {
		t.Fatal(e)
	}
	with.Call("POST", base+"/heartbeat", heartbeat, 200, "DispositionResponse")
	next := authorized(anonymous.WithCertificate(pair(t, issued.Certificate, nextKey)))
	next.Call("POST", base+"/certificates/confirmations", struct{}{}, 200, "CertificateConfirmedResponse")
	next.Call("POST", base+"/heartbeat", heartbeat, 200, "DispositionResponse")
	retired := authorized(anonymous.WithCertificate(pair(t, node.Certificate, key)))
	problem(t, retired, "POST", base+"/heartbeat", heartbeat, 401, "AGENT_CERTIFICATE_SUPERSEDED")
}

func problem(t *testing.T, c *httpfixture.Client, method, path string, body any, status int, code string) {
	t.Helper()
	data := c.Problem(method, path, body, status)
	if data != code {
		t.Fatalf("%s %s: got %q, want %q", method, path, data, code)
	}
}

func TestEnrollmentIsThrottledPerRack(t *testing.T) {
	s := fixture.New(t)
	password := fixture.Bootstrap(t, s)
	admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
	policy := config.DefaultRuntimePolicy()
	policy.Rates.AgentEnrollIPCount = 10000
	policy.Rates.AgentEnrollRackCount = 1
	policy.Rates.AgentEnrollRackWindowSeconds = 60
	s.Config.Runtime = &policy
	handler := transport.New(s)
	server := httpfixture.NewTLSServer(t, handler)
	client := httpfixture.New(t, server, "admin", httpfixture.Schemas(t, mtlsContract))
	mint := func(name string) wire.Enrollment {
		token, e := handler.Nodes.Service.Mint(t.Context(), admin, admin.Me.Workspace.ID, name, uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		return wire.Enrollment{JoinToken: token.Token, Handshake: agentHandshake()}
	}
	client.Call("POST", "/agent/v1/enroll", mint("First in the rack"), 201, "EnrolledResponse")
	refused := client.Problem("POST", "/agent/v1/enroll", mint("Second in the rack"), 429)
	if !strings.Contains(refused, "RATE_LIMITED") {
		t.Fatal(refused)
	}
}
