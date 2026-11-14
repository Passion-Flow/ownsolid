package healthprobe

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// These are real loopback TLS/HTTP clients and explicitly synthetic health
// responders. They exercise probe mechanics, not product or SQL readiness.
func syntheticMachineProbeConfiguration(server *httptest.Server, ca string, mode config.TransportMode, origin string) (config.ServerConfiguration, *config.MachineServerConfiguration, Input) {
	browser := syntheticServerConfiguration(server, mode)
	browser.ListenAddress = "0.0.0.0:18443"
	// The probe validates reference structure, not the serving key pair. Actual
	// server TLS uses the process-local synthetic certificate in the responder.
	machine := &config.MachineServerConfiguration{
		ListenAddress:      server.Listener.Addr().String(),
		Origin:             origin,
		TLSCertificateFile: filepath.Join(filepath.Dir(ca), "synthetic-serving-certificate.pem"),
		TLSPrivateKeyFile:  filepath.Join(filepath.Dir(ca), "synthetic-serving-key.pem"),
	}
	input := syntheticInput(server, ca)
	input.Audience = "machine"
	return browser, machine, input
}

func syntheticMachineProbeRequest(t *testing.T, observation *syntheticHealthObservation, authority, kind, reference string) {
	t.Helper()
	requests, sni := observation.snapshot()
	if len(requests) != 1 || observation.accepts.Load() != 1 {
		t.Fatal("machine probe did not use exactly one request/dial")
	}
	syntheticPlainRequest(t, requests[0], authority, "/health/"+kind, true)
	if requests[0].headers.Get("Origin") != "" || requests[0].headers.Get("Accept") != "application/json" {
		t.Fatal("machine probe changed the shared credential-free request contract")
	}
	wantedSNI := reference
	if net.ParseIP(reference) != nil {
		wantedSNI = ""
	}
	if len(sni) != 1 || sni[0] != wantedSNI {
		t.Fatal("machine probe did not preserve the configured DNS/IP TLS reference")
	}
}

func TestMachineHealthProbeActualTLSReferences(t *testing.T) {
	for _, address := range []struct{ name, reference, authority, network string }{
		{"dns", "machine.example.invalid", "machine.example.invalid", "tcp4"},
		{"dns_nondefault", "machine.example.invalid", "machine.example.invalid:39443", "tcp4"},
		{"localhost", "localhost", "localhost:39443", "tcp4"},
		{"ipv4", "127.0.0.1", "127.0.0.1:39443", "tcp4"},
		{"ipv6", "::1", "[::1]:39443", "tcp6"},
	} {
		for _, kind := range []string{"live", "ready"} {
			t.Run(address.name+"_"+kind, func(t *testing.T) {
				certificate, raw := syntheticCertificate(t, []string{"machine.example.invalid", "localhost"}, []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, false)
				observation := &syntheticHealthObservation{}
				server := syntheticHealthServer(t, address.network, &certificate, http.HandlerFunc(syntheticHealthOK), observation)
				browser, machine, input := syntheticMachineProbeConfiguration(server, syntheticTrustPath(t, raw), config.TransportHTTP, "https://"+address.authority)
				input.Kind = kind
				beforeMachine := *machine
				if e := ValidateInput(input); e != nil {
					t.Fatal("explicit machine audience input refused", e)
				}
				if e := RunWithMachine(context.Background(), browser, machine, input); e != nil {
					t.Fatal(e)
				}
				if *machine != beforeMachine || browser.TransportMode != config.TransportHTTP {
					t.Fatal("machine probe rewrote selected machine identity or browser HTTP mode")
				}
				syntheticMachineProbeRequest(t, observation, address.authority, kind, address.reference)
			})
		}
	}
	for _, mode := range []config.TransportMode{config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		t.Run("browser_"+string(mode), func(t *testing.T) {
			certificate, raw := syntheticCertificate(t, []string{"machine.example.invalid"}, nil, false)
			observation := &syntheticHealthObservation{}
			server := syntheticHealthServer(t, "tcp4", &certificate, http.HandlerFunc(syntheticHealthOK), observation)
			browser, machine, input := syntheticMachineProbeConfiguration(server, syntheticTrustPath(t, raw), mode, "https://machine.example.invalid:39443")
			if e := RunWithMachine(context.Background(), browser, machine, input); e != nil {
				t.Fatal(e)
			}
			syntheticMachineProbeRequest(t, observation, "machine.example.invalid:39443", "ready", "machine.example.invalid")
		})
	}
}

