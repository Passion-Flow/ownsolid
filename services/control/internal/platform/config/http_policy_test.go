package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

func TestHTTPPolicySchemaExamplesAndEnvironment(t *testing.T) {
	var definition any
	if json.Unmarshal(machineTestSource(t, "api/configuration/v1/control-http-policy/schema.json"), &definition) != nil {
		t.Fatal("HTTP effective schema unavailable")
	}
	compiler := jsonschema.NewCompiler()
	const address = "https://projectone.invalid/configuration/control-http-policy/v1"
	if e := compiler.AddResource(address, definition); e != nil {
		t.Fatal(e)
	}
	schema, e := compiler.Compile(address)
	if e != nil {
		t.Fatal(e)
	}
	var defaults map[string]any
	raw, _ := yaml.Marshal(DefaultHTTPPolicy())
	if yaml.Unmarshal(raw, &defaults) != nil {
		t.Fatal("HTTP defaults unavailable")
	}
	// Convert YAML integer values to ordinary JSON numbers for the JSON validator.
	toJSON := func(v any) any {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		var out any
		if json.Unmarshal(b, &out) != nil {
			t.Fatal("JSON conversion failed")
		}
		return out
	}
	if schema.Validate(toJSON(defaults)) != nil {
		t.Fatal("HTTP defaults violate effective schema")
	}
	var example struct {
		Runtime struct {
			HTTP HTTPPolicy `yaml:"http"`
		} `yaml:"runtime"`
	}
	if yaml.Unmarshal(machineTestSource(t, "services/control/config/examples/control/control.example.yaml"), &example) != nil || example.Runtime.HTTP != DefaultHTTPPolicy() {
		t.Fatal("full control example drifted from HTTP defaults")
	}
	properties := definition.(map[string]any)["properties"].(map[string]any)
	if len(properties) != reflect.TypeFor[HTTPPolicy]().NumField() {
		t.Fatal("effective schema field inventory drift")
	}
	envRaw := string(machineTestSource(t, "services/control/config/examples/control/http-policy.env.example"))
	seen := map[string]bool{}
	for _, line := range strings.Split(envRaw, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok || seen[key] {
			t.Fatal("duplicate or invalid documented HTTP key")
		}
		seen[key] = true
	}
	if len(seen) != len(properties) {
		t.Fatal("HTTP documented environment inventory drift")
	}
	for field, v := range properties {
		property := v.(map[string]any)
		if int(property["default"].(float64)) != defaults[field].(int) {
			t.Fatal("schema default drift")
		}
		key := "PROJECT_ONE_HTTP_" + strings.ToUpper(field)
		if !seen[key] {
			t.Fatal("missing adjacent documented HTTP key")
		}
		p := DefaultRuntimePolicy()
		if p.httpEnvironmentField(key) == nil {
			t.Fatal("schema key has no typed environment mapping")
		}
		for _, n := range []int{1, int(property["maximum"].(float64))} {
			p = DefaultRuntimePolicy()
			p.HTTP.ReadHeaderSeconds, p.HTTP.MaxActiveRequests = 1, 1
			if field == "read_header_seconds" {
				p.HTTP.RequestSeconds = 300
			}
			if field == "max_active_requests" {
				p.HTTP.MaxConnections = 4096
			}
			*p.httpEnvironmentField(key) = n
			if e := p.Validate(); e != nil {
				t.Fatal("HTTP boundary rejected", field, e)
			}
			base := validConfig()
			base.Runtime = &p
			// A valid independent base plus an explicit override must reach exactly the typed field.
			raw, _ = yaml.Marshal(base)
			actual, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, fmt.Sprintf("%s='%d'\n", key, n)))
			if e != nil || actual.Policy().HTTP != p.HTTP {
				t.Fatal("HTTP boundary overlay changed meaning", field, e)
			}
		}
		for _, value := range []string{"", "false", "0", "-1", "1.5", "999999999999999999999999", "$(sensitive-value)", fmt.Sprint(int(property["maximum"].(float64)) + 1)} {
			if _, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, key+"="+value+"\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" || strings.Contains(e.Error(), "sensitive-value") {
				t.Fatal("invalid HTTP overlay accepted or leaked", field, e)
			}
		}
		if _, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, key+"=2\n"+key+"=3\n")); e == nil || e.Error() != "CONFIG_POLICY_ENVIRONMENT_INVALID" {
			t.Fatal("duplicate HTTP overlay accepted", field)
		}
		bad := map[string]any{}
		for k, value := range defaults {
			bad[k] = value
		}
		delete(bad, field)
		if schema.Validate(toJSON(bad)) == nil {
			t.Fatal("effective schema accepted missing field")
		}
		bad[field] = false
		if schema.Validate(toJSON(bad)) == nil {
			t.Fatal("effective schema accepted false integer")
		}
	}
	actual, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, envRaw))
	if e != nil || actual.Policy().HTTP != DefaultHTTPPolicy() {
		t.Fatal("documented HTTP example failed real loader", e)
	}
	if _, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, "PROJECT_ONE_HTTP_UNKNOWN=3\n")); e == nil {
		t.Fatal("unknown HTTP key accepted")
	}
	for _, overlay := range []string{"PROJECT_ONE_HTTP_MAX_CONNECTIONS=1\n", "PROJECT_ONE_HTTP_READ_HEADER_SECONDS=60\n"} {
		if _, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, overlay)); e == nil {
			t.Fatal("contradictory HTTP overlay accepted")
		}
	}
}

