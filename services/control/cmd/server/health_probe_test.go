package main

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

// These responders are explicit probe-control doubles, not product readiness
// or database/installed-deployment acceptance. The CLI dispatcher itself is real.
func newProbeCLITarget(t *testing.T, authorities ...string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	allowed := map[string]bool{}
	for _, authority := range authorities {
		allowed[authority] = true
	}
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || !allowed[r.Host] || r.URL.RawQuery != "" || r.URL.Path != "/health/live" && r.URL.Path != "/health/ready" || r.TLS != nil {
			t.Error("probe changed its selected synthetic HTTP identity or health request")
			w.WriteHeader(http.StatusMisdirectedRequest)
			return
		}
		for name := range r.Header {
			lower := strings.ToLower(name)
			if lower == "authorization" || lower == "proxy-authorization" || lower == "cookie" || lower == "forwarded" || lower == "x-real-ip" || strings.HasPrefix(lower, "x-forwarded") {
				t.Error("probe sent authentication or fabricated forwarding authority")
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", "synthetic-probe-cli-request")
		_, _ = w.Write([]byte(`{"data":{"status":"ok"}}`))
	}))
	t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", server.Listener.Addr())
	t.Cleanup(server.Close)
	return server, calls
}

func probeCLIConfiguration(t *testing.T, listener string) string {
	t.Helper()
	return probeCLIConfigurationWithServer(t, config.ServerConfiguration{TransportMode: config.TransportHTTP,
		ListenAddress: listener, PortalOrigin: "http://portal.example.invalid:31081",
		AdminOrigin: "http://admin.example.invalid:31082"})
}

func probeCLIConfigurationWithServer(t *testing.T, server config.ServerConfiguration) string {
	t.Helper()
	private := t.TempDir()
	if e := os.Chmod(private, 0700); e != nil {
		t.Fatal(e)
	}
	// Every credential/runtime reference deliberately does not exist. Opening
	// the ordinary database or business owners would make this command fail.
	raw, e := yaml.Marshal(map[string]any{
		"server": server,
		"database": map[string]any{"dsn_file": filepath.Join(private, "unavailable-dsn"),
			"max_open_connections": 4, "max_idle_connections": 1, "statement_timeout_seconds": 5},
		"security": map[string]any{"auth_hmac_key_file": filepath.Join(private, "unavailable-hmac"),
			"mail_keyring_file":       filepath.Join(private, "unavailable-mail"),
			"password_blocklist_file": filepath.Join(private, "unavailable-blocklist")},
		"smtp": map[string]any{"host": "smtp.example.invalid", "port": 587, "from_address": "probe@example.invalid"},
	})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(private, "control.yaml")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}

func probeCLIArguments(path, dial string) []string {
	return []string{"--config", path, "--audience", "portal", "--kind", "ready",
		"--dial-address", dial, "--timeout-seconds", "3"}
}

func TestHealthProbeCLIActualDispatcherWithoutDatabaseOrWorkers(t *testing.T) {
	server, calls := newProbeCLITarget(t, "portal.example.invalid:31081", "admin.example.invalid:31082")
	path := probeCLIConfiguration(t, server.Listener.Addr().String())
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	for _, audience := range []string{"portal", "admin"} {
		for _, kind := range []string{"live", "ready"} {
			args := probeCLIArguments(path, server.Listener.Addr().String())
			args[3], args[5] = audience, kind
			os.Args = append([]string{"control", healthProbeCommand}, args...)
			if e := run(); e != nil {
				t.Fatal("actual dispatcher opened unavailable dependencies or refused the selected target", e)
			}
		}
	}
	if calls.Load() != 4 {
		t.Fatal("probe failed its exact one-GET-per-invocation count")
	}
	if _, e := os.Stat(filepath.Join(filepath.Dir(path), "unavailable-dsn")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("probe created a database credential artifact")
	}
}

