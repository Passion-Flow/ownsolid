package distribution_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

const declaredOrigin = "https://gw.rack7.example:18443"

// deliveredScenario is setup with a gateway CA, so the enrolled inference node's agent runs
// the gateway instead of an administrator (GW-114, ADR 0052).
func deliveredScenario(t *testing.T) (scenario, *x509.CertPool) {
	t.Helper()
	c := setup(t)
	bundle, caKey := testGatewayCAMaterial(t, "test model gateway CA")
	var e error
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "certificate.pem"), filepath.Join(dir, "key.pem")
	if e := os.WriteFile(certPath, bundle, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(keyPath, caKey, 0600); e != nil {
		t.Fatal(e)
	}
	c.s.Identity.Config.ModelGatewayIdentity = &config.ModelGatewayIdentityConfiguration{
		CACertificateFile: certPath, CAPrivateKeyFile: keyPath, CertificateLifetimeHours: 24,
	}
	c.s.Certificates, e = agentidentity.NewAuthority(bundle, caKey, 24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	c.s.Nodes.Gateways = c.s
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(bundle)
	return c, roots
}

func testGatewayCAMaterial(t *testing.T, name string) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func (c scenario) deliveredInput() distribution.ProvisionRequest {
	in := c.input()
	in.ClientOrigin = ""
	return in
}

func (c scenario) claim(t *testing.T) agentwire.Lease {
	t.Helper()
	if _, e := c.s.Nodes.Heartbeat(context.Background(), c.node.NodeID, c.node.Credential, c.handshake); e != nil {
		t.Fatal(e)
	}
	claim, e := c.s.Nodes.Claim(context.Background(), c.node.NodeID, c.node.Credential, uuid.NewString())
	if e != nil || claim.Lease == nil {
		t.Fatalf("no lease: %v", e)
	}
	return *claim.Lease
}

func gatewayCredential(t *testing.T, gateway string) string {
	t.Helper()
	secret := make([]byte, 32)
	if _, e := rand.Read(secret); e != nil {
		t.Fatal(e)
	}
	return agentwire.GatewayCredential(gateway, base64.RawURLEncoding.EncodeToString(secret))
}

func certificateRequest(t *testing.T) string {
	t.Helper()
	key, e := agentwire.NewCertificateKey()
	if e != nil {
		t.Fatal(e)
	}
	request, e := agentwire.NewCertificateRequest(key)
	if e != nil {
		t.Fatal(e)
	}
	return request
}

func gatewayByID(t *testing.T, c scenario, id string) distribution.Gateway {
	t.Helper()
	listed, e := c.s.Gateways(context.Background(), c.a, c.a.Me.Workspace.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, g := range listed {
		if g.ID == id {
			return g
		}
	}
	t.Fatalf("gateway %s not listed", id)
	return distribution.Gateway{}
}

func TestTheNodesAgentStartsAGatewayWhoseCredentialNoPersonSees(t *testing.T) {
	c, roots := deliveredScenario(t)
	ctx := context.Background()
	key := uuid.NewString()
	_, e := c.s.Provision(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
	check(t, e, "GATEWAY_ORIGIN_DECLARED_BY_NODE")
	provisioned, e := c.s.Provision(ctx, c.a, c.deliveredInput(), key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	g := provisioned.Gateway
	if !provisioned.Created || provisioned.Secret != nil || provisioned.SecretAvailable || g.Delivery != "agent" || g.DeliveryState == nil || *g.DeliveryState != "starting" || g.ClientOrigin != nil {
		t.Fatalf("%+v", provisioned)
	}
	again, e := c.s.Provision(ctx, c.a, c.deliveredInput(), key, uuid.NewString())
	if e != nil || again.Created || again.Gateway.ID != g.ID {
		t.Fatal("a repeated registration queued another start", e)
	}

	// Nothing is published to a gateway that has no address yet.
	if _, e = c.s.Publish(ctx, c.a, distribution.PublishRequest{WorkspaceID: c.a.Me.Workspace.ID, GatewayID: g.ID, DeploymentID: uuid.NewString(), DeploymentRevision: 1, Name: "early", Reason: "Too early"}, uuid.NewString(), uuid.NewString()); e == nil || e.Error() != "GATEWAY_NOT_STARTED" {
		t.Fatalf("published to a gateway that was not started: %v", e)
	}
	lease := c.claim(t)
	machineOrigin, _ := c.s.Identity.Config.MachineOrigin()
	if lease.Operation != agentwire.GatewayOperation("start") || lease.Gateway == nil || lease.Gateway.GatewayID != g.ID || lease.Gateway.ControlOrigin != machineOrigin || lease.Inference != nil {
		t.Fatalf("%+v", lease)
	}
	taskPath := lease.TaskID
	credential := gatewayCredential(t, g.ID)
	activation := agentwire.GatewayActivation{LeaseToken: lease.LeaseToken, Credential: credential, CertificateRequest: certificateRequest(t), Origin: declaredOrigin}

	// Nothing activates without the task's lease, and a credential names its gateway.
	stale := activation
	stale.LeaseToken = strings.Repeat("A", 42) + "Q"
	if _, e = c.s.Nodes.ActivateGateway(ctx, c.node.NodeID, c.node.Credential, taskPath, stale, uuid.NewString()); e == nil {
		t.Fatal("an activation without the lease was accepted")
	}
	other := activation
	other.Credential = gatewayCredential(t, uuid.NewString())
	_, e = c.s.Nodes.ActivateGateway(ctx, c.node.NodeID, c.node.Credential, taskPath, other, uuid.NewString())
	check(t, e, "VALIDATION_FAILED")
	if _, e = c.s.AuthorizeModels(ctx, g.ID, credential, "pk1.x.y"); e == nil || e.Error() != "GATEWAY_UNAUTHORIZED" {
		t.Fatalf("a credential worked before its activation: %v", e)
	}

	activated, e := c.s.Nodes.ActivateGateway(ctx, c.node.NodeID, c.node.Credential, taskPath, activation, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	block, _ := pem.Decode([]byte(activated.Certificate))
	leaf, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = leaf.Verify(x509.VerifyOptions{DNSName: "gw.rack7.example", Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); e != nil {
		t.Fatal(e)
	}
	if activated.CertificateAuthority == "" || !activated.NotAfter.Equal(leaf.NotAfter) {
		t.Fatalf("%+v", activated)
	}
	if _, e = c.s.AuthorizeModels(ctx, g.ID, credential, "pk1.x.y"); e == nil || e.Error() == "GATEWAY_UNAUTHORIZED" {
		t.Fatalf("the activated credential was refused: %v", e)
	}
	listed := gatewayByID(t, c, g.ID)
	if listed.ClientOrigin == nil || *listed.ClientOrigin != declaredOrigin || listed.CertificateNotAfter == nil {
		t.Fatalf("%+v", listed)
	}

	running := agentwire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Gateway: &agentwire.GatewayObservation{GatewayID: g.ID, State: "running", ContainerID: strings.Repeat("c", 64)}}
	if state, e := c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, running, uuid.NewString()); e != nil || state != "succeeded" {
		t.Fatal(state, e)
	}
	if listed = gatewayByID(t, c, g.ID); *listed.DeliveryState != "running" || listed.DeliveryErrorCode != nil {
		t.Fatalf("%+v", listed)
	}
	// A finished task activates nothing more.
	if _, e = c.s.Nodes.ActivateGateway(ctx, c.node.NodeID, c.node.Credential, taskPath, activation, uuid.NewString()); e == nil {
		t.Fatal("a finished start activated again")
	}

	revoked, e := c.s.RevokeGateway(ctx, c.a, c.a.Me.Workspace.ID, g.ID, "Retire the node", uuid.NewString())
	if e != nil || revoked.State != "revoked" || *revoked.DeliveryState != "stopping" {
		t.Fatalf("%+v %v", revoked, e)
	}
	stop := c.claim(t)
	if stop.Operation != agentwire.GatewayOperation("stop") || stop.Gateway == nil || stop.Gateway.GatewayID != g.ID || stop.Gateway.ControlOrigin != "" {
		t.Fatalf("%+v", stop)
	}
	stopped := agentwire.Result{LeaseToken: stop.LeaseToken, Outcome: "success", Gateway: &agentwire.GatewayObservation{GatewayID: g.ID, State: "stopped"}}
	if state, e := c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, stop.TaskID, stopped, uuid.NewString()); e != nil || state != "succeeded" {
		t.Fatal(state, e)
	}
	if listed = gatewayByID(t, c, g.ID); *listed.DeliveryState != "stopped" {
		t.Fatalf("%+v", listed)
	}
	if _, e = c.s.AuthorizeModels(ctx, g.ID, credential, "pk1.x.y"); e == nil || e.Error() != "GATEWAY_UNAUTHORIZED" {
		t.Fatalf("a revoked gateway's credential still worked: %v", e)
	}
	// Revoking a stopped gateway again queues nothing.
	if _, e = c.s.RevokeGateway(ctx, c.a, c.a.Me.Workspace.ID, g.ID, "Retire the node", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if claim, e := c.s.Nodes.Claim(ctx, c.node.NodeID, c.node.Credential, uuid.NewString()); e != nil || claim.Lease != nil {
		t.Fatalf("a stopped gateway was stopped again: %+v %v", claim.Lease, e)
	}
}

func TestAGatewayStartThatFailsOrIsLostSaysWhy(t *testing.T) {
	c, _ := deliveredScenario(t)
	ctx := context.Background()
	provisioned, e := c.s.Provision(ctx, c.a, c.deliveredInput(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	id := provisioned.Gateway.ID
	lease := c.claim(t)
	failed := agentwire.Result{LeaseToken: lease.LeaseToken, Outcome: "failure", ErrorCode: "GATEWAY_IMAGE_MISSING", Gateway: &agentwire.GatewayObservation{GatewayID: id, State: "stopped", ErrorCode: "GATEWAY_IMAGE_MISSING"}}
	// A result must match its command.
	wrong := failed
	wrong.Gateway = &agentwire.GatewayObservation{GatewayID: uuid.NewString(), State: "stopped", ErrorCode: "GATEWAY_IMAGE_MISSING"}
	if _, e = c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, wrong, uuid.NewString()); e == nil {
		t.Fatal("a result about another gateway was accepted")
	}
	if state, e := c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, failed, uuid.NewString()); e != nil || state != "failed" {
		t.Fatal(state, e)
	}
	g := gatewayByID(t, c, id)
	if *g.DeliveryState != "failed" || g.DeliveryErrorCode == nil || *g.DeliveryErrorCode != "GATEWAY_IMAGE_MISSING" {
		t.Fatalf("%+v", g)
	}
	// Revoking a gateway whose start failed asks the node to clean up after it.
	revoked, e := c.s.RevokeGateway(ctx, c.a, c.a.Me.Workspace.ID, id, "Failed to start", uuid.NewString())
	if e != nil || *revoked.DeliveryState != "stopping" {
		t.Fatalf("%+v %v", revoked, e)
	}
	stop := c.claim(t)
	// The task ends without a result: control reports what it cannot know.
	if _, e = c.s.DB.Exec(ctx, "UPDATE node_tasks SET state='failed',error_code='AGENT_LEASE_STALE',lease_token_hash=NULL,lease_expires_at=NULL,completed_at=clock_timestamp() WHERE id=$1", stop.TaskID); e != nil {
		t.Fatal(e)
	}
	g = gatewayByID(t, c, id)
	if *g.DeliveryState != "failed" || *g.DeliveryErrorCode != "GATEWAY_TASK_EXPIRED" {
		t.Fatalf("%+v", g)
	}
}

func TestANodeThatRunsNoGatewayKeepsTheAdministratorsCredential(t *testing.T) {
	c, _ := deliveredScenario(t)
	ctx := context.Background()
	// An inference node enrolled before protocol 1.6 cannot run gateway tasks.
	legacy := `["collect_facts.v1","inference.deploy.v1","inference.stop.v1","inference.remove.v1","inference.observe.v1","inference.verify.v1"]`
	if _, e := c.s.DB.Exec(ctx, "UPDATE managed_nodes SET capabilities=$2::jsonb,protocol_version='1.3' WHERE id=$1", c.node.NodeID, legacy); e != nil {
		t.Fatal(e)
	}
	_, e := c.s.Provision(ctx, c.a, c.deliveredInput(), uuid.NewString(), uuid.NewString())
	check(t, e, "VALIDATION_FAILED")
	manual, e := c.s.Provision(ctx, c.a, c.input(), uuid.NewString(), uuid.NewString())
	if e != nil || manual.Secret == nil || manual.Gateway.Delivery != "manual" || manual.Gateway.DeliveryState != nil {
		t.Fatalf("%+v %v", manual, e)
	}
	// Without a gateway CA, every node keeps the administrator's credential.
	c2, _ := deliveredScenario(t)
	c2.s.Identity.Config.ModelGatewayIdentity = nil
	manual, e = c2.s.Provision(ctx, c2.a, c2.input(), uuid.NewString(), uuid.NewString())
	if e != nil || manual.Secret == nil || manual.Gateway.Delivery != "manual" {
		t.Fatalf("%+v %v", manual, e)
	}
}

func TestOnlyTheNodeOfAGatewayCanActivateIt(t *testing.T) {
	c, _ := deliveredScenario(t)
	ctx := context.Background()
	provisioned, e := c.s.Provision(ctx, c.a, c.deliveredInput(), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	lease := c.claim(t)
	// A second node of the same workspace, with its own credential, cannot use this lease.
	minted, e := c.s.Nodes.MintForProfile(ctx, c.a, c.a.Me.Workspace.ID, "Second synthetic node", agentwire.DockerInferenceProfile, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	second, e := c.s.Nodes.Enroll(ctx, agentwire.Enrollment{JoinToken: minted.Token, Handshake: c.handshake}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	activation := agentwire.GatewayActivation{LeaseToken: lease.LeaseToken, Credential: gatewayCredential(t, provisioned.Gateway.ID), CertificateRequest: certificateRequest(t), Origin: declaredOrigin}
	if _, e = c.s.Nodes.ActivateGateway(ctx, second.NodeID, second.Credential, lease.TaskID, activation, uuid.NewString()); e == nil {
		t.Fatal("another node activated this node's gateway")
	}
	// Revoked while starting: the start can no longer activate.
	if _, e = c.s.RevokeGateway(ctx, c.a, c.a.Me.Workspace.ID, provisioned.Gateway.ID, "Changed my mind", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	_, e = c.s.Nodes.ActivateGateway(ctx, c.node.NodeID, c.node.Credential, lease.TaskID, activation, uuid.NewString())
	check(t, e, "GATEWAY_INSTANCE_INACTIVE")
	// The node is still busy with that start, so a new gateway waits for it.
	_, e = c.s.Provision(ctx, c.a, c.deliveredInput(), uuid.NewString(), uuid.NewString())
	check(t, e, "NODE_BUSY")
}

func TestTheConsoleRegistersAnAgentsGatewayWithoutAnAddressOverHTTP(t *testing.T) {
	c, _ := deliveredScenario(t)
	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, 200, "")
	base := "/api/v1/admin/distribution/workspaces/" + c.a.Me.Workspace.ID + "/gateways"
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	made := admin.Write("POST", base, map[string]any{"node_id": c.node.NodeID, "name": "Agent gateway", "reason": "Publish"}, 201, "ProvisionedGatewayResponse")
	gateway := made["gateway"].(map[string]any)
	if made["secret"] != nil || made["secret_available"] != false || gateway["delivery"] != "agent" || gateway["delivery_state"] != "starting" || gateway["client_origin"] != nil {
		t.Fatalf("%v", made)
	}
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	refused := admin.Write("POST", base, map[string]any{"node_id": c.node.NodeID, "name": "Agent gateway", "reason": "Publish", "unknown": true}, 400, "")
	if refused["code"] != "VALIDATION_FAILED" {
		t.Fatalf("%v", refused)
	}
}
