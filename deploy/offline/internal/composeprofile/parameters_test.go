package composeprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func portValuesFixture(t *testing.T, p Parameters) []byte {
	t.Helper()
	raw, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestComposePortValuesStrictInputs(t *testing.T) {
	base := Parameters{FormatVersion: 1, BindAddress: "127.0.0.1", PublishedPort: 18443, TargetPort: 9443}
	for name, address := range map[string]string{
		"loopback-ipv4": "127.0.0.1", "loopback-ipv6": "::1",
		"wildcard-ipv4": "0.0.0.0", "wildcard-ipv6": "::",
		"selected-ipv4": "192.0.2.23", "selected-ipv6": "2001:db8::23",
		"private-ipv4": "10.23.1.2", "private-ipv6": "fd23::1",
	} {
		t.Run(name, func(t *testing.T) {
			p := base
			p.BindAddress = address
			actual, e := Load(portValuesFixture(t, p), nil)
			if e != nil || actual != p {
				t.Fatalf("explicit address changed: %+v, %v", actual, e)
			}
		})
	}
	for _, port := range []int{1, 65535} {
		t.Run(fmt.Sprintf("port-boundary-%d", port), func(t *testing.T) {
			p := base
			p.PublishedPort, p.TargetPort = port, port
			if actual, e := Load(portValuesFixture(t, p), nil); e != nil || actual != p {
				t.Fatal(actual, e)
			}
		})
	}
	for name, address := range map[string]string{
		"empty": "", "dns": "portal.example.invalid", "localhost": "localhost",
		"bracketed": "[::1]", "ipv4-leading-zero": "127.000.0.1",
		"ipv6-expanded": "0:0:0:0:0:0:0:1", "ipv6-uppercase": "2001:DB8::1",
		"ipv4-mapped": "::ffff:192.0.2.1", "ipv4-mapped-hex": "::ffff:c000:201",
		"zone": "fe80::1%lo0", "linklocal-ipv6": "fe80::1",
		"linklocal-ipv4": "169.254.1.2", "multicast-ipv4": "224.0.0.1",
		"multicast-ipv6": "ff02::1", "broadcast": "255.255.255.255",
		"whitespace": " 127.0.0.1", "port": "127.0.0.1:8443",
		"yaml-injection": "127.0.0.1\nservices: {}", "shell": "$(hostname)",
	} {
		t.Run("address-"+name, func(t *testing.T) {
			p := base
			p.BindAddress = address
			if _, e := Load(portValuesFixture(t, p), nil); e == nil || e.Error() != valuesInvalid {
				t.Fatal("unsafe address accepted or error leaked", e)
			}
			if value, e := p.Values(); e == nil || value != nil {
				t.Fatal("direct construction bypassed validation")
			}
		})
	}
	valid := string(portValuesFixture(t, base))
	invalid := map[string][]byte{
		"empty": nil, "object": []byte(`{}`), "array": []byte(`[]`), "null": []byte(`null`),
		"missing-version":   []byte(`{"bind_address":"127.0.0.1","published_port":18443,"target_port":9443}`),
		"missing-address":   []byte(`{"format_version":1,"published_port":18443,"target_port":9443}`),
		"missing-published": []byte(`{"format_version":1,"bind_address":"127.0.0.1","target_port":9443}`),
		"missing-target":    []byte(`{"format_version":1,"bind_address":"127.0.0.1","published_port":18443}`),
		"unknown":           []byte(strings.TrimSuffix(valid, "}") + `,"secret":"synthetic-do-not-log"}`),
		"duplicate":         []byte(strings.TrimSuffix(valid, "}") + `,"target_port":8443}`),
		"escaped-duplicate": []byte(strings.TrimSuffix(valid, "}") + `,"target\u005fport":8443}`),
		"case-drift":        []byte(strings.Replace(valid, `"bind_address"`, `"Bind_Address"`, 1)),
		"trailing":          []byte(valid + `{}`), "invalid-utf8": append([]byte(valid), 0xff),
		"oversized": []byte(valid + strings.Repeat(" ", maximumConfigurationBytes)),
		"deep":      []byte(strings.Repeat("[", 66) + "1" + strings.Repeat("]", 66)),
	}
	for name, replacement := range map[string]string{
		"zero-version": `"format_version":0`, "future-version": `"format_version":2`,
		"null-version": `"format_version":null`, "boolean-version": `"format_version":true`,
		"string-version": `"format_version":"1"`, "exponent-version": `"format_version":1e0`,
	} {
		invalid[name] = []byte(strings.Replace(valid, `"format_version":1`, replacement, 1))
	}
	for name, value := range map[string]string{"null": "null", "boolean": "false", "number": "127", "object": "{}", "array": "[]"} {
		invalid["address-type-"+name] = []byte(strings.Replace(valid, `"bind_address":"127.0.0.1"`, `"bind_address":`+value, 1))
	}
	for _, field := range []string{"published_port", "target_port"} {
		old := fmt.Sprintf(`"%s":%d`, field, map[string]int{"published_port": 18443, "target_port": 9443}[field])
		for name, value := range map[string]string{"zero": "0", "negative": "-1", "too-large": "65536", "huge": "999999999999999999999999", "false": "false", "null": "null", "fraction": "8443.5", "exponent": "1e3", "string": `"8443"`, "object": "{}", "array": "[]"} {
			invalid[field+"-"+name] = []byte(strings.Replace(valid, old, `"`+field+`":`+value, 1))
		}
	}
	for name, raw := range invalid {
		t.Run("json-"+name, func(t *testing.T) {
			if p, e := Load(raw, []byte("PROJECT_ONE_CONTROL_PORT=8443\n")); e == nil || e.Error() != valuesInvalid || p != (Parameters{}) {
				t.Fatal("invalid base repaired by overlay or error leaked", e)
			}
		})
	}
	for _, mutate := range []func(*Parameters){
		func(p *Parameters) { p.FormatVersion = 0 },
		func(p *Parameters) { p.PublishedPort = 0 },
		func(p *Parameters) { p.PublishedPort = 65536 },
		func(p *Parameters) { p.TargetPort = 0 },
		func(p *Parameters) { p.TargetPort = 65536 },
	} {
		p := base
		mutate(&p)
		if value, e := p.Values(); e == nil || e.Error() != valuesInvalid || value != nil {
			t.Fatal("invalid direct Parameters emitted a projection", e)
		}
	}
	if p, e := Load(append([]byte(valid), bytes.Repeat([]byte{' '}, maximumConfigurationBytes-len(valid))...), nil); e != nil || p != base {
		t.Fatal("exact JSON byte boundary refused", e)
	}
}

func TestComposePortValuesExactEnvironmentMapping(t *testing.T) {
	base := Parameters{FormatVersion: 1, BindAddress: "127.0.0.1", PublishedPort: 18443, TargetPort: 9443}
	raw := portValuesFixture(t, base)
	for _, key := range []string{bindAddressEnvironment, publishedPortEnvironment, targetPortEnvironment} {
		t.Setenv(key, "synthetic-ambient-value-must-not-be-read")
	}
	if p, e := Load(raw, nil); e != nil || p != base {
		t.Fatal("ambient environment changed explicit intent", e)
	}
	for name, environment := range map[string]string{
		"raw":           "PROJECT_ONE_BIND_ADDRESS=::1\nPROJECT_ONE_HTTPS_PORT=19443\nPROJECT_ONE_CONTROL_PORT=10443\n",
		"json-quotes":   "PROJECT_ONE_BIND_ADDRESS=\"::1\"\nPROJECT_ONE_HTTPS_PORT=\"19443\"\nPROJECT_ONE_CONTROL_PORT=\"10443\"\n",
		"crlf-comments": "# Public synthetic overlay\r\n\r\nPROJECT_ONE_BIND_ADDRESS=::1\r\nPROJECT_ONE_HTTPS_PORT=19443\r\nPROJECT_ONE_CONTROL_PORT=10443\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			want := Parameters{FormatVersion: 1, BindAddress: "::1", PublishedPort: 19443, TargetPort: 10443}
			if p, e := Load(raw, []byte(environment)); e != nil || p != want {
				t.Fatal("explicit exact mapping failed", p, e)
			}
		})
	}
	if p, e := Load(raw, []byte("PROJECT_ONE_CONTROL_PORT=1")); e != nil || p.TargetPort != 1 || p.BindAddress != base.BindAddress || p.PublishedPort != base.PublishedPort {
		t.Fatal("partial overlay changed unrelated intent", p, e)
	}
	invalid := map[string][]byte{
		"unknown":              []byte("SECRET_FILE=synthetic-do-not-log"),
		"new-alias":            []byte("PROJECT_ONE_PUBLISHED_PORT=8443"),
		"duplicate":            []byte("PROJECT_ONE_CONTROL_PORT=9443\nPROJECT_ONE_CONTROL_PORT=8443"),
		"unknown-case":         []byte("project_one_control_port=8443"),
		"export":               []byte("export PROJECT_ONE_CONTROL_PORT=8443"),
		"missing-equals":       []byte("PROJECT_ONE_CONTROL_PORT"),
		"empty":                []byte("PROJECT_ONE_CONTROL_PORT="),
		"empty-quoted":         []byte(`PROJECT_ONE_CONTROL_PORT=""`),
		"key-space":            []byte("PROJECT_ONE_CONTROL_PORT =8443"),
		"leading-value-space":  []byte("PROJECT_ONE_CONTROL_PORT= 8443"),
		"trailing-value-space": []byte("PROJECT_ONE_CONTROL_PORT=8443 "),
		"single-quotes":        []byte("PROJECT_ONE_CONTROL_PORT='8443'"),
		"quoted-trailing":      []byte(`PROJECT_ONE_CONTROL_PORT="8443" "9443"`),
		"inline-comment":       []byte("PROJECT_ONE_CONTROL_PORT=8443 # port"),
		"shell":                []byte("PROJECT_ONE_CONTROL_PORT=$(id)"),
		"expansion":            []byte("PROJECT_ONE_CONTROL_PORT=${PORT}"),
		"backticks":            []byte("PROJECT_ONE_BIND_ADDRESS=`hostname`"),
		"noncanonical-ip":      []byte("PROJECT_ONE_BIND_ADDRESS=0:0:0:0:0:0:0:1"),
		"multicast":            []byte("PROJECT_ONE_BIND_ADDRESS=ff02::1"),
		"invalid-utf8":         append([]byte("# "), 0xff),
		"nul":                  []byte("#\x00 ignored"),
		"oversized":            bytes.Repeat([]byte{'#'}, maximumEnvironmentBytes+1),
	}
	for _, key := range []string{publishedPortEnvironment, targetPortEnvironment} {
		for name, value := range map[string]string{"false": "false", "null": "null", "fraction": "8443.5", "exponent": "1e3", "leading-zero": "08443", "plus": "+8443", "minus-zero": "-0", "zero": "0", "large": "65536", "huge": "999999999999999999999999", "quoted-zero": `"0"`, "quoted-false": `"false"`} {
			invalid[key+"-"+name] = []byte(key + "=" + value)
		}
	}
	for name, environment := range invalid {
		t.Run(name, func(t *testing.T) {
			if p, e := Load(raw, environment); e == nil || e.Error() != environmentInvalid || p != (Parameters{}) {
				t.Fatal("unsafe overlay accepted or error leaked", e)
			}
		})
	}
	if p, e := Load(raw, bytes.Repeat([]byte{'#'}, maximumEnvironmentBytes)); e != nil || p != base {
		t.Fatal("exact environment byte boundary refused", e)
	}
}