func TestHealthProbeCLIExplicitEnvironmentOverridesOnlyDeclaredOrigin(t *testing.T) {
	server, calls := newProbeCLITarget(t, "overridden.example.invalid:31083")
	path := probeCLIConfiguration(t, server.Listener.Addr().String())
	environment := filepath.Join(filepath.Dir(path), "policy.env")
	if e := os.WriteFile(environment, []byte("PROJECT_ONE_PORTAL_ORIGIN=http://overridden.example.invalid:31083\n"), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PROJECT_ONE_PORTAL_ORIGIN", "http://untrusted.example.invalid:31084")
	args := append(probeCLIArguments(path, server.Listener.Addr().String()), "--env-file", environment)
	var output bytes.Buffer
	if e := runHealthProbeCommand(args, &output); e != nil || output.String() != "CONTROL_HEALTH_PROBE_OK\n" || calls.Load() != 1 {
		t.Fatal("explicit configuration authority or fixed output failed", e)
	}
}

func TestHealthProbeCLIRejectsDuplicateUnknownAndMissingArgumentsBeforeIO(t *testing.T) {
	base := probeCLIArguments(filepath.Join(t.TempDir(), "unavailable.yaml"), "127.0.0.1:1")
	cases := map[string][]string{
		"missing_all":    nil,
		"missing_config": append([]string{}, base[2:]...),
		"unknown":        append(append([]string{}, base...), "--unowned=synthetic-sensitive-value"),
		"positional":     append(append([]string{}, base...), "synthetic-sensitive-value"),
		"relative_env":   append(append([]string{}, base...), "--env-file=relative-private-file"),
		"empty_env":      append(append([]string{}, base...), "--env-file="),
		"empty_ca":       append(append([]string{}, base...), "--ca-file="),
		"duplicate_env":  append(append([]string{}, base...), "--env-file=/missing-one", "--env-file=/missing-two"),
		"duplicate_ca":   append(append([]string{}, base...), "--ca-file=/missing-one", "--ca-file=/missing-two"),
	}
	for _, name := range []string{"config", "audience", "kind", "dial-address", "timeout-seconds"} {
		cases["duplicate_"+name] = append(append([]string{}, base...), "--"+name+"=untrusted-duplicate")
	}
	for name, replacement := range map[string]string{"audience": "foreign", "kind": "login", "dial-address": "example.invalid:80", "timeout-seconds": "0", "config": "relative.yaml"} {
		args := append([]string{}, base...)
		for index := 0; index < len(args); index += 2 {
			if args[index] == "--"+name {
				args[index+1] = replacement
			}
		}
		cases["invalid_"+name] = args
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if e := runHealthProbeCommand(args, &output); e == nil || e.Error() != "HEALTH_PROBE_ARGUMENT_INVALID" || output.Len() != 0 {
				t.Fatal("invalid flag reached configuration/network or leaked its value", e)
			}
		})
	}
}

func TestHealthProbeCLIRedactsConfigurationAndTransportFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-sensitive-unavailable.yaml")
	var output bytes.Buffer
	if e := runHealthProbeCommand(probeCLIArguments(path, "127.0.0.1:1"), &output); e == nil || e.Error() != "HEALTH_PROBE_CONFIGURATION_INVALID" || output.Len() != 0 {
		t.Fatal("configuration error exposed input or opened a runtime owner", e)
	}
	if e := os.WriteFile(path, []byte("unowned_sensitive_field: synthetic-sensitive-value\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := runHealthProbeCommand(probeCLIArguments(path, "127.0.0.1:1"), &output); e == nil || e.Error() != "HEALTH_PROBE_CONFIGURATION_INVALID" || output.Len() != 0 {
		t.Fatal("invalid configuration leaked a parser value", e)
	}
	server, calls := newProbeCLITarget(t, "portal.example.invalid:31081")
	dial := server.Listener.Addr().String()
	path = probeCLIConfiguration(t, dial)
	server.Close()
	if e := runHealthProbeCommand(probeCLIArguments(path, dial), &output); e == nil || e.Error() != "HEALTH_PROBE_TRANSPORT_FAILED" || output.Len() != 0 || calls.Load() != 0 {
		t.Fatal("closed target error leaked a URL, retried or passed", e)
	}
}

type failedProbeOutput struct{}

func (failedProbeOutput) Write([]byte) (int, error) {
	return 0, errors.New("synthetic-sensitive-output-error")
}

func TestHealthProbeCLIOutputFailureIsRedacted(t *testing.T) {
	server, calls := newProbeCLITarget(t, "portal.example.invalid:31081")
	path := probeCLIConfiguration(t, server.Listener.Addr().String())
	if e := runHealthProbeCommand(probeCLIArguments(path, server.Listener.Addr().String()), failedProbeOutput{}); e == nil || e.Error() != "HEALTH_PROBE_OUTPUT_FAILED" || calls.Load() != 1 {
		t.Fatal("output error leaked, passed or repeated the health request", e)
	}
}

