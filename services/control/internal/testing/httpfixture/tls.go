package httpfixture

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
	"time"
)

// These are fixed synthetic test identities, not deployment configuration.
const (
	PortalOrigin = "https://portal.example.invalid"
	AdminOrigin  = "https://admin.example.invalid"

	fixturePortalHostname      = "portal.example.invalid"
	fixtureAdminHostname       = "admin.example.invalid"
	fixtureCertificateLifetime = time.Hour
	fixtureCertificateSkew     = time.Minute
	fixtureDialTimeout         = 2 * time.Second
	fixtureRequestTimeout      = 30 * time.Second
	fixtureMaximumRedirects    = 10
)

func fixtureOrigin(audience string) (string, error) {
	switch audience {
	case "portal":
		return PortalOrigin, nil
	case "admin":
		return AdminOrigin, nil
	default:
		return "", errors.New("HTTP_FIXTURE_AUDIENCE_INVALID")
	}
}

// NewTLSServer installs its synthetic self-signed CA/server certificate before
// starting the real loopback TLS listener. No shared trust store or running TLS
// configuration is changed. The returned server retains its actual loopback URL
// for ownership inventory; clients use the separately declared DNS origins.
func NewTLSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	return newTLSServer(t, handler, nil)
}

// NewTLSServerAskingClients is NewTLSServer on a listener that asks for and verifies client
// certificates of clientCAs, as an agent entrance does.
func NewTLSServerAskingClients(t *testing.T, handler http.Handler, clientCAs *x509.CertPool) *httptest.Server {
	t.Helper()
	return newTLSServer(t, handler, clientCAs)
}

func newTLSServer(t *testing.T, handler http.Handler, clientCAs *x509.CertPool) *httptest.Server {
	t.Helper()
	if handler == nil {
		t.Fatal("HTTP_FIXTURE_HANDLER_REQUIRED")
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal("HTTP_FIXTURE_KEY_GENERATION_FAILED")
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		t.Fatal("HTTP_FIXTURE_CERTIFICATE_GENERATION_FAILED")
	}
	serial.Add(serial, big.NewInt(1))
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, NotBefore: now.Add(-fixtureCertificateSkew), NotAfter: now.Add(fixtureCertificateLifetime), DNSNames: []string{fixturePortalHostname, fixtureAdminHostname}, BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal("HTTP_FIXTURE_CERTIFICATE_GENERATION_FAILED")
	}
	server := httptest.NewUnstartedServer(handler)
	t.Cleanup(server.Close)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	if clientCAs != nil {
		server.TLS.ClientAuth, server.TLS.ClientCAs = tls.VerifyClientCertIfGiven, clientCAs
	}
	server.StartTLS()
	client, e := fixtureTLSClient(server, []string{PortalOrigin, AdminOrigin})
	if e != nil {
		t.Fatal("HTTP_FIXTURE_CLIENT_CONFIGURATION_INVALID")
	}
	// Configure only the fixture-owned client before returning it to any caller.
	// The running server's TLS configuration and handler are never patched.
	*server.Client() = *client
	t.Cleanup(server.Client().CloseIdleConnections)
	return server
}

// fixtureTLSClient admits only an explicit subset of the fixed fixture origins.
// Its map is immutable and private to this client; no DNS lookup, ambient proxy,
// global registry or alternate socket can expand the selected target.
func fixtureTLSClient(server *httptest.Server, origins []string) (*http.Client, error) {
	bad := errors.New("HTTP_FIXTURE_TLS_CONFIGURATION_INVALID")
	if server == nil || server.Listener == nil || server.TLS == nil || server.Certificate() == nil || len(origins) == 0 {
		return nil, bad
	}
	parsed, e := url.Parse(server.URL)
	address, parseError := netip.ParseAddrPort(server.Listener.Addr().String())
	if e != nil || parseError != nil || !address.Addr().IsLoopback() || address.Addr().Zone() != "" || address.Port() == 0 || parsed.Scheme != "https" || parsed.Host != address.String() || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, bad
	}
	certificate := server.Certificate()
	if time.Now().Before(certificate.NotBefore) || time.Now().After(certificate.NotAfter) {
		return nil, bad
	}
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		if origin != PortalOrigin && origin != AdminOrigin {
			return nil, bad
		}
		u, _ := url.Parse(origin)
		if certificate.VerifyHostname(u.Hostname()) != nil {
			return nil, bad
		}
		allowed[net.JoinHostPort(u.Hostname(), "443")] = true
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	transport := &http.Transport{
		Proxy:                 nil,
		TLSClientConfig:       &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   fixtureDialTimeout,
		ResponseHeaderTimeout: fixtureRequestTimeout,
		IdleConnTimeout:       fixtureRequestTimeout,
		MaxIdleConns:          len(allowed), MaxIdleConnsPerHost: 1,
		DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
			if network != "tcp" || !allowed[target] {
				return nil, errors.New("HTTP_FIXTURE_DIAL_TARGET_REJECTED")
			}
			return (&net.Dialer{Timeout: fixtureDialTimeout}).DialContext(ctx, "tcp", address.String())
		},
	}
	client := &http.Client{Transport: transport, Timeout: fixtureRequestTimeout}
	client.CheckRedirect = func(request *http.Request, previous []*http.Request) error {
		if len(previous) >= fixtureMaximumRedirects {
			return errors.New("HTTP_FIXTURE_REDIRECT_LIMIT")
		}
		if request.URL == nil || request.URL.Scheme != "https" || request.URL.User != nil || !allowed[net.JoinHostPort(request.URL.Hostname(), "443")] || request.URL.Port() != "" {
			return errors.New("HTTP_FIXTURE_REDIRECT_TARGET_REJECTED")
		}
		return nil
	}
	return client, nil
}
