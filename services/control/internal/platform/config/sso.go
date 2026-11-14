package config

import (
	"errors"
	"net/netip"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

const (
	SSOMaximumConnectors      = 64
	SSOMaximumPendingFlows    = 10000
	SSOMaximumBindingsPerUser = 256
)

// SSOConfiguration selects operator-approved destinations; browser input never selects transport policy.
type SSOConfiguration struct {
	Enabled     bool          `yaml:"enabled"`
	KeyringFile string        `yaml:"keyring_file"`
	Providers   []SSOProvider `yaml:"providers"`
}

type SSOProvider struct {
	Origin        string   `yaml:"origin"`
	Issuer        string   `yaml:"issuer"`
	DialAddresses []string `yaml:"dial_addresses"`
	CAFile        string   `yaml:"ca_file"`
}

func ssoPrivatePath(value string) bool {
	return value != string(filepath.Separator) && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

// SSOOrigin accepts one canonical HTTPS origin, including a nondefault explicit port.
func SSOOrigin(value string) bool {
	u, e := url.Parse(value)
	if e != nil || len(value) > 2048 || u.Scheme != "https" || u.Host == "" || u.Host != strings.ToLower(u.Host) || u.User != nil || u.Path != "" || u.RawPath != "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(value, "\\%") || strings.ContainsFunc(value, unicode.IsControl) {
		return false
	}
	host := u.Hostname()
	if host == "" || len(host) > 253 {
		return false
	}
	if ip, e := netip.ParseAddr(host); e == nil {
		if ip.Zone() != "" || ip.String() != host || !(ip.IsGlobalUnicast() || ip.IsLoopback()) {
			return false
		}
	} else {
		labels := strings.Split(host, ".")
		last := labels[len(labels)-1]
		if _, e := strconv.ParseUint(last, 0, 64); e == nil {
			return false
		}
		if _, e := strconv.ParseUint(last, 10, 64); e == nil {
			return false
		}
		for _, label := range labels {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return false
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return false
				}
			}
		}
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 || n == 443 || strconv.Itoa(n) != port {
			return false
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return false
	}
	return u.String() == value
}

func (c SSOConfiguration) Validate() error {
	bad := errors.New("CONFIG_SSO_INVALID")
	if c.KeyringFile != "" && !ssoPrivatePath(c.KeyringFile) || len(c.Providers) > SSOMaximumConnectors || c.Enabled && (c.KeyringFile == "" || len(c.Providers) == 0) {
		return bad
	}
	origins := map[string]bool{}
	for _, p := range c.Providers {
		issuer, e := url.Parse(p.Issuer)
		if !SSOOrigin(p.Origin) || origins[p.Origin] || e != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.Opaque != "" || issuer.RawQuery != "" || issuer.ForceQuery || issuer.Fragment != "" || len(p.Issuer) > 2048 || strings.ContainsFunc(p.Issuer, unicode.IsControl) || len(p.DialAddresses) < 1 || len(p.DialAddresses) > 16 || p.CAFile != "" && !ssoPrivatePath(p.CAFile) {
			return bad
		}
		origins[p.Origin] = true
		addresses := map[string]bool{}
		for _, value := range p.DialAddresses {
			ip, e := netip.ParseAddr(value)
			if e != nil || ip.Zone() != "" || ip.String() != value || ip.Is4In6() || !(ip.IsGlobalUnicast() || ip.IsLoopback()) || addresses[value] {
				return bad
			}
			addresses[value] = true
		}
	}
	// Existence/decryption and CA loading belong to enabled service startup, not recovery-mode parsing.
	return nil
}

type SSOPolicy struct {
	FlowSeconds            int `yaml:"flow_seconds"`
	OperationSeconds       int `yaml:"operation_seconds"`
	ConnectSeconds         int `yaml:"connect_seconds"`
	ResponseBytes          int `yaml:"response_bytes"`
	MaxConcurrentExchanges int `yaml:"max_concurrent_exchanges"`
	MaximumConnectors      int `yaml:"maximum_connectors"`
	MaximumBindingsPerUser int `yaml:"maximum_bindings_per_user"`
	MaximumPendingFlows    int `yaml:"maximum_pending_flows"`
	StartIPCount           int `yaml:"start_ip_count"`
	StartWindowSeconds     int `yaml:"start_window_seconds"`
	CallbackIPCount        int `yaml:"callback_ip_count"`
	CallbackWindowSeconds  int `yaml:"callback_window_seconds"`
	PasswordIPCount        int `yaml:"password_ip_count"`
	PasswordWindowSeconds  int `yaml:"password_window_seconds"`
	CleanupSeconds         int `yaml:"cleanup_seconds"`
	CleanupTimeoutSeconds  int `yaml:"cleanup_timeout_seconds"`
	CleanupBatchSize       int `yaml:"cleanup_batch_size"`
	FlowRetentionDays      int `yaml:"flow_retention_days"`
}

func DefaultSSOPolicy() SSOPolicy {
	return SSOPolicy{FlowSeconds: 300, OperationSeconds: 10, ConnectSeconds: 3, ResponseBytes: 65536, MaxConcurrentExchanges: 4, MaximumConnectors: 16, MaximumBindingsPerUser: 64, MaximumPendingFlows: 1000, StartIPCount: 20, StartWindowSeconds: 60, CallbackIPCount: 120, CallbackWindowSeconds: 60, PasswordIPCount: 10, PasswordWindowSeconds: 60, CleanupSeconds: 60, CleanupTimeoutSeconds: 5, CleanupBatchSize: 100, FlowRetentionDays: 7}
}

