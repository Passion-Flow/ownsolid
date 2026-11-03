package httpserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// All certificates, addresses and handlers in this file are synthetic local
// transport fixtures. No test response represents business authorization or SQL.
func machineUnusedAddress(t *testing.T) string {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := listener.Addr().String()
	if e := listener.Close(); e != nil {
		t.Fatal(e)
	}
	t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", address)
	return address
}

func machineListenerInputs(t *testing.T) (config.ServerConfiguration, config.MachineServerConfiguration, *tls.Config, *x509.CertPool) {
	t.Helper()
	browser := transportFixture(config.TransportHTTP)
	browser.ListenAddress = machineUnusedAddress(t)
	_, browserPort, _ := net.SplitHostPort(browser.ListenAddress)
	browser.PortalOrigin, browser.AdminOrigin = "http://portal.test:"+browserPort, "http://admin.test:"+browserPort
	machine := machineRequestConfiguration("https://localhost")
	machine.ListenAddress = machineUnusedAddress(t)
	if machine.ListenAddress == browser.ListenAddress {
		t.Fatal("fixture allocator returned the same released port twice")
	}
	_, machinePort, _ := net.SplitHostPort(machine.ListenAddress)
	machine.Origin = "https://localhost:" + machinePort
	certificate, roots := transportCertificate(t)
	return browser, machine, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, roots
}

func machineListenerClient(t *testing.T, browser config.ServerConfiguration, machine config.MachineServerConfiguration, roots *x509.CertPool) *http.Client {
	t.Helper()
	_, browserPort, _ := net.SplitHostPort(browser.ListenAddress)
	_, machinePort, _ := net.SplitHostPort(machine.ListenAddress)
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			var target string
			switch address {
			case "portal.test:" + browserPort, "admin.test:" + browserPort:
				target = browser.ListenAddress
			case "localhost:" + machinePort:
				target = machine.ListenAddress
			default:
				return nil, context.Canceled
			}
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, target)
		}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return context.Canceled }}
}

func machineStartGroup(t *testing.T, group *ListenerGroup) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- group.Serve(ctx) }()
	t.Cleanup(func() { cancel(); _ = group.Close() })
	return cancel, done
}

func machineAwait(t *testing.T, event <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(3 * time.Second):
		t.Fatal(label)
	}
}

func machineAwaitServe(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case e := <-done:
		return e
	case <-time.After(4 * time.Second):
		t.Fatal("listener group did not join bounded shutdown")
		return nil
	}
}

func machineAssertReleased(t *testing.T, addresses ...string) {
	t.Helper()
	for _, address := range addresses {
		listener, e := net.Listen("tcp", address)
		if e != nil {
			t.Fatal("owned listener retained", e)
		}
		if e = listener.Close(); e != nil {
			t.Fatal(e)
		}
	}
}

func machineDiscardResponse(client *http.Client, origin string) <-chan error {
	done := make(chan error, 1)
	go func() {
		response, e := client.Get(origin + "/")
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		done <- e
	}()
	return done
}

