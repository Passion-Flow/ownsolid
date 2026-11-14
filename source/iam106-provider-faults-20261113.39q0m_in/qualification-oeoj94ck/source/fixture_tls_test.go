package boundary

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureAuthority struct {
	cert  *x509.Certificate
	key   *ecdsa.PrivateKey
	roots *x509.CertPool
}

func testAuthority(t *testing.T) fixtureAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("private CA generation failed")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal("private CA serial failed")
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "IAM106 Private Protocol CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal("private CA signing failed")
	}
	cert, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal("private CA parsing failed")
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return fixtureAuthority{cert: cert, key: key, roots: roots}
}
func (a fixtureAuthority) leaf(t *testing.T, folder, name string) (tls.Certificate, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("private TLS key generation failed")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal("private TLS serial failed")
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal("private TLS signing failed")
	}
	priv, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal("private TLS key encoding failed")
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	encodedKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv})
	certPath, keyPath := filepath.Join(folder, name+"-certificate.pem"), filepath.Join(folder, name+"-key.pem")
	privateWrite(t, certPath, cert)
	privateWrite(t, keyPath, encodedKey)
	pair, err := tls.X509KeyPair(cert, encodedKey)
	clear(encodedKey)
	clear(priv)
	if err != nil {
		t.Fatal("private TLS pair invalid")
	}
	return pair, certPath, keyPath
}
func privateWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal("private fixture file write failed")
	}
}

type approvedTransport struct {
	tokenRequests atomic.Int64
	record        func(...string)
	inner         *http.Transport
	destinations  map[string]string
	maximum       int
}

func (t *approvedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.record != nil {
		for _, value := range r.Header.Values("Authorization") {
			t.record(value)
			if _, credential, ok := strings.Cut(value, " "); ok {
				t.record(credential)
			}
		}
		for _, cookie := range r.Cookies() {
			t.record(cookie.Value)
		}
	}
	if r.URL != nil && r.URL.Path == "/oauth/token" {
		t.tokenRequests.Add(1)
	}
	if r.URL == nil || r.URL.Scheme != "https" || r.URL.User != nil || t.destinations[r.URL.Host] == "" {
		return nil, errors.New("PRIVATE_HTTPS_DESTINATION_REFUSED")
	}
	response, err := t.inner.RoundTrip(r)
	if err != nil {
		return nil, errors.New("PRIVATE_HTTPS_TRANSPORT_FAILED")
	}
	defer response.Body.Close()
	if t.record != nil {
		for _, cookie := range response.Cookies() {
			t.record(cookie.Value)
		}
	}
	if response.Header.Get("Content-Encoding") != "" {
		return nil, errors.New("PRIVATE_HTTPS_ENCODING_REFUSED")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(t.maximum)+1))
	if err != nil || len(raw) > t.maximum {
		return nil, errors.New("PRIVATE_HTTPS_RESPONSE_BUDGET_REFUSED")
	}
	if t.record != nil && r.URL.Path == "/oauth/token" {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) == nil {
			for _, name := range []string{"access_token", "id_token"} {
				var value string
				if json.Unmarshal(fields[name], &value) == nil {
					t.record(value)
				}
			}
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	response.ContentLength = int64(len(raw))
	return response, nil
}
func newApprovedTransport(roots *x509.CertPool, destinations map[string]string, maximum int, timeout time.Duration) *approvedTransport {
	t := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DisableCompression: true, MaxConnsPerHost: 8, MaxIdleConnsPerHost: 4, IdleConnTimeout: timeout, ResponseHeaderTimeout: timeout, TLSHandshakeTimeout: timeout}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dial := destinations[address]
		if dial == "" {
			return nil, errors.New("PRIVATE_DIAL_DESTINATION_REFUSED")
		}
		return (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", dial)
	}
	return &approvedTransport{inner: t, destinations: destinations, maximum: maximum}
}
