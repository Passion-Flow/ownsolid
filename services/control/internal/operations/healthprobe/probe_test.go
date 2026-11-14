package healthprobe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const syntheticEnvelope = `{"data":{"status":"ok"}}`

// Every server in this package is an explicitly synthetic health responder.
// These tests exercise probe mechanics, never actual product/database readiness.
type syntheticHealthObservation struct {
	mu       sync.Mutex
	requests []syntheticHealthRequest
	sni      []string
	active   map[net.Conn]bool
	accepts  atomic.Int32
}

type syntheticHealthRequest struct {
	method, host, path, query string
	headers                   http.Header
	tls                       bool
	contentLength             int64
}

func (o *syntheticHealthObservation) record(r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests = append(o.requests, syntheticHealthRequest{r.Method, r.Host, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), r.TLS != nil, r.ContentLength})
}

func (o *syntheticHealthObservation) snapshot() ([]syntheticHealthRequest, []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]syntheticHealthRequest(nil), o.requests...), append([]string(nil), o.sni...)
}

type syntheticCountingListener struct {
	net.Listener
	observation *syntheticHealthObservation
}

func (l *syntheticCountingListener) Accept() (net.Conn, error) {
	c, e := l.Listener.Accept()
	if e == nil {
		l.observation.accepts.Add(1)
	}
	return c, e
}

func syntheticHealthServer(t *testing.T, network string, certificate *tls.Certificate, handler http.Handler, observation *syntheticHealthObservation) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observation.record(r)
		handler.ServeHTTP(w, r)
	}))
	server.Listener.Close()
	address := "127.0.0.1:0"
	if network == "tcp6" {
		address = "[::1]:0"
	}
	listener, e := net.Listen(network, address)
	if e != nil {
		t.Fatal("synthetic owned loopback prerequisite", e)
	}
	server.Listener = &syntheticCountingListener{listener, observation}
	observation.active = map[net.Conn]bool{}
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Config.ConnState = func(c net.Conn, state http.ConnState) {
		observation.mu.Lock()
		defer observation.mu.Unlock()
		if state == http.StateNew {
			observation.active[c] = true
		}
		if state == http.StateClosed || state == http.StateHijacked {
			delete(observation.active, c)
		}
	}
	if certificate == nil {
		server.Start()
	} else {
		server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*certificate}, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			observation.mu.Lock()
			observation.sni = append(observation.sni, hello.ServerName)
			observation.mu.Unlock()
			return nil, nil
		}}
		server.StartTLS()
	}
	t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", server.Listener.Addr())
	t.Cleanup(func() {
		server.Close()
		deadline := time.Now().Add(2 * time.Second)
		for {
			observation.mu.Lock()
			active := len(observation.active)
			observation.mu.Unlock()
			if active == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Error("synthetic health connections were not joined")
				return
			}
			time.Sleep(time.Millisecond)
		}
	})
	return server
}