func TestMachineListenerGroupActualHTTPAndVerifiedTLS(t *testing.T) {
	browser, machine, material, roots := machineListenerInputs(t)
	echo := func(machineEntrance bool) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var value RequestTransport
			var e error
			if machineEntrance {
				value, e = ResolveMachineRequest(r, machine)
			} else {
				value, e = ResolveBrowserRequest(r, browser)
			}
			if e != nil {
				Problem(w, "synthetic-machine-transport", e)
				return
			}
			json.NewEncoder(w).Encode(struct {
				Connection RequestTransport
				TLS        bool
			}{value, r.TLS != nil})
		})
	}
	group, e := ListenConfigured(browser, nil, echo(false), &machine, material, echo(true), config.DefaultRuntimePolicy().HTTP)
	if e != nil {
		t.Fatal(e)
	}
	cancel, done := machineStartGroup(t, group)
	client := machineListenerClient(t, browser, machine, roots)
	for _, origin := range []string{browser.PortalOrigin, browser.AdminOrigin, machine.Origin} {
		response, e := client.Get(origin + "/")
		if e != nil {
			t.Fatal("normal verified local transport", e)
		}
		var observed struct {
			Connection RequestTransport
			TLS        bool
		}
		if json.Unmarshal(transportRead(t, response, 200), &observed) != nil || observed.TLS != (origin == machine.Origin) || observed.Connection.Origin != origin || observed.Connection.ClientIP != "127.0.0.1" || observed.Connection.Forwarded {
			t.Fatal("wrong actual entrance", observed)
		}
	}
	request, _ := http.NewRequest(http.MethodGet, machine.Origin+"/", nil)
	request.Host = "foreign.test"
	response, e := client.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	transportRead(t, response, 421)
	request, _ = http.NewRequest(http.MethodGet, machine.Origin+"/", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	response, e = client.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	transportRead(t, response, 403)
	untrusted := machineListenerClient(t, browser, machine, x509.NewCertPool())
	if response, e := untrusted.Get(machine.Origin + "/"); e == nil {
		response.Body.Close()
		t.Fatal("untrusted machine certificate accepted")
	}
	wrongName := machineListenerClient(t, browser, machine, roots)
	wrongName.Transport.(*http.Transport).TLSClientConfig.ServerName = "foreign.test"
	if response, e := wrongName.Get(machine.Origin + "/"); e == nil {
		response.Body.Close()
		t.Fatal("wrong certificate reference name accepted")
	}
	wrongSNI := machineListenerClient(t, browser, machine, roots)
	// This name is genuinely covered by the synthetic certificate. Normal TLS
	// verification succeeds; the machine authority/SNI binding must still refuse.
	wrongSNI.Transport.(*http.Transport).TLSClientConfig.ServerName = "admin.test"
	response, e = wrongSNI.Get(machine.Origin + "/")
	if e != nil {
		t.Fatal("valid alternate SAN did not reach request authority check", e)
	}
	transportRead(t, response, 421)
	cleartext := machine.Origin
	cleartext = "http" + strings.TrimPrefix(cleartext, "https")
	if response, e := client.Get(cleartext + "/"); e == nil {
		if response.StatusCode < 400 {
			response.Body.Close()
			t.Fatal("machine listener accepted plaintext")
		}
		response.Body.Close()
	}
	cancel()
	if e := machineAwaitServe(t, done); e != nil {
		t.Fatal(e)
	}
	machineAssertReleased(t, browser.ListenAddress, machine.ListenAddress)
}

func TestMachineListenerGroupPrebindFailureAndCloseBeforeServe(t *testing.T) {
	t.Run("occupied_second_socket", func(t *testing.T) {
		browser, machine, material, _ := machineListenerInputs(t)
		foreign, e := net.Listen("tcp", machine.ListenAddress)
		if e != nil {
			t.Fatal(e)
		}
		defer foreign.Close()
		if group, e := ListenConfigured(browser, nil, http.NotFoundHandler(), &machine, material, http.NotFoundHandler(), config.DefaultRuntimePolicy().HTTP); e == nil || group != nil {
			t.Fatal("occupied machine socket accepted")
		}
		machineAssertReleased(t, browser.ListenAddress)
		probe, e := net.DialTimeout("tcp", machine.ListenAddress, time.Second)
		if e != nil {
			t.Fatal("foreign socket was closed")
		}
		probe.Close()
	})
	t.Run("close_before_serve", func(t *testing.T) {
		browser, machine, material, _ := machineListenerInputs(t)
		group, e := ListenConfigured(browser, nil, http.NotFoundHandler(), &machine, material, http.NotFoundHandler(), config.DefaultRuntimePolicy().HTTP)
		if e != nil {
			t.Fatal(e)
		}
		defer group.Close()
		if e := group.Serve(nil); e == nil {
			t.Fatal("nil serving context accepted")
		}
		if group.Close() != nil || group.Close() != nil {
			t.Fatal("idempotent prestart close failed")
		}
		if e := group.Serve(context.Background()); e == nil || e.Error() != "HTTP_LISTENER_GROUP_CLOSED" {
			t.Fatal("closed group started", e)
		}
		machineAssertReleased(t, browser.ListenAddress, machine.ListenAddress)
	})
	t.Run("cancelled_before_serve", func(t *testing.T) {
		browser, machine, material, _ := machineListenerInputs(t)
		group, e := ListenConfigured(browser, nil, http.NotFoundHandler(), &machine, material, http.NotFoundHandler(), config.DefaultRuntimePolicy().HTTP)
		if e != nil {
			t.Fatal(e)
		}
		defer group.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if e := group.Serve(ctx); e != nil {
			t.Fatal(e)
		}
		machineAssertReleased(t, browser.ListenAddress, machine.ListenAddress)
	})
}

