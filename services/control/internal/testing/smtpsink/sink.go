// Package smtpsink is an isolated TLS SMTP receiver for synthetic tests.
package smtpsink

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type Sink struct {
	Listener         net.Listener
	Certificate      tls.Certificate
	CAFile           string
	CertificateFile  string
	KeyFile          string
	Messages         chan []byte
	Implicit         bool
	DisableSTARTTLS  bool
	mu               sync.Mutex
	connections      map[net.Conn]bool
	temporaryFailure bool
}

func Certificate(t *testing.T, dir string, names ...string) (tls.Certificate, string, string, string) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Project-one synthetic test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	ca, e = x509.ParseCertificate(caDER)
	if e != nil {
		t.Fatal(e)
	}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if len(names) > 0 {
		leaf.DNSNames = names
		leaf.IPAddresses = nil
	}
	der, e := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})
	cert, e := tls.X509KeyPair(certPEM, keyPEM)
	if e != nil {
		t.Fatal(e)
	}
	caFile, certFile, keyFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "certificate.pem"), filepath.Join(dir, "private-key.pem")
	os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600)
	os.WriteFile(certFile, certPEM, 0600)
	os.WriteFile(keyFile, keyPEM, 0600)
	return cert, caFile, certFile, keyFile
}
func Start(t *testing.T, implicit, disableSTARTTLS bool) *Sink {
	t.Helper()
	dir := t.TempDir()
	cert, ca, certFile, keyFile := Certificate(t, dir)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	s := &Sink{Listener: listener, Certificate: cert, CAFile: ca, CertificateFile: certFile, KeyFile: keyFile, Messages: make(chan []byte, 64), Implicit: implicit, DisableSTARTTLS: disableSTARTTLS, connections: map[net.Conn]bool{}}
	t.Cleanup(s.Close)
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			s.mu.Lock()
			s.connections[conn] = true
			s.mu.Unlock()
			go s.receive(conn)
		}
	}()
	return s
}
func (s *Sink) Close() {
	s.Listener.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.connections {
		c.Close()
	}
}
func (s *Sink) receive(original net.Conn) {
	defer func() { original.Close(); s.mu.Lock(); delete(s.connections, original); s.mu.Unlock() }()
	original.SetDeadline(time.Now().Add(30 * time.Second))
	conn := original
	secure := false
	s.mu.Lock()
	certificate := s.Certificate
	s.mu.Unlock()
	cfg := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	if s.Implicit {
		c := tls.Server(conn, cfg)
		if c.HandshakeContext(context.Background()) != nil {
			return
		}
		conn = c
		secure = true
	}
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	reply := func(v string) bool {
		if _, e := writer.WriteString(v + "\r\n"); e != nil {
			return false
		}
		return writer.Flush() == nil
	}
	reply("220 projectone-test ESMTP")
	for {
		line, e := reader.ReadString('\n')
		if e != nil || len(line) > 1024 {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO") || strings.HasPrefix(cmd, "HELO"):
			if !secure && !s.DisableSTARTTLS {
				reply("250-projectone-test\r\n250 STARTTLS")
			} else {
				reply("250 projectone-test")
			}
		case cmd == "STARTTLS":
			if secure || s.DisableSTARTTLS {
				reply("502 unavailable")
				continue
			}
			if !reply("220 TLS ready") {
				return
			}
			c := tls.Server(conn, cfg)
			if c.HandshakeContext(context.Background()) != nil {
				return
			}
			conn = c
			secure = true
			reader = bufio.NewReader(conn)
			writer = bufio.NewWriter(conn)
		case strings.HasPrefix(cmd, "MAIL FROM:") || strings.HasPrefix(cmd, "RCPT TO:"):
			if !secure {
				reply("530 TLS required")
				continue
			}
			s.mu.Lock()
			temporaryFailure := s.temporaryFailure
			s.mu.Unlock()
			if temporaryFailure && strings.HasPrefix(cmd, "MAIL FROM:") {
				if !reply("451 synthetic temporary outage") {
					return
				}
				continue
			}
			if strings.HasPrefix(cmd, "RCPT TO:") && !strings.Contains(strings.ToLower(line), "@example.invalid") {
				reply("550 synthetic recipients only")
				continue
			}
			reply("250 ok")
		case cmd == "DATA":
			if !secure {
				reply("530 TLS required")
				continue
			}
			reply("354 send data")
			message, e := io.ReadAll(io.LimitReader(textproto.NewReader(reader).DotReader(), 65537))
			if e != nil || len(message) > 65536 {
				return
			}
			select {
			case s.Messages <- message:
				reply("250 accepted")
			default:
				reply("452 mailbox full")
			}
		case cmd == "QUIT":
			reply("221 bye")
			return
		case cmd == "RSET" || cmd == "NOOP":
			reply("250 ok")
		default:
			reply("502 unsupported")
		}
	}
}

func (s *Sink) SetCertificate(certificate tls.Certificate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Certificate = certificate
}

// SetTemporaryFailure is an explicit synthetic transport fault. It preserves
// real STARTTLS and reports a transient SMTP failure before message acceptance.
func (s *Sink) SetTemporaryFailure(value bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.temporaryFailure = value
}
