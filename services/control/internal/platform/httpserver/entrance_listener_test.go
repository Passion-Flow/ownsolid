package httpserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func entranceInputs(t *testing.T, mode config.TransportMode) (config.ServerConfiguration, config.MachineServerConfiguration, *tls.Config, *x509.CertPool) {
	t.Helper()
	browser, machine, material, roots := machineListenerInputs(t)
	browser.PortalListenAddress = machineUnusedAddress(t)
	if browser.PortalListenAddress == browser.ListenAddress || browser.PortalListenAddress == machine.ListenAddress {
		t.Fatal("fixture reused an owned address")
	}
	browser.TransportMode = mode
	scheme := "https"
	if mode == config.TransportHTTP {
		scheme = "http"
	}
	_, adminPort, _ := net.SplitHostPort(browser.ListenAddress)
	_, portalPort, _ := net.SplitHostPort(browser.PortalListenAddress)
	browser.AdminOrigin, browser.PortalOrigin = scheme+"://admin.test:"+adminPort, scheme+"://portal.test:"+portalPort
	if mode == config.TransportTrustedProxyHTTPS {
		browser.TrustedProxyCIDRs = []string{"127.0.0.1/32"}
	}
	return browser, machine, material, roots
}

func entranceClient(t *testing.T, roots *x509.CertPool, forceAddress string) *http.Client {
	t.Helper()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || network != "tcp" || host != "admin.test" && host != "portal.test" && host != "localhost" {
				return nil, context.Canceled
			}
			target := net.JoinHostPort("127.0.0.1", port)
			if forceAddress != "" {
				target = forceAddress
			}
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", target)
		}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return context.Canceled }}
}

func entranceRequest(t *testing.T, client *http.Client, mode config.TransportMode, origin, path string) *http.Response {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, origin+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if mode == config.TransportTrustedProxyHTTPS {
		r.URL.Scheme = "http"
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", r.Host)
		r.Header.Set("X-Forwarded-For", "192.0.2.20")
	}
	out, err := client.Do(r)
	if err != nil {
		t.Fatal("actual local entrance request failed", err)
	}
	return out
}

func entranceDiscard(client *http.Client, address string) <-chan error {
	done := make(chan error, 1)
	go func() {
		response, err := client.Get(address)
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		done <- err
	}()
	return done
}

func TestSplitBrowserListenersActualAuthorityAndSiblingRefusal(t *testing.T) {
	for _, mode := range []config.TransportMode{config.TransportHTTP, config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		t.Run(string(mode), func(t *testing.T) {
			browser, machine, material, roots := entranceInputs(t, mode)
			var browserTLS *tls.Config
			if mode == config.TransportDirectHTTPS {
				browserTLS = material
			}
			var admitted atomic.Int32
			echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				value, err := ResolveBrowserRequest(r, browser)
				if err != nil {
					t.Error("listener admitted an invalid transport")
					return
				}
				admitted.Add(1)
				json.NewEncoder(w).Encode(struct {
					Audience string
					TLS      bool
				}{value.Audience, r.TLS != nil})
			})
			group, err := ListenConfigured(browser, browserTLS, echo, &machine, material, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), config.DefaultRuntimePolicy().HTTP)
			if err != nil {
				t.Fatal(err)
			}
			cancel, done := machineStartGroup(t, group)
			client := entranceClient(t, roots, "")
			for _, entry := range []struct{ audience, origin, address string }{{"admin", browser.AdminOrigin, browser.ListenAddress}, {"portal", browser.PortalOrigin, browser.PortalListenAddress}} {
				response := entranceRequest(t, client, mode, entry.origin, "/")
				var got struct {
					Audience string
					TLS      bool
				}
				if json.Unmarshal(transportRead(t, response, 200), &got) != nil || got.Audience != entry.audience || got.TLS != (mode == config.TransportDirectHTTPS) {
					t.Fatal("wrong actual browser entrance", got)
				}
				sibling := browser.PortalListenAddress
				if entry.audience == "portal" {
					sibling = browser.ListenAddress
				}
				wrongSocket := entranceClient(t, roots, sibling)
				for _, path := range []string{"/", "/runtime-config.json", "/health/live", "/health/ready", "/api/v1/me"} {
					response = entranceRequest(t, wrongSocket, mode, entry.origin, path)
					var problem map[string]any
					if response.Header.Get("X-Request-ID") == "" || response.Header.Get("Cache-Control") != "no-store" || json.Unmarshal(transportRead(t, response, 421), &problem) != nil || problem["code"] != "MISDIRECTED_REQUEST" {
						t.Fatal("wrong-socket request lacked a correlated refusal")
					}
				}
			}
			if admitted.Load() != 2 {
				t.Fatal("wrong-socket request reached the business handler")
			}
			metrics := group.Samples()
			if len(metrics) != 2 {
				t.Fatal("split rejection metrics are absent")
			}
			for index, audience := range []string{"admin", "portal"} {
				metric := metrics[index]
				if metric.Name != "ownsolid_http_entrance_rejections_total" || metric.Value != 5 || len(metric.Labels) != 1 || metric.Labels[0].Name != "audience" || metric.Labels[0].Value != audience {
					t.Fatal("rejections were not counted on their actual listener", metric)
				}
			}
			transportRead(t, entranceRequest(t, client, config.TransportDirectHTTPS, machine.Origin, "/"), 204)
			cancel()
			if err := machineAwaitServe(t, done); err != nil {
				t.Fatal(err)
			}
			machineAssertReleased(t, browser.ListenAddress, browser.PortalListenAddress, machine.ListenAddress)
		})
	}
}

