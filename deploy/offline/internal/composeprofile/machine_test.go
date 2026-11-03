package composeprofile

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Synthetic public intent only: these files and listeners are never provisioned.
func machineProfileFixture() MachineProfile {
	return MachineProfile{
		FormatVersion:             1,
		BrowserBinding:            Binding{BindAddress: "127.0.0.1", PublishedPort: 18080, TargetPort: 8080},
		MachineBinding:            Binding{BindAddress: "127.0.0.1", PublishedPort: 19443, TargetPort: 9443},
		PolicyEnvironmentBasename: "runtime.env", ProbeCABasename: "machine-ca.pem",
		ProbeDialAddress: "127.0.0.1:9443", ProbeTimeoutSeconds: 3,
		HealthIntervalSeconds: 30, HealthTimeoutSeconds: 5, HealthStartPeriodSeconds: 0, HealthRetries: 3,
	}
}

func machineProfileJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func machineProfileJSONChange(t *testing.T, raw []byte, change func(map[string]json.RawMessage)) []byte {
	t.Helper()
	var members map[string]json.RawMessage
	if e := json.Unmarshal(raw, &members); e != nil {
		t.Fatal(e)
	}
	change(members)
	return machineProfileJSON(t, members)
}

func TestComposeMachineProfileStrictInputs(t *testing.T) {
	base := machineProfileFixture()
	valid := map[string]MachineProfile{"nondefault": base}
	ipv6 := base
	ipv6.BrowserBinding.BindAddress, ipv6.MachineBinding.BindAddress = "::1", "::"
	ipv6.ProbeDialAddress = "[::1]:9443"
	valid["ipv6"] = ipv6
	wildcard := base
	wildcard.BrowserBinding.BindAddress, wildcard.MachineBinding.BindAddress = "0.0.0.0", "0.0.0.0"
	valid["wildcards"] = wildcard
	minimum := base
	minimum.BrowserBinding.PublishedPort, minimum.BrowserBinding.TargetPort = 65535, 65535
	minimum.MachineBinding.PublishedPort, minimum.MachineBinding.TargetPort = 1, 1
	minimum.ProbeDialAddress, minimum.ProbeTimeoutSeconds = "127.0.0.1:1", 1
	minimum.HealthTimeoutSeconds, minimum.HealthIntervalSeconds, minimum.HealthRetries = 2, 2, 1
	valid["minimum-boundaries"] = minimum
	maximum := base
	maximum.ProbeTimeoutSeconds, maximum.HealthTimeoutSeconds = 60, 120
	maximum.HealthIntervalSeconds, maximum.HealthStartPeriodSeconds, maximum.HealthRetries = 3600, 3600, 100
	maximum.PolicyEnvironmentBasename, maximum.ProbeCABasename = strings.Repeat("p", 128), strings.Repeat("c", 128)
	valid["maximum-boundaries"] = maximum
	for name, p := range valid {
		t.Run("valid-"+name, func(t *testing.T) {
			if got, e := LoadMachine(machineProfileJSON(t, p), nil); e != nil || got != p {
				t.Fatal("complete explicit intent changed", got, e)
			}
		})
	}
	raw := machineProfileJSON(t, base)
	invalid := map[string][]byte{
		"empty": nil, "null": []byte(`null`), "array": []byte(`[]`),
		"unknown":      append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"secret":"synthetic-do-not-log"}`)...),
		"duplicate":    append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"format_version":1}`)...),
		"trailing":     append(append([]byte(nil), raw...), []byte(`{}`)...),
		"invalid-utf8": append(append([]byte(nil), raw...), 0xff),
		"oversized":    append(append([]byte(nil), raw...), bytes.Repeat([]byte{' '}, maximumConfigurationBytes)...),
		"deep":         []byte(strings.Repeat("[", 66) + "1" + strings.Repeat("]", 66)),
	}
	for _, key := range []string{"format_version", "browser_binding", "machine_binding", "policy_environment_basename", "probe_ca_basename", "probe_dial_address", "probe_timeout_seconds", "health_interval_seconds", "health_timeout_seconds", "health_start_period_seconds", "health_retries"} {
		invalid["missing-"+key] = machineProfileJSONChange(t, raw, func(m map[string]json.RawMessage) { delete(m, key) })
		invalid["null-"+key] = machineProfileJSONChange(t, raw, func(m map[string]json.RawMessage) { m[key] = json.RawMessage(`null`) })
		invalid["wrong-type-"+key] = machineProfileJSONChange(t, raw, func(m map[string]json.RawMessage) { m[key] = json.RawMessage(`false`) })
		invalid["case-"+key] = machineProfileJSONChange(t, raw, func(m map[string]json.RawMessage) { m[strings.ToUpper(key)] = m[key]; delete(m, key) })
	}
	for _, binding := range []string{"browser_binding", "machine_binding"} {
		for _, key := range []string{"bind_address", "published_port", "target_port"} {
			for _, operation := range []string{"missing", "null", "wrong-type", "case"} {
				invalid[binding+"-"+key+"-"+operation] = machineProfileJSONChange(t, raw, func(m map[string]json.RawMessage) {
					m[binding] = machineProfileJSONChange(t, m[binding], func(b map[string]json.RawMessage) {
						switch operation {
						case "missing":
							delete(b, key)
						case "null":
							b[key] = json.RawMessage(`null`)
						case "wrong-type":
							b[key] = json.RawMessage(`false`)
						case "case":
							b[strings.ToUpper(key)] = b[key]
							delete(b, key)
						}
					})
				})
			}
		}
		invalid[binding+"-unknown"] = machineProfileJSONChange(t, raw, func(m map[string]json.RawMessage) {
			m[binding] = machineProfileJSONChange(t, m[binding], func(b map[string]json.RawMessage) { b["unknown"] = json.RawMessage(`1`) })
		})
		invalid[binding+"-duplicate"] = machineProfileJSONChange(t, raw, func(m map[string]json.RawMessage) {
			m[binding] = append(append([]byte(nil), m[binding][:len(m[binding])-1]...), []byte(`,"target_port":9443}`)...)
		})
	}
	for _, key := range []string{"format_version", "probe_timeout_seconds", "health_interval_seconds", "health_timeout_seconds", "health_start_period_seconds", "health_retries"} {
		for name, value := range map[string]string{"string": `"1"`, "fraction": "1.5", "exponent": "1e0"} {
			invalid[key+"-"+name] = machineProfileJSONChange(t, raw, func(m map[string]json.RawMessage) { m[key] = json.RawMessage(value) })
		}
	}
	for _, binding := range []string{"browser_binding", "machine_binding"} {
		for _, key := range []string{"published_port", "target_port"} {
			for name, value := range map[string]string{"string": `"8443"`, "fraction": "8443.5", "exponent": "1e3"} {
				invalid[binding+"-"+key+"-"+name] = machineProfileJSONChange(t, raw, func(m map[string]json.RawMessage) {
					m[binding] = machineProfileJSONChange(t, m[binding], func(b map[string]json.RawMessage) { b[key] = json.RawMessage(value) })
				})
			}
		}
	}
	for name, bad := range invalid {
		t.Run("json-"+name, func(t *testing.T) {
			if p, e := LoadMachine(bad, []byte("PROJECT_ONE_COMPOSE_HEALTH_RETRIES=3")); e == nil || e.Error() != machineValuesInvalid || p != (MachineProfile{}) {
				t.Fatal("invalid base was admitted or repaired by overlay", e)
			}
		})
	}
	mutations := map[string]func(*MachineProfile){
		"version":            func(p *MachineProfile) { p.FormatVersion = 2 },
		"published-conflict": func(p *MachineProfile) { p.MachineBinding.PublishedPort = p.BrowserBinding.PublishedPort },
		"target-conflict": func(p *MachineProfile) {
			p.MachineBinding.TargetPort = p.BrowserBinding.TargetPort
			p.ProbeDialAddress = "127.0.0.1:8080"
		},
		"published-conflict-distinct-host": func(p *MachineProfile) {
			p.MachineBinding.BindAddress = "192.0.2.23"
			p.MachineBinding.PublishedPort = p.BrowserBinding.PublishedPort
		},
		"target-conflict-distinct-host": func(p *MachineProfile) {
			p.MachineBinding.BindAddress = "192.0.2.23"
			p.MachineBinding.TargetPort = p.BrowserBinding.TargetPort
			p.ProbeDialAddress = "127.0.0.1:8080"
		},
		"policy-empty":          func(p *MachineProfile) { p.PolicyEnvironmentBasename = "" },
		"ca-empty":              func(p *MachineProfile) { p.ProbeCABasename = "" },
		"policy-reserved":       func(p *MachineProfile) { p.PolicyEnvironmentBasename = "control.yaml" },
		"ca-reserved":           func(p *MachineProfile) { p.ProbeCABasename = "control.yaml" },
		"basename-conflict":     func(p *MachineProfile) { p.ProbeCABasename = p.PolicyEnvironmentBasename },
		"policy-traversal":      func(p *MachineProfile) { p.PolicyEnvironmentBasename = "../runtime.env" },
		"ca-traversal":          func(p *MachineProfile) { p.ProbeCABasename = "private/ca.pem" },
		"policy-long":           func(p *MachineProfile) { p.PolicyEnvironmentBasename = strings.Repeat("p", 129) },
		"ca-long":               func(p *MachineProfile) { p.ProbeCABasename = strings.Repeat("c", 129) },
		"policy-injection":      func(p *MachineProfile) { p.PolicyEnvironmentBasename = "runtime.env\nservices: {}" },
		"ca-expansion":          func(p *MachineProfile) { p.ProbeCABasename = "${CA}" },
		"dial-public":           func(p *MachineProfile) { p.ProbeDialAddress = "192.0.2.23:9443" },
		"dial-dns":              func(p *MachineProfile) { p.ProbeDialAddress = "localhost:9443" },
		"dial-zone":             func(p *MachineProfile) { p.ProbeDialAddress = "[::1%lo0]:9443" },
		"dial-mapped":           func(p *MachineProfile) { p.ProbeDialAddress = "[::ffff:127.0.0.1]:9443" },
		"dial-noncanonical":     func(p *MachineProfile) { p.ProbeDialAddress = "[0:0:0:0:0:0:0:1]:9443" },
		"dial-other-port":       func(p *MachineProfile) { p.ProbeDialAddress = "127.0.0.1:9444" },
		"dial-no-port":          func(p *MachineProfile) { p.ProbeDialAddress = "127.0.0.1" },
		"dial-wildcard":         func(p *MachineProfile) { p.ProbeDialAddress = "0.0.0.0:9443" },
		"dial-url":              func(p *MachineProfile) { p.ProbeDialAddress = "https://127.0.0.1:9443" },
		"probe-zero":            func(p *MachineProfile) { p.ProbeTimeoutSeconds = 0 },
		"probe-large":           func(p *MachineProfile) { p.ProbeTimeoutSeconds = 61 },
		"health-timeout-zero":   func(p *MachineProfile) { p.HealthTimeoutSeconds = 0 },
		"health-timeout-equal":  func(p *MachineProfile) { p.HealthTimeoutSeconds = p.ProbeTimeoutSeconds },
		"health-timeout-large":  func(p *MachineProfile) { p.HealthTimeoutSeconds = 121 },
		"health-interval-short": func(p *MachineProfile) { p.HealthIntervalSeconds = p.HealthTimeoutSeconds - 1 },
		"health-interval-large": func(p *MachineProfile) { p.HealthIntervalSeconds = 3601 },
		"health-start-negative": func(p *MachineProfile) { p.HealthStartPeriodSeconds = -1 },
		"health-start-large":    func(p *MachineProfile) { p.HealthStartPeriodSeconds = 3601 },
		"health-retries-zero":   func(p *MachineProfile) { p.HealthRetries = 0 },
		"health-retries-large":  func(p *MachineProfile) { p.HealthRetries = 101 },
	}
	for _, binding := range []string{"browser", "machine"} {
		for name, address := range map[string]string{"empty": "", "dns": "localhost", "noncanonical": "0:0:0:0:0:0:0:1", "multicast": "ff02::1", "zone": "fe80::1%lo0", "mapped": "::ffff:127.0.0.1"} {
			mutations[binding+"-address-"+name] = func(p *MachineProfile) {
				b := &p.BrowserBinding
				if binding == "machine" {
					b = &p.MachineBinding
				}
				b.BindAddress = address
			}
		}
		for _, field := range []string{"published", "target"} {
			for _, port := range []int{-1, 0, 65536} {
				mutations[binding+"-"+field+"-"+strconv.Itoa(port)] = func(p *MachineProfile) {
					b := &p.BrowserBinding
					if binding == "machine" {
						b = &p.MachineBinding
					}
					if field == "published" {
						b.PublishedPort = port
					} else {
						b.TargetPort = port
					}
				}
			}
		}
	}
	for name, change := range mutations {
		t.Run("bounds-"+name, func(t *testing.T) {
			p := base
			change(&p)
			if got, e := LoadMachine(machineProfileJSON(t, p), nil); e == nil || e.Error() != machineValuesInvalid || got != (MachineProfile{}) {
				t.Fatal("invalid profile admitted", e)
			}
			if value, e := p.Values(); e == nil || e.Error() != machineValuesInvalid || value != nil {
				t.Fatal("direct profile bypassed validation", e)
			}
		})
	}
	if got, e := LoadMachine(append(append([]byte(nil), raw...), bytes.Repeat([]byte{' '}, maximumConfigurationBytes-len(raw))...), nil); e != nil || got != base {
		t.Fatal("exact JSON bound refused", e)
	}
}