func TestMachineListenerGroupSharesConnectionBudget(t *testing.T) {
	browser, machine, material, roots := machineListenerInputs(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var machineRequests atomic.Int32
	policy := config.DefaultRuntimePolicy().HTTP
	policy.MaxConnections, policy.MaxActiveRequests = 1, 1
	group, e := ListenConfigured(browser, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}), &machine, material, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { machineRequests.Add(1) }), policy)
	if e != nil {
		t.Fatal(e)
	}
	cancel, done := machineStartGroup(t, group)
	t.Cleanup(unblock)
	client := machineListenerClient(t, browser, machine, roots)
	browserDone := machineDiscardResponse(client, browser.PortalOrigin)
	machineAwait(t, entered, "browser did not occupy connection budget")
	if response, e := client.Get(machine.Origin + "/"); e == nil {
		response.Body.Close()
		t.Fatal("machine listener obtained a second independent budget")
	}
	if machineRequests.Load() != 0 {
		t.Fatal("over-budget machine request entered handler")
	}
	unblock()
	if e := machineAwaitServe(t, browserDone); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(group.endpoints[0].listener.(*bounded).slots) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("browser connection budget was not returned")
		}
		time.Sleep(time.Millisecond)
	}
	response, e := client.Get(machine.Origin + "/")
	if e != nil {
		t.Fatal("returned budget unavailable on machine listener", e)
	}
	transportRead(t, response, 200)
	if machineRequests.Load() != 1 {
		t.Fatal("machine request count")
	}
	cancel()
	if e := machineAwaitServe(t, done); e != nil {
		t.Fatal(e)
	}
}

func TestMachineListenerGroupJoinsBothRequestCleanupsAndRejectsRepeatStart(t *testing.T) {
	browser, machine, material, roots := machineListenerInputs(t)
	entered := []chan struct{}{make(chan struct{}), make(chan struct{})}
	cancelled := []chan struct{}{make(chan struct{}), make(chan struct{})}
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	cleaned := []chan struct{}{make(chan struct{}), make(chan struct{})}
	handler := func(index int) http.Handler {
		return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(entered[index])
			<-r.Context().Done()
			close(cancelled[index])
			<-release[index]
			close(cleaned[index])
		})
	}
	group, e := ListenConfigured(browser, nil, handler(0), &machine, material, handler(1), config.DefaultRuntimePolicy().HTTP)
	if e != nil {
		t.Fatal(e)
	}
	_, done := machineStartGroup(t, group)
	var releaseOnce [2]sync.Once
	unblock := func(index int) { releaseOnce[index].Do(func() { close(release[index]) }) }
	t.Cleanup(func() { unblock(0); unblock(1) })
	client := machineListenerClient(t, browser, machine, roots)
	requests := []<-chan error{machineDiscardResponse(client, browser.PortalOrigin), machineDiscardResponse(client, machine.Origin)}
	for _, event := range entered {
		machineAwait(t, event, "request not admitted")
	}
	if e := group.Serve(context.Background()); e == nil || e.Error() != "HTTP_LISTENER_GROUP_ALREADY_STARTED" {
		t.Fatal("second acceptance loop started", e)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- group.Close() }()
	for _, event := range cancelled {
		machineAwait(t, event, "request context did not cancel")
	}
	unblock(0)
	machineAwait(t, cleaned[0], "first independent cleanup incomplete")
	select {
	case <-done:
		t.Fatal("group returned before sibling cleanup")
	default:
	}
	select {
	case <-closeDone:
		t.Fatal("Close returned before sibling cleanup")
	default:
	}
	unblock(1)
	if e := machineAwaitServe(t, done); e != nil {
		t.Fatal(e)
	}
	if e := machineAwaitServe(t, closeDone); e != nil {
		t.Fatal(e)
	}
	for _, event := range cleaned {
		machineAwait(t, event, "cleanup not joined")
	}
	for _, request := range requests {
		_ = machineAwaitServe(t, request)
	}
	machineAssertReleased(t, browser.ListenAddress, machine.ListenAddress)
}