func TestSplitBrowserListenerRetainsForwardingAndSNIChecks(t *testing.T) {
	for _, mode := range []config.TransportMode{config.TransportHTTP, config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		server := transportFixture(mode)
		next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("confused transport reached the business handler") })
		for _, change := range []func(*http.Request){
			func(r *http.Request) { r.Host = "foreign.test" },
			func(r *http.Request) {
				if r.TLS != nil {
					r.TLS.ServerName = "admin.test"
				} else {
					r.Header.Set("Forwarded", "host=portal.test")
				}
			},
		} {
			r := transportRequest(mode)
			change(r)
			w := httptest.NewRecorder()
			var rejected atomic.Uint64
			browserEntrance(server, "portal", next, config.DefaultHTTPPolicy(), &rejected).ServeHTTP(w, r)
			if w.Code < 400 || w.Header().Get("X-Request-ID") == "" {
				t.Fatal("transport confusion was not refused")
			}
			if rejected.Load() != 1 {
				t.Fatal("transport refusal was not counted")
			}
		}
	}
}

func TestSplitBrowserListenersReleaseEarlierBindsOnFailure(t *testing.T) {
	for _, position := range []string{"portal", "machine", "portal_alias", "machine_alias"} {
		t.Run(position, func(t *testing.T) {
			browser, machine, material, _ := entranceInputs(t, config.TransportHTTP)
			occupied := browser.PortalListenAddress
			if position == "machine" || position == "machine_alias" {
				occupied = machine.ListenAddress
			}
			foreign, err := net.Listen("tcp", occupied)
			if err != nil {
				t.Fatal(err)
			}
			defer foreign.Close()
			if position == "machine_alias" {
				_, port, _ := net.SplitHostPort(browser.PortalListenAddress)
				machine.ListenAddress = "0.0.0.0:" + port
			}
			if position == "portal_alias" {
				_, port, _ := net.SplitHostPort(browser.ListenAddress)
				browser.PortalListenAddress = "0.0.0.0:" + port
			}
			group, err := ListenConfigured(browser, nil, http.NotFoundHandler(), &machine, material, http.NotFoundHandler(), config.DefaultHTTPPolicy())
			if err == nil || !strings.Contains(err.Error(), "HTTP_LISTENER_BIND_FAILED") || group != nil {
				if group != nil {
					group.Close()
				}
				t.Fatal("occupied listener was admitted", err)
			}
			machineAssertReleased(t, browser.ListenAddress)
			if position != "portal" {
				machineAssertReleased(t, browser.PortalListenAddress)
			}
			if replacement, err := net.Listen("tcp", occupied); err == nil {
				replacement.Close()
				t.Fatal("failure closed the separately owned socket")
			}
		})
	}
}