func TestMachineHealthProbeRefusesUnsafeTargets(t *testing.T) {
	observation := &syntheticHealthObservation{}
	trap := syntheticHealthServer(t, "tcp4", nil, http.HandlerFunc(syntheticHealthOK), observation)
	missingCA := filepath.Join(t.TempDir(), "must-not-open-trust.pem")
	baseBrowser, baseMachine, baseInput := syntheticMachineProbeConfiguration(trap, missingCA, config.TransportDirectHTTPS, "https://machine.example.invalid:39443")
	for _, item := range []struct {
		name string
		edit func(*Input, *config.ServerConfiguration, **config.MachineServerConfiguration)
		code string
	}{
		{"absent_group", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) { *m = nil }, configurationInvalid},
		{"empty_group", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			*m = &config.MachineServerConfiguration{}
		}, configurationInvalid},
		{"missing_listener", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).ListenAddress = ""
		}, configurationInvalid},
		{"missing_origin", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) { (*m).Origin = "" }, configurationInvalid},
		{"missing_certificate", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).TLSCertificateFile = ""
		}, configurationInvalid},
		{"missing_key", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).TLSPrivateKeyFile = ""
		}, configurationInvalid},
		{"http_origin", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).Origin = "http://machine.example.invalid"
		}, configurationInvalid},
		{"origin_path", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).Origin += "/health/ready"
		}, configurationInvalid},
		{"noncanonical_origin", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).Origin = "https://Machine.example.invalid"
		}, configurationInvalid},
		{"invalid_listener", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).ListenAddress = "machine.example.invalid:0"
		}, configurationInvalid},
		{"same_browser_listener", func(_ *Input, c *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).ListenAddress = c.ListenAddress
		}, configurationInvalid},
		{"same_portal_origin", func(_ *Input, c *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).Origin = c.PortalOrigin
		}, configurationInvalid},
		{"same_admin_origin", func(_ *Input, c *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).Origin = c.AdminOrigin
		}, configurationInvalid},
		{"aliased_tls_pair", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).TLSPrivateKeyFile = (*m).TLSCertificateFile
		}, configurationInvalid},
		{"relative_certificate", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).TLSCertificateFile = "certificate.pem"
		}, configurationInvalid},
		{"relative_key", func(_ *Input, _ *config.ServerConfiguration, m **config.MachineServerConfiguration) {
			(*m).TLSPrivateKeyFile = "key.pem"
		}, configurationInvalid},
		{"invalid_browser", func(_ *Input, c *config.ServerConfiguration, _ **config.MachineServerConfiguration) {
			c.TransportMode = "invalid"
		}, configurationInvalid},
		{"missing_ca", func(i *Input, _ *config.ServerConfiguration, _ **config.MachineServerConfiguration) { i.CAFile = "" }, argumentInvalid},
		{"missing_ca_with_http_browser", func(i *Input, c *config.ServerConfiguration, _ **config.MachineServerConfiguration) {
			i.CAFile = ""
			c.TransportMode = config.TransportHTTP
			c.PortalOrigin = "http://portal.example.invalid"
			c.AdminOrigin = "http://admin.example.invalid"
		}, argumentInvalid},
		{"relative_ca", func(i *Input, _ *config.ServerConfiguration, _ **config.MachineServerConfiguration) {
			i.CAFile = "trust.pem"
		}, argumentInvalid},
		{"invalid_audience", func(i *Input, _ *config.ServerConfiguration, _ **config.MachineServerConfiguration) {
			i.Audience = "Machine"
		}, argumentInvalid},
		{"dns_dial", func(i *Input, _ *config.ServerConfiguration, _ **config.MachineServerConfiguration) {
			i.DialAddress = "localhost:39443"
		}, argumentInvalid},
		{"unavailable_private_ca", func(_ *Input, _ *config.ServerConfiguration, _ **config.MachineServerConfiguration) {}, trustInvalid},
	} {
		t.Run(item.name, func(t *testing.T) {
			browser, input, copied := baseBrowser, baseInput, *baseMachine
			machine := &copied
			item.edit(&input, &browser, &machine)
			healthFailure(t, RunWithMachine(context.Background(), browser, machine, input), item.code)
		})
	}
	t.Run("legacy_wrapper_never_falls_back_to_admin", func(t *testing.T) {
		healthFailure(t, Run(context.Background(), baseBrowser, baseInput), configurationInvalid)
	})
	t.Run("nil_context", func(t *testing.T) {
		healthFailure(t, RunWithMachine(nil, baseBrowser, baseMachine, baseInput), argumentInvalid)
	})
	if requests, _ := observation.snapshot(); len(requests) != 0 || observation.accepts.Load() != 0 {
		t.Fatal("unsafe machine selection reached a socket or trust failure fell back to HTTP")
	}
	for _, name := range []string{"unknown_ca", "wrong_dns", "wrong_ip", "expired", "plaintext_responder"} {
		t.Run(name, func(t *testing.T) {
			dns, ips := []string{"machine.example.invalid"}, []net.IP{net.ParseIP("127.0.0.1")}
			if name == "wrong_dns" {
				dns = []string{"another.example.invalid"}
			}
			if name == "wrong_ip" {
				ips = []net.IP{net.ParseIP("192.0.2.1")}
			}
			value, raw := syntheticCertificate(t, dns, ips, name == "expired")
			if name == "unknown_ca" {
				_, raw = syntheticCertificate(t, dns, ips, false)
			}
			var certificate *tls.Certificate = &value
			if name == "plaintext_responder" {
				certificate = nil
			}
			failed := &syntheticHealthObservation{}
			server := syntheticHealthServer(t, "tcp4", certificate, http.HandlerFunc(syntheticHealthOK), failed)
			origin := "https://machine.example.invalid:39443"
			if name == "wrong_ip" {
				origin = "https://127.0.0.1:39443"
			}
			browser, machine, input := syntheticMachineProbeConfiguration(server, syntheticTrustPath(t, raw), config.TransportHTTP, origin)
			healthFailure(t, RunWithMachine(context.Background(), browser, machine, input), transportFailed)
			if requests, _ := failed.snapshot(); len(requests) != 0 || failed.accepts.Load() != 1 {
				t.Fatal("unverified machine TLS reached the health responder or retried/downgraded")
			}
		})
	}
}

