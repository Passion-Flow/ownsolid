package servingcheck

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func syntheticMachineIntent() config.MachineServerConfiguration {
	return config.MachineServerConfiguration{ListenAddress: "127.0.0.1:28443", Origin: "https://machine.example.invalid:29443",
		TLSCertificateFile: "/synthetic-serving-unavailable/machine-certificate.pem", TLSPrivateKeyFile: "/synthetic-serving-unavailable/machine-private-key.pem"}
}

func machineIntentBytes(t *testing.T, value config.MachineServerConfiguration) []byte {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestMachineServingExpectedStrictContract(t *testing.T) {
	base := syntheticMachineIntent()
	for _, origin := range []string{"https://machine.example.invalid:29443", "https://localhost:29443", "https://127.0.0.1:29443", "https://[::1]:29443"} {
		t.Run(origin, func(t *testing.T) {
			value := base
			value.Origin = origin
			raw := machineIntentBytes(t, value)
			before := append([]byte{}, raw...)
			parsed, e := ParseExpectedMachine(raw)
			if e != nil || parsed != value || !bytes.Equal(raw, before) {
				t.Fatal("strict machine intent changed or opened material", e)
			}
		})
	}
	good := machineIntentBytes(t, base)
	for _, field := range []string{"listen_address", "origin", "tls_certificate_file", "tls_private_key_file"} {
		for _, variant := range []string{"missing", "null", "type", "empty", "case"} {
			t.Run(field+"_"+variant, func(t *testing.T) {
				var fields map[string]any
				if json.Unmarshal(good, &fields) != nil {
					t.Fatal("fixture JSON")
				}
				switch variant {
				case "missing":
					delete(fields, field)
				case "null":
					fields[field] = nil
				case "type":
					fields[field] = false
				case "empty":
					fields[field] = ""
				case "case":
					fields[strings.ToUpper(field)] = fields[field]
					delete(fields, field)
				}
				raw, _ := json.Marshal(fields)
				parsed, e := ParseExpectedMachine(raw)
				servingCheckFailure(t, e, expectationInvalid)
				if parsed != (config.MachineServerConfiguration{}) {
					t.Fatal("partial intent escaped")
				}
			})
		}
	}
	for name, raw := range map[string][]byte{
		"empty": nil, "null": []byte("null"), "array": []byte("[]"), "boolean": []byte("false"),
		"duplicate": append([]byte(`{"origin":"https://machine.example.invalid:29443",`), good[1:]...),
		"unknown":   append([]byte(`{"sensitive_unknown":"synthetic-sensitive-value",`), good[1:]...),
		"trailing":  append(append([]byte{}, good...), []byte(" {}")...),
		"oversize":  bytes.Repeat([]byte(" "), MaximumExpectedBytes+1),
	} {
		t.Run(name, func(t *testing.T) { _, e := ParseExpectedMachine(raw); servingCheckFailure(t, e, expectationInvalid) })
	}
	for name, change := range map[string]func(*config.MachineServerConfiguration){
		"http":     func(m *config.MachineServerConfiguration) { m.Origin = "http://machine.example.invalid" },
		"bind":     func(m *config.MachineServerConfiguration) { m.ListenAddress = "127.0.0.1:028443" },
		"relative": func(m *config.MachineServerConfiguration) { m.TLSPrivateKeyFile = "private.pem" },
		"alias":    func(m *config.MachineServerConfiguration) { m.TLSPrivateKeyFile = m.TLSCertificateFile },
	} {
		t.Run(name, func(t *testing.T) {
			m := base
			change(&m)
			_, e := ParseExpectedMachine(machineIntentBytes(t, m))
			servingCheckFailure(t, e, expectationInvalid)
		})
	}
	padded := append(append([]byte{}, good...), bytes.Repeat([]byte(" "), MaximumExpectedBytes-len(good))...)
	if actual, e := ParseExpectedMachine(padded); e != nil || actual != base {
		t.Fatal("inclusive bound", e)
	}
}

func actualMachineConfiguration(t *testing.T, c *config.Config, origin string, names []string, ips []net.IP, expired bool) {
	t.Helper()
	material := *c
	syntheticServingPair(t, &material, names, ips, expired)
	m := syntheticMachineIntent()
	m.Origin = origin
	m.TLSCertificateFile, m.TLSPrivateKeyFile = material.Server.TLSCertificateFile, material.Server.TLSPrivateKeyFile
	c.MachineServer = &m
}

func TestMachineServingCheckMatchesDedicatedAndLegacy(t *testing.T) {
	for _, mode := range []config.TransportMode{config.TransportHTTP, config.TransportDirectHTTPS, config.TransportTrustedProxyHTTPS} {
		t.Run(string(mode), func(t *testing.T) {
			c := syntheticServingConfiguration(t, mode)
			if mode == config.TransportDirectHTTPS {
				syntheticServingPair(t, &c, []string{"portal.example.invalid", "admin.example.invalid"}, nil, false)
			}
			if e := Verify(c, syntheticExpected(c)); e != nil {
				t.Fatal("legacy changed", e)
			}
			actualMachineConfiguration(t, &c, "https://machine.example.invalid:29443", []string{"machine.example.invalid"}, nil, false)
			intent := *c.MachineServer
			before := machineIntentBytes(t, intent)
			if e := VerifyWithMachine(c, syntheticExpected(c), &intent); e != nil {
				t.Fatal("matching actual dual material", e)
			}
			if !bytes.Equal(before, machineIntentBytes(t, intent)) || !reflect.DeepEqual(*c.MachineServer, intent) {
				t.Fatal("intent mutated")
			}
		})
	}
	for _, origin := range []string{"https://localhost:29443", "https://127.0.0.1:29443", "https://[::1]:29443"} {
		t.Run(origin, func(t *testing.T) {
			c := syntheticServingConfiguration(t, config.TransportHTTP)
			actualMachineConfiguration(t, &c, origin, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}, false)
			if e := VerifyWithMachine(c, syntheticExpected(c), c.MachineServer); e != nil {
				t.Fatal("actual selected SAN", e)
			}
		})
	}
}

