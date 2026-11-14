package httpserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func transportCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Synthetic transport fixture"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true, DNSNames: []string{"portal.test", "admin.test", "localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	private, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	certificate, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	if e != nil {
		t.Fatal(e)
	}
	leaf, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return certificate, roots
}

func transportTLSClient(t *testing.T, address string, roots *x509.CertPool) *http.Client {
	t.Helper()
	_, port, e := net.SplitHostPort(address)
	if e != nil {
		t.Fatal(e)
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, requested string) (net.Conn, error) {
		if requested != "portal.test:"+port && requested != "admin.test:"+port {
			return nil, context.Canceled
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", address)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second}
}

func transportEcho(server *config.ServerConfiguration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, e := ResolveBrowserRequest(r, *server)
		if e != nil {
			Problem(w, "synthetic-transport-request", e)
			return
		}
		cookies, e := NewCookiePolicy(value.Mode)
		if e != nil {
			panic(e)
		}
		cookies.Set(w, value.Audience, SessionCookie, "synthetic-session", 60)
		json.NewEncoder(w).Encode(struct {
			Connection RequestTransport
			SocketTLS  bool
		}{value, r.TLS != nil})
	})
}

func transportRead(t *testing.T, response *http.Response, expected int) []byte {
	t.Helper()
	defer response.Body.Close()
	data, e := io.ReadAll(io.LimitReader(response.Body, 8193))
	if e != nil || len(data) > 8192 || response.StatusCode != expected {
		t.Fatal("real transport response", response.StatusCode, e)
	}
	if expected != 200 && len(response.Cookies()) != 0 {
		t.Fatal("authentication cookie issued on transport refusal")
	}
	return data
}

func TestTransportActualTLSAuthorityAndVerifiedProxy(t *testing.T) {
	certificate, roots := transportCertificate(t)
	for _, mode := range []config.TransportMode{config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		t.Run(string(mode), func(t *testing.T) {
			server := transportFixture(mode)
			var backend *httptest.Server
			var entrance *httptest.Server
			if mode == config.TransportDirectHTTPS {
				entrance = httptest.NewUnstartedServer(transportEcho(&server))
			} else {
				backend = httptest.NewServer(transportEcho(&server))
				t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", backend.Listener.Addr())
				t.Cleanup(backend.Close)
				target, _ := url.Parse(backend.URL)
				proxyTransport := &http.Transport{Proxy: nil}
				t.Cleanup(proxyTransport.CloseIdleConnections)
				proxy := &httputil.ReverseProxy{Transport: proxyTransport, Rewrite: func(request *httputil.ProxyRequest) {
					request.SetURL(target)
					request.Out.Host = request.In.Host
					for key := range request.Out.Header {
						if forwardingHeader(strings.ToLower(key)) {
							request.Out.Header.Del(key)
						}
					}
					request.Out.Header.Set("X-Forwarded-Proto", "https")
					request.Out.Header.Set("X-Forwarded-Host", request.In.Host)
					client, _, _ := net.SplitHostPort(request.In.RemoteAddr)
					request.Out.Header.Set("X-Forwarded-For", client)
				}}
				entrance = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// The TLS terminator owns this check before rewriting headers.
					if r.TLS == nil || r.TLS.ServerName != strings.Split(r.Host, ":")[0] {
						w.WriteHeader(http.StatusMisdirectedRequest)
						return
					}
					proxy.ServeHTTP(w, r)
				}))
			}
			entrance.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
			_, port, _ := net.SplitHostPort(entrance.Listener.Addr().String())
			server.PortalOrigin, server.AdminOrigin = "https://portal.test:"+port, "https://admin.test:"+port
			t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", entrance.Listener.Addr())
			entrance.StartTLS()
			t.Cleanup(entrance.Close)
			client := transportTLSClient(t, entrance.Listener.Addr().String(), roots)
			for _, audience := range []string{"portal", "admin"} {
				r, _ := http.NewRequest(http.MethodGet, "https://"+audience+".test:"+port+"/", nil)
				if mode == config.TransportTrustedProxyHTTPS {
					r.Header.Set("Forwarded", "for=attacker;proto=http")
					r.Header.Set("X-Real-IP", "192.0.2.19")
					r.Header.Set("X-Forwarded-For", "192.0.2.19,192.0.2.20")
					r.Header.Set("X-Forwarded-Host", "foreign.test")
					r.Header.Set("X-Forwarded-Proto", "http")
				}
				response, e := client.Do(r)
				if e != nil {
					t.Fatal("verified normal TLS request", e)
				}
				var observed struct {
					Connection RequestTransport
					SocketTLS  bool
				}
				if json.Unmarshal(transportRead(t, response, 200), &observed) != nil || observed.Connection.Audience != audience || observed.Connection.ClientIP != "127.0.0.1" || observed.Connection.Forwarded != (mode == config.TransportTrustedProxyHTTPS) || observed.SocketTLS != (mode == config.TransportDirectHTTPS) {
					t.Fatal("real connection/proxy provenance", observed)
				}
				cookies := response.Cookies()
				if len(cookies) != 1 || cookies[0].Name != "__Host-"+audience+"_session" || !cookies[0].Secure {
					t.Fatal("TLS/proxy cookie policy")
				}
			}
			r, _ := http.NewRequest(http.MethodGet, server.AdminOrigin+"/", nil)
			r.Host = "portal.test:" + port
			response, e := client.Do(r)
			if e != nil {
				t.Fatal(e)
			}
			transportRead(t, response, 421)
			if backend != nil {
				r, _ := http.NewRequest(http.MethodGet, backend.URL+"/", nil)
				r.Host = "portal.test:" + port
				response, e := backend.Client().Do(r)
				if e != nil {
					t.Fatal(e)
				}
				transportRead(t, response, 403)
			}
			untrusted := transportTLSClient(t, entrance.Listener.Addr().String(), x509.NewCertPool())
			if response, e := untrusted.Get(server.PortalOrigin + "/"); e == nil {
				response.Body.Close()
				t.Fatal("untrusted certificate accepted")
			}
		})
	}
}

