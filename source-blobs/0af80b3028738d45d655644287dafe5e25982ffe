package main

import (
	"bytes"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
)

// These responders exercise the actual protected CLI and normal private TLS;
// they are synthetic health responses, not product or SQL readiness evidence.
func machineProbeCLIConfiguration(t *testing.T, dial string) string {
	t.Helper()
	path := probeCLIConfiguration(t, "127.0.0.1:31081")
	raw, err := os.ReadFile(path)
	var document map[string]any
	if err != nil || yaml.Unmarshal(raw, &document) != nil {
		t.Fatal("synthetic protected configuration unavailable", err)
	}
	document["machine_server"] = config.MachineServerConfiguration{
		ListenAddress: dial, Origin: "https://portal.example.invalid:19443",
		TLSCertificateFile: filepath.Join(filepath.Dir(path), "unavailable-machine-certificate.pem"),
		TLSPrivateKeyFile:  filepath.Join(filepath.Dir(path), "unavailable-machine-key.pem"),
	}
	raw, err = yaml.Marshal(document)
	if err != nil || os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("synthetic machine configuration unavailable", err)
	}
	return path
}

func TestMachineHealthProbeCLIUsesProtectedDedicatedConfiguration(t *testing.T) {
	var calls atomic.Int32
	front := httpfixture.NewTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.TLS == nil || r.TLS.ServerName != strings.Split(r.Host, ":")[0] ||
			r.Host != "portal.example.invalid:19443" && r.Host != "admin.example.invalid:29443" ||
			r.Method != http.MethodGet || r.URL.RawQuery != "" || r.URL.Path != "/health/live" && r.URL.Path != "/health/ready" || r.ContentLength != 0 {
			t.Error("machine CLI changed the selected dedicated identity or health request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for name := range r.Header {
			lower := strings.ToLower(name)
			if lower == "authorization" || lower == "proxy-authorization" || lower == "cookie" || lower == "origin" || lower == "forwarded" || lower == "x-real-ip" || strings.HasPrefix(lower, "x-forwarded") {
				t.Error("machine CLI supplied credentials or client-selected authority")
				w.WriteHeader(http.StatusForbidden)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", "synthetic-machine-cli")
		_, _ = w.Write([]byte(`{"data":{"status":"ok"}}`))
	}))
	t.Logf("PROJECT_ONE_TRANSPORT_OWNED_ADDRESS=%s", front.Listener.Addr())
	path := machineProbeCLIConfiguration(t, front.Listener.Addr().String())
	ca := filepath.Join(filepath.Dir(path), "machine-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: front.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROJECT_ONE_MACHINE_ORIGIN", "https://ambient.example.invalid")
	environment := filepath.Join(filepath.Dir(path), "machine-policy.env")
	if err := os.WriteFile(environment, []byte("PROJECT_ONE_MACHINE_ORIGIN=https://admin.example.invalid:29443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, overlay := range []bool{false, true} {
		for _, kind := range []string{"live", "ready"} {
			name := kind
			if overlay {
				name += "_explicit_overlay"
			}
			t.Run(name, func(t *testing.T) {
				args := append(probeCLIArguments(path, front.Listener.Addr().String()), "--ca-file", ca)
				args[3], args[5] = "machine", kind
				if overlay {
					args = append(args, "--env-file", environment)
				}
				var output bytes.Buffer
				if err := runHealthProbeCommand(args, &output); err != nil || output.String() != "CONTROL_HEALTH_PROBE_OK\n" {
					t.Fatal("actual machine CLI failed without opening runtime credentials", err)
				}
			})
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || calls.Load() != 4 {
		t.Fatal("CLI changed configuration or its exact one-request count", err)
	}
	for _, name := range []string{"unavailable-dsn", "unavailable-hmac", "unavailable-machine-key.pem", "unavailable-machine-certificate.pem"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), name)); !os.IsNotExist(err) {
			t.Fatal("probe opened or created a serving/credential artifact", err)
		}
	}
}

func TestMachineHealthProbeCLIRefusesAbsentAndInvalidSelection(t *testing.T) {
	front, calls := newProbeCLITarget(t, "portal.example.invalid:31081")
	absent := probeCLIConfiguration(t, "127.0.0.1:31081")
	present := machineProbeCLIConfiguration(t, front.Listener.Addr().String())
	base := probeCLIArguments(present, front.Listener.Addr().String())
	base[3] = "machine"
	cases := []struct {
		name string
		args []string
		code string
	}{
		{"missing-group", append([]string{"--config", absent}, base[2:]...), "HEALTH_PROBE_CONFIGURATION_INVALID"},
		{"missing-ca", base, "HEALTH_PROBE_ARGUMENT_INVALID"},
		{"duplicate-audience", append(append([]string{}, base...), "--audience", "machine"), "HEALTH_PROBE_ARGUMENT_INVALID"},
		{"url-override", append(append([]string{}, base...), "--machine-url", "https://private.example.invalid"), "HEALTH_PROBE_ARGUMENT_INVALID"},
		{"missing-kind", []string{"--config", present, "--audience", "machine", "--dial-address", front.Listener.Addr().String(), "--timeout-seconds", "3"}, "HEALTH_PROBE_ARGUMENT_INVALID"},
	}
	badEnvironment := filepath.Join(filepath.Dir(present), "invalid-policy.env")
	if err := os.WriteFile(badEnvironment, []byte("PROJECT_ONE_MACHINE_ORIGIN=http://private.example.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cases = append(cases, struct {
		name string
		args []string
		code string
	}{"invalid-explicit-machine-origin", append(append([]string{}, base...), "--env-file", badEnvironment), "HEALTH_PROBE_CONFIGURATION_INVALID"})
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			var output bytes.Buffer
			err := runHealthProbeCommand(item.args, &output)
			if err == nil || err.Error() != item.code || output.Len() != 0 {
				t.Fatal("unsafe machine selection did not refuse with exact redacted error", err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("invalid machine selection reached a network endpoint")
	}
}