func TestComposeMachineProfileEnvironmentMapping(t *testing.T) {
	base := machineProfileFixture()
	raw := machineProfileJSON(t, base)
	type mapping struct {
		key, value string
		change     func(*MachineProfile)
	}
	mappings := []mapping{
		{bindAddressEnvironment, "192.0.2.23", func(p *MachineProfile) { p.BrowserBinding.BindAddress = "192.0.2.23" }},
		{publishedPortEnvironment, "28080", func(p *MachineProfile) { p.BrowserBinding.PublishedPort = 28080 }},
		{targetPortEnvironment, "8081", func(p *MachineProfile) { p.BrowserBinding.TargetPort = 8081 }},
		{machineBindEnvironment, "::", func(p *MachineProfile) { p.MachineBinding.BindAddress = "::" }},
		{machinePublishEnvironment, "29443", func(p *MachineProfile) { p.MachineBinding.PublishedPort = 29443 }},
		{machineTargetEnvironment, "9444", func(p *MachineProfile) { p.MachineBinding.TargetPort = 9444; p.ProbeDialAddress = "[::1]:9444" }},
		{policyBasenameEnvironment, "alternate.env", func(p *MachineProfile) { p.PolicyEnvironmentBasename = "alternate.env" }},
		{machineCAEnvironment, "other-ca.pem", func(p *MachineProfile) { p.ProbeCABasename = "other-ca.pem" }},
		{machineDialEnvironment, "[::1]:9444", func(p *MachineProfile) { p.ProbeDialAddress = "[::1]:9444"; p.MachineBinding.TargetPort = 9444 }},
		{machineTimeoutEnvironment, "4", func(p *MachineProfile) { p.ProbeTimeoutSeconds = 4 }},
		{healthIntervalEnvironment, "31", func(p *MachineProfile) { p.HealthIntervalSeconds = 31 }},
		{healthTimeoutEnvironment, "6", func(p *MachineProfile) { p.HealthTimeoutSeconds = 6 }},
		{healthStartEnvironment, "7", func(p *MachineProfile) { p.HealthStartPeriodSeconds = 7 }},
		{healthRetriesEnvironment, "4", func(p *MachineProfile) { p.HealthRetries = 4 }},
	}
	for _, m := range mappings {
		t.Setenv(m.key, "synthetic-ambient-must-not-be-read")
	}
	if got, e := LoadMachine(raw, nil); e != nil || got != base {
		t.Fatal("ambient configuration acquired authority", e)
	}
	var lines []string
	wantAll := base
	for _, m := range mappings {
		lines = append(lines, m.key+"="+m.value)
		m.change(&wantAll)
		t.Run("mapping-"+m.key, func(t *testing.T) {
			want := base
			m.change(&want)
			env := m.key + "=" + m.value
			if m.key == machineTargetEnvironment {
				env += "\n" + machineDialEnvironment + "=[::1]:9444"
			}
			if m.key == machineDialEnvironment {
				env += "\n" + machineTargetEnvironment + "=9444"
			}
			if got, e := LoadMachine(raw, []byte(env)); e != nil || got != want {
				t.Fatal("mapping changed unrelated intent", got, e)
			}
		})
	}
	quoted := make([]string, 0, len(lines))
	for _, m := range mappings {
		value, _ := json.Marshal(m.value)
		quoted = append(quoted, m.key+"="+string(value))
	}
	for name, env := range map[string]string{"all-raw": strings.Join(lines, "\n"), "all-quotes": strings.Join(quoted, "\n"), "all-crlf-comments": "# Synthetic public overlay\r\n\r\n" + strings.Join(quoted, "\r\n") + "\r\n"} {
		t.Run(name, func(t *testing.T) {
			if got, e := LoadMachine(raw, []byte(env)); e != nil || got != wantAll {
				t.Fatal("complete fourteen-key overlay failed", got, e)
			}
		})
	}
	invalid := map[string][]byte{
		"unknown":             []byte("SECRET_FILE=synthetic-do-not-log"),
		"alias":               []byte("PROJECT_ONE_PUBLISHED_PORT=8443"),
		"export":              []byte("export " + machineCAEnvironment + "=ca.pem"),
		"missing-equals":      []byte(machineCAEnvironment),
		"key-whitespace":      []byte(machineCAEnvironment + " =ca.pem"),
		"value-whitespace":    []byte(machineCAEnvironment + "= ca.pem"),
		"trailing-whitespace": []byte(machineCAEnvironment + "=ca.pem "),
		"single-quotes":       []byte(machineCAEnvironment + "='ca.pem'"),
		"inline-comment":      []byte(machineCAEnvironment + "=ca.pem # invalid"),
		"shell":               []byte(machineCAEnvironment + "=$(hostname)"),
		"expansion":           []byte(machineCAEnvironment + "=${CA}"),
		"backticks":           []byte(machineCAEnvironment + "=`hostname`"),
		"invalid-utf8":        append([]byte("# "), 0xff),
		"nul":                 []byte("#\x00 not ignored"),
		"oversized":           bytes.Repeat([]byte{'#'}, maximumEnvironmentBytes+1),
		"quoted-trailing":     []byte(machineCAEnvironment + `="ca.pem" "other.pem"`),
		"target-without-dial": []byte(machineTargetEnvironment + "=9444"),
		"dial-without-target": []byte(machineDialEnvironment + "=[::1]:9444"),
		"equal-health-probe":  []byte(healthTimeoutEnvironment + "=3"),
		"published-conflict":  []byte(machinePublishEnvironment + "=18080"),
		"basename-conflict":   []byte(machineCAEnvironment + "=runtime.env"),
	}
	for _, m := range mappings {
		invalid["duplicate-"+m.key] = []byte(m.key + "=" + m.value + "\n" + m.key + "=" + m.value)
		invalid["empty-"+m.key] = []byte(m.key + "=")
		invalid["case-"+m.key] = []byte(strings.ToLower(m.key) + "=" + m.value)
	}
	for _, key := range []string{publishedPortEnvironment, targetPortEnvironment, machinePublishEnvironment, machineTargetEnvironment, machineTimeoutEnvironment, healthIntervalEnvironment, healthTimeoutEnvironment, healthStartEnvironment, healthRetriesEnvironment} {
		for name, value := range map[string]string{"false": "false", "negative": "-1", "noncanonical": "01"} {
			invalid[key+"-"+name] = []byte(key + "=" + value)
		}
	}
	for name, env := range invalid {
		t.Run(name, func(t *testing.T) {
			if got, e := LoadMachine(raw, env); e == nil || e.Error() != machineEnvironmentInvalid || got != (MachineProfile{}) {
				t.Fatal("invalid overlay admitted or error leaked", e)
			}
		})
	}
	if got, e := LoadMachine(raw, []byte(healthStartEnvironment+"=0")); e != nil || got != base {
		t.Fatal("explicit zero start period refused", e)
	}
	if got, e := LoadMachine(raw, bytes.Repeat([]byte{'#'}, maximumEnvironmentBytes)); e != nil || got != base {
		t.Fatal("exact environment byte bound refused", e)
	}
}