func TestComposePortValuesSafeLongSyntaxOverride(t *testing.T) {
	for name, p := range map[string]Parameters{
		"independent-ipv4":    {FormatVersion: 1, BindAddress: "192.0.2.23", PublishedPort: 18443, TargetPort: 9443},
		"unbracketed-ipv6":    {FormatVersion: 1, BindAddress: "2001:db8::23", PublishedPort: 19443, TargetPort: 10443},
		"wildcard-boundaries": {FormatVersion: 1, BindAddress: "::", PublishedPort: 65535, TargetPort: 1},
	} {
		t.Run(name, func(t *testing.T) {
			value, e := p.Values()
			if e != nil {
				t.Fatal(e)
			}
			want := fmt.Sprintf("services:\n  control:\n    ports: !override\n      - target: %d\n        published: %q\n        host_ip: %q\n        protocol: \"tcp\"\n", p.TargetPort, fmt.Sprint(p.PublishedPort), p.BindAddress)
			if string(value) != want || bytes.Contains(value, []byte("environment:")) || bytes.Count(value, []byte("- target:")) != 1 {
				t.Fatal("projection changed unrelated fields or port semantics")
			}
			again, e := p.Values()
			if e != nil || !bytes.Equal(value, again) {
				t.Fatal("projection is not deterministic", e)
			}
			value[0] = 'X'
			if bytes.Equal(value, again) {
				t.Fatal("separate projections share mutable storage")
			}
		})
	}
}
