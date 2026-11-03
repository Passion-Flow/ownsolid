package taskbus

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

const EnvironmentPrefix = "PROJECT_ONE_TASK_EVENTS_"
const DriverJetStream = "nats_jetstream"
const NativeMaximumDeliveries = -1

// Configuration is the sole task-event transport policy boundary. Callers inject
// decoded values explicitly; this package never consults ambient environment.
type Configuration struct {
	Enabled                  bool     `json:"enabled" yaml:"enabled"`
	Driver                   string   `json:"driver" yaml:"driver"`
	Endpoint                 string   `json:"endpoint" yaml:"endpoint"`
	DialAddresses            []string `json:"dial_addresses" yaml:"dial_addresses"`
	CAFile                   string   `json:"ca_file" yaml:"ca_file"`
	ClientCertificateFile    string   `json:"client_certificate_file" yaml:"client_certificate_file"`
	ClientKeyFile            string   `json:"client_key_file" yaml:"client_key_file"`
	PublisherSeedFile        string   `json:"publisher_seed_file" yaml:"publisher_seed_file"`
	ConsumerSeedFile         string   `json:"consumer_seed_file" yaml:"consumer_seed_file"`
	NamespaceID              string   `json:"namespace_id" yaml:"namespace_id"`
	Stream                   string   `json:"stream" yaml:"stream"`
	Consumer                 string   `json:"consumer" yaml:"consumer"`
	ConnectSeconds           int      `json:"connect_seconds" yaml:"connect_seconds"`
	RequestSeconds           int      `json:"request_seconds" yaml:"request_seconds"`
	PullSeconds              int      `json:"pull_seconds" yaml:"pull_seconds"`
	AckWaitSeconds           int      `json:"ack_wait_seconds" yaml:"ack_wait_seconds"`
	WorkerPollSeconds        int      `json:"worker_poll_seconds" yaml:"worker_poll_seconds"`
	RetrySeconds             int      `json:"retry_seconds" yaml:"retry_seconds"`
	ReconnectSeconds         int      `json:"reconnect_seconds" yaml:"reconnect_seconds"`
	MaximumReconnectAttempts int      `json:"maximum_reconnect_attempts" yaml:"maximum_reconnect_attempts"`
	MaximumPublishAttempts   int      `json:"maximum_publish_attempts" yaml:"maximum_publish_attempts"`
	MaximumDeliveries        int      `json:"maximum_deliveries" yaml:"maximum_deliveries"`
	MaximumAckPending        int      `json:"maximum_ack_pending" yaml:"maximum_ack_pending"`
	StreamMaximumMessages    int64    `json:"stream_maximum_messages" yaml:"stream_maximum_messages"`
	StreamMaximumBytes       int64    `json:"stream_maximum_bytes" yaml:"stream_maximum_bytes"`
	StreamRetentionSeconds   int      `json:"stream_retention_seconds" yaml:"stream_retention_seconds"`
	DuplicateWindowSeconds   int      `json:"duplicate_window_seconds" yaml:"duplicate_window_seconds"`
	RetryLifetimeSeconds     int      `json:"retry_lifetime_seconds" yaml:"retry_lifetime_seconds"`
	HistoryRetentionSeconds  int      `json:"history_retention_seconds" yaml:"history_retention_seconds"`
	CleanupBatch             int      `json:"cleanup_batch" yaml:"cleanup_batch"`
	ShutdownSeconds          int      `json:"shutdown_seconds" yaml:"shutdown_seconds"`
}

func (Configuration) String() string   { return "TASK_BUS_CONFIGURATION_REDACTED" }
func (Configuration) GoString() string { return "TASK_BUS_CONFIGURATION_REDACTED" }

var brokerName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var brokerHost = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

func ValidResourceName(value string) bool { return brokerName.MatchString(value) }

