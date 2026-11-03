package taskbus

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func syntheticConfiguration() Configuration {
	return Configuration{Enabled: true, Driver: DriverJetStream, Endpoint: "tls://broker.example.invalid:4422", DialAddresses: []string{"127.0.0.1"},
		CAFile: "/synthetic/private/ca.pem", ClientCertificateFile: "/synthetic/private/client.pem", ClientKeyFile: "/synthetic/private/client.key", PublisherSeedFile: "/synthetic/private/publisher.nk", ConsumerSeedFile: "/synthetic/private/consumer.nk",
		NamespaceID: "11111111-1111-4111-8111-111111111111", Stream: "SyntheticTaskEvents", Consumer: "SyntheticTaskReceiver", ConnectSeconds: 2, RequestSeconds: 7, PullSeconds: 3, AckWaitSeconds: 25, WorkerPollSeconds: 2, RetrySeconds: 4, ReconnectSeconds: 3, MaximumReconnectAttempts: 4, MaximumPublishAttempts: 7, MaximumDeliveries: 5, MaximumAckPending: 3, StreamMaximumMessages: 100, StreamMaximumBytes: 1 << 20, StreamRetentionSeconds: 600, DuplicateWindowSeconds: 60, RetryLifetimeSeconds: 300, HistoryRetentionSeconds: 1000, CleanupBatch: 9, ShutdownSeconds: 20}
}

