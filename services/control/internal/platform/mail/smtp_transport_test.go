package mail_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

// This explicit synthetic SMTP responder uses real TLS and sockets. It never
// forwards mail or retains AUTH/message contents in evidence.
type smtpTransportCounters struct {
	Connections, Handshakes, FailedHandshakes   int
	EncryptedAUTH, PlaintextAUTH, PlaintextMail int
	Messages, MinimumTLSVersion                 int
}

type smtpTransportResponder struct {
	listener                              net.Listener
	mu                                    sync.Mutex
	wg                                    sync.WaitGroup
	connections                           map[net.Conn]bool
	certificate                           tls.Certificate
	implicit, missingSTARTTLS, rejectAUTH bool
	username, password                    string
	counters                              smtpTransportCounters
}

func smtpTransportCertificates(t *testing.T, root, mode string) (tls.Certificate, tls.Certificate, string) {
	t.Helper()
	now := time.Now()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal("SMTP_TEST_CA_KEY_FAILED")
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic SMTP transport CA"}, NotBefore: now.Add(-2 * time.Hour), NotAfter: now.Add(2 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		t.Fatal("SMTP_TEST_CA_FAILED")
	}
	caFile := filepath.Join(root, "ca.pem")
	if os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600) != nil {
		t.Fatal("SMTP_TEST_CA_FILE_FAILED")
	}
	leafKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal("SMTP_TEST_LEAF_KEY_FAILED")
	}
	makeLeaf := func(serial int64, variant string) tls.Certificate {
		leaf := &x509.Certificate{SerialNumber: big.NewInt(serial), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		if variant == "expired_leaf" {
			leaf.NotAfter = now.Add(-time.Minute)
		}
		if variant == "wrong_name" {
			leaf.DNSNames, leaf.IPAddresses = []string{"wrong.example.invalid"}, nil
		}
		der, e := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
		if e != nil {
			t.Fatal("SMTP_TEST_LEAF_FAILED")
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: leafKey}
	}
	return makeLeaf(2, mode), makeLeaf(3, "trusted"), caFile
}

func startSMTPTransportResponder(t *testing.T, certificate tls.Certificate, implicit, missing, rejected bool, username, password string) *smtpTransportResponder {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal("SMTP_TEST_LISTENER_FAILED")
	}
	s := &smtpTransportResponder{listener: listener, connections: map[net.Conn]bool{}, certificate: certificate, implicit: implicit, missingSTARTTLS: missing, rejectAUTH: rejected, username: username, password: password}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			connection, e := listener.Accept()
			if e != nil {
				return
			}
			s.mu.Lock()
			s.connections[connection] = true
			s.counters.Connections++
			s.mu.Unlock()
			s.wg.Add(1)
			go s.receive(connection)
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		s.mu.Lock()
		connections := make([]net.Conn, 0, len(s.connections))
		for c := range s.connections {
			connections = append(connections, c)
		}
		s.mu.Unlock()
		for _, c := range connections {
			c.Close()
		}
		s.wg.Wait()
	})
	return s
}

func (s *smtpTransportResponder) secure(connection net.Conn) (net.Conn, bool) {
	c := tls.Server(connection, &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		certificate := s.certificate
		return &certificate, nil
	}})
	e := c.HandshakeContext(context.Background())
	s.mu.Lock()
	defer s.mu.Unlock()
	if e != nil {
		s.counters.FailedHandshakes++
		return connection, false
	}
	s.counters.Handshakes++
	version := int(c.ConnectionState().Version)
	if s.counters.MinimumTLSVersion == 0 || version < s.counters.MinimumTLSVersion {
		s.counters.MinimumTLSVersion = version
	}
	return c, true
}