func syntheticCertificate(t *testing.T, dns []string, ips []net.IP, expired bool) (tls.Certificate, []byte) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		t.Fatal(e)
	}
	serial.Add(serial, big.NewInt(1))
	now := time.Now()
	before, after := now.Add(-time.Minute), now.Add(time.Hour)
	if expired {
		before, after = now.Add(-2*time.Hour), now.Add(-time.Hour)
	}
	// Process-local self-signed CA/server fixture, never installed in shared trust.
	template := &x509.Certificate{SerialNumber: serial, NotBefore: before, NotAfter: after, DNSNames: dns, IPAddresses: ips, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func syntheticTrustPath(t *testing.T, raw []byte) string {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	directory := filepath.Join(base, "private")
	if e = os.Mkdir(directory, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(directory, "trust.pem")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}

func syntheticHealthOK(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Request-ID", "synthetic-health-request")
	w.WriteHeader(200)
	_, _ = io.WriteString(w, syntheticEnvelope)
}

func syntheticServerConfiguration(server *httptest.Server, mode config.TransportMode) config.ServerConfiguration {
	scheme := "https"
	if mode == config.TransportHTTP {
		scheme = "http"
	}
	value := config.ServerConfiguration{TransportMode: mode, ListenAddress: server.Listener.Addr().String(), PortalOrigin: scheme + "://portal.example.invalid", AdminOrigin: scheme + "://admin.example.invalid"}
	if mode == config.TransportTrustedProxyHTTPS {
		value.TrustedProxyCIDRs = []string{"127.0.0.1/32"}
	}
	return value
}

func syntheticInput(server *httptest.Server, ca string) Input {
	return Input{Audience: "portal", Kind: "ready", DialAddress: server.Listener.Addr().String(), CAFile: ca, TimeoutSeconds: 5}
}

func healthFailure(t *testing.T, e error, expected string) {
	t.Helper()
	if e == nil || e.Error() != expected || errors.Unwrap(e) != nil {
		t.Fatalf("expected fixed redacted %s, observed %v", expected, e)
	}
}

func syntheticPlainRequest(t *testing.T, request syntheticHealthRequest, host, path string, secure bool) {
	t.Helper()
	if request.method != "GET" || request.host != host || request.path != path || request.query != "" || request.tls != secure || request.contentLength != 0 || request.headers.Get("Authorization") != "" || request.headers.Get("Proxy-Authorization") != "" || request.headers.Get("Cookie") != "" {
		t.Fatal("probe changed the selected reference/path or supplied request authority")
	}
	for key := range request.headers {
		lower := strings.ToLower(key)
		if lower == "forwarded" || lower == "x-real-ip" || strings.HasPrefix(lower, "x-forwarded") {
			t.Fatal("probe manufactured proxy provenance")
		}
	}
}

func TestHealthProbeActualTransportModesAndEntrances(t *testing.T) {
	for _, mode := range []config.TransportMode{config.TransportHTTP, config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		for _, audience := range []string{"portal", "admin"} {
			for _, kind := range []string{"live", "ready"} {
				t.Run(string(mode)+"_"+audience+"_"+kind, func(t *testing.T) {
					observation := &syntheticHealthObservation{}
					var server, backend *httptest.Server
					var backendObservation *syntheticHealthObservation
					var ca string
					if mode == config.TransportHTTP {
						server = syntheticHealthServer(t, "tcp4", nil, http.HandlerFunc(syntheticHealthOK), observation)
					} else {
						certificate, bundle := syntheticCertificate(t, []string{"portal.example.invalid", "admin.example.invalid"}, nil, false)
						ca = syntheticTrustPath(t, bundle)
						var handler http.Handler = http.HandlerFunc(syntheticHealthOK)
						if mode == config.TransportTrustedProxyHTTPS {
							backendObservation = &syntheticHealthObservation{}
							backend = syntheticHealthServer(t, "tcp4", nil, http.HandlerFunc(syntheticHealthOK), backendObservation)
							target, e := url.Parse(backend.URL)
							if e != nil {
								t.Fatal(e)
							}
							roundTrip := http.DefaultTransport.(*http.Transport).Clone()
							roundTrip.Proxy = nil
							t.Cleanup(roundTrip.CloseIdleConnections)
							handler = &httputil.ReverseProxy{Transport: roundTrip, Rewrite: func(p *httputil.ProxyRequest) {
								p.SetURL(target)
								p.Out.Host = p.In.Host
								peer, _, e := net.SplitHostPort(p.In.RemoteAddr)
								if e != nil {
									panic("synthetic actual ingress socket invalid")
								}
								p.Out.Header.Set("X-Forwarded-Proto", "https")
								p.Out.Header.Set("X-Forwarded-Host", p.In.Host)
								p.Out.Header.Set("X-Forwarded-For", peer)
							}}
						}
						server = syntheticHealthServer(t, "tcp4", &certificate, handler, observation)
					}
					configuration := syntheticServerConfiguration(server, mode)
					if backend != nil {
						configuration.ListenAddress = backend.Listener.Addr().String()
					}
					input := syntheticInput(server, ca)
					input.Audience, input.Kind = audience, kind
					if e := Run(context.Background(), configuration, input); e != nil {
						t.Fatal(e)
					}
					requests, sni := observation.snapshot()
					if len(requests) != 1 || observation.accepts.Load() != 1 {
						t.Fatal("probe did not use exactly one request/dial")
					}
					syntheticPlainRequest(t, requests[0], audience+".example.invalid", "/health/"+kind, mode != config.TransportHTTP)
					if mode != config.TransportHTTP && (len(sni) != 1 || sni[0] != audience+".example.invalid") {
						t.Fatal("declared DNS reference was not the actual TLS SNI")
					}
					if backendObservation != nil {
						requests, _ = backendObservation.snapshot()
						if len(requests) != 1 || requests[0].tls || requests[0].host != audience+".example.invalid" || requests[0].headers.Get("X-Forwarded-For") != "127.0.0.1" || requests[0].headers.Get("X-Forwarded-Host") != requests[0].host || requests[0].headers.Get("X-Forwarded-Proto") != "https" {
							t.Fatal("synthetic real TLS terminator did not preserve isolated backend provenance")
						}
					}
				})
			}
		}
	}
}

func TestHealthProbeActualAddressReferenceMatrix(t *testing.T) {
	for _, address := range []struct{ name, reference, network, port string }{
		{"dns", "portal.example.invalid", "tcp4", ""},
		{"dns_nondefault", "portal.example.invalid", "tcp4", "9443"},
		{"localhost", "localhost", "tcp4", "9443"},
		{"ipv4", "127.0.0.1", "tcp4", "9443"},
		{"ipv6", "::1", "tcp6", "9443"},
	} {
		for _, secure := range []bool{false, true} {
			suffix := "_http"
			if secure {
				suffix = "_https"
			}
			t.Run(address.name+suffix, func(t *testing.T) {
				observation := &syntheticHealthObservation{}
				var certificate *tls.Certificate
				var ca string
				mode := config.TransportHTTP
				scheme := "http"
				if secure {
					value, raw := syntheticCertificate(t, []string{"portal.example.invalid", "localhost"}, []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, false)
					certificate, ca = &value, syntheticTrustPath(t, raw)
					mode, scheme = config.TransportDirectHTTPS, "https"
				}
				server := syntheticHealthServer(t, address.network, certificate, http.HandlerFunc(syntheticHealthOK), observation)
				configuration := syntheticServerConfiguration(server, mode)
				authority := address.reference
				if address.port != "" {
					authority = net.JoinHostPort(address.reference, address.port)
				}
				configuration.PortalOrigin = scheme + "://" + authority
				if e := Run(context.Background(), configuration, syntheticInput(server, ca)); e != nil {
					t.Fatal(e)
				}
				requests, sni := observation.snapshot()
				if len(requests) != 1 || observation.accepts.Load() != 1 {
					t.Fatal("reference mapping changed request/dial count")
				}
				syntheticPlainRequest(t, requests[0], authority, "/health/ready", secure)
				if secure {
					wanted := address.reference
					if net.ParseIP(address.reference) != nil {
						wanted = ""
					}
					if len(sni) != 1 || sni[0] != wanted {
						t.Fatal("IP/DNS reference used incorrect actual SNI")
					}
				}
			})
		}
	}
}

func TestHealthProbeCertificateFailures(t *testing.T) {
	for _, name := range []string{"unknown_ca", "wrong_dns", "wrong_ip", "expired"} {
		t.Run(name, func(t *testing.T) {
			dns, ips := []string{"portal.example.invalid"}, []net.IP{net.ParseIP("127.0.0.1")}
			if name == "wrong_dns" {
				dns = []string{"another.example.invalid"}
			}
			if name == "wrong_ip" {
				ips = []net.IP{net.ParseIP("192.0.2.1")}
			}
			certificate, raw := syntheticCertificate(t, dns, ips, name == "expired")
			if name == "unknown_ca" {
				_, raw = syntheticCertificate(t, dns, ips, false)
			}
			observation := &syntheticHealthObservation{}
			server := syntheticHealthServer(t, "tcp4", &certificate, http.HandlerFunc(syntheticHealthOK), observation)
			configuration := syntheticServerConfiguration(server, config.TransportDirectHTTPS)
			if name == "wrong_ip" {
				configuration.PortalOrigin = "https://127.0.0.1"
			}
			healthFailure(t, Run(context.Background(), configuration, syntheticInput(server, syntheticTrustPath(t, raw))), transportFailed)
			requests, _ := observation.snapshot()
			if len(requests) != 0 || observation.accepts.Load() != 1 {
				t.Fatal("failed certificate reached health handler or retried dial")
			}
		})
	}
}

func TestHealthProbeNoRedirectRetryProxyOrCredentialHeaders(t *testing.T) {
	trapObservation := &syntheticHealthObservation{}
	trap := syntheticHealthServer(t, "tcp4", nil, http.HandlerFunc(syntheticHealthOK), trapObservation)
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, trap.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	for _, name := range []string{"redirect", "unavailable", "closed_response", "valid_ignores_ambient_proxy", "tls_ignores_ambient_proxy"} {
		t.Run(name, func(t *testing.T) {
			observation := &syntheticHealthObservation{}
			var certificate *tls.Certificate
			var ca string
			mode := config.TransportHTTP
			if name == "tls_ignores_ambient_proxy" {
				value, raw := syntheticCertificate(t, []string{"portal.example.invalid"}, nil, false)
				certificate, ca, mode = &value, syntheticTrustPath(t, raw), config.TransportDirectHTTPS
			}
			server := syntheticHealthServer(t, "tcp4", certificate, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch name {
				case "redirect":
					w.Header().Set("Location", "https://foreign.example.invalid/login")
					w.WriteHeader(302)
				case "unavailable":
					w.WriteHeader(503)
				case "closed_response":
					connection, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					connection.Close()
				default:
					syntheticHealthOK(w, r)
				}
			}), observation)
			e := Run(context.Background(), syntheticServerConfiguration(server, mode), syntheticInput(server, ca))
			if name == "valid_ignores_ambient_proxy" || name == "tls_ignores_ambient_proxy" {
				if e != nil {
					t.Fatal(e)
				}
			} else if name == "closed_response" {
				healthFailure(t, e, transportFailed)
			} else {
				healthFailure(t, e, responseInvalid)
			}
			requests, _ := observation.snapshot()
			if len(requests) != 1 || observation.accepts.Load() != 1 {
				t.Fatal("probe followed a redirect, retried or opened extra sockets")
			}
			syntheticPlainRequest(t, requests[0], "portal.example.invalid", "/health/ready", certificate != nil)
		})
	}
	if requests, _ := trapObservation.snapshot(); len(requests) != 0 || trapObservation.accepts.Load() != 0 {
		t.Fatal("ambient proxy received a probe")
	}
}

