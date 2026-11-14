package nodes

import (
	"context"
	"crypto/subtle"
	"crypto/x509"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// CertificateAuthority signs node certificates (ADR 0050). Control has one; a gateway, which
// only verifies, has none and never enrolls.
type CertificateAuthority interface {
	Issue(request *x509.CertificateRequest, scope wire.Scope, node string) (string, time.Time, error)
}

// certificatePins are the keys a node's connections may use.
type certificatePins struct{ current, previous []byte }

// pinned reports that the node has a certificate key, so its calls need the certificate.
func (p certificatePins) pinned() bool { return len(p.current) != 0 }

func (p certificatePins) holds(key []byte) bool {
	return subtle.ConstantTimeCompare(p.current, key) == 1 || subtle.ConstantTimeCompare(p.previous, key) == 1
}

// readPins reads a node's pins, from its view when one is held. A node that does not exist has
// none, and the credential check that follows refuses it. Only PostgreSQL stores pins.
func (s *Service) readPins(ctx context.Context, id string) (certificatePins, error) {
	if view, cached := s.views.lookup(id, time.Now()); cached {
		return view.pins, nil
	}
	store, e := s.repository()
	if e != nil {
		return certificatePins{}, e
	}
	var pins certificatePins
	if store.Dialect() != transaction.PostgreSQL {
		return pins, nil
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	e = store.QueryRow(bounded, transaction.SQL(transaction.PostgreSQL, `SELECT certificate_key_sha256,previous_certificate_key_sha256 FROM managed_nodes WHERE id=$1`, id)).Scan(&pins.current, &pins.previous)
	if errors.Is(e, sql.ErrNoRows) {
		return certificatePins{}, nil
	}
	if e != nil {
		return certificatePins{}, nodeRepositoryError(e)
	}
	return pins, nil
}

// CheckPeer decides whether a connection may act for a node, before any work for the call. It
// is half of the node's authentication; the caller still checks the credential. A certificate
// names one node and must carry a pinned key, and a node with a pinned key needs its certificate.
func (s *Service) CheckPeer(ctx context.Context, id string, peer agentidentity.Peer) error {
	if e := checkPeerNamesNode(id, peer); e != nil {
		return e
	}
	pins, e := s.readPins(ctx, id)
	if e != nil {
		return e
	}
	switch {
	case !pins.pinned():
		return nil
	case !peer.Presented:
		return fail("AGENT_CERTIFICATE_REQUIRED")
	case !pins.holds(peer.Key):
		return fail("AGENT_CERTIFICATE_SUPERSEDED")
	}
	return nil
}

// RenewCertificate issues a certificate for the key of a request. With a certificate on the
// connection, its key must be pinned and a new key is rotated in. Without one, the credential
// must come with a request signed by a pinned key, so a leaked credential alone cannot get a
// certificate: that is how a node recovers from an outage longer than its certificate's life.
// A node from before certificates has no pin, and takes its first one the same way.
func (s *Service) RenewCertificate(ctx context.Context, id, credential, request string, input wire.CertificateRenewal, peer agentidentity.Peer) (wire.IssuedCertificate, error) {
	if s.Certificates == nil {
		return wire.IssuedCertificate{}, fail("AGENT_IDENTITY_UNAVAILABLE")
	}
	csr, e := wire.ParseCertificateRequest(input.CertificateRequest)
	if e != nil {
		return wire.IssuedCertificate{}, fail(e.Error())
	}
	requested, e := wire.KeyDigest(csr.PublicKey)
	if e != nil {
		return wire.IssuedCertificate{}, fail(e.Error())
	}
	if e := checkPeerNamesNode(id, peer); e != nil {
		return wire.IssuedCertificate{}, e
	}
	var out wire.IssuedCertificate
	e = s.machineWithin(ctx, id, credential, func(ctx context.Context, q transaction.Executor, n Node, active bool, _ pgx.Tx) error {
		if !active {
			return fail("ACCESS_DENIED")
		}
		pins, e := lockPins(ctx, q, id)
		if e != nil {
			return e
		}
		next, action, e := renewedPins(pins, requested, peer)
		if e != nil {
			return e
		}
		certificate, notAfter, e := s.Certificates.Issue(csr, wire.LocalScope(), id)
		if e != nil {
			return fail(e.Error())
		}
		if _, e = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE managed_nodes SET certificate_key_sha256=$1,previous_certificate_key_sha256=$2,certificate_not_after=$3 WHERE id=$4`, next.current, next.previous, notAfter, id)); e != nil {
			return e
		}
		if _, e = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),NULL,$2,$3,'system',$4,'success','node',$5,NULL,$6)`, uuid.NewString(), n.TenantID, n.WorkspaceID, action, id, request)); e != nil {
			return e
		}
		out.Certificate = certificate
		return nil
	})
	if e != nil {
		return wire.IssuedCertificate{}, e
	}
	s.views.drop(id)
	return out, nil
}