func TestMachineServingCheckRefusesHiddenOrMismatchedIngress(t *testing.T) {
	c := syntheticServingConfiguration(t, config.TransportDirectHTTPS)
	intent := syntheticMachineIntent()
	c.MachineServer = &intent
	// Every material path is unavailable: exact mismatch must precede any read.
	servingCheckFailure(t, Verify(c, syntheticExpected(c)), transportMismatch)
	for name, change := range map[string]func(*config.MachineServerConfiguration){
		"bind":   func(m *config.MachineServerConfiguration) { m.ListenAddress = "127.0.0.1:28444" },
		"origin": func(m *config.MachineServerConfiguration) { m.Origin = "https://other.example.invalid:29443" },
		"certificate": func(m *config.MachineServerConfiguration) {
			m.TLSCertificateFile = "/synthetic-serving-unavailable/other-certificate.pem"
		},
		"key": func(m *config.MachineServerConfiguration) {
			m.TLSPrivateKeyFile = "/synthetic-serving-unavailable/other-private-key.pem"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := intent
			change(&changed)
			servingCheckFailure(t, VerifyWithMachine(c, syntheticExpected(c), &changed), transportMismatch)
		})
	}
	browser := syntheticExpected(c)
	browser.PortalOrigin = "https://other-portal.example.invalid"
	servingCheckFailure(t, VerifyWithMachine(c, browser, &intent), transportMismatch)
	c.MachineServer = nil
	servingCheckFailure(t, VerifyWithMachine(c, syntheticExpected(c), &intent), transportMismatch)
	bad := intent
	bad.Origin = "http://machine.example.invalid"
	servingCheckFailure(t, VerifyWithMachine(c, syntheticExpected(c), &bad), expectationInvalid)
}

func TestMachineServingCheckRetainsActualTLSRefusals(t *testing.T) {
	for _, variant := range []string{"wrong_san", "expired", "malformed", "public_private_key"} {
		t.Run(variant, func(t *testing.T) {
			c := syntheticServingConfiguration(t, config.TransportHTTP)
			names := []string{"machine.example.invalid"}
			if variant == "wrong_san" {
				names = []string{"other.example.invalid"}
			}
			actualMachineConfiguration(t, &c, "https://machine.example.invalid:29443", names, nil, variant == "expired")
			if variant == "malformed" {
				if e := os.WriteFile(c.MachineServer.TLSCertificateFile, []byte("synthetic invalid PEM\n"), 0644); e != nil {
					t.Fatal(e)
				}
			}
			if variant == "public_private_key" {
				if e := os.Chmod(c.MachineServer.TLSPrivateKeyFile, 0644); e != nil {
					t.Fatal(e)
				}
			}
			servingCheckFailure(t, VerifyWithMachine(c, syntheticExpected(c), c.MachineServer), configurationInvalid)
		})
	}
}
