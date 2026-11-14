package nodes_test

import (
	"crypto/tls"
	"crypto/x509"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentca"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentcost"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiagnosticInferenceHeartbeatHTTPSCost(t *testing.T) {
	ctx := t.Context()
	var recorder agentcost.Recorder
	account := fixture.NewConfigured(t, func(c *pgxpool.Config) {
		recorder.Configure(c)
		c.ConnConfig.RuntimeParams["auto_explain.log_parameter_max_length"] = "0"
	})
	password := fixture.Bootstrap(t, account)
	admin := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	handler := transport.New(account)
	s := handler.Nodes.Service
	authority := agentca.Authority(t)
	s.Certificates = authority
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(authority.Bundle())) {
		t.Fatal("invalid fixture authority")
	}
	server := httpfixture.NewTLSServerAskingClients(t, handler, roots)
	t.Cleanup(server.Close)
	key, err := wire.NewCertificateKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := wire.NewCertificateRequest(key)
	if err != nil {
		t.Fatal(err)
	}
	profile := wire.DockerInferenceProfile
	token, err := s.MintForProfile(ctx, admin, admin.Me.Workspace.ID, "Inference transport cost node", profile, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	h := wire.Handshake{ProtocolVersion: wire.ProfileVersion(profile), ExecutionProfile: profile, Capabilities: wire.Capabilities(profile), Facts: facts()}
	node, err := s.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, CertificateRequest: csr, Handshake: h}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair([]byte(node.Certificate), []byte(key))
	if err != nil {
		t.Fatal(err)
	}
	client := httpfixture.New(t, server, "admin", nil).WithCertificate(cert)
	client.Headers = map[string]string{"Authorization": "Bearer " + node.Credential}
	h.Inference = &wire.InferenceObservation{GPUs: []wire.NVIDIAGPU{}, ObservedAt: h.Facts.ObservedAt}
	cost, err := recorder.Record(func() {
		client.Call("POST", "/agent/v1/nodes/"+node.NodeID+"/heartbeat", h, 200, "")
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("cold mTLS HTTPS heartbeat: statements=%d rows=%d blocks=%d", len(cost.Statements), cost.Rows(), cost.Blocks())
	if len(cost.Statements) == 0 || len(cost.Statements) > wire.CallHeartbeat.Cost().Statements {
		t.Errorf("recorded %d statements for a ceiling of %d", len(cost.Statements), wire.CallHeartbeat.Cost().Statements)
	}
}
