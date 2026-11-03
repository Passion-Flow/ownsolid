package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/servingcheck"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

// This fixture generates real synthetic TLS material without starting a listener.
// Every business credential is deliberately unavailable in the original fixture.
func machineServingCLIConfiguration(t *testing.T) (string, config.Config) {
	t.Helper()
	path := probeCLIConfiguration(t, "127.0.0.1:1")
	base, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil {
		t.Fatal(e)
	}
	path = filepath.Join(base, filepath.Base(path))
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"machine.example.invalid", "overridden-machine.example.invalid"}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	private, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	m := config.MachineServerConfiguration{ListenAddress: "127.0.0.1:29443", Origin: "https://machine.example.invalid:29443",
		TLSCertificateFile: filepath.Join(base, "machine-certificate.pem"), TLSPrivateKeyFile: filepath.Join(base, "machine-key.pem")}
	for path, raw := range map[string][]byte{m.TLSCertificateFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), m.TLSPrivateKeyFile: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})} {
		if e := os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]any
	if yaml.Unmarshal(raw, &fields) != nil {
		t.Fatal("fixture YAML")
	}
	fields["machine_server"] = m
	raw, e = yaml.Marshal(fields)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	c, e := config.LoadProtectedWithEnvironment(path, "")
	if e != nil {
		t.Fatal("actual machine CLI fixture", e)
	}
	return path, c
}

func machineServingCLIJSON(t *testing.T, m *config.MachineServerConfiguration) string {
	t.Helper()
	raw, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}

func machineServingCLIArguments(t *testing.T, path string, c config.Config) []string {
	t.Helper()
	return []string{"--config", path, "--expected-server-json", servingCLIExpectation(t, c.Server), "--expected-machine-json", machineServingCLIJSON(t, c.MachineServer)}
}

func TestMachineServingCheckCLIExactIntentAndExplicitEnvironment(t *testing.T) {
	path, c := machineServingCLIConfiguration(t)
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = append([]string{"control", servingCheckCommand}, machineServingCLIArguments(t, path, c)...)
	if e := run(); e != nil {
		t.Fatal("actual early machine checker opened business owner", e)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	changed := *c.MachineServer
	changed.ListenAddress = "127.0.0.1:29444"
	changed.Origin = "https://overridden-machine.example.invalid:29444"
	for _, pair := range [][2]string{{c.MachineServer.TLSCertificateFile, "override-certificate.pem"}, {c.MachineServer.TLSPrivateKeyFile, "override-key.pem"}} {
		raw, e := os.ReadFile(pair[0])
		if e != nil {
			t.Fatal(e)
		}
		target := filepath.Join(filepath.Dir(path), pair[1])
		if e = os.WriteFile(target, raw, 0600); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(pair[1], "certificate") {
			changed.TLSCertificateFile = target
		} else {
			changed.TLSPrivateKeyFile = target
		}
	}
	env := filepath.Join(filepath.Dir(path), "machine-check.env")
	values := "PROJECT_ONE_MACHINE_LISTEN_ADDRESS=" + changed.ListenAddress + "\nPROJECT_ONE_MACHINE_ORIGIN=" + changed.Origin + "\nPROJECT_ONE_MACHINE_TLS_CERTIFICATE_FILE=" + changed.TLSCertificateFile + "\nPROJECT_ONE_MACHINE_TLS_PRIVATE_KEY_FILE=" + changed.TLSPrivateKeyFile + "\n"
	if e = os.WriteFile(env, []byte(values), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PROJECT_ONE_MACHINE_ORIGIN", "https://untrusted-ambient.example.invalid")
	c.MachineServer = &changed
	args := append(machineServingCLIArguments(t, path, c), "--env-file", env)
	var out bytes.Buffer
	if e = runServingCheckCommand(args, &out); e != nil || out.String() != servingcheck.Success+"\n" {
		t.Fatal("same artifact explicit machine environment", e)
	}
	out.Reset()
	if e = runServingCheckCommand(args[:6], &out); e == nil || e.Error() != "SERVING_TRANSPORT_MISMATCH" || out.Len() != 0 {
		t.Fatal("ambient policy or base intent silently adopted", e)
	}
	after, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("checker changed configuration", e)
	}
	for _, name := range []string{"unavailable-dsn", "unavailable-hmac", "unavailable-mail", "unavailable-blocklist"} {
		if _, e = os.Stat(filepath.Join(filepath.Dir(path), name)); !os.IsNotExist(e) {
			t.Fatal("checker created business state")
		}
	}
}

func TestMachineServingCheckCLIRejectsArgumentsAndIntentBeforeIO(t *testing.T) {
	machineServingSchemaControls(t)
	server := config.ServerConfiguration{TransportMode: config.TransportHTTP, ListenAddress: "127.0.0.1:1", PortalOrigin: "http://portal.example.invalid", AdminOrigin: "http://admin.example.invalid"}
	base := []string{"--config", filepath.Join(t.TempDir(), "synthetic-sensitive-unavailable.yaml"), "--expected-server-json", servingCLIExpectation(t, server)}
	for name, extra := range map[string][]string{"empty": {"--expected-machine-json="}, "duplicate": {"--expected-machine-json={}", "--expected-machine-json={}"}, "missing_value": {"--expected-machine-json"}} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			e := runServingCheckCommand(append(append([]string{}, base...), extra...), &out)
			if e == nil || e.Error() != "SERVING_TRANSPORT_ARGUMENT_INVALID" || out.Len() != 0 {
				t.Fatal("argument reached I/O or leaked", e)
			}
		})
	}
	for _, raw := range []string{"null", "{}", "false", `{"origin":"synthetic-sensitive-value"}`, strings.Repeat(" ", servingcheck.MaximumExpectedBytes+1)} {
		var out bytes.Buffer
		e := runServingCheckCommand(append(append([]string{}, base...), "--expected-machine-json", raw), &out)
		if e == nil || e.Error() != "SERVING_TRANSPORT_EXPECTATION_INVALID" || out.Len() != 0 {
			t.Fatal("intent reached unavailable config or leaked", e)
		}
	}
}