func TestTransportActualPlainListenerAndFailClosedSelector(t *testing.T) {
	if e := Serve(context.Background(), "127.0.0.1:0", nil, http.NotFoundHandler()); e == nil {
		t.Fatal("legacy TLS listener silently downgraded")
	}
	reserve, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := reserve.Addr().String()
	t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", address)
	reserve.Close()
	server := transportFixture(config.TransportHTTP)
	server.ListenAddress = address
	_, port, _ := net.SplitHostPort(address)
	server.PortalOrigin, server.AdminOrigin = "http://portal.test:"+port, "http://admin.test:"+port
	policy := config.DefaultRuntimePolicy().HTTP
	if e := ServeConfigured(context.Background(), server, &tls.Config{}, http.NotFoundHandler(), policy); e == nil {
		t.Fatal("HTTP listener accepted contradictory TLS material")
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- ServeConfigured(ctx, server, nil, transportEcho(&server), policy) }()
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-finished:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("owned plain listener did not join")
		}
		if connection, e := net.DialTimeout("tcp", address, 100*time.Millisecond); e == nil {
			connection.Close()
			t.Error("owned listener remains after shutdown")
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		connection, e := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if e == nil {
			connection.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("plain listener did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
	t.Cleanup(client.CloseIdleConnections)
	for _, audience := range []string{"portal", "admin"} {
		r, _ := http.NewRequest(http.MethodGet, "http://"+address+"/", nil)
		r.Host = audience + ".test:" + port
		response, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		transportRead(t, response, 200)
		cookies := response.Cookies()
		if len(cookies) != 1 || cookies[0].Name != "projectone_http_"+audience+"_session" || cookies[0].Secure {
			t.Fatal("explicit HTTP listener cookie mismatch")
		}
	}
}

// Actual IPv6 sockets exercise bracketed IP authority and localhost DNS/SNI
// without changing host DNS or trusting an unrelated certificate.
func TestTransportActualIPv6HTTPAndHTTPS(t *testing.T) {
	certificate, roots := transportCertificate(t)
	for _, mode := range []config.TransportMode{config.TransportHTTP, config.TransportDirectHTTPS} {
		t.Run(string(mode), func(t *testing.T) {
			listener, e := net.Listen("tcp6", "[::1]:0")
			if e != nil {
				t.Fatal("local IPv6 prerequisite", e)
			}
			address := listener.Addr().String()
			server := transportFixture(mode)
			scheme := "https"
			if mode == config.TransportHTTP {
				scheme = "http"
			}
			_, port, _ := net.SplitHostPort(address)
			server.ListenAddress = address
			server.PortalOrigin, server.AdminOrigin = scheme+"://localhost:"+port, scheme+"://[::1]:"+port
			entrance := httptest.NewUnstartedServer(transportEcho(&server))
			entrance.Listener.Close()
			entrance.Listener = listener
			if mode == config.TransportDirectHTTPS {
				entrance.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
				entrance.StartTLS()
			} else {
				entrance.Start()
			}
			defer entrance.Close()
			transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
				DialContext: func(ctx context.Context, network, requested string) (net.Conn, error) {
					if requested != "localhost:"+port && requested != "[::1]:"+port {
						return nil, context.Canceled
					}
					return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp6", address)
				}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			for index, origin := range []string{server.PortalOrigin, server.AdminOrigin} {
				response, e := client.Get(origin + "/")
				if e != nil {
					t.Fatal("verified IPv6 transport", e)
				}
				var observed struct {
					Connection RequestTransport
					SocketTLS  bool
				}
				if json.Unmarshal(transportRead(t, response, 200), &observed) != nil {
					t.Fatal("IPv6 response")
				}
				audience := []string{"portal", "admin"}[index]
				if observed.Connection.Audience != audience || observed.Connection.Origin != origin || observed.Connection.ClientIP != "::1" || observed.Connection.SocketPeerIP != "::1" || observed.SocketTLS != (mode == config.TransportDirectHTTPS) {
					t.Fatal("IPv6 authority/provenance", observed)
				}
				if len(response.Cookies()) != 1 || response.Cookies()[0].Secure != (mode == config.TransportDirectHTTPS) {
					t.Fatal("IPv6 cookie transport")
				}
			}
			t.Logf("actual IPv6 %s localhost and bracketed IP verified", mode)
		})
	}
}