func TestHealthProbeStrictResponseEnvelope(t *testing.T) {
	for _, item := range []struct {
		name, body, contentType, cache, requestID string
		status                                    int
		valid, duplicateCache, truncated          bool
		omitType, omitCache, omitID               bool
		duplicateType, duplicateID                bool
	}{
		{name: "exact", body: syntheticEnvelope, valid: true},
		{name: "exact_boundary", body: syntheticEnvelope + strings.Repeat(" ", int(maximumResponseBytes)-len(syntheticEnvelope)), valid: true},
		{name: "whitespace", body: " { \"data\" : { \"status\" : \"ok\" } } \n", valid: true},
		{name: "unknown_root", body: `{"data":{"status":"ok"},"extra":true}`},
		{name: "duplicate_root", body: `{"data":{"status":"ok"},"data":{"status":"ok"}}`},
		{name: "duplicate_status", body: `{"data":{"status":"ok","status":"ok"}}`},
		{name: "unknown_data", body: `{"data":{"status":"ok","extra":true}}`},
		{name: "null_root", body: `null`},
		{name: "null_data", body: `{"data":null}`},
		{name: "null_status", body: `{"data":{"status":null}}`},
		{name: "different_status", body: `{"data":{"status":"ready"}}`},
		{name: "malformed", body: `{"data":{"status":"ok"}`},
		{name: "empty", body: ""},
		{name: "trailing_json", body: syntheticEnvelope + `{}`},
		{name: "trailing_garbage", body: syntheticEnvelope + `trailing`},
		{name: "oversize", body: syntheticEnvelope + strings.Repeat(" ", int(maximumResponseBytes)+1-len(syntheticEnvelope))},
		{name: "truncated", body: syntheticEnvelope, truncated: true},
		{name: "html", body: `<html>login</html>`, contentType: "text/html"},
		{name: "bad_mime", body: syntheticEnvelope, contentType: "application/json; broken"},
		{name: "wrong_cache", body: syntheticEnvelope, cache: "public"},
		{name: "duplicate_cache", body: syntheticEnvelope, duplicateCache: true},
		{name: "missing_type", body: syntheticEnvelope, omitType: true},
		{name: "missing_cache", body: syntheticEnvelope, omitCache: true},
		{name: "missing_request_id", body: syntheticEnvelope, omitID: true},
		{name: "duplicate_type", body: syntheticEnvelope, duplicateType: true},
		{name: "duplicate_request_id", body: syntheticEnvelope, duplicateID: true},
		{name: "empty_request_id", body: syntheticEnvelope, requestID: " "},
		{name: "unavailable", body: syntheticEnvelope, status: 503},
	} {
		t.Run(item.name, func(t *testing.T) {
			observation := &syntheticHealthObservation{}
			server := syntheticHealthServer(t, "tcp4", nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				media, cache, request := item.contentType, item.cache, item.requestID
				if media == "" {
					media = "application/json; charset=utf-8"
				}
				if cache == "" {
					cache = "no-store"
				}
				if request == "" {
					request = "synthetic-envelope-request"
				}
				if !item.omitType {
					w.Header().Set("Content-Type", media)
				}
				if !item.omitCache {
					w.Header().Set("Cache-Control", cache)
				}
				if !item.omitID {
					w.Header().Set("X-Request-ID", request)
				}
				if item.duplicateCache {
					w.Header().Add("Cache-Control", "no-store")
				}
				if item.duplicateType {
					w.Header().Add("Content-Type", "application/json")
				}
				if item.duplicateID {
					w.Header().Add("X-Request-ID", "synthetic-second-request")
				}
				if item.truncated {
					w.Header().Set("Content-Length", "100")
				}
				status := item.status
				if status == 0 {
					status = 200
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, item.body)
			}), observation)
			e := Run(context.Background(), syntheticServerConfiguration(server, config.TransportHTTP), syntheticInput(server, ""))
			if item.valid {
				if e != nil {
					t.Fatal(e)
				}
			} else {
				healthFailure(t, e, responseInvalid)
			}
			if requests, _ := observation.snapshot(); len(requests) != 1 || observation.accepts.Load() != 1 {
				t.Fatal("invalid response was replayed")
			}
		})
	}
}

