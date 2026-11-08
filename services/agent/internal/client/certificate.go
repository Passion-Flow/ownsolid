package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// certificateCodes are the refusals about a connection's certificate. They say to get or use
// another certificate, not that the node has been revoked (ADR 0050).
var certificateCodes = map[string]bool{
	"AGENT_CERTIFICATE_REQUIRED":   true,
	"AGENT_CERTIFICATE_EXPIRED":    true,
	"AGENT_CERTIFICATE_SUPERSEDED": true,
}

// CertificateRefused reports whether e is such a refusal.
func CertificateRefused(e error) bool {
	var p *Problem
	return errors.As(e, &p) && certificateCodes[p.Code]
}

// UseCertificate presents this certificate on new connections from now on.
func (c *Client) UseCertificate(certificate, privateKey string) error {
	pair, e := tls.X509KeyPair([]byte(certificate), []byte(privateKey))
	if e != nil {
		return errors.New("CERTIFICATE_STATE_INVALID")
	}
	leaf, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		return errors.New("CERTIFICATE_STATE_INVALID")
	}
	pair.Leaf = leaf
	c.certificate.Store(&pair)
	c.closeIdle()
	return nil
}

// presented is the certificate a handshake sends. Near its end none is sent, and an empty
// certificate means that: control would refuse an expired one during the handshake, before
// the node could say anything, and without one it can still ask for a new certificate.
func (c *Client) presented(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	current := c.certificate.Load()
	if current == nil || !time.Now().Add(wire.ExpirySafety).Before(current.Leaf.NotAfter) {
		return &tls.Certificate{}, nil
	}
	return current, nil
}

func (c *Client) closeIdle() {
	c.HTTP.CloseIdleConnections()
	if c.preparationHTTP != nil {
		c.preparationHTTP.CloseIdleConnections()
	}
}

// RenewCertificate asks for a certificate for the key that signed request.
func (c *Client) RenewCertificate(ctx context.Context, node, request string) (wire.IssuedCertificate, error) {
	var out wire.IssuedCertificate
	e := c.Post(ctx, "/agent/v1/nodes/"+node+"/"+wire.CertificatesPath, wire.CertificateRenewal{CertificateRequest: request}, &out)
	return out, e
}

// ConfirmCertificate tells control the old key of a rotation is finished with.
func (c *Client) ConfirmCertificate(ctx context.Context, node string) error {
	return c.Post(ctx, "/agent/v1/nodes/"+node+"/"+wire.CertificatesPath+"/"+wire.CertificateConfirmationsPath, struct{}{}, nil)
}