func TestMachineServingCheckCLIRejectsHiddenOrBrokenTLS(t *testing.T) {
	path, c := machineServingCLIConfiguration(t)
	args := machineServingCLIArguments(t, path, c)
	var out bytes.Buffer
	if e := runServingCheckCommand(args[:4], &out); e == nil || e.Error() != "SERVING_TRANSPORT_MISMATCH" || out.Len() != 0 {
		t.Fatal("hidden dedicated machine passed browser verification", e)
	}
	if e := runServingCheckCommand(args, failedProbeOutput{}); e == nil || e.Error() != "SERVING_TRANSPORT_OUTPUT_FAILED" {
		t.Fatal("output failure exposed or passed", e)
	}
	if e := os.WriteFile(c.MachineServer.TLSCertificateFile, []byte("synthetic-sensitive-invalid-certificate\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := runServingCheckCommand(args, &out); e == nil || e.Error() != "SERVING_TRANSPORT_CONFIGURATION_INVALID" || out.Len() != 0 {
		t.Fatal("actual invalid machine TLS material leaked or passed", e)
	}
}

func machineServingSchemaControls(t *testing.T) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..")
	raw, e := os.ReadFile(filepath.Join(root, "api/configuration/v1/control-machine-server/schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	var doc any
	if json.Unmarshal(raw, &doc) != nil {
		t.Fatal("schema JSON")
	}
	compiler := jsonschema.NewCompiler()
	const id = "https://projectone.invalid/configuration/control-machine-server/v1"
	if e = compiler.AddResource(id, doc); e != nil {
		t.Fatal(e)
	}
	schema, e := compiler.Compile(id)
	if e != nil {
		t.Fatal(e)
	}
	raw, e = os.ReadFile(filepath.Join(root, "services/control/config/examples/control/machine-server.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	var example map[string]any
	if json.Unmarshal(raw, &example) != nil || schema.Validate(example) != nil {
		t.Fatal("actual example/schema drift")
	}
	if _, e = servingcheck.ParseExpectedMachine(raw); e != nil {
		t.Fatal("actual example/parser drift", e)
	}
	for field := range example {
		for _, kind := range []string{"missing", "null", "type"} {
			t.Run("machine_schema_"+field+"_"+kind, func(t *testing.T) {
				candidate := map[string]any{}
				for key, value := range example {
					candidate[key] = value
				}
				switch kind {
				case "missing":
					delete(candidate, field)
				case "null":
					candidate[field] = nil
				case "type":
					candidate[field] = false
				}
				raw, e := json.Marshal(candidate)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = servingcheck.ParseExpectedMachine(raw); e == nil || schema.Validate(candidate) == nil {
					t.Fatal("required field accepted")
				}
			})
		}
	}
}