func TestHealthProbeActualDeadlineAndCancellation(t *testing.T) {
	for _, name := range []string{"delayed_headers", "delayed_body", "earlier_parent_deadline", "inflight_cancel", "already_cancelled"} {
		t.Run(name, func(t *testing.T) {
			entered := make(chan struct{})
			observation := &syntheticHealthObservation{}
			server := syntheticHealthServer(t, "tcp4", nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				if name == "delayed_body" {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Cache-Control", "no-store")
					w.Header().Set("X-Request-ID", "synthetic-delayed-body")
					w.WriteHeader(200)
					_, _ = io.WriteString(w, `{"data":`)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}), observation)
			input := syntheticInput(server, "")
			input.TimeoutSeconds = 1
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "earlier_parent_deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer deadlineCancel()
				input.TimeoutSeconds = 5
			}
			if name == "already_cancelled" {
				cancel()
			}
			start := time.Now()
			result := make(chan error, 1)
			go func() { result <- Run(ctx, syntheticServerConfiguration(server, config.TransportHTTP), input) }()
			if name == "inflight_cancel" {
				select {
				case <-entered:
					cancel()
				case <-time.After(2 * time.Second):
					t.Fatal("actual probe never reached delayed responder")
				}
			}
			var e error
			select {
			case e = <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("bounded probe did not terminate")
			}
			wanted := deadlineExceeded
			if name == "inflight_cancel" || name == "already_cancelled" {
				wanted = cancelled
			}
			healthFailure(t, e, wanted)
			if time.Since(start) > 2*time.Second {
				t.Fatal("probe exceeded selected/parent bound plus local scheduling allowance")
			}
			requests, _ := observation.snapshot()
			expected := 1
			if name == "already_cancelled" {
				expected = 0
			}
			if len(requests) != expected || int(observation.accepts.Load()) != expected {
				t.Fatal("cancelled/deadline operation dialed or replayed unexpectedly")
			}
		})
	}
}