func TestTaskEventConfigurationNondefaultJSONYAMLAndExplicitEnvironment(t *testing.T) {
	c := syntheticConfiguration()
	minimumAck := c
	minimumAck.AckWaitSeconds = 3*c.RequestSeconds + c.PullSeconds
	if minimumAck.Validate() != nil {
		t.Fatal("declared ACK stage budget refused")
	}
	minimumAck.AckWaitSeconds--
	if minimumAck.Validate() == nil {
		t.Fatal("insufficient ACK stage budget admitted")
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	capacity := c
	capacity.StreamMaximumBytes = int64(c.MaximumAckPending) * MaximumTransportBytes
	if capacity.Validate() != nil {
		t.Fatal("exact bounded transport capacity refused")
	}
	capacity.StreamMaximumBytes--
	if capacity.Validate() == nil {
		t.Fatal("capacity below maximum in-flight transport bytes admitted")
	}

	raw, _ := json.Marshal(c)
	decoded, err := DecodeConfiguration(raw)
	if err != nil || !reflect.DeepEqual(decoded, c) {
		t.Fatal("nondefault JSON failed", err)
	}
	yamlRaw, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = DecodeYAMLConfiguration(yamlRaw)
	if err != nil || !reflect.DeepEqual(decoded, c) {
		t.Fatal("nondefault YAML failed", err)
	}
	values := map[string]string{}
	typ := reflect.TypeOf(c)
	value := reflect.ValueOf(c)
	for n := 0; n < typ.NumField(); n++ {
		field := value.Field(n)
		var text string
		switch field.Kind() {
		case reflect.Bool:
			text = strconv.FormatBool(field.Bool())
		case reflect.String:
			text = field.String()
		case reflect.Int, reflect.Int64:
			text = strconv.FormatInt(field.Int(), 10)
		case reflect.Slice:
			raw, _ := json.Marshal(field.Interface())
			text = string(raw)
		default:
			t.Fatal("unsupported typed environment field")
		}
		values[EnvironmentPrefix+strings.ToUpper(typ.Field(n).Tag.Get("json"))] = text
	}
	decoded, err = ApplyEnvironment(Configuration{}, values)
	if err != nil || !reflect.DeepEqual(decoded, c) {
		t.Fatal("explicit complete environment map failed", err)
	}
	for _, endpoint := range []struct{ url, address string }{{"tls://localhost:4422", "127.0.0.1"}, {"tls://127.0.0.1:4422", "127.0.0.1"}, {"tls://[::1]:4422", "::1"}, {"tls://[fd00::25]:4422", "fd00::25"}} {
		selected := c
		selected.Endpoint = endpoint.url
		selected.DialAddresses = []string{endpoint.address}
		if selected.Validate() != nil {
			t.Fatal("explicit private endpoint refused", endpoint.url)
		}
	}
	if fmt.Sprintf("%+v %#v", c, c) != "TASK_BUS_CONFIGURATION_REDACTED TASK_BUS_CONFIGURATION_REDACTED" {
		t.Fatal("configuration formatting exposed references")
	}
}

func TestTaskEventConfigurationFalseUnknownNullDuplicateAndPolicyBoundaries(t *testing.T) {
	for _, raw := range []string{`{"enabled":false}`, "enabled: false\n"} {
		var c Configuration
		var err error
		if strings.HasPrefix(raw, "{") {
			c, err = DecodeConfiguration([]byte(raw))
		} else {
			c, err = DecodeYAMLConfiguration([]byte(raw))
		}
		if err != nil || !reflect.DeepEqual(c, Configuration{}) {
			t.Fatal("false did not remain disabled", err)
		}
	}
	if c, err := ApplyEnvironment(Configuration{}, map[string]string{EnvironmentPrefix + "ENABLED": "false"}); err != nil || c.Enabled {
		t.Fatal("false environment enabled transport", err)
	}
	c := syntheticConfiguration()
	raw, _ := json.Marshal(c)
	cases := [][]byte{
		[]byte(`{"enabled":false,"driver":""}`), []byte(`{}`), []byte(`{"enabled":"false"}`),
		[]byte(`{"enabled":false,"enabled":false}`), []byte(`{"Enabled":false}`), []byte(`{"enabled":null}`),
		append(append([]byte{}, raw...), []byte("{}")...),
		[]byte(strings.Replace(string(raw), `"enabled":true`, `"enabled":true,"unknown":1`, 1)),
		[]byte(strings.Replace(string(raw), `"dial_addresses":["127.0.0.1"]`, `"dial_addresses":null`, 1)),
	}
	for _, input := range cases {
		if out, err := DecodeConfiguration(input); err == nil || !reflect.DeepEqual(out, Configuration{}) {
			t.Fatal("invalid JSON produced configuration")
		}
	}
	for _, input := range []string{"enabled: False\n", "enabled: yes\n", "enabled: 'false'\n", "enabled: false\nunknown: 1\n", "enabled: false\nenabled: false\n", "enabled: false\n---\nenabled: false\n", "enabled: &enabled false\ndriver: *enabled\n"} {
		if out, err := DecodeYAMLConfiguration([]byte(input)); err == nil || !reflect.DeepEqual(out, Configuration{}) {
			t.Fatal("invalid YAML produced configuration")
		}
	}
	for _, values := range []map[string]string{
		{EnvironmentPrefix + "ENABLED": "False"}, {EnvironmentPrefix + "ENABLED": ""}, {EnvironmentPrefix + "UNKNOWN": "1"}, {"NATS_URL": "tls://broker.example.invalid:4422"},
		{EnvironmentPrefix + "REQUEST_SECONDS": "07"}, {EnvironmentPrefix + "REQUEST_SECONDS": "+7"}, {EnvironmentPrefix + "REQUEST_SECONDS": "9223372036854775808"},
		{EnvironmentPrefix + "DIAL_ADDRESSES": "null"}, {EnvironmentPrefix + "DIAL_ADDRESSES": "[\"127.0.0.1\", \"::1\"]"},
	} {
		if out, err := ApplyEnvironment(c, values); err == nil || !reflect.DeepEqual(out, Configuration{}) {
			t.Fatal("invalid environment produced configuration")
		}
	}
	for name, mutate := range map[string]func(*Configuration){
		"disabled_contradiction": func(v *Configuration) { v.Enabled = false },
		"plaintext":              func(v *Configuration) { v.Endpoint = "nats://broker.example.invalid:4422" },
		"embedded_secret":        func(v *Configuration) { v.Endpoint = "tls://user:forbidden@broker.example.invalid:4422" },
		"no_port":                func(v *Configuration) { v.Endpoint = "tls://broker.example.invalid" },
		"path":                   func(v *Configuration) { v.Endpoint += "/" }, "public_dial": func(v *Configuration) { v.DialAddresses = []string{"8.8.8.8"} },
		"ip_substitution":   func(v *Configuration) { v.Endpoint = "tls://127.0.0.2:4422" },
		"duplicate_ip":      func(v *Configuration) { v.DialAddresses = []string{"127.0.0.1", "127.0.0.1"} },
		"foreign_namespace": func(v *Configuration) { v.NamespaceID = "*" }, "wildcard_stream": func(v *Configuration) { v.Stream = "Other.>" },
		"relative_secret": func(v *Configuration) { v.PublisherSeedFile = "publisher.nk" }, "shared_identity": func(v *Configuration) { v.ConsumerSeedFile = v.PublisherSeedFile },
		"zero_reconnect_budget": func(v *Configuration) { v.MaximumReconnectAttempts = 0 }, "excess_reconnect_budget": func(v *Configuration) { v.MaximumReconnectAttempts = 1001 },
		"unsafe_ack": func(v *Configuration) { v.AckWaitSeconds = v.RequestSeconds }, "unbounded_pending": func(v *Configuration) { v.MaximumAckPending = -1 },
		"empty_capacity": func(v *Configuration) { v.StreamMaximumBytes = 0 }, "lost_duplicate_window": func(v *Configuration) { v.DuplicateWindowSeconds = v.StreamRetentionSeconds + 1 },
		"short_receipt_retention": func(v *Configuration) { v.HistoryRetentionSeconds = v.StreamRetentionSeconds }, "empty_cleanup": func(v *Configuration) { v.CleanupBatch = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := c
			mutate(&bad)
			if bad.Validate() == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}