func (s *smtpTransportResponder) receive(original net.Conn) {
	defer s.wg.Done()
	defer func() { original.Close(); s.mu.Lock(); delete(s.connections, original); s.mu.Unlock() }()
	original.SetDeadline(time.Now().Add(5 * time.Second))
	connection, secure, authenticated := original, false, false
	if s.implicit {
		connection, secure = s.secure(original)
		if !secure {
			return
		}
	}
	reader, writer := bufio.NewReaderSize(connection, 2048), bufio.NewWriter(connection)
	reply := func(line string) bool {
		_, e := writer.WriteString(line + "\r\n")
		return e == nil && writer.Flush() == nil
	}
	if !reply("220 synthetic SMTP") {
		return
	}
	for {
		line, e := reader.ReadSlice('\n')
		if e != nil {
			return
		}
		fields := strings.Fields(strings.TrimSpace(string(line)))
		if len(fields) == 0 {
			return
		}
		switch strings.ToUpper(fields[0]) {
		case "EHLO", "HELO":
			// Deliberately advertise AUTH before TLS as a hostile relay might.
			if !secure && !s.missingSTARTTLS {
				if !reply("250-synthetic\r\n250-AUTH PLAIN\r\n250 STARTTLS") {
					return
				}
			} else if !reply("250-synthetic\r\n250 AUTH PLAIN") {
				return
			}
		case "STARTTLS":
			if secure || s.missingSTARTTLS {
				if !reply("502 unavailable") {
					return
				}
				continue
			}
			if !reply("220 TLS ready") {
				return
			}
			connection, secure = s.secure(connection)
			if !secure {
				return
			}
			reader, writer = bufio.NewReaderSize(connection, 2048), bufio.NewWriter(connection)
		case "AUTH":
			s.mu.Lock()
			if secure {
				s.counters.EncryptedAUTH++
			} else {
				s.counters.PlaintextAUTH++
			}
			s.mu.Unlock()
			if !secure {
				if !reply("530 TLS required") {
					return
				}
				continue
			}
			var decoded []byte
			if len(fields) == 3 && fields[1] == "PLAIN" {
				decoded, _ = base64.StdEncoding.DecodeString(fields[2])
			}
			parts := strings.Split(string(decoded), "\x00")
			authenticated = len(parts) == 3 && parts[0] == "" && parts[1] == s.username && parts[2] == s.password && !s.rejectAUTH
			if authenticated {
				if !reply("235 authenticated") {
					return
				}
			} else if !reply("535 rejected") {
				return
			}
		case "MAIL", "RCPT", "DATA":
			if !secure {
				s.mu.Lock()
				s.counters.PlaintextMail++
				s.mu.Unlock()
				if !reply("530 TLS required") {
					return
				}
				continue
			}
			if !authenticated {
				if !reply("530 AUTH required") {
					return
				}
				continue
			}
			if strings.ToUpper(fields[0]) != "DATA" {
				if !reply("250 accepted") {
					return
				}
				continue
			}
			if !reply("354 send synthetic data") {
				return
			}
			message, e := io.ReadAll(io.LimitReader(textproto.NewReader(reader).DotReader(), 65537))
			if e != nil || len(message) > 65536 {
				return
			}
			if string(message) != "Subject: Synthetic transport test\n\nSynthetic message.\n" {
				reply("554 invalid synthetic message")
				return
			}
			s.mu.Lock()
			s.counters.Messages++
			s.mu.Unlock()
			if !reply("250 accepted") {
				return
			}
		case "QUIT":
			reply("221 closing")
			return
		case "RSET", "NOOP":
			if !reply("250 ok") {
				return
			}
		default:
			if !reply("502 unsupported") {
				return
			}
		}
	}
}