func configurationError() error                { return errors.New("CONFIG_TASK_EVENTS_INVALID") }
func bounded(value, minimum, maximum int) bool { return value >= minimum && value <= maximum }
func privateAddress(value string) bool {
	address, err := netip.ParseAddr(value)
	return err == nil && address.String() == value && !address.Is4In6() && (address.IsPrivate() || address.IsLoopback()) && !address.IsUnspecified() && !address.IsMulticast() && !address.IsLinkLocalUnicast()
}
func protectedPath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && value != "/" && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}
func (c Configuration) Validate() error {
	bad := configurationError()
	if !c.Enabled {
		if !reflect.DeepEqual(c, Configuration{}) {
			return bad
		}
		return nil
	}
	if c.Driver != DriverJetStream || !ValidID(c.NamespaceID) || !brokerName.MatchString(c.Stream) || !brokerName.MatchString(c.Consumer) || c.Stream == c.Consumer {
		return bad
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "tls" || u.User != nil || u.Hostname() == "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.Contains(u.Hostname(), "%") || u.String() != c.Endpoint {
		return bad
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || !bounded(port, 1, 65535) || strconv.Itoa(port) != u.Port() {
		return bad
	}
	host := u.Hostname()
	if address, e := netip.ParseAddr(host); e == nil {
		if !privateAddress(host) || address.String() != host {
			return bad
		}
	} else if len(host) > 253 || !brokerHost.MatchString(host) {
		return bad
	}
	if len(c.DialAddresses) < 1 || len(c.DialAddresses) > 8 {
		return bad
	}
	seen := map[string]bool{}
	for _, address := range c.DialAddresses {
		if !privateAddress(address) || seen[address] {
			return bad
		}
		seen[address] = true
	}
	if _, e := netip.ParseAddr(host); e == nil && !seen[host] {
		return bad
	}
	paths := map[string]bool{}
	for _, path := range []string{c.CAFile, c.ClientCertificateFile, c.ClientKeyFile, c.PublisherSeedFile, c.ConsumerSeedFile} {
		if !protectedPath(path) || paths[path] {
			return bad
		}
		paths[path] = true
	}
	if !bounded(c.ConnectSeconds, 1, 60) || !bounded(c.RequestSeconds, 1, 300) || c.ConnectSeconds > c.RequestSeconds || !bounded(c.PullSeconds, 1, c.RequestSeconds) || !bounded(c.AckWaitSeconds, 3*c.RequestSeconds+c.PullSeconds, 3600) || !bounded(c.WorkerPollSeconds, 1, 60) || !bounded(c.RetrySeconds, 1, 3600) || !bounded(c.ReconnectSeconds, 1, 60) {
		return bad
	}
	if !bounded(c.MaximumReconnectAttempts, 1, 1000) || !bounded(c.MaximumPublishAttempts, 1, 1000) || !bounded(c.MaximumDeliveries, 1, 1000) || !bounded(c.MaximumAckPending, 1, 64) || c.StreamMaximumMessages < int64(c.MaximumAckPending) || c.StreamMaximumMessages > 100000000 || c.StreamMaximumBytes < int64(c.MaximumAckPending)*MaximumTransportBytes || c.StreamMaximumBytes > 1<<40 {
		return bad
	}
	if !bounded(c.StreamRetentionSeconds, 60, 31536000) || !bounded(c.DuplicateWindowSeconds, 1, c.StreamRetentionSeconds) || !bounded(c.RetryLifetimeSeconds, c.RequestSeconds, c.StreamRetentionSeconds) || c.RetrySeconds > c.RetryLifetimeSeconds || !bounded(c.HistoryRetentionSeconds, c.StreamRetentionSeconds+c.RetryLifetimeSeconds+c.AckWaitSeconds, 3*31536000) || !bounded(c.CleanupBatch, 1, 1000) || !bounded(c.ShutdownSeconds, c.RequestSeconds+c.PullSeconds, 3600) {
		return bad
	}
	return nil
}

// DecodeConfiguration rejects duplicate/unknown/case-aliased/null fields and
// requires every enabled policy explicitly. Disabled JSON has only enabled=false.
func DecodeConfiguration(raw []byte) (Configuration, error) {
	var out Configuration
	if len(raw) == 0 || len(raw) > 16384 {
		return out, configurationError()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return out, configurationError()
	}
	known := configurationFields()
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || known[key] == nil || fields[key] != nil {
			return Configuration{}, configurationError()
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return Configuration{}, configurationError()
		}
		fields[key] = value
	}
	if _, err = decoder.Token(); err != nil {
		return Configuration{}, configurationError()
	}
	if _, err = decoder.Token(); err != io.EOF {
		return Configuration{}, configurationError()
	}
	if json.Unmarshal(raw, &out) != nil || fields["enabled"] == nil || out.Enabled && len(fields) != len(known) || !out.Enabled && len(fields) != 1 || out.Validate() != nil {
		return Configuration{}, configurationError()
	}
	return out, nil
}

