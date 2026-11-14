//go:build linux || darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const siemTestTenant = "00000000-0000-4000-8000-000000000010"

func siemTestRows(t *testing.T) []record {
	t.Helper()
	rows := testChain(t)
	fourth := make(record)
	for field, value := range rows[2] {
		fourth[field] = value
	}
	rows = append(rows, fourth)
	set := func(row record, key, value string) { row[key] = &value }
	previous := zeroHash
	for i, row := range rows {
		set(row, "position", strconv.Itoa(i+1))
		set(row, "id", fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1))
		set(row, "tenant_id", siemTestTenant)
		set(row, "actor_user_id", "private-actor-marker")
		set(row, "request_id", "private-request-marker")
		set(row, "target_type", "private-target-marker")
		set(row, "reason_sha256", strings.Repeat("b", 64))
		if i == 1 {
			set(row, "tenant_id", "00000000-0000-4000-8000-000000000020")
			set(row, "action", "foreign.audit")
		}
		if i >= 2 {
			set(row, "format", scopeFormat)
			set(row, "site_id", "private-site-marker")
			set(row, "cell_id", "private-cell-marker")
		}
		set(row, "prev_hash", previous)
		prior, _ := hashBytes(previous)
		previous = calculateHash(row, prior)
		set(row, "hash", previous)
	}
	return rows
}

func privateSIEMTestDir(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cursor")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func siemTestWindow(rows []record) siemWindow {
	start, _ := strconv.ParseInt(rows[0].value("position"), 10, 64)
	return siemWindow{from: start - 1, through: start + int64(len(rows)) - 1, previous: rows[0].value("prev_hash"), last: rows[len(rows)-1].value("hash")}
}

func TestSIEMRefusesToAdvancePastFailedFrameAndResumes(t *testing.T) {
	rows := siemTestRows(t)
	window := siemTestWindow(rows)
	directory := privateSIEMTestDir(t)
	grant := siemGrant{TenantID: siemTestTenant}
	open := func() *siemCursorStore {
		store, err := openSIEMCursor(directory, grant.binding(), window)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(store.close)
		return store
	}

	// Cancellation after a completed write retains exactly its prefix.
	store := open()
	ctx, cancel := context.WithCancel(t.Context())
	first := forwardSIEM(ctx, strings.NewReader(encodeRows(t, rows)), window, grant, store, func(context.Context, []byte) error {
		cancel()
		return nil
	})
	if first != (siemReport{Position: 1, Sent: 1, TotalSent: 1, Error: "AUDIT_SIEM_INTERRUPTED"}) {
		t.Fatalf("interrupted result: %+v", first)
	}
	if _, err := openSIEMCursor(directory, grant.binding(), window); err == nil || err.Error() != "AUDIT_SIEM_CURSOR_LOCKED" {
		t.Fatalf("concurrent cursor was accepted: %v", err)
	}
	store.close()

	store = open()
	var attempts int
	failed := forwardSIEM(t.Context(), strings.NewReader(encodeRows(t, rows)), window, grant, store, func(_ context.Context, frame []byte) error {
		attempts++
		if !bytes.Contains(frame, []byte(rows[2].value("id"))) {
			t.Fatalf("first undelivered event was bypassed: %q", frame)
		}
		return errors.New("AUDIT_SIEM_TLS_WRITE_FAILED")
	})
	if attempts != 1 || failed != (siemReport{Position: 2, TotalSent: 1, Error: "AUDIT_SIEM_TLS_WRITE_FAILED"}) {
		t.Fatalf("failure result: %+v, attempts: %d", failed, attempts)
	}
	store.close()

	store = open()
	var delivered []string
	suffix := rows[2:]
	resumed := forwardSIEM(t.Context(), strings.NewReader(encodeRows(t, suffix)), siemTestWindow(suffix), grant, store, func(_ context.Context, frame []byte) error {
		delivered = append(delivered, string(frame))
		return nil
	})
	if resumed != (siemReport{Position: 4, Sent: 2, TotalSent: 3}) ||
		len(delivered) != 2 || !strings.Contains(delivered[0], rows[2].value("id")) ||
		!strings.Contains(delivered[1], rows[3].value("id")) {
		t.Fatalf("resume result: %+v, frames: %v", resumed, delivered)
	}
	store.close()

	foreignGrant := siemGrant{TenantID: "00000000-0000-4000-8000-000000000020"}
	if _, err := openSIEMCursor(directory, foreignGrant.binding(), window); err == nil || err.Error() != "AUDIT_SIEM_CURSOR_GRANT_MISMATCH" {
		t.Fatalf("cross-tenant reuse was accepted: %v", err)
	}
}