func (s *smtpTransportResponder) observation(t *testing.T) smtpTransportCounters {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		count, result := len(s.connections), s.counters
		s.mu.Unlock()
		if count == 0 {
			return result
		}
		if time.Now().After(deadline) {
			t.Fatal("SMTP_TEST_CONNECTIONS_NOT_JOINED")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestActualSMTPVerifiedTLSAndAuthentication(t *testing.T) {
	cases := []struct {
		name, mode                  string
		implicit, missing, rejected bool
	}{
		{"trusted_starttls", "trusted", false, false, false}, {"trusted_implicit_tls", "trusted", true, false, false},
		{"untrusted_ca_starttls", "untrusted_ca", false, false, false}, {"untrusted_ca_implicit_tls", "untrusted_ca", true, false, false},
		{"expired_leaf_starttls", "expired_leaf", false, false, false}, {"expired_leaf_implicit_tls", "expired_leaf", true, false, false},
		{"wrong_name_starttls", "wrong_name", false, false, false}, {"wrong_name_implicit_tls", "wrong_name", true, false, false},
		{"missing_starttls", "trusted", false, true, false}, {"rejected_auth_starttls", "trusted", false, false, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			root := t.TempDir()
			if os.Chmod(root, 0700) != nil {
				t.Fatal("SMTP_TEST_PRIVATE_ROOT_FAILED")
			}
			certificate, valid, caFile := smtpTransportCertificates(t, root, item.mode)
			secret := make([]byte, 32)
			if _, e := rand.Read(secret); e != nil {
				t.Fatal("SMTP_TEST_CREDENTIAL_FAILED")
			}
			password := base64.RawURLEncoding.EncodeToString(secret)
			passwordFile := filepath.Join(root, "password")
			if os.WriteFile(passwordFile, []byte(password), 0600) != nil {
				t.Fatal("SMTP_TEST_CREDENTIAL_FILE_FAILED")
			}
			s := startSMTPTransportResponder(t, certificate, item.implicit, item.missing, item.rejected, "synthetic-smtp-user", password)
			host, port, e := net.SplitHostPort(s.listener.Addr().String())
			if e != nil {
				t.Fatal("SMTP_TEST_ADDRESS_FAILED")
			}
			var c config.Config
			c.SMTP.Host, c.SMTP.CAFile, c.SMTP.Username, c.SMTP.PasswordFile = host, caFile, s.username, passwordFile
			c.SMTP.Port, _ = strconv.Atoi(port)
			c.SMTP.FromAddress, c.SMTP.TLSMode = "sender@example.invalid", "starttls_required"
			c.SMTP.ConnectTimeoutSeconds, c.SMTP.OperationTimeoutSeconds = 2, 3
			if item.implicit {
				c.SMTP.TLSMode = "implicit_tls"
			}
			if item.mode == "untrusted_ca" {
				other := filepath.Join(root, "other")
				if os.Mkdir(other, 0700) != nil {
					t.Fatal("SMTP_TEST_OTHER_CA_ROOT_FAILED")
				}
				_, _, c.SMTP.CAFile = smtpTransportCertificates(t, other, "trusted")
			}
			send := func() error {
				ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
				defer cancel()
				return mail.Send(ctx, c, "recipient@example.invalid", []byte("Subject: Synthetic transport test\r\n\r\nSynthetic message.\r\n"))
			}
			e = send()
			observed := s.observation(t)
			if observed.Connections != 1 || observed.PlaintextAUTH != 0 || observed.PlaintextMail != 0 {
				t.Fatal("SMTP_TEST_RETRY_OR_PLAINTEXT_DISCLOSURE", observed)
			}
			expected := ""
			if item.mode != "trusted" {
				expected = "smtp_tls_rejected"
			}
			if item.missing {
				expected = "smtp_tls_required"
			}
			if item.rejected {
				expected = "smtp_auth_rejected"
			}
			if expected == "" {
				if e != nil || observed.Handshakes != 1 || observed.EncryptedAUTH != 1 || observed.Messages != 1 || observed.MinimumTLSVersion < tls.VersionTLS12 {
					t.Fatal("SMTP_TEST_VERIFIED_DELIVERY_FAILED", observed)
				}
			} else {
				if e == nil || e.Error() != expected {
					t.Fatal("SMTP_TEST_EXPECTED_STATIC_FAILURE_MISSING")
				}
				code, permanent := mail.Classify(e)
				if code != expected || !permanent || observed.Messages != 0 || !item.rejected && observed.EncryptedAUTH != 0 {
					t.Fatal("SMTP_TEST_FAILED_TLS_OR_AUTH_TRANSMITTED_DATA", observed)
				}
				if item.rejected && (observed.Handshakes != 1 || observed.EncryptedAUTH != 1 || observed.MinimumTLSVersion < tls.VersionTLS12) {
					t.Fatal("SMTP_TEST_AUTH_WAS_NOT_ENCRYPTED", observed)
				}
			}
			label := expected
			if label == "" {
				label = "none"
			}
			t.Logf("SMTP_VERIFIED_AUTH_ASSERTIONS phase=initial case=%s port=%d connections=%d handshakes=%d encrypted_AUTH=%d plaintext_AUTH=%d plaintext_mail=%d messages=%d minimum_tls=%d expected_error=%s", item.name, c.SMTP.Port, observed.Connections, observed.Handshakes, observed.EncryptedAUTH, observed.PlaintextAUTH, observed.PlaintextMail, observed.Messages, observed.MinimumTLSVersion, label)
			if item.mode == "expired_leaf" {
				s.mu.Lock()
				s.certificate = valid
				s.mu.Unlock()
				if send() != nil {
					t.Fatal("SMTP_TEST_EXPLICIT_CERTIFICATE_RECOVERY_FAILED")
				}
				recovered := s.observation(t)
				if recovered.Connections != 2 || recovered.Handshakes != 1 || recovered.EncryptedAUTH != 1 || recovered.Messages != 1 || recovered.PlaintextAUTH != 0 || recovered.PlaintextMail != 0 || recovered.MinimumTLSVersion < tls.VersionTLS12 {
					t.Fatal("SMTP_TEST_RECOVERY_ASSERTIONS_FAILED", recovered)
				}
				t.Logf("SMTP_VERIFIED_AUTH_ASSERTIONS phase=recovery case=%s port=%d connections=%d handshakes=%d encrypted_AUTH=%d plaintext_AUTH=%d plaintext_mail=%d messages=%d minimum_tls=%d expected_error=none", item.name, c.SMTP.Port, recovered.Connections, recovered.Handshakes, recovered.EncryptedAUTH, recovered.PlaintextAUTH, recovered.PlaintextMail, recovered.Messages, recovered.MinimumTLSVersion)
			}
		})
	}
}