func TestMachineListenerGroupSiblingFailureCancelsBoth(t *testing.T) {
	browser, machine, material, roots := machineListenerInputs(t)
	entered, cancelled := make(chan struct{}), make(chan struct{})
	group, e := ListenConfigured(browser, nil, http.NotFoundHandler(), &machine, material, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	}), config.DefaultRuntimePolicy().HTTP)
	if e != nil {
		t.Fatal(e)
	}
	_, done := machineStartGroup(t, group)
	request := machineDiscardResponse(machineListenerClient(t, browser, machine, roots), machine.Origin)
	machineAwait(t, entered, "machine request not admitted")
	if e := group.endpoints[0].listener.Close(); e != nil {
		t.Fatal(e)
	}
	machineAwait(t, cancelled, "browser failure did not cancel machine request")
	if e := machineAwaitServe(t, done); e == nil || !strings.Contains(e.Error(), "HTTP_LISTENER_SERVE_FAILED") {
		t.Fatal("unexpected listener termination was successful", e)
	}
	_ = machineAwaitServe(t, request)
	machineAssertReleased(t, browser.ListenAddress, machine.ListenAddress)
}

func TestMachineListenerGroupShutdownDeadlineClosesBoth(t *testing.T) {
	browser, machine, material, roots := machineListenerInputs(t)
	entered := []chan struct{}{make(chan struct{}), make(chan struct{})}
	finished := []chan struct{}{make(chan struct{}), make(chan struct{})}
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	handler := func(index int) http.Handler {
		return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(entered[index])
			<-release
			close(finished[index])
		})
	}
	policy := config.DefaultRuntimePolicy().HTTP
	policy.ShutdownSeconds = 1
	group, e := ListenConfigured(browser, nil, handler(0), &machine, material, handler(1), policy)
	if e != nil {
		t.Fatal(e)
	}
	cancel, done := machineStartGroup(t, group)
	client := machineListenerClient(t, browser, machine, roots)
	requests := []<-chan error{machineDiscardResponse(client, browser.PortalOrigin), machineDiscardResponse(client, machine.Origin)}
	for _, event := range entered {
		machineAwait(t, event, "deadline fixture not admitted")
	}
	cancel()
	if e := machineAwaitServe(t, done); e == nil || !strings.Contains(e.Error(), "HTTP_LISTENER_SHUTDOWN_FAILED") {
		t.Fatal("uncooperative cleanup claimed successful drain", e)
	}
	machineAssertReleased(t, browser.ListenAddress, machine.ListenAddress)
	for _, request := range requests {
		_ = machineAwaitServe(t, request)
	}
	// Test-owned barriers explicitly release and join the deliberately uncooperative
	// handlers before this case ends; forced socket close alone did not do so.
	unblock()
	for _, event := range finished {
		machineAwait(t, event, "test-owned handler not joined")
	}
}

func TestMachineListenerGroupRejectsInvalidSelections(t *testing.T) {
	browser, machine, material, _ := machineListenerInputs(t)
	for _, name := range []string{"browser_tls", "browser_handler", "machine_tls", "machine_handler", "dangling_tls", "dangling_handler", "same_bind", "same_origin", "invalid_policy"} {
		t.Run(name, func(t *testing.T) {
			selected := machine
			machineInput := &selected
			var browserMaterial *tls.Config
			machineMaterial := material
			var browserHandler http.Handler = http.NotFoundHandler()
			var machineHandler http.Handler = http.NotFoundHandler()
			policy := config.DefaultRuntimePolicy().HTTP
			switch name {
			case "browser_tls":
				browserMaterial = material
			case "browser_handler":
				browserHandler = nil
			case "machine_tls":
				machineMaterial = nil
			case "machine_handler":
				machineHandler = nil
			case "dangling_tls":
				machineInput, machineHandler = nil, nil
			case "dangling_handler":
				machineInput, machineMaterial = nil, nil
			case "same_bind":
				selected.ListenAddress = browser.ListenAddress
			case "same_origin":
				selected.Origin = browser.AdminOrigin
			case "invalid_policy":
				policy.MaxConnections = 0
			}
			if group, e := ListenConfigured(browser, browserMaterial, browserHandler, machineInput, machineMaterial, machineHandler, policy); e == nil || group != nil {
				if group != nil {
					group.Close()
				}
				t.Fatal("invalid selection accepted")
			}
			machineAssertReleased(t, browser.ListenAddress, machine.ListenAddress)
		})
	}
	var zero ListenerGroup
	if e := zero.Serve(context.Background()); e == nil || zero.Close() != nil {
		t.Fatal("zero group contract", e)
	}
	legacy, e := ListenConfigured(browser, nil, http.NotFoundHandler(), nil, nil, nil, config.DefaultRuntimePolicy().HTTP)
	if e != nil || legacy == nil {
		t.Fatal("single-listener selection rejected", e)
	}
	if e := legacy.Close(); e != nil {
		t.Fatal(e)
	}
	machineAssertReleased(t, browser.ListenAddress, machine.ListenAddress)
}