func TestSIEMWindowFailsClosedBeforeSending(t *testing.T) {
	rows := siemTestRows(t)
	window := siemTestWindow(rows)
	intact := encodeRows(t, rows)
	for _, tc := range []struct {
		name  string
		input string
		scope siemWindow
	}{
		{"modified suffix", strings.Replace(intact, rows[3].value("id"), "changed", 1), window},
		{"missing foreign position", encodeRows(t, append([]record{rows[0]}, rows[2:]...)), window},
		{"wrong final checkpoint", intact, siemWindow{0, 4, zeroHash, strings.Repeat("a", 64)}},
		{"wrong preceding position", encodeRows(t, rows[2:]), siemWindow{1, 4, rows[1].value("hash"), window.last}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grant := siemGrant{TenantID: siemTestTenant}
			store, err := openSIEMCursor(privateSIEMTestDir(t), grant.binding(), tc.scope)
			if err != nil {
				t.Fatal(err)
			}
			defer store.close()
			result := forwardSIEM(t.Context(), strings.NewReader(tc.input), tc.scope, grant, store, func(context.Context, []byte) error {
				t.Fatal("unverified export transmitted")
				return nil
			})
			if result.Error != "AUDIT_SIEM_WINDOW_INVALID" || store.exists {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

type siemTestPKI struct {
	server tls.Certificate
	client tls.Certificate
	roots  *x509.CertPool
	caPEM  []byte
}

func newSIEMTestPKI(t *testing.T) siemTestPKI {
	t.Helper()
	key := func() *ecdsa.PrivateKey {
		result, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	now := time.Now()
	caKey := key()
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "site test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leaf := func(serial int64, name string, usage x509.ExtKeyUsage) tls.Certificate {
		leafKey := key()
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), DNSNames: []string{name},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		}
		raw, err := x509.CreateCertificate(rand.Reader, template, root, &leafKey.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: leafKey}
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	return siemTestPKI{
		server: leaf(2, "siem.site.test", x509.ExtKeyUsageServerAuth),
		client: leaf(3, "audit.site.test", x509.ExtKeyUsageClientAuth),
		roots:  pool, caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
	}
}

func (p siemTestPKI) receiver() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{p.server},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: p.roots,
		VerifyConnection: func(state tls.ConnectionState) error {
			if !bytes.Equal(state.PeerCertificates[0].Raw, p.client.Certificate[0]) {
				return errors.New("client identity denied")
			}
			return nil
		},
	}
}

func writeSIEMTestFile(t *testing.T, directory, name string, raw []byte) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (p siemTestPKI) grant(t *testing.T, endpoint string) siemGrant {
	t.Helper()
	directory := t.TempDir()
	key, err := x509.MarshalPKCS8PrivateKey(p.client.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(p.server.Certificate[0])
	return siemGrant{
		Version: 1, TenantID: siemTestTenant, Endpoint: endpoint, ServerName: "siem.site.test",
		ServerCertificateSHA256: hex.EncodeToString(sum[:]), CursorDirectory: privateSIEMTestDir(t),
		CAFile:                writeSIEMTestFile(t, directory, "ca.pem", p.caPEM),
		ClientCertificateFile: writeSIEMTestFile(t, directory, "client.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.client.Certificate[0]})),
		ClientKeyFile:         writeSIEMTestFile(t, directory, "client-key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})),
	}
}

func siemTestListen(t *testing.T) *net.TCPListener {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if err := listener.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return listener
}

func siemTestReadFrame(reader *bufio.Reader) (string, error) {
	prefix, err := reader.ReadString(' ')
	if err != nil {
		return "", err
	}
	n, err := strconv.Atoi(strings.TrimSuffix(prefix, " "))
	if err != nil || n <= 0 || n > 2048 {
		return "", errors.New("invalid octet count")
	}
	body := make([]byte, n)
	_, err = io.ReadFull(reader, body)
	return string(body), err
}

func siemTestArgs(t *testing.T, grant siemGrant, rows []record, attempts string) []string {
	t.Helper()
	directory := t.TempDir()
	raw, err := json.Marshal(grant)
	if err != nil {
		t.Fatal(err)
	}
	window := siemTestWindow(rows)
	return []string{
		"forward-syslog", "--grant", writeSIEMTestFile(t, directory, "grant.json", raw),
		"--file", writeSIEMTestFile(t, directory, "export.jsonl", []byte(encodeRows(t, rows))),
		"--previous-position", strconv.FormatInt(window.from, 10), "--last-position", strconv.FormatInt(window.through, 10),
		"--previous-hash", window.previous, "--expected-last-hash", window.last, "--timeout", "2s", "--attempts", attempts,
	}
}

func siemTestRun(t *testing.T, args []string) (int, siemReport) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, strings.NewReader(""), &stdout, &stderr)
	var result siemReport
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("bad report: %v, stdout=%q, stderr=%q", err, stdout.String(), stderr.String())
	}
	return code, result
}

func TestSIEMTLSLoopbackRetryRedactionAndReplay(t *testing.T) {
	pki := newSIEMTestPKI(t)
	listener := siemTestListen(t)
	rows := siemTestRows(t)
	grant := pki.grant(t, listener.Addr().String())
	type received struct {
		frames []string
		err    error
	}
	done := make(chan received, 1)
	go func() {
		// Drop the first TCP connection before TLS. Retry must start at the same event.
		conn, err := listener.Accept()
		if err != nil {
			done <- received{err: err}
			return
		}
		_ = conn.Close()
		conn, err = listener.Accept()
		if err != nil {
			done <- received{err: err}
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		server := tls.Server(conn, pki.receiver())
		defer server.Close()
		reader := bufio.NewReader(server)
		var result received
		for {
			frame, err := siemTestReadFrame(reader)
			if err != nil {
				if err != io.EOF {
					result.err = err
				}
				done <- result
				return
			}
			result.frames = append(result.frames, frame)
		}
	}()

	args := siemTestArgs(t, grant, rows, "2")
	if code, result := siemTestRun(t, args); code != 0 || result != (siemReport{Position: 4, Sent: 3, TotalSent: 3}) {
		t.Fatalf("delivery result: %d %+v", code, result)
	}
	receivedResult := <-done
	if receivedResult.err != nil || len(receivedResult.frames) != 3 {
		t.Fatalf("receiver: %+v", receivedResult)
	}
	for index, row := range []record{rows[0], rows[2], rows[3]} {
		frame := receivedResult.frames[index]
		header := "<134>1 " + row.value("occurred_at") + " - ownsolid - AUDIT - "
		body, ok := strings.CutPrefix(frame, header)
		var fields map[string]string
		if !ok || json.Unmarshal([]byte(body), &fields) != nil || len(fields) != 5 ||
			fields["format"] != "ownsolid.audit.siem.v1" || fields["tenant_id"] != siemTestTenant ||
			fields["event_id"] != row.value("id") || fields["action"] != row.value("action") ||
			fields["outcome"] != row.value("outcome") || strings.Contains(frame, "private-") ||
			strings.Contains(frame, row.value("reason_sha256")) {
			t.Fatalf("wrong syslog projection: %q", frame)
		}
	}
	_ = listener.Close()
	if code, result := siemTestRun(t, args); code != 0 || result != (siemReport{Position: 4, TotalSent: 3}) {
		t.Fatalf("same-tenant replay result: %d %+v", code, result)
	}
}

func TestSIEMTLSRejectsUntrustedReceiver(t *testing.T) {
	pki := newSIEMTestPKI(t)
	for _, test := range []struct {
		name string
		edit func(*siemGrant)
	}{
		{"wrong CA", func(grant *siemGrant) {
			grant.CAFile = writeSIEMTestFile(t, t.TempDir(), "other-ca.pem", newSIEMTestPKI(t).caPEM)
		}},
		{"wrong name", func(grant *siemGrant) { grant.ServerName = "wrong.site.test" }},
		{"wrong pin", func(grant *siemGrant) { grant.ServerCertificateSHA256 = strings.Repeat("a", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener := siemTestListen(t)
			grant := pki.grant(t, listener.Addr().String())
			test.edit(&grant)
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				server := tls.Server(conn, pki.receiver())
				defer server.Close()
				done <- server.Handshake()
			}()
			code, result := siemTestRun(t, siemTestArgs(t, grant, siemTestRows(t), "1"))
			if code != 1 || result != (siemReport{Error: "AUDIT_SIEM_TLS_PEER_REJECTED"}) {
				t.Fatalf("untrusted recipient was not refused: %d %+v", code, result)
			}
			if err := <-done; err == nil {
				t.Fatal("handshake with the untrusted recipient completed")
			}
		})
	}
}

func TestSIEMGrantRejectsPublicEndpoint(t *testing.T) {
	pki := newSIEMTestPKI(t)
	grant := pki.grant(t, "192.0.2.10:6514")
	raw, err := json.Marshal(grant)
	if err != nil {
		t.Fatal(err)
	}
	path := writeSIEMTestFile(t, t.TempDir(), "grant.json", raw)
	if _, _, err := loadSIEMGrant(path); err == nil || err.Error() != "AUDIT_SIEM_GRANT_INVALID" {
		t.Fatalf("public destination was accepted: %v", err)
	}
}

func TestSIEMReceiverRejectsUntrustedClient(t *testing.T) {
	pki, other := newSIEMTestPKI(t), newSIEMTestPKI(t)
	listener := siemTestListen(t)
	grant := pki.grant(t, listener.Addr().String())
	untrusted := other.grant(t, listener.Addr().String())
	grant.ClientCertificateFile, grant.ClientKeyFile = untrusted.ClientCertificateFile, untrusted.ClientKeyFile
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		server := tls.Server(conn, pki.receiver())
		defer server.Close()
		var firstByte [1]byte
		// Read must authenticate the client before releasing any syslog bytes.
		n, err := server.Read(firstByte[:])
		if n != 0 || err == nil {
			done <- errors.New("receiver admitted the untrusted client")
			return
		}
		if server.ConnectionState().HandshakeComplete {
			done <- errors.New("receiver completed an unauthorized TLS session")
			return
		}
		done <- nil
	}()
	// TLS 1.3 can complete a local client write before it observes the alert.
	// The receiver's handshake is authoritative for this acceptance check.
	siemTestRun(t, siemTestArgs(t, grant, siemTestRows(t), "1"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