func TestMachineHealthProbeSingleRequestAndCancellation(t *testing.T) {
	trapObservation := &syntheticHealthObservation{}
	trap := syntheticHealthServer(t, "tcp4", nil, http.HandlerFunc(syntheticHealthOK), trapObservation)
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, trap.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	for _, name := range []string{"valid_ignores_ambient_proxy", "redirect", "unavailable", "closed_response", "duplicate_envelope", "oversize"} {
		t.Run(name, func(t *testing.T) {
			certificate, raw := syntheticCertificate(t, []string{"machine.example.invalid"}, nil, false)
			observation := &syntheticHealthObservation{}
			server := syntheticHealthServer(t, "tcp4", &certificate, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch name {
				case "redirect":
					w.Header().Set("Location", trap.URL+"/health/ready")
					w.WriteHeader(http.StatusFound)
				case "unavailable":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "closed_response":
					connection, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					connection.Close()
				case "duplicate_envelope", "oversize":
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Cache-Control", "no-store")
					w.Header().Set("X-Request-ID", "synthetic-machine-response")
					body := `{"data":{"status":"ok","status":"ok"}}`
					if name == "oversize" {
						body = syntheticEnvelope + strings.Repeat(" ", int(maximumResponseBytes)+1-len(syntheticEnvelope))
					}
					_, _ = io.WriteString(w, body)
				default:
					syntheticHealthOK(w, r)
				}
			}), observation)
			browser, machine, input := syntheticMachineProbeConfiguration(server, syntheticTrustPath(t, raw), config.TransportHTTP, "https://machine.example.invalid:39443")
			e := RunWithMachine(context.Background(), browser, machine, input)
			if name == "valid_ignores_ambient_proxy" {
				if e != nil {
					t.Fatal(e)
				}
			} else if name == "closed_response" {
				healthFailure(t, e, transportFailed)
			} else {
				healthFailure(t, e, responseInvalid)
			}
			syntheticMachineProbeRequest(t, observation, "machine.example.invalid:39443", "ready", "machine.example.invalid")
		})
	}
	for _, name := range []string{"delayed_headers", "delayed_body", "earlier_parent_deadline", "inflight_cancel", "already_cancelled"} {
		t.Run(name, func(t *testing.T) {
			entered := make(chan struct{})
			certificate, raw := syntheticCertificate(t, []string{"machine.example.invalid"}, nil, false)
			observation := &syntheticHealthObservation{}
			server := syntheticHealthServer(t, "tcp4", &certificate, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				if name == "delayed_body" {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Cache-Control", "no-store")
					w.Header().Set("X-Request-ID", "synthetic-machine-delayed-body")
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, `{"data":`)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}), observation)
			browser, machine, input := syntheticMachineProbeConfiguration(server, syntheticTrustPath(t, raw), config.TransportHTTP, "https://machine.example.invalid:39443")
			input.TimeoutSeconds = 1
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "earlier_parent_deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 250*time.Millisecond)
				defer deadlineCancel()
				input.TimeoutSeconds = 5
			}
			if name == "already_cancelled" {
				cancel()
			}
			result := make(chan error, 1)
			joined := make(chan struct{})
			start := time.Now()
			go func() {
				defer close(joined)
				result <- RunWithMachine(ctx, browser, machine, input)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-joined:
				case <-time.After(2 * time.Second):
					t.Error("owned machine probe did not join after cancellation")
				}
			})
			if name == "inflight_cancel" {
				select {
				case <-entered:
					cancel()
				case <-time.After(2 * time.Second):
					t.Fatal("machine probe did not enter the delayed synthetic responder")
				}
			}
			var e error
			select {
			case e = <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("bounded machine probe did not terminate")
			}
			wanted := deadlineExceeded
			if name == "inflight_cancel" || name == "already_cancelled" {
				wanted = cancelled
			}
			healthFailure(t, e, wanted)
			if time.Since(start) > 2*time.Second {
				t.Fatal("machine probe exceeded its selected/parent bound plus scheduling allowance")
			}
			requests, sni := observation.snapshot()
			if name == "already_cancelled" {
				if len(requests) != 0 || len(sni) != 0 || observation.accepts.Load() != 0 {
					t.Fatal("already cancelled machine probe reached trust-independent network execution")
				}
			} else {
				syntheticMachineProbeRequest(t, observation, "machine.example.invalid:39443", "ready", "machine.example.invalid")
			}
		})
	}
	if requests, _ := trapObservation.snapshot(); len(requests) != 0 || trapObservation.accepts.Load() != 0 {
		t.Fatal("machine probe followed a redirect or ambient proxy")
	}
}
