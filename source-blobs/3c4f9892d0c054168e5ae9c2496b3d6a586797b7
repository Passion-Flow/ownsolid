//go:build repository_integration && object_storage_integration && object_storage_wire_integration

package artifactstores

import (
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
	"encoding/pem"
	"encoding/xml"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

const maximumS3FaultAcknowledgmentBytes = 64 << 10

type objectWireFault struct {
	mu       sync.Mutex
	armed    string
	dropped  int
	invalid  bool
	requests map[string]int
}

func wireMutation(r *http.Request) string {
	q := r.URL.Query()
	switch {
	case r.Method == "POST" && q.Has("uploads"):
		return "create"
	case r.Method == "PUT" && q.Has("partNumber") && q.Has("uploadId"):
		return "part"
	case r.Method == "POST" && q.Has("uploadId"):
		return "complete"
	case r.Method == "DELETE" && !q.Has("uploadId"):
		return "delete"
	}
	return ""
}

// This proxy never supplies an S3 result. It forwards to the actual private
// backend and can withhold only one fully received, successful upstream ACK.
func (p *objectWireFault) prepare(t *testing.T, c config.ObjectStorageConfiguration, root string) config.ObjectStorageConfiguration {
	t.Helper()
	if c.Transport != "https" {
		t.Fatal("ACTUAL_TLS_UPSTREAM_REQUIRED_FOR_WIRE_FAULTS")
	}
	endpoint, e := url.Parse(c.Endpoint)
	if e != nil {
		t.Fatal("actual private endpoint")
	}
	ca, e := c.ObjectStorageCA()
	if e != nil {
		t.Fatal("actual private upstream CA")
	}
	settings := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(ca) != 0 {
		settings.RootCAs = x509.NewCertPool()
		if !settings.RootCAs.AppendCertsFromPEM(ca) {
			t.Fatal("actual private upstream CA")
		}
	}
	port := endpoint.Port()
	if port == "" {
		port = "443"
	}
	upstreamAddresses := append([]string(nil), c.DialAddresses...)
	dialer := &net.Dialer{Timeout: time.Duration(c.OperationSeconds) * time.Second}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: settings, DisableKeepAlives: true, DisableCompression: true, ForceAttemptHTTP2: false, MaxConnsPerHost: c.MaximumConcurrentTransfers, TLSHandshakeTimeout: time.Duration(c.OperationSeconds) * time.Second, ResponseHeaderTimeout: time.Duration(c.OperationSeconds) * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, actualPort, e := net.SplitHostPort(address)
		if e != nil || host != endpoint.Hostname() || actualPort != port {
			return nil, fail("OBJECT_ENDPOINT_CHANGED")
		}
		for _, ip := range upstreamAddresses {
			conn, e := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
			if e == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, fail("OBJECT_STORAGE_UNAVAILABLE")
	}
	upstream := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, incoming *http.Request) {
		ctx, cancel := context.WithTimeout(incoming.Context(), time.Duration(c.TransferSeconds)*time.Second)
		defer cancel()
		request := incoming.Clone(ctx)
		target := *incoming.URL
		target.Scheme, target.Host = endpoint.Scheme, endpoint.Host
		request.URL, request.RequestURI = &target, ""
		// Host remains exactly the authority covered by the original SigV4
		// signature. TLS still verifies the actual upstream endpoint hostname.
		kind := wireMutation(incoming)
		p.mu.Lock()
		if kind != "" {
			p.requests[kind]++
		}
		withhold := kind != "" && p.armed == kind
		if withhold {
			p.armed = ""
		}
		p.mu.Unlock()
		response, e := upstream.Do(request)
		if e != nil {
			p.refuse(w)
			return
		}
		defer response.Body.Close()
		if withhold {
			raw, e := io.ReadAll(io.LimitReader(response.Body, maximumS3FaultAcknowledgmentBytes+1))
			valid := e == nil && len(raw) <= maximumS3FaultAcknowledgmentBytes
			switch kind {
			case "create", "complete":
				var body struct {
					XMLName  xml.Name
					UploadID string `xml:"UploadId"`
					ETag     string `xml:"ETag"`
				}
				valid = valid && response.StatusCode == http.StatusOK && xml.Unmarshal(raw, &body) == nil
				if kind == "create" {
					valid = valid && body.XMLName.Local == "InitiateMultipartUploadResult" && body.UploadID != ""
				} else {
					valid = valid && body.XMLName.Local == "CompleteMultipartUploadResult" && body.ETag != ""
				}
			case "part":
				valid = valid && response.StatusCode == http.StatusOK && response.Header.Get("ETag") != ""
			case "delete":
				valid = valid && response.StatusCode == http.StatusNoContent && len(raw) == 0
			}
			if !valid {
				p.refuse(w)
				return
			}
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				p.refuse(w)
				return
			}
			connection, _, e := hijacker.Hijack()
			if e != nil {
				p.mu.Lock()
				p.invalid = true
				p.mu.Unlock()
				return
			}
			p.mu.Lock()
			p.dropped++
			p.mu.Unlock()
			connection.Close()
			return
		}
		for name, values := range response.Header {
			if name == "Connection" || name == "Transfer-Encoding" || name == "Keep-Alive" {
				continue
			}
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		if _, e = io.Copy(w, response.Body); e != nil && ctx.Err() == nil {
			p.mu.Lock()
			p.invalid = true
			p.mu.Unlock()
		}
	}))
	t.Cleanup(func() { server.Close(); transport.CloseIdleConnections() })
	server.Config.ReadHeaderTimeout = time.Duration(c.OperationSeconds) * time.Second
	server.Config.ReadTimeout = time.Duration(c.TransferSeconds) * time.Second
	server.Config.WriteTimeout = time.Duration(c.TransferSeconds) * time.Second
	server.Config.IdleTimeout = time.Duration(c.OperationSeconds) * time.Second
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal("private wire certificate key generation")
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		t.Fatal("private wire certificate serial generation")
	}
	address, ok := server.Listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() {
		t.Fatal("OWNED_LOOPBACK_TLS_PROXY_REQUIRED")
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Synthetic M22 private wire proxy"}, NotBefore: now.Add(-time.Duration(c.OperationSeconds) * time.Second), NotAfter: now.Add(time.Duration(c.UploadSeconds+c.OperationSeconds) * time.Second), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true, IPAddresses: []net.IP{address.IP}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal("private wire certificate generation")
	}
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	proxyCA := filepath.Join(root, "wire-proxy-ca.pem")
	if e = os.WriteFile(proxyCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); e != nil {
		t.Fatal(e)
	}
	proxyURL, e := url.Parse(server.URL)
	if e != nil || net.ParseIP(proxyURL.Hostname()) == nil || !net.ParseIP(proxyURL.Hostname()).IsLoopback() {
		t.Fatal("OWNED_LOOPBACK_TLS_PROXY_REQUIRED")
	}
	c.Endpoint, c.CAFile, c.DialAddresses = server.URL, proxyCA, []string{proxyURL.Hostname()}
	return c
}