func configurationFields() map[string]reflect.Type {
	typ := reflect.TypeFor[Configuration]()
	result := map[string]reflect.Type{}
	for n := 0; n < typ.NumField(); n++ {
		result[typ.Field(n).Tag.Get("json")] = typ.Field(n).Type
	}
	return result
}

// DecodeYAMLConfiguration is a strict standalone component input. Root can reuse
// ValidateYAMLNode for its selected subtree before ordinary typed decoding.
func DecodeYAMLConfiguration(raw []byte) (Configuration, error) {
	var out Configuration
	if len(raw) == 0 || len(raw) > 16384 {
		return out, configurationError()
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var document yaml.Node
	if decoder.Decode(&document) != nil || len(document.Content) != 1 || ValidateYAMLNode(document.Content[0]) != nil || decoder.Decode(new(yaml.Node)) != io.EOF || document.Content[0].Decode(&out) != nil || out.Validate() != nil {
		return Configuration{}, configurationError()
	}
	return out, nil
}
func ValidateYAMLNode(node *yaml.Node) error {
	if node == nil || node.Kind != yaml.MappingNode || len(node.Content)%2 != 0 {
		return configurationError()
	}
	known := configurationFields()
	seen := map[string]bool{}
	enabled := false
	for n := 0; n < len(node.Content); n += 2 {
		key, value := node.Content[n], node.Content[n+1]
		typ := known[key.Value]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || typ == nil || seen[key.Value] || value.Alias != nil {
			return configurationError()
		}
		seen[key.Value] = true
		switch typ.Kind() {
		case reflect.Bool:
			if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" || value.Value != "true" && value.Value != "false" {
				return configurationError()
			}
			enabled = value.Value == "true"
		case reflect.String:
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return configurationError()
			}
		case reflect.Int, reflect.Int64:
			if value.Kind != yaml.ScalarNode || value.Tag != "!!int" || !decimal(value.Value) {
				return configurationError()
			}
		case reflect.Slice:
			if value.Kind != yaml.SequenceNode || value.Tag != "!!seq" {
				return configurationError()
			}
			for _, element := range value.Content {
				if element.Kind != yaml.ScalarNode || element.Tag != "!!str" || element.Alias != nil {
					return configurationError()
				}
			}
		default:
			return configurationError()
		}
	}
	if !seen["enabled"] || enabled && len(seen) != len(known) || !enabled && len(seen) != 1 {
		return configurationError()
	}
	return nil
}
func decimal(value string) bool {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ApplyEnvironment overlays only an explicitly supplied, already unquoted map.
// No os.Getenv, shell evaluation, defaults or second configuration authority exist.
func ApplyEnvironment(c Configuration, values map[string]string) (Configuration, error) {
	result := reflect.ValueOf(&c).Elem()
	typ := result.Type()
	fields := map[string]int{}
	for n := 0; n < typ.NumField(); n++ {
		fields[EnvironmentPrefix+strings.ToUpper(typ.Field(n).Tag.Get("json"))] = n
	}
	for key, value := range values {
		index, ok := fields[key]
		if !ok || value == "" || strings.TrimSpace(value) != value {
			return Configuration{}, configurationError()
		}
		field := result.Field(index)
		switch field.Kind() {
		case reflect.Bool:
			if value != "true" && value != "false" {
				return Configuration{}, configurationError()
			}
			field.SetBool(value == "true")
		case reflect.String:
			field.SetString(value)
		case reflect.Int, reflect.Int64:
			number, err := strconv.ParseInt(value, 10, 64)
			if !decimal(value) || err != nil || field.OverflowInt(number) {
				return Configuration{}, configurationError()
			}
			field.SetInt(number)
		case reflect.Slice:
			var addresses []string
			if json.Unmarshal([]byte(value), &addresses) != nil || addresses == nil {
				return Configuration{}, configurationError()
			}
			canonical, _ := json.Marshal(addresses)
			if string(canonical) != value {
				return Configuration{}, configurationError()
			}
			field.Set(reflect.ValueOf(addresses))
		default:
			return Configuration{}, configurationError()
		}
	}
	if err := c.Validate(); err != nil {
		return Configuration{}, err
	}
	return c, nil
}