func TestHTTPPolicyPrivateNondefaultAndInvalidBase(t *testing.T) {
	override := "PROJECT_ONE_HTTP_INSTALLATION_LEASE_INTERVAL_SECONDS=7\nPROJECT_ONE_HTTP_STARTUP_SECONDS=21\nPROJECT_ONE_HTTP_READ_HEADER_SECONDS=3\nPROJECT_ONE_HTTP_REQUEST_SECONDS=31\nPROJECT_ONE_HTTP_IDLE_SECONDS=61\nPROJECT_ONE_HTTP_SHUTDOWN_SECONDS=6\nPROJECT_ONE_HTTP_MAX_CONNECTIONS=96\nPROJECT_ONE_HTTP_MAX_ACTIVE_REQUESTS=32\nPROJECT_ONE_HTTP_RATE_LIMIT_FALLBACK_RETRY_SECONDS=17\nPROJECT_ONE_HTTP_SERVICE_UNAVAILABLE_RETRY_SECONDS=7\n"
	for _, line := range strings.Split(strings.TrimSpace(override), "\n") {
		key, _, _ := strings.Cut(line, "=")
		t.Setenv(key, "false")
	}
	if c, e := decodeWithEnvironment(workspaceConfigInput(t), ""); e != nil || c.Policy().HTTP != DefaultHTTPPolicy() {
		t.Fatal("ambient HTTP input became authority", e)
	}
	c, e := decodeWithEnvironment(workspaceConfigInput(t), workspaceEnvironmentFixture(t, override))
	want := HTTPPolicy{InstallationLeaseIntervalSeconds: 7, StartupSeconds: 21, ReadHeaderSeconds: 3, RequestSeconds: 31, IdleSeconds: 61, ShutdownSeconds: 6, MaxConnections: 96, MaxActiveRequests: 32, RateLimitFallbackRetrySeconds: 17, ServiceUnavailableRetrySeconds: 7}
	if e != nil || c.Policy().HTTP != want {
		t.Fatal("protected nondefault HTTP values did not reach typed policy", e)
	}
	base := validConfig()
	p := DefaultRuntimePolicy()
	p.HTTP.ServiceUnavailableRetrySeconds = 0
	base.Runtime = &p
	raw, _ := yaml.Marshal(base)
	if _, e := decodeWithEnvironment(raw, workspaceEnvironmentFixture(t, override)); e == nil || e.Error() != "CONFIG_HTTP_BOUNDS_INVALID" {
		t.Fatal("overlay masked invalid independent YAML", e)
	}
}

func TestInstallationLeaseCadenceYAMLShapeCannotBeRepairedByOverlay(t *testing.T) {
	base := validConfig()
	policy := DefaultRuntimePolicy()
	base.Runtime = &policy
	raw, e := yaml.Marshal(base)
	if e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"null", "false", "'5'", "0", "-1", "61", "1.5", "[5]", "&cadence 5"} {
		bad := strings.Replace(string(raw), "installation_lease_interval_seconds: 5", "installation_lease_interval_seconds: "+value, 1)
		if bad == string(raw) {
			t.Fatal("cadence YAML fixture did not change")
		}
		if _, e := decodeWithEnvironment([]byte(bad), workspaceEnvironmentFixture(t, httpInstallationLeaseIntervalEnvironment+"=7\n")); e == nil {
			t.Fatal("invalid independent cadence YAML accepted or repaired", value)
		}
	}
}