func (p *objectWireFault) refuse(w http.ResponseWriter) {
	p.mu.Lock()
	p.invalid = true
	p.mu.Unlock()
	w.WriteHeader(http.StatusBadGateway)
}
func (p *objectWireFault) arm(t *testing.T, kind string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.armed != "" || p.dropped != 0 || p.invalid {
		t.Fatal("wire fault was not cleanly armed")
	}
	p.armed = kind
}
func (p *objectWireFault) proof(t *testing.T, kind string, expected int) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.armed != "" || p.dropped != 1 || p.invalid || p.requests[kind] != expected {
		t.Fatal("actual upstream ACK loss/request count unverified", p.dropped, p.requests[kind])
	}
}

func TestActualPrivateObjectStorageWireFaults(t *testing.T) {
	for _, leaf := range []string{"create_ack_lost", "part_ack_lost", "complete_ack_lost", "delete_ack_lost"} {
		t.Run(leaf, func(t *testing.T) {
			proxy := &objectWireFault{requests: map[string]int{}}
			g := objectSetupWithS3(t, proxy.prepare)
			source := []byte("Synthetic actual S3 wire recovery \u4e16\u754c\n")
			o, _, _ := g.create(t, source)
			kind := leaf[:len(leaf)-len("_ack_lost")]
			if kind != "create" {
				g.work(t)
			}
			if kind == "complete" || kind == "delete" {
				g.parts(t, o, source)
				if _, e := g.s.Complete(g.ctx, g.portal, g.f.Workspace, o.ID, "Prepare actual wire completion", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				if kind == "delete" {
					g.work(t)
					if _, e := g.s.Delete(g.ctx, g.portal, g.f.Workspace, o.ID, "Prepare actual wire deletion", uuid.NewString()); e != nil {
						t.Fatal(e)
					}
				}
			}
			proxy.arm(t, kind)
			if kind == "part" {
				digest := sha256.Sum256(source)
				v, e := g.s.PutPart(g.ctx, g.portal, g.f.Workspace, o.ID, objectstore.Part{Number: 1, Bytes: int64(len(source)), SHA256: hex.EncodeToString(digest[:])}, bytes.NewReader(source), uuid.NewString())
				objectZero(t, v, e, "OBJECT_REMOTE_OUTCOME_UNKNOWN")
			} else if worked, e := g.s.ProcessNext(g.ctx); worked || e == nil || e.Error() != "OBJECT_REMOTE_OUTCOME_UNKNOWN" {
				t.Fatal("lost actual ACK projected worker success", e)
			}
			proxy.proof(t, kind, 1)
			if g.count(t, "SELECT count(*) FROM artifact_objects WHERE id=$1 AND operation_phase='sent' AND error_code='OBJECT_REMOTE_OUTCOME_UNKNOWN'", "SELECT count(*) FROM artifact_objects WHERE id=? AND operation_phase='sent' AND error_code='OBJECT_REMOTE_OUTCOME_UNKNOWN'", `SELECT COUNT(*) FROM "ARTIFACT_OBJECTS" WHERE "ID"=? AND "OPERATION_PHASE"='sent' AND "ERROR_CODE"='OBJECT_REMOTE_OUTCOME_UNKNOWN'`, o.ID) != 1 {
				t.Fatal("actual unknown sent generation missing")
			}
			ref := objectstore.Ref{Tenant: g.f.Tenant, Workspace: g.f.Workspace, Object: o.ID}
			switch kind {
			case "create":
				uploads, e := g.s.Transport.FindUploads(g.ctx, ref)
				if e != nil || len(uploads) != 1 {
					t.Fatal("actual accepted upload not independently visible", e)
				}
			case "part":
				var upload string
				if e := g.f.Store.QueryRow(g.ctx, statement(g.f.Store, "SELECT remote_upload_id FROM artifact_objects WHERE id=$1", "SELECT remote_upload_id FROM artifact_objects WHERE id=?", `SELECT "REMOTE_UPLOAD_ID" FROM "ARTIFACT_OBJECTS" WHERE "ID"=?`, o.ID)).Scan(&upload); e != nil {
					t.Fatal(e)
				}
				parts, e := g.s.Transport.Parts(g.ctx, ref, upload)
				if e != nil || len(parts) != 1 || parts[0].Number != 1 || parts[0].Bytes != int64(len(source)) {
					t.Fatal("actual accepted part not independently visible", e)
				}
			case "complete":
				info, e := g.s.Transport.Head(g.ctx, ref)
				if e != nil || info.Bytes != int64(len(source)) || info.Metadata["sha256"] != o.SHA256 {
					t.Fatal("actual completed object not independently visible", e)
				}
			case "delete":
				if _, e := g.s.Transport.Head(g.ctx, ref); e == nil || e.Error() != "OBJECT_NOT_FOUND" {
					t.Fatal("actual removal not independently confirmed", e)
				}
			}
			value, e := g.s.Reconcile(g.ctx, g.portal, g.f.Workspace, o.ID, "Reconcile exact actual remote ACK loss", uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			if kind == "delete" {
				if value.State != "deleted" {
					t.Fatal("actual deletion not confirmed")
				}
				proxy.proof(t, kind, 1)
				return
			}
			if kind == "create" {
				if value.State != "uploading" {
					t.Fatal("actual unique upload not adopted")
				}
				g.parts(t, o, source)
			}
			if kind == "create" || kind == "part" {
				if _, e = g.s.Complete(g.ctx, g.portal, g.f.Workspace, o.ID, "Complete recovered actual wire upload", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				g.work(t)
			}
			content, e := g.s.Download(g.ctx, g.portal, g.f.Workspace, o.ID, "")
			if e != nil {
				t.Fatal(e)
			}
			raw, e := io.ReadAll(content.File)
			content.File.Close()
			if e != nil || !bytes.Equal(raw, source) {
				t.Fatal("actual wire recovery changed whole source bytes", e)
			}
			if kind != "part" {
				proxy.proof(t, kind, 1)
			} else {
				proxy.mu.Lock()
				n := proxy.requests[kind]
				proxy.mu.Unlock()
				if n != 1 && n != 2 {
					t.Fatal("part replay exceeded exact retained source recovery")
				}
				proxy.proof(t, kind, n)
			}
		})
	}
}