// checkPeerNamesNode refuses a certificate that names another node or has run out. A connection
// without one passes: what that means depends on the node's pins.
func checkPeerNamesNode(id string, peer agentidentity.Peer) error {
	if !peer.Presented {
		return nil
	}
	if peer.Malformed || peer.Node != id || peer.Scope != wire.LocalScope() {
		return fail("AGENT_UNAUTHORIZED")
	}
	if !time.Now().Before(peer.NotAfter) {
		return fail("AGENT_CERTIFICATE_EXPIRED")
	}
	return nil
}

// Audit actions of a renewal.
const (
	actionCertificateRenewed   = "node.certificate_renewed"
	actionCertificateRecovered = "node.certificate_recovered"
)

// renewedPins applies a renewal to a node's pins and names what happened. A key already being
// rotated in may be asked for again: the reply to the first request can be lost.
func renewedPins(pins certificatePins, requested []byte, peer agentidentity.Peer) (certificatePins, string, error) {
	if peer.Presented {
		if pins.pinned() && !pins.holds(peer.Key) {
			return certificatePins{}, "", fail("AGENT_CERTIFICATE_SUPERSEDED")
		}
		previous := pins.previous
		if subtle.ConstantTimeCompare(pins.current, peer.Key) == 1 {
			previous = pins.current
		}
		if subtle.ConstantTimeCompare(previous, requested) == 1 {
			previous = nil
		}
		return certificatePins{current: requested, previous: previous}, actionCertificateRenewed, nil
	}
	if pins.pinned() && !pins.holds(requested) {
		return certificatePins{}, "", fail("AGENT_UNAUTHORIZED")
	}
	return certificatePins{current: requested}, actionCertificateRecovered, nil
}

// ConfirmCertificate ends a rotation from a connection that uses the new key, and the old key
// stops working here. Confirming again changes nothing, so a lost reply can be retried.
func (s *Service) ConfirmCertificate(ctx context.Context, id, credential string, peer agentidentity.Peer) (wire.CertificateConfirmed, error) {
	if !security.ValidToken(credential) {
		return wire.CertificateConfirmed{}, fail("AGENT_UNAUTHORIZED")
	}
	if !peer.Presented {
		return wire.CertificateConfirmed{}, fail("AGENT_CERTIFICATE_REQUIRED")
	}
	if e := checkPeerNamesNode(id, peer); e != nil {
		return wire.CertificateConfirmed{}, e
	}
	e := s.machineWithin(ctx, id, credential, func(ctx context.Context, q transaction.Executor, _ Node, _ bool, _ pgx.Tx) error {
		pins, e := lockPins(ctx, q, id)
		if e != nil {
			return e
		}
		if subtle.ConstantTimeCompare(pins.current, peer.Key) != 1 {
			return fail("AGENT_CERTIFICATE_SUPERSEDED")
		}
		if pins.previous == nil {
			return nil
		}
		_, e = q.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE managed_nodes SET previous_certificate_key_sha256=NULL WHERE id=$1`, id))
		return e
	})
	if e != nil {
		return wire.CertificateConfirmed{}, e
	}
	s.views.drop(id)
	return wire.CertificateConfirmed{Confirmed: true}, nil
}

// lockPins reads the pins under the node lock machineWithin holds.
func lockPins(ctx context.Context, q transaction.Executor, id string) (certificatePins, error) {
	var pins certificatePins
	e := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT certificate_key_sha256,previous_certificate_key_sha256 FROM managed_nodes WHERE id=$1 FOR UPDATE`, id)).Scan(&pins.current, &pins.previous)
	return pins, e
}