func TestSplitBrowserListenersShareOneConnectionBudget(t *testing.T) {
	browser, machine, material, roots := entranceInputs(t, config.TransportHTTP)
	entered := make(chan string, 3)
	release := make(map[string]chan struct{})
	for _, audience := range []string{"admin", "portal", "machine"} {
		release[audience] = make(chan struct{})
	}
	var unblocked sync.Once
	unblock := func() {
		unblocked.Do(func() {
			for _, c := range release {
				close(c)
			}
		})
	}
	handler := func(machineIngress bool) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			audience := "machine"
			if !machineIngress {
				value, err := ResolveBrowserRequest(r, browser)
				if err != nil {
					t.Error(err)
					return
				}
				audience = value.Audience
			}
			if r.URL.Path == "/hold" {
				entered <- audience
				select {
				case <-release[audience]:
				case <-r.Context().Done():
				}
			}
		})
	}
	policy := config.DefaultHTTPPolicy()
	policy.MaxConnections = 1
	policy.MaxActiveRequests = 1
	group, err := ListenConfigured(browser, nil, handler(false), &machine, material, handler(true), policy)
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := machineStartGroup(t, group)
	t.Cleanup(unblock)
	client := entranceClient(t, roots, "")
	held := entranceDiscard(client, browser.AdminOrigin+"/hold")
	select {
	case got := <-entered:
		if got != "admin" {
			t.Fatal("wrong holder")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Admin did not hold its socket")
	}
	for _, origin := range []string{browser.PortalOrigin, machine.Origin} {
		if response, err := client.Get(origin + "/"); err == nil {
			response.Body.Close()
			t.Fatal("sibling acquired an independent connection budget")
		}
	}
	unblock()
	if err := machineAwaitServe(t, held); err != nil {
		t.Fatal(err)
	}
	budget := group.endpoints[0].listener.(*bounded).slots
	for deadline := time.Now().Add(2 * time.Second); len(budget) != 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("connection budget was not returned")
		}
	}
	for _, origin := range []string{browser.PortalOrigin, machine.Origin} {
		response, err := client.Get(origin + "/")
		if err != nil {
			t.Fatal(err)
		}
		transportRead(t, response, 200)
	}
	cancel()
	if err := machineAwaitServe(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestSplitBrowserListenersJoinAllRequestCleanups(t *testing.T) {
	for _, siblingFailure := range []bool{false, true} {
		browser, machine, material, roots := entranceInputs(t, config.TransportHTTP)
		entered, cancelled := make(chan struct{}, 3), make(chan struct{}, 3)
		release := make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			entered <- struct{}{}
			<-r.Context().Done()
			cancelled <- struct{}{}
			<-release
		})
		group, err := ListenConfigured(browser, nil, handler, &machine, material, handler, config.DefaultHTTPPolicy())
		if err != nil {
			t.Fatal(err)
		}
		cancel, done := machineStartGroup(t, group)
		t.Cleanup(unblock)
		client := entranceClient(t, roots, "")
		requests := []<-chan error{}
		for _, origin := range []string{browser.AdminOrigin, browser.PortalOrigin, machine.Origin} {
			requests = append(requests, machineDiscardResponse(client, origin))
			machineAwait(t, entered, "request did not enter")
		}
		if siblingFailure {
			group.endpoints[1].listener.Close()
		} else {
			cancel()
		}
		for range 3 {
			machineAwait(t, cancelled, "a sibling request was not cancelled")
		}
		select {
		case <-done:
			t.Fatal("Serve returned before request cleanup")
		default:
		}
		unblock()
		err = machineAwaitServe(t, done)
		if (err != nil) != siblingFailure {
			t.Fatal("wrong joined listener result", err)
		}
		for _, request := range requests {
			if err := machineAwaitServe(t, request); err != nil {
				t.Fatal(err)
			}
		}
		machineAssertReleased(t, browser.ListenAddress, browser.PortalListenAddress, machine.ListenAddress)
	}
}

func TestServeConfiguredHonorsSplitBrowserSelection(t *testing.T) {
	browser, _, _, roots := entranceInputs(t, config.TransportHTTP)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- ServeConfigured(ctx, browser, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), config.DefaultHTTPPolicy())
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(4 * time.Second):
			t.Error("configured server did not exit")
		}
	})
	client := entranceClient(t, roots, "")
	for _, origin := range []string{browser.AdminOrigin, browser.PortalOrigin} {
		deadline := time.Now().Add(3 * time.Second)
		for {
			response, err := client.Get(origin + "/")
			if err == nil {
				transportRead(t, response, 204)
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("configured split listener unavailable", err)
			}
			time.Sleep(time.Millisecond)
		}
	}
	wrong := entranceClient(t, roots, browser.ListenAddress)
	transportRead(t, entranceRequest(t, wrong, config.TransportHTTP, browser.PortalOrigin, "/"), 421)
	cancel()
	if err := machineAwaitServe(t, done); err != nil {
		t.Fatal(err)
	}
	machineAssertReleased(t, browser.ListenAddress, browser.PortalListenAddress)
}
