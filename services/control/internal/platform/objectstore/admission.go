package objectstore

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const tlsCertificateRequiredAlert = 116
const tlsHandshakeFailureAlert = 40
const tlsBadCertificateAlert = 42
const tlsCertificateUnknownAlert = 46
const tlsUnknownCAAlert = 48
const managementClientAccepted = 0
const managementClientAbsent = 1
const managementClientUntrusted = 2

// A successful certificate-authenticated HTTP/2 handshake is the positive
// control. Only a TLS certificate-required/handshake-failure alert without that
// certificate is a negative proof; timeout/reset/unavailability is not proof.
func managementHandshake(ctx context.Context, p config.ObjectStorageManagementProbe, mode int, seconds int) error {
	ca, cert, key, e := p.Material()
	if e != nil {
		return e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return failure("OBJECT_MANAGEMENT_CA_INVALID")
	}
	pair, e := tls.X509KeyPair(cert, key)
	if e != nil {
		return failure("OBJECT_MANAGEMENT_CERTIFICATE_INVALID")
	}
	host, port, e := net.SplitHostPort(p.Authority)
	if e != nil {
		return failure("OBJECT_MANAGEMENT_PROFILE_INVALID")
	}
	settings := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: host, NextProtos: []string{"h2"}}
	if mode == managementClientAccepted {
		settings.Certificates = []tls.Certificate{pair}
	} else if mode == managementClientUntrusted {
		untrusted, e := untrustedManagementCertificate(seconds)
		if e != nil {
			return e
		}
		// Force this synthetic untrusted certificate even if the peer advertises
		// acceptable CA names. No server verification is disabled.
		settings.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &untrusted, nil }
	}
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: time.Duration(seconds) * time.Second}, Config: settings}
	var last error
	for _, ip := range p.DialAddresses {
		conn, e := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
		if e != nil {
			last = e
			continue
		}
		func() {
			defer conn.Close()
			if e = conn.SetDeadline(time.Now().Add(time.Duration(seconds) * time.Second)); e != nil {
				last = e
				return
			}
			tlsConn, ok := conn.(*tls.Conn)
			if !ok || tlsConn.ConnectionState().NegotiatedProtocol != "h2" {
				last = failure("OBJECT_MANAGEMENT_PROTOCOL_UNVERIFIED")
				return
			}
			// HTTP/2 client preface plus an empty SETTINGS frame invokes no RPC method.
			preface := append([]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"), []byte{0, 0, 0, 4, 0, 0, 0, 0, 0}...)
			if _, e = conn.Write(preface); e != nil {
				last = e
				return
			}
			var frame [9]byte
			if _, e = io.ReadFull(conn, frame[:]); e != nil {
				last = e
				return
			}
			if frame[3] != 4 || frame[5] != 0 || frame[6] != 0 || frame[7] != 0 || frame[8] != 0 {
				last = failure("OBJECT_MANAGEMENT_PROTOCOL_UNVERIFIED")
				return
			}
			last = nil
		}()
		if last == nil {
			return nil
		}
		if mode != managementClientAccepted {
			if managementClientRejected(last, mode) {
				return last
			}
		}
	}
	if last == nil {
		return failure("OBJECT_MANAGEMENT_PROFILE_UNVERIFIED")
	}
	return last
}
func (s *S3) verifyManagement(ctx context.Context) error {
	for _, target := range s.configuration.Management {
		for _, ip := range target.DialAddresses {
			p := target
			p.DialAddresses = []string{ip}
			bounded, cancel := context.WithTimeout(ctx, time.Duration(s.configuration.OperationSeconds)*time.Second)
			e := managementHandshake(bounded, p, managementClientAccepted, s.configuration.OperationSeconds)
			cancel()
			if e != nil {
				return failure("OBJECT_MANAGEMENT_PROFILE_UNVERIFIED")
			}
			bounded, cancel = context.WithTimeout(ctx, time.Duration(s.configuration.OperationSeconds)*time.Second)
			e = managementHandshake(bounded, p, managementClientAbsent, s.configuration.OperationSeconds)
			cancel()
			if e == nil {
				return failure("OBJECT_MANAGEMENT_MTLS_UNENFORCED")
			}
			if !managementCertificateDenied(e) {
				return failure("OBJECT_MANAGEMENT_PROFILE_UNVERIFIED")
			}
			bounded, cancel = context.WithTimeout(ctx, time.Duration(s.configuration.OperationSeconds)*time.Second)
			e = managementHandshake(bounded, p, managementClientUntrusted, s.configuration.OperationSeconds)
			cancel()
			if e == nil {
				return failure("OBJECT_MANAGEMENT_MTLS_UNENFORCED")
			}
			if !managementClientRejected(e, managementClientUntrusted) {
				return failure("OBJECT_MANAGEMENT_PROFILE_UNVERIFIED")
			}

		}
	}
	return nil
}
func (s *S3) negativeRequest(ctx context.Context, path string, wrongSignature bool) error {
	endpoint, e := url.Parse(s.configuration.Endpoint)
	if e != nil {
		return failure("OBJECT_CONFIGURATION_INVALID")
	}
	endpoint.Path = path
	req, e := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if e != nil {
		return failure("OBJECT_PROFILE_UNVERIFIED")
	}
	if wrongSignature {
		credentials, e := s.client.Options().Credentials.Retrieve(ctx)
		if e != nil {
			return failure("OBJECT_PROFILE_UNVERIFIED")
		}
		credentials.SecretAccessKey = "SYNTHETIC_INVALID_SIGNATURE_ONLY"
		empty := sha256.Sum256(nil)
		if e = v4.NewSigner().SignHTTP(ctx, credentials, req, hex.EncodeToString(empty[:]), "s3", s.configuration.Region, time.Now().UTC()); e != nil {
			return failure("OBJECT_PROFILE_UNVERIFIED")
		}
	}
	client := pinnedHTTP{client: &http.Client{Transport: s.http, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, scheme: endpoint.Scheme, host: endpoint.Host}
	response, e := client.Do(req)
	if e != nil {
		return failure("OBJECT_PROFILE_UNVERIFIED")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusUnauthorized {
		return failure("OBJECT_BACKEND_AUTH_UNENFORCED")
	}
	return nil
}
func (s *S3) PrivateObject(ctx context.Context, r Ref) error {
	key, e := r.Key()
	if e != nil {
		return e
	}
	ctx, done, e := s.operation(ctx)
	if e != nil {
		return e
	}
	defer done()
	path := "/" + s.configuration.Bucket + "/" + key
	if e = s.negativeRequest(ctx, path, false); e != nil {
		return e
	}
	return s.negativeRequest(ctx, path, true)
}
func (s *S3) privateBucket(ctx context.Context) error {
	path := "/" + s.configuration.Bucket
	if e := s.negativeRequest(ctx, path, false); e != nil {
		return e
	}
	return s.negativeRequest(ctx, path, true)
}

var _ aws.CredentialsProvider = staticCredentials{}

// TCP TLS in the pinned Go1.27 toolchain wraps its private alert type in a
// remote net.OpError. QUIC instead exports AlertError. Match only those reviewed
// certificate-required alert forms, never arbitrary error text or timeouts.
func managementCertificateDenied(e error) bool {
	var alert tls.AlertError
	if errors.As(e, &alert) {
		return uint8(alert) == tlsCertificateRequiredAlert || uint8(alert) == tlsHandshakeFailureAlert
	}
	var operation *net.OpError
	if !errors.As(e, &operation) || operation.Op != "remote error" || operation.Err == nil {
		return false
	}
	switch operation.Err.Error() {
	case "tls: certificate required", "tls: handshake failure":
		return true
	}
	return false
}

func managementClientRejected(e error, mode int) bool {
	if mode == managementClientAbsent {
		return managementCertificateDenied(e)
	}
	var alert tls.AlertError
	if errors.As(e, &alert) {
		return uint8(alert) == tlsUnknownCAAlert || uint8(alert) == tlsBadCertificateAlert || uint8(alert) == tlsCertificateUnknownAlert
	}
	var operation *net.OpError
	if !errors.As(e, &operation) || operation.Op != "remote error" || operation.Err == nil {
		return false
	}
	switch operation.Err.Error() {
	case "tls: unknown certificate authority", "tls: bad certificate", "tls: unknown certificate":
		return true
	}
	return false
}
func untrustedManagementCertificate(seconds int) (tls.Certificate, error) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return tls.Certificate{}, failure("OBJECT_MANAGEMENT_PROFILE_UNVERIFIED")
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return tls.Certificate{}, failure("OBJECT_MANAGEMENT_PROFILE_UNVERIFIED")
	}
	serial.Add(serial, big.NewInt(1))
	now := time.Now()
	certificate := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "projectone-untrusted-admission-probe"}, NotBefore: now.Add(-time.Duration(seconds) * time.Second), NotAfter: now.Add(2 * time.Duration(seconds) * time.Second), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	raw, e := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if e != nil {
		return tls.Certificate{}, failure("OBJECT_MANAGEMENT_PROFILE_UNVERIFIED")
	}
	return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}, nil
}