func (p SSOPolicy) Validate() error {
	checks := [][3]int{{p.FlowSeconds, 60, 900}, {p.OperationSeconds, 2, 30}, {p.ConnectSeconds, 1, 10}, {p.ResponseBytes, 4096, 262144}, {p.MaxConcurrentExchanges, 1, 16}, {p.MaximumConnectors, 1, SSOMaximumConnectors}, {p.MaximumPendingFlows, 1, SSOMaximumPendingFlows}, {p.StartIPCount, 1, 120}, {p.StartWindowSeconds, 10, 3600}, {p.CallbackIPCount, 1, 600}, {p.CallbackWindowSeconds, 10, 3600}, {p.PasswordIPCount, 1, 60}, {p.PasswordWindowSeconds, 10, 3600}, {p.CleanupSeconds, 10, 3600}, {p.CleanupTimeoutSeconds, 1, 30}, {p.CleanupBatchSize, 1, 1000}, {p.FlowRetentionDays, 1, 90}}
	for _, c := range checks {
		if c[0] < c[1] || c[0] > c[2] {
			return errors.New("CONFIG_SSO_POLICY_INVALID")
		}
	}
	if p.MaximumBindingsPerUser < 1 || p.MaximumBindingsPerUser > SSOMaximumBindingsPerUser {
		return errors.New("CONFIG_SSO_POLICY_INVALID")
	}
	if p.ConnectSeconds > p.OperationSeconds {
		return errors.New("CONFIG_SSO_POLICY_INVALID")
	}
	return nil
}

// Reject null, aliases, coercion, duplicate and unknown fields before default merging.
func strictSSOYAML(node *yaml.Node, typ reflect.Type) error {
	bad := errors.New("CONFIG_SSO_INVALID")
	if node == nil || node.Kind == yaml.AliasNode {
		return bad
	}
	switch typ.Kind() {
	case reflect.Struct:
		if node.Kind != yaml.MappingNode {
			return bad
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			fields[f.Tag.Get("yaml")] = f.Type
		}
		seen := map[string]bool{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			field, ok := fields[key.Value]
			if key.Kind != yaml.ScalarNode || key.ShortTag() != "!!str" || !ok || seen[key.Value] {
				return bad
			}
			seen[key.Value] = true
			if e := strictSSOYAML(value, field); e != nil {
				return e
			}
		}
	case reflect.Slice:
		if node.Kind != yaml.SequenceNode {
			return bad
		}
		for _, value := range node.Content {
			if e := strictSSOYAML(value, typ.Elem()); e != nil {
				return e
			}
		}
	default:
		tag := "!!int"
		if typ.Kind() == reflect.String {
			tag = "!!str"
		}
		if typ.Kind() == reflect.Bool {
			tag = "!!bool"
		}
		if node.Kind != yaml.ScalarNode || node.ShortTag() != tag {
			return bad
		}
	}
	return nil
}

func (p *SSOPolicy) UnmarshalYAML(node *yaml.Node) error {
	if e := strictSSOYAML(node, reflect.TypeOf(*p)); e != nil {
		return e
	}
	type plain SSOPolicy
	v := plain(*p)
	if e := node.Decode(&v); e != nil {
		return errors.New("CONFIG_SSO_POLICY_INVALID")
	}
	*p = SSOPolicy(v)
	return nil
}

func (p *SSOConfiguration) UnmarshalYAML(node *yaml.Node) error {
	if e := strictSSOYAML(node, reflect.TypeOf(*p)); e != nil {
		return e
	}
	type plain SSOConfiguration
	var v plain
	if e := node.Decode(&v); e != nil {
		return errors.New("CONFIG_SSO_INVALID")
	}
	*p = SSOConfiguration(v)
	return nil
}

// The YAML library bypasses UnmarshalYAML for null; inspect the owned groups before decoding.
func validateSSOYAML(raw []byte) error {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil {
		return errors.New("CONFIG_INVALID")
	}
	resolve := func(n *yaml.Node) *yaml.Node {
		seen := map[*yaml.Node]bool{}
		for n != nil && n.Kind == yaml.AliasNode {
			if seen[n] {
				return nil
			}
			seen[n] = true
			n = n.Alias
		}
		return n
	}
	if len(document.Content) != 1 {
		return errors.New("CONFIG_INVALID")
	}
	root := resolve(document.Content[0])
	if root == nil || root.Kind != yaml.MappingNode {
		return errors.New("CONFIG_INVALID")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		name, value := root.Content[i].Value, root.Content[i+1]
		if name == "sso" {
			if e := strictSSOYAML(value, reflect.TypeOf(SSOConfiguration{})); e != nil {
				return e
			}
		}
		if name != "runtime" {
			continue
		}
		body := resolve(value)
		if body == nil {
			return errors.New("CONFIG_SSO_INVALID")
		}
		if body.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(body.Content); j += 2 {
			if body.Content[j].Value == "sso" {
				if e := strictSSOYAML(body.Content[j+1], reflect.TypeOf(SSOPolicy{})); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