func TestHealthProbeCLINonProbeCommandsRetainPolicyFlagAdmission(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = []string{"control", "migrate", "--env-file", "/unavailable-private-input"}
	if e := run(); e == nil || e.Error() != "POLICY_ENVIRONMENT_FLAG_INVALID" {
		t.Fatal("health branch widened ordinary operator policy flags", e)
	}
}

func TestHealthProbeCLIRejectsUnsafeProtectedConfigurationSnapshots(t *testing.T) {
	const childInput = "PROJECT_ONE_TEST_PROBE_CONFIGURATION_CHILD"
	if path := os.Getenv(childInput); path != "" {
		var output bytes.Buffer
		if e := runHealthProbeCommand(probeCLIArguments(path, "127.0.0.1:1"), &output); e == nil || e.Error() != "HEALTH_PROBE_CONFIGURATION_INVALID" || output.Len() != 0 {
			t.Fatal("unsafe configuration did not fail closed before network or runtime construction", e)
		}
		return
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"file_mode", "parent_mode", "symlink", "parent_symlink", "hardlink", "oversized", "fifo", "directory"} {
		t.Run(name, func(t *testing.T) {
			path := probeCLIConfiguration(t, "127.0.0.1:1")
			switch name {
			case "file_mode":
				e = os.Chmod(path, 0644)
			case "parent_mode":
				e = os.Chmod(filepath.Dir(path), 0755)
			case "symlink":
				link := filepath.Join(filepath.Dir(path), "linked.yaml")
				e = os.Symlink(path, link)
				path = link
			case "parent_symlink":
				link := filepath.Join(t.TempDir(), "linked-parent")
				e = os.Symlink(filepath.Dir(path), link)
				path = filepath.Join(link, filepath.Base(path))
			case "hardlink":
				e = os.Link(path, filepath.Join(filepath.Dir(path), "second-link.yaml"))
			case "oversized":
				var raw []byte
				raw, e = os.ReadFile(path)
				if e == nil {
					e = os.WriteFile(path, append(raw, []byte("\n#"+strings.Repeat("x", config.MaximumProtectedConfigurationBytes))...), 0600)
				}
			case "fifo", "directory":
				e = os.Remove(path)
				if e == nil && name == "fifo" {
					e = syscall.Mkfifo(path, 0600)
				} else if e == nil {
					e = os.Mkdir(path, 0700)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			// A separate owned child keeps a regressed blocking FIFO read out of
			// the parent test. Go's timeout and the joined outer deadline both
			// bound failure; the enclosing fixture also owns this process group.
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestHealthProbeCLIRejectsUnsafeProtectedConfigurationSnapshots$", "-test.timeout=2s")
			command.Env = append(os.Environ(), childInput+"="+path)
			output, runError := command.CombinedOutput()
			if ctx.Err() != nil || runError != nil {
				t.Fatalf("protected configuration refusal failed or hung; child joined: %v, %s", runError, output)
			}
		})
	}
}

func TestHealthProbeCLIActualVerifiedTLSAndProxyEntrances(t *testing.T) {
	for _, mode := range []config.TransportMode{config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		t.Run(string(mode), func(t *testing.T) {
			var frontCalls, backendCalls atomic.Int32
			writeHealth := func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.URL.Path != "/health/live" && r.URL.Path != "/health/ready" {
					t.Error("CLI changed the synthetic health request")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("X-Request-ID", "synthetic-cli-tls-request")
				_, _ = w.Write([]byte(`{"data":{"status":"ok"}}`))
			}
			var forward http.Handler
			var backend *httptest.Server
			if mode == config.TransportTrustedProxyHTTPS {
				backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					backendCalls.Add(1)
					if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") != "https" || r.Header.Get("X-Forwarded-Host") != r.Host || r.Header.Get("X-Forwarded-For") == "" {
						t.Error("actual test proxy lost its TLS termination observation")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					writeHealth(w, r)
				}))
				t.Cleanup(backend.Close)
				t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", backend.Listener.Addr())
				target, e := url.Parse(backend.URL)
				if e != nil {
					t.Fatal(e)
				}
				transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
				t.Cleanup(transport.CloseIdleConnections)
				forward = &httputil.ReverseProxy{Transport: transport, Rewrite: func(r *httputil.ProxyRequest) {
					r.SetURL(target)
					r.Out.Host = r.In.Host
					r.SetXForwarded()
				}}
			}
			front := httpfixture.NewTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				frontCalls.Add(1)
				if r.TLS == nil || r.Host != "portal.example.invalid" && r.Host != "admin.example.invalid" || r.TLS.ServerName != r.Host {
					t.Error("CLI did not preserve actual DNS Host/SNI/TLS identity")
					w.WriteHeader(http.StatusMisdirectedRequest)
					return
				}
				for name := range r.Header {
					lower := strings.ToLower(name)
					if lower == "authorization" || lower == "proxy-authorization" || lower == "cookie" || lower == "forwarded" || lower == "x-real-ip" || strings.HasPrefix(lower, "x-forwarded") {
						t.Error("CLI sent credentials or manufactured proxy observations")
						w.WriteHeader(http.StatusForbidden)
						return
					}
				}
				if forward != nil {
					forward.ServeHTTP(w, r)
				} else {
					writeHealth(w, r)
				}
			}))
			t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", front.Listener.Addr())
			server := config.ServerConfiguration{TransportMode: mode, ListenAddress: front.Listener.Addr().String(), PortalOrigin: httpfixture.PortalOrigin, AdminOrigin: httpfixture.AdminOrigin}
			if backend != nil {
				server.ListenAddress = backend.Listener.Addr().String()
				server.TrustedProxyCIDRs = []string{"127.0.0.1/32"}
			}
			path := probeCLIConfigurationWithServer(t, server)
			ca := filepath.Join(filepath.Dir(path), "synthetic-ca.pem")
			if e := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: front.Certificate().Raw}), 0600); e != nil {
				t.Fatal(e)
			}
			for _, audience := range []string{"portal", "admin"} {
				for _, kind := range []string{"live", "ready"} {
					args := append(probeCLIArguments(path, front.Listener.Addr().String()), "--ca-file", ca)
					args[3], args[5] = audience, kind
					var output bytes.Buffer
					if e := runHealthProbeCommand(args, &output); e != nil || output.String() != "CONTROL_HEALTH_PROBE_OK\n" {
						t.Fatal("actual TLS/proxy CLI configuration or trust mapping failed", e)
					}
				}
			}
			if frontCalls.Load() != 4 || backend != nil && backendCalls.Load() != 4 {
				t.Fatal("CLI changed its exact one-request invocation count")
			}
		})
	}
}