func TestHealthProbeRejectsInputsBeforeDial(t *testing.T) {
	observation := &syntheticHealthObservation{}
	server := syntheticHealthServer(t, "tcp4", nil, http.HandlerFunc(syntheticHealthOK), observation)
	base := syntheticServerConfiguration(server, config.TransportHTTP)
	for _, item := range []struct {
		name string
		edit func(*Input, *config.ServerConfiguration)
		code string
	}{
		{"audience", func(i *Input, _ *config.ServerConfiguration) { i.Audience = "system" }, argumentInvalid},
		{"kind", func(i *Input, _ *config.ServerConfiguration) { i.Kind = "health" }, argumentInvalid},
		{"timeout_zero", func(i *Input, _ *config.ServerConfiguration) { i.TimeoutSeconds = 0 }, argumentInvalid},
		{"timeout_high", func(i *Input, _ *config.ServerConfiguration) { i.TimeoutSeconds = 61 }, argumentInvalid},
		{"dial_dns", func(i *Input, _ *config.ServerConfiguration) { i.DialAddress = "localhost:80" }, argumentInvalid},
		{"dial_unspecified", func(i *Input, _ *config.ServerConfiguration) { i.DialAddress = "0.0.0.0:80" }, argumentInvalid},
		{"dial_multicast", func(i *Input, _ *config.ServerConfiguration) { i.DialAddress = "224.0.0.1:80" }, argumentInvalid},
		{"dial_mapped", func(i *Input, _ *config.ServerConfiguration) { i.DialAddress = "[::ffff:127.0.0.1]:80" }, argumentInvalid},
		{"dial_zone", func(i *Input, _ *config.ServerConfiguration) { i.DialAddress = "[fe80::1%lo0]:80" }, argumentInvalid},
		{"dial_unbracketed", func(i *Input, _ *config.ServerConfiguration) { i.DialAddress = "::1:80" }, argumentInvalid},
		{"dial_leading_zero", func(i *Input, _ *config.ServerConfiguration) { i.DialAddress = "127.0.0.1:080" }, argumentInvalid},
		{"dial_zero_port", func(i *Input, _ *config.ServerConfiguration) { i.DialAddress = "127.0.0.1:0" }, argumentInvalid},
		{"dial_high_port", func(i *Input, _ *config.ServerConfiguration) { i.DialAddress = "127.0.0.1:65536" }, argumentInvalid},
		{"ca_relative", func(i *Input, _ *config.ServerConfiguration) { i.CAFile = "trust.pem" }, argumentInvalid},
		{"ca_noncanonical", func(i *Input, _ *config.ServerConfiguration) {
			i.CAFile = filepath.Join(os.TempDir(), "synthetic-health") + "/../trust.pem"
		}, argumentInvalid},
		{"http_forbidden_ca", func(i *Input, _ *config.ServerConfiguration) {
			i.CAFile = filepath.Join(os.TempDir(), "unused-synthetic-trust.pem")
		}, argumentInvalid},
		{"unknown_mode", func(_ *Input, c *config.ServerConfiguration) { c.TransportMode = "false" }, configurationInvalid},
		{"scheme_mismatch", func(_ *Input, c *config.ServerConfiguration) { c.PortalOrigin = "https://portal.example.invalid" }, configurationInvalid},
		{"same_origin", func(_ *Input, c *config.ServerConfiguration) { c.AdminOrigin = c.PortalOrigin }, configurationInvalid},
		{"unknown_proxy", func(_ *Input, c *config.ServerConfiguration) { c.TrustedProxyCIDRs = []string{"127.0.0.1/32"} }, configurationInvalid},
		{"missing_https_ca", func(_ *Input, c *config.ServerConfiguration) {
			c.TransportMode = config.TransportDirectHTTPS
			c.PortalOrigin = "https://portal.example.invalid"
			c.AdminOrigin = "https://admin.example.invalid"
		}, argumentInvalid},
	} {
		t.Run(item.name, func(t *testing.T) {
			input, configuration := syntheticInput(server, ""), base
			item.edit(&input, &configuration)
			healthFailure(t, Run(context.Background(), configuration, input), item.code)
			if e := ValidateInput(input); item.code == argumentInvalid && item.name != "http_forbidden_ca" && item.name != "missing_https_ca" {
				healthFailure(t, e, argumentInvalid)
			}
		})
	}
	healthFailure(t, Run(nil, base, syntheticInput(server, "")), argumentInvalid)
	// Standalone validation accepts a canonical reference without opening it.
	noIO := syntheticInput(server, filepath.Join(t.TempDir(), "does-not-exist", "trust.pem"))
	if e := ValidateInput(noIO); e != nil {
		t.Fatal("standalone input validation performed trust I/O", e)
	}
	if requests, _ := observation.snapshot(); len(requests) != 0 || observation.accepts.Load() != 0 {
		t.Fatal("invalid input/configuration reached a network listener")
	}
}
