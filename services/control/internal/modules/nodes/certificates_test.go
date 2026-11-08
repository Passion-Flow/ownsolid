package nodes_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/agentca"
)

// nodeKey is a private key and what control would see of it on a connection.
type nodeKey struct {
	pem, request string
	digest       []byte
}

func newNodeKey(t *testing.T) nodeKey {
	t.Helper()
	text, e := wire.NewCertificateKey()
	if e != nil {
		t.Fatal(e)
	}
	request, e := wire.NewCertificateRequest(text)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := wire.ParseCertificateRequest(request)
	if e != nil {
		t.Fatal(e)
	}
	digest, e := wire.KeyDigest(parsed.PublicKey)
	if e != nil {
		t.Fatal(e)
	}
	return nodeKey{pem: text, request: request, digest: digest}
}

func (k nodeKey) peer(node string) agentidentity.Peer {
	return agentidentity.Peer{Presented: true, Scope: wire.LocalScope(), Node: node, Key: k.digest, NotAfter: time.Now().Add(time.Hour)}
}

func code(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

// enrollWithKey enrolls a node holding key through a service with a CA.
func enrollWithKey(t *testing.T, s *nodes.Service, a identity.Auth, name string, key nodeKey) wire.Enrolled {
	t.Helper()
	token, e := s.Mint(context.Background(), a, a.Me.Workspace.ID, name, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	enrolled, e := s.Enroll(context.Background(), wire.Enrollment{JoinToken: token.Token, CertificateRequest: key.request, Handshake: handshake()}, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return enrolled
}

func TestEnrollmentWithACAIssuesACertificateAndPinsItsKey(t *testing.T) {
	s, a := setup(t)
	s.Certificates = agentca.Authority(t)
	ctx := context.Background()
	token, e := s.Mint(ctx, a, a.Me.Workspace.ID, "No request", uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, Handshake: handshake()}, uuid.NewString()); code(e) != "AGENT_CERTIFICATE_REQUEST_INVALID" {
		t.Fatalf("enrollment without a request: %v", e)
	}
	key := newNodeKey(t)
	enrolled, e := s.Enroll(ctx, wire.Enrollment{JoinToken: token.Token, CertificateRequest: key.request, Handshake: handshake()}, uuid.NewString())
	if e != nil {
		t.Fatal("the join token survives a refused request", e)
	}
	if _, e := wire.CheckIssuedCertificate(enrolled.Certificate, key.pem, enrolled.NodeID); e != nil {
		t.Fatal(e)
	}
	if e := s.CheckPeer(ctx, enrolled.NodeID, key.peer(enrolled.NodeID)); e != nil {
		t.Fatal("its own certificate", e)
	}
}

func TestPinnedNodeNeedsItsOwnCurrentCertificate(t *testing.T) {
	s, a := setup(t)
	s.Certificates = agentca.Authority(t)
	ctx := context.Background()
	key := newNodeKey(t)
	enrolled := enrollWithKey(t, s, a, "Pinned", key)
	node := enrolled.NodeID
	stolen := newNodeKey(t)
	expired := key.peer(node)
	expired.NotAfter = time.Now().Add(-time.Second)
	other := key.peer(uuid.NewString())
	foreign := key.peer(node)
	foreign.Scope = wire.Scope{SiteID: "elsewhere", CellID: wire.DefaultCellID}
	for name, check := range map[string]struct {
		peer agentidentity.Peer
		want string
	}{
		"its own":       {key.peer(node), ""},
		"none":          {agentidentity.Peer{}, "AGENT_CERTIFICATE_REQUIRED"},
		"expired":       {expired, "AGENT_CERTIFICATE_EXPIRED"},
		"another key":   {stolen.peer(node), "AGENT_CERTIFICATE_SUPERSEDED"},
		"another node":  {other, "AGENT_UNAUTHORIZED"},
		"another site":  {foreign, "AGENT_UNAUTHORIZED"},
		"names no node": {agentidentity.Peer{Presented: true, Malformed: true}, "AGENT_UNAUTHORIZED"},
	} {
		if e := s.CheckPeer(ctx, node, check.peer); code(e) != check.want {
			t.Errorf("%s: got %v, want %s", name, e, check.want)
		}
	}
}

func TestLegacyNodeWithoutACertificateStillWorks(t *testing.T) {
	s, a := setup(t)
	legacy := enroll(t, s, a, "Legacy node")
	if e := s.CheckPeer(context.Background(), legacy.NodeID, agentidentity.Peer{}); e != nil {
		t.Fatal(e)
	}
}

func TestRotationMovesToTheNewKeyAndConfirmationEndsTheOld(t *testing.T) {
	s, a := setup(t)
	s.Certificates = agentca.Authority(t)
	ctx := context.Background()
	first, second := newNodeKey(t), newNodeKey(t)
	enrolled := enrollWithKey(t, s, a, "Rotating", first)
	node := enrolled.NodeID
	issued, e := s.RenewCertificate(ctx, node, enrolled.Credential, uuid.NewString(), wire.CertificateRenewal{CertificateRequest: second.request}, first.peer(node))
	if e != nil {
		t.Fatal(e)
	}
	if _, e := wire.CheckIssuedCertificate(issued.Certificate, second.pem, node); e != nil {
		t.Fatal(e)
	}
	for name, key := range map[string]nodeKey{"old": first, "new": second} {
		if e := s.CheckPeer(ctx, node, key.peer(node)); e != nil {
			t.Fatalf("%s key during the rotation: %v", name, e)
		}
	}
	if _, e := s.ConfirmCertificate(ctx, node, enrolled.Credential, first.peer(node)); code(e) != "AGENT_CERTIFICATE_SUPERSEDED" {
		t.Fatalf("the old key may not end the rotation: %v", e)
	}
	if _, e := s.ConfirmCertificate(ctx, node, enrolled.Credential, second.peer(node)); e != nil {
		t.Fatal(e)
	}
	if e := s.CheckPeer(ctx, node, first.peer(node)); code(e) != "AGENT_CERTIFICATE_SUPERSEDED" {
		t.Fatalf("the old key after confirmation: %v", e)
	}
	if _, e := s.ConfirmCertificate(ctx, node, enrolled.Credential, second.peer(node)); e != nil {
		t.Fatal("confirming again changes nothing", e)
	}
	third := newNodeKey(t)
	if _, e := s.RenewCertificate(ctx, node, enrolled.Credential, uuid.NewString(), wire.CertificateRenewal{CertificateRequest: third.request}, first.peer(node)); code(e) != "AGENT_CERTIFICATE_SUPERSEDED" {
		t.Fatalf("a retired key may not renew: %v", e)
	}
}

func TestLostRenewalReplyCanBeRetried(t *testing.T) {
	s, a := setup(t)
	s.Certificates = agentca.Authority(t)
	ctx := context.Background()
	first, second := newNodeKey(t), newNodeKey(t)
	enrolled := enrollWithKey(t, s, a, "Lost reply", first)
	node := enrolled.NodeID
	for attempt := range 3 {
		if _, e := s.RenewCertificate(ctx, node, enrolled.Credential, uuid.NewString(), wire.CertificateRenewal{CertificateRequest: second.request}, first.peer(node)); e != nil {
			t.Fatalf("attempt %d: %v", attempt, e)
		}
	}
	if e := s.CheckPeer(ctx, node, first.peer(node)); e != nil {
		t.Fatal("the old key still works until the node confirms", e)
	}
	if _, e := s.ConfirmCertificate(ctx, node, enrolled.Credential, second.peer(node)); e != nil {
		t.Fatal(e)
	}
}

func TestRecoveryNeedsThePinnedKeyNotJustTheCredential(t *testing.T) {
	s, a := setup(t)
	s.Certificates = agentca.Authority(t)
	ctx := context.Background()
	key := newNodeKey(t)
	enrolled := enrollWithKey(t, s, a, "Outage", key)
	node := enrolled.NodeID
	thief := newNodeKey(t)
	if _, e := s.RenewCertificate(ctx, node, enrolled.Credential, uuid.NewString(), wire.CertificateRenewal{CertificateRequest: thief.request}, agentidentity.Peer{}); code(e) != "AGENT_UNAUTHORIZED" {
		t.Fatalf("a leaked credential must not get a certificate: %v", e)
	}
	issued, e := s.RenewCertificate(ctx, node, enrolled.Credential, uuid.NewString(), wire.CertificateRenewal{CertificateRequest: key.request}, agentidentity.Peer{})
	if e != nil {
		t.Fatal("the node itself recovers", e)
	}
	if _, e := wire.CheckIssuedCertificate(issued.Certificate, key.pem, node); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RenewCertificate(ctx, node, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", uuid.NewString(), wire.CertificateRenewal{CertificateRequest: key.request}, agentidentity.Peer{}); code(e) != "AGENT_UNAUTHORIZED" {
		t.Fatalf("a wrong credential: %v", e)
	}
}

func TestLegacyNodeTakesItsFirstCertificateAndIsThenPinned(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	legacy := enroll(t, s, a, "Before certificates")
	s.Certificates = agentca.Authority(t)
	key := newNodeKey(t)
	if _, e := s.RenewCertificate(ctx, legacy.NodeID, legacy.Credential, uuid.NewString(), wire.CertificateRenewal{CertificateRequest: key.request}, agentidentity.Peer{}); e != nil {
		t.Fatal(e)
	}
	if e := s.CheckPeer(ctx, legacy.NodeID, agentidentity.Peer{}); code(e) != "AGENT_CERTIFICATE_REQUIRED" {
		t.Fatalf("the credential alone no longer works once pinned: %v", e)
	}
}

func TestRenewalIsRefusedWithoutAnAuthorityOrAValidRequest(t *testing.T) {
	s, a := setup(t)
	ctx := context.Background()
	legacy := enroll(t, s, a, "No authority")
	key := newNodeKey(t)
	if _, e := s.RenewCertificate(ctx, legacy.NodeID, legacy.Credential, uuid.NewString(), wire.CertificateRenewal{CertificateRequest: key.request}, agentidentity.Peer{}); code(e) != "AGENT_IDENTITY_UNAVAILABLE" {
		t.Fatalf("no authority: %v", e)
	}
	s.Certificates = agentca.Authority(t)
	if _, e := s.RenewCertificate(ctx, legacy.NodeID, legacy.Credential, uuid.NewString(), wire.CertificateRenewal{CertificateRequest: "nonsense"}, agentidentity.Peer{}); code(e) != "AGENT_CERTIFICATE_REQUEST_INVALID" {
		t.Fatalf("a bad request: %v", e)
	}
}

func TestRemovedNodeCannotRenew(t *testing.T) {
	s, a := setup(t)
	s.Certificates = agentca.Authority(t)
	ctx := context.Background()
	key := newNodeKey(t)
	enrolled := enrollWithKey(t, s, a, "Removed", key)
	if _, e := s.DB.Exec(ctx, "UPDATE managed_nodes SET status='removed',credential_hash=NULL WHERE id=$1", enrolled.NodeID); e != nil {
		t.Fatal(e)
	}
	next := newNodeKey(t)
	if _, e := s.RenewCertificate(ctx, enrolled.NodeID, enrolled.Credential, uuid.NewString(), wire.CertificateRenewal{CertificateRequest: next.request}, key.peer(enrolled.NodeID)); code(e) != "AGENT_UNAUTHORIZED" {
		t.Fatalf("a removed node: %v", e)
	}
}
