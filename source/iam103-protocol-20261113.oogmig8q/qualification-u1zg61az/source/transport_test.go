package directory

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testTLS(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ldap.iam103.test"}, DNSNames: []string{"ldap.iam103.test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	kb, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert, e := tls.X509KeyPair(certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
	if e != nil {
		t.Fatal(e)
	}
	return cert, certPEM
}
func testClient(t *testing.T, addr string, ca []byte) *client {
	t.Helper()
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	secretFile := filepath.Join(dir, "search-secret")
	if e := os.WriteFile(caFile, ca, 0600); e != nil {
		t.Fatal(e)
	}
	secretBytes := make([]byte, 32)
	if _, e := rand.Read(secretBytes); e != nil {
		t.Fatal(e)
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for i := range secretBytes {
		secretBytes[i] = alphabet[int(secretBytes[i])%len(alphabet)]
	}
	if e := os.WriteFile(secretFile, secretBytes, 0600); e != nil {
		t.Fatal(e)
	}
	_, port, e := net.SplitHostPort(addr)
	if e != nil {
		t.Fatal(e)
	}
	c, e := newClient(config{Origin: "ldaps://ldap.iam103.test:" + port, DialAddress: addr, CAFile: caFile, SearchDN: "uid=reader,dc=fixture,dc=test", SearchPasswordFile: secretFile, UserBase: "ou=people,dc=fixture,dc=test", GroupBase: "ou=groups,dc=fixture,dc=test", Timeout: 5 * time.Second, FrameBytes: 64 << 10, TotalBytes: 1 << 20, Depth: 16, Nodes: 1024, PageSize: 2, MaxPages: 10, MaxEntries: 20, CookieBytes: 256})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func readRequest(c net.Conn) error {
	var h [6]byte
	if _, e := io.ReadFull(c, h[:2]); e != nil {
		return e
	}
	n := int(h[1])
	if n&128 != 0 {
		k := n & 127
		if k == 0 || k > 4 {
			return errFrame
		}
		if _, e := io.ReadFull(c, h[2:2+k]); e != nil {
			return e
		}
		n = 0
		for _, v := range h[2 : 2+k] {
			n = n*256 + int(v)
		}
	}
	if n > 64<<10 {
		return errFrame
	}
	_, e := io.CopyN(io.Discard, c, int64(n))
	return e
}
func sdkWorkers() int {
	b := make([]byte, 1<<20)
	n := runtime.Stack(b, true)
	s := string(b[:n])
	return strings.Count(s, "github.com/go-ldap/ldap/v3.(*Conn).reader(") + strings.Count(s, "github.com/go-ldap/ldap/v3.(*Conn).processMessages(") + strings.Count(s, "github.com/go-ldap/ldap/v3.(*Conn).processMessages.func")
}
func assertWorkersDrained(t *testing.T, baseline int) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for sdkWorkers() > baseline && time.Now().Before(until) {
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	if sdkWorkers() > baseline {
		t.Fatal("SDK workers did not drain")
	}
}

func TestBlockedBindAndSearchCancellation(t *testing.T) {
	cert, ca := testTLS(t)
	for _, search := range []bool{false, true} {
		t.Run(map[bool]string{false: "bind", true: "search"}[search], func(t *testing.T) {
			for round := 0; round < 6; round++ {
				baseline := sdkWorkers()
				listener, e := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
				if e != nil {
					t.Fatal(e)
				}
				c := testClient(t, listener.Addr().String(), ca)
				observed := make(chan struct{})
				serverDone := make(chan error, 1)
				go func() {
					conn, e := listener.Accept()
					if e != nil {
						serverDone <- e
						return
					}
					defer conn.Close()
					conn.SetDeadline(time.Now().Add(3 * time.Second))
					if e = readRequest(conn); e != nil {
						serverDone <- e
						return
					}
					if search {
						if _, e = conn.Write(bindReply(1)); e != nil {
							serverDone <- e
							return
						}
						if e = readRequest(conn); e != nil {
							serverDone <- e
							return
						}
					}
					close(observed)
					var one [1]byte
					_, e = conn.Read(one[:])
					serverDone <- e
				}()
				ctx, cancel := context.WithCancel(context.Background())
				result := make(chan error, 1)
				pw := stringSecret(t, c.cfg.SearchPasswordFile)
				go func() { _, e := c.login(ctx, "synthetic", pw); result <- e }()
				select {
				case <-observed:
				case <-time.After(2 * time.Second):
					t.Fatal("blocked operation never reached server")
				}
				cancel()
				select {
				case e := <-result:
					if !errors.Is(e, context.Canceled) {
						t.Fatal(e)
					}
				case <-time.After(time.Second):
					t.Fatal("cancellation did not join operation")
				}
				select {
				case e := <-serverDone:
					if e == nil {
						t.Fatal("socket remained open")
					}
				case <-time.After(time.Second):
					t.Fatal("raw socket not closed")
				}
				listener.Close()
				assertWorkersDrained(t, baseline)
			}
		})
	}
}
func stringSecret(t *testing.T, path string) string {
	t.Helper()
	s, e := secret(path)
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestTLSAndLocalCredentialRefusals(t *testing.T) {
	cert, ca := testTLS(t)
	listener, e := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	c := testClient(t, listener.Addr().String(), ca)
	for _, v := range []struct{ login, password string }{{"synthetic", ""}, {"", "x"}, {"a\x00b", "x"}, {"a", "x\x00b"}} {
		if got, e := c.login(context.Background(), v.login, v.password); !errors.Is(e, errCredentials) || got.ID != "" {
			t.Fatal("local credentials accepted", e)
		}
	}
	if c.dials.Load() != 0 {
		t.Fatal("empty credentials opened socket")
	}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		for i := 0; i < 2; i++ {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(time.Second))
			readRequest(conn)
			conn.Close()
		}
	}()
	wrongName := testClient(t, listener.Addr().String(), ca)
	wrongName.name = "other.iam103.test"
	if _, e := wrongName.login(context.Background(), "synthetic", stringSecret(t, c.cfg.SearchPasswordFile)); !errors.Is(e, errUnavailable) {
		t.Fatal(e)
	}
	_, otherCA := testTLS(t)
	wrongCA := testClient(t, listener.Addr().String(), otherCA)
	if _, e := wrongCA.login(context.Background(), "synthetic", stringSecret(t, c.cfg.SearchPasswordFile)); !errors.Is(e, errUnavailable) {
		t.Fatal(e)
	}
	<-serverDone
	for _, origin := range []string{"ldap://ldap.iam103.test:1636", "ldaps://user@ldap.iam103.test:1636", "ldaps://ldap.iam103.test:1636/path"} {
		cfg := c.cfg
		cfg.Origin = origin
		if _, e := newClient(cfg); !errors.Is(e, errConfiguration) {
			t.Fatal("bad origin accepted")
		}
	}
	cfg := c.cfg
	cfg.DialAddress = "attacker.example:1636"
	if _, e := newClient(cfg); !errors.Is(e, errConfiguration) {
		t.Fatal("unapproved DNS dial accepted")
	}
}

func TestHostileBEROverVerifiedTLS(t *testing.T) {
	cert, ca := testTLS(t)
	for name, reply := range map[string][]byte{"oversized": {0x30, 0x84, 0x7f, 0xff, 0xff, 0xff}, "indefinite": {0x30, 0x80}, "unknown-control": envelope(1, wire(0x61), wire(0xa0, wire(0x30, wire(4, []byte("1.2.3")), wire(4))))} {
		t.Run(name, func(t *testing.T) {
			baseline := sdkWorkers()
			ln, e := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
			if e != nil {
				t.Fatal(e)
			}
			defer ln.Close()
			c := testClient(t, ln.Addr().String(), ca)
			done := make(chan struct{})
			go func() {
				defer close(done)
				x, e := ln.Accept()
				if e != nil {
					return
				}
				defer x.Close()
				x.SetDeadline(time.Now().Add(time.Second))
				if readRequest(x) == nil {
					x.Write(reply)
					var b [1]byte
					x.Read(b[:])
				}
			}()
			if got, e := c.login(context.Background(), "synthetic", stringSecret(t, c.cfg.SearchPasswordFile)); e == nil || got.ID != "" {
				t.Fatal("hostile server authenticated")
			}
			<-done
			assertWorkersDrained(t, baseline)
		})
	}
}

func TestPostBindIdentityReplacementRefused(t *testing.T) {
	cert, ca := testTLS(t)
	ln, e := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	c := testClient(t, ln.Addr().String(), ca)
	done := make(chan struct{})
	serverError := make(chan error, 1)
	go func() {
		defer close(done)
		for i, id := range []string{"12345678-1234-4234-8234-123456789abc", "87654321-1234-4234-8234-123456789abc"} {
			x, e := ln.Accept()
			if e != nil {
				serverError <- e
				return
			}
			x.SetDeadline(time.Now().Add(2 * time.Second))
			if e = readRequest(x); e == nil {
				_, e = x.Write(bindReply(1))
			}
			if e == nil {
				e = readRequest(x)
			}
			if e == nil {
				entry := envelope(2, wire(0x64, wire(4, []byte("uid=synthetic,"+c.cfg.UserBase)), wire(0x30, wire(0x30, wire(4, []byte("entryUUID")), wire(0x31, wire(4, []byte(id)))))))
				finished := envelope(2, wire(0x65, wire(0x0a, []byte{0}), wire(4), wire(4)))
				_, e = x.Write(append(entry, finished...))
			}
			if e != nil {
				serverError <- e
				x.Close()
				return
			}
			if i == 0 {
				var b [1]byte
				x.Read(b[:])
			}
			x.Close()
		}
	}()
	if got, e := c.login(context.Background(), "synthetic", stringSecret(t, c.cfg.SearchPasswordFile)); !errors.Is(e, errCredentials) || got.ID != "" {
		t.Fatal("post-bind identity replacement accepted", e)
	}
	<-done
	select {
	case e := <-serverError:
		t.Fatal(e)
	default:
	}
}