func TestHealthProbeCLIActualSignalCancellation(t *testing.T) {
	const childInput = "PROJECT_ONE_TEST_PROBE_SIGNAL_CHILD"
	if path := os.Getenv(childInput); path != "" {
		var output bytes.Buffer
		if e := runHealthProbeCommand(probeCLIArguments(path, os.Getenv(childInput+"_DIAL")), &output); e == nil || e.Error() != "HEALTH_PROBE_CANCELLED" || output.Len() != 0 {
			t.Fatal("actual signal did not cancel the CLI without success output", e)
		}
		return
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, selected := range []struct {
		name   string
		signal os.Signal
	}{{"interrupt", os.Interrupt}, {"terminate", syscall.SIGTERM}} {
		t.Run(selected.name, func(t *testing.T) {
			entered, cancelled := make(chan struct{}, 1), make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				<-r.Context().Done()
				cancelled <- struct{}{}
			}))
			t.Cleanup(server.Close)
			t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", server.Listener.Addr())
			path := probeCLIConfiguration(t, server.Listener.Addr().String())
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestHealthProbeCLIActualSignalCancellation$", "-test.timeout=6s")
			command.Env = append(os.Environ(), childInput+"="+path, childInput+"_DIAL="+server.Listener.Addr().String())
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if e := command.Start(); e != nil {
				t.Fatal(e)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				cancel()
				_ = command.Wait()
				t.Fatal("child never reached its owned HTTP target")
			}
			signalError := command.Process.Signal(selected.signal)
			waitError := command.Wait()
			if signalError != nil || waitError != nil || ctx.Err() != nil {
				t.Fatalf("signal cancellation failed; child joined: signal=%v wait=%v output=%s", signalError, waitError, output.Bytes())
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("signal did not cancel the actual target request")
			}
		})
	}
}