func TestComposeMachineProfileNativeHealthProjection(t *testing.T) {
	base := machineProfileFixture()
	ipv6 := base
	ipv6.BrowserBinding.BindAddress = "::"
	ipv6.MachineBinding.BindAddress = "::1"
	ipv6.ProbeDialAddress = "[::1]:9443"
	maximum := base
	maximum.ProbeTimeoutSeconds = 60
	maximum.HealthTimeoutSeconds = 120
	maximum.HealthIntervalSeconds = 3600
	maximum.HealthStartPeriodSeconds = 3600
	maximum.HealthRetries = 100
	for name, p := range map[string]MachineProfile{"http-neutral-browser": base, "ipv6": ipv6, "maximum-schedule": maximum} {
		t.Run(name, func(t *testing.T) {
			value, e := p.Values()
			if e != nil {
				t.Fatal(e)
			}
			if bytes.Count(value, []byte("ports: !override")) != 1 || bytes.Count(value, []byte("- target:")) != 2 || bytes.Contains(value, []byte("environment:")) || bytes.Contains(value, []byte("volumes:")) || bytes.Contains(value, []byte("image:")) || bytes.Contains(value, []byte("CMD-SHELL")) {
				t.Fatal("projection changed unrelated runtime authority")
			}
			var command, probe []string
			fields := map[string]string{}
			var actualBindings []Binding
			for _, line := range strings.Split(string(value), "\n") {
				key, raw, ok := strings.Cut(strings.TrimSpace(line), ": ")
				if !ok {
					continue
				}
				switch key {
				case "command":
					if json.Unmarshal([]byte(raw), &command) != nil {
						t.Fatal("invalid command flow sequence")
					}
				case "test":
					if json.Unmarshal([]byte(raw), &probe) != nil {
						t.Fatal("invalid probe flow sequence")
					}
				case "- target":
					port, e := strconv.Atoi(raw)
					if e != nil {
						t.Fatal(e)
					}
					actualBindings = append(actualBindings, Binding{TargetPort: port})
				case "published":
					var port string
					if json.Unmarshal([]byte(raw), &port) != nil || len(actualBindings) == 0 {
						t.Fatal("invalid published scalar")
					}
					n, e := strconv.Atoi(port)
					if e != nil {
						t.Fatal(e)
					}
					actualBindings[len(actualBindings)-1].PublishedPort = n
				case "host_ip":
					if len(actualBindings) == 0 || json.Unmarshal([]byte(raw), &actualBindings[len(actualBindings)-1].BindAddress) != nil {
						t.Fatal("invalid bind scalar")
					}
				case "interval", "timeout", "start_period":
					var duration string
					if json.Unmarshal([]byte(raw), &duration) != nil {
						t.Fatal("invalid duration scalar")
					}
					fields[key] = duration
				case "retries":
					fields[key] = raw
				case "protocol":
					if raw != `"tcp"` {
						t.Fatal("unexpected port protocol")
					}
				}
			}
			wantCommand := []string{"serve", "--config", "/run/projectone/control.yaml", "--env-file", "/run/projectone/" + p.PolicyEnvironmentBasename}
			wantProbe := []string{"CMD", "/usr/local/bin/control", "health-probe", "--config", "/run/projectone/control.yaml", "--env-file", "/run/projectone/" + p.PolicyEnvironmentBasename, "--audience", "machine", "--kind", "ready", "--dial-address", p.ProbeDialAddress, "--timeout-seconds", strconv.Itoa(p.ProbeTimeoutSeconds), "--ca-file", "/run/projectone/" + p.ProbeCABasename}
			if !reflect.DeepEqual(command, wantCommand) || !reflect.DeepEqual(probe, wantProbe) || !reflect.DeepEqual(actualBindings, []Binding{p.BrowserBinding, p.MachineBinding}) {
				t.Fatal("publication or matched native argv differs", command, probe, actualBindings)
			}
			wantFields := map[string]string{"interval": strconv.Itoa(p.HealthIntervalSeconds) + "s", "timeout": strconv.Itoa(p.HealthTimeoutSeconds) + "s", "start_period": strconv.Itoa(p.HealthStartPeriodSeconds) + "s", "retries": strconv.Itoa(p.HealthRetries)}
			if !reflect.DeepEqual(fields, wantFields) {
				t.Fatal("native scheduling differs", fields)
			}
			again, e := p.Values()
			if e != nil || !bytes.Equal(value, again) {
				t.Fatal("projection is not deterministic", e)
			}
			value[0] = 'X'
			if bytes.Equal(value, again) {
				t.Fatal("projection aliases returned mutable bytes")
			}
		})
	}
}
