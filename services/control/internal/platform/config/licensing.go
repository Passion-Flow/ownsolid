package config

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

const MaximumLicenseEnvironmentBytes = 16 << 10

type LicensingConfiguration struct {
	TrustFile       string `yaml:"trust_file" json:"trust_file"`
	EnvironmentFile string `yaml:"environment_file" json:"environment_file,omitempty"`
}
type LicensePolicy wire.Policy

func (p LicensePolicy) Validate() error { return wire.Policy(p).Validate() }
func (c Config) LicensePolicy() wire.Policy {
	if c.Policy().Licensing == nil {
		return wire.DefaultPolicy()
	}
	return wire.Policy(*c.Policy().Licensing)
}
func licensePath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && filepath.Base(value) != string(filepath.Separator) && !strings.ContainsAny(value, "\x00\r\n")
}
func (c LicensingConfiguration) Validate() error {
	if c.TrustFile != "" && !licensePath(c.TrustFile) || c.EnvironmentFile != "" && !licensePath(c.EnvironmentFile) {
		return errors.New("CONFIG_LICENSING_INVALID")
	}
	return nil
}
func (c *LicensingConfiguration) UnmarshalYAML(node *yaml.Node) error {
	if strictKnowledgeYAML(node, reflect.TypeOf(*c)) != nil {
		return errors.New("CONFIG_LICENSING_INVALID")
	}
	type plain LicensingConfiguration
	var out plain
	if node.Decode(&out) != nil {
		return errors.New("CONFIG_LICENSING_INVALID")
	}
	*c = LicensingConfiguration(out)
	return nil
}
func (p *LicensePolicy) UnmarshalYAML(node *yaml.Node) error {
	if strictKnowledgeYAML(node, reflect.TypeOf(*p)) != nil {
		return errors.New("CONFIG_LICENSING_POLICY_INVALID")
	}
	type plain LicensePolicy
	out := plain(wire.DefaultPolicy())
	if node.Decode(&out) != nil {
		return errors.New("CONFIG_LICENSING_POLICY_INVALID")
	}
	*p = LicensePolicy(out)
	return nil
}
func validateLicensingYAML(raw []byte) error {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("CONFIG_LICENSING_INVALID")
	}
	root := document.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		name, value := root.Content[i].Value, root.Content[i+1]
		if name == "licensing" && strictKnowledgeYAML(value, reflect.TypeOf(LicensingConfiguration{})) != nil {
			return errors.New("CONFIG_LICENSING_INVALID")
		}
		if name == "runtime" && value.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(value.Content); j += 2 {
				if value.Content[j].Value == "licensing" && strictKnowledgeYAML(value.Content[j+1], reflect.TypeOf(LicensePolicy{})) != nil {
					return errors.New("CONFIG_LICENSING_POLICY_INVALID")
				}
			}
		}
	}
	return nil
}

func (c *Config) resolveLicenseEnvironment() error {
	if c.Licensing == nil || c.Licensing.EnvironmentFile == "" {
		return nil
	}
	if c.Licensing.Validate() != nil {
		return errors.New("CONFIG_LICENSING_INVALID")
	}
	raw, e := readLicenseFile(c.Licensing.EnvironmentFile, MaximumLicenseEnvironmentBytes)
	if e != nil {
		return errors.New("CONFIG_LICENSING_ENVIRONMENT_REQUIRED")
	}
	if !utf8.Valid(raw) || strings.ContainsAny(string(raw), "\x00\r") {
		return errors.New("CONFIG_LICENSING_ENVIRONMENT_INVALID")
	}
	seen := map[string]bool{}
	policy := c.LicensePolicy()
	overridden := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || seen[name] || value == "" {
			return errors.New("CONFIG_LICENSING_ENVIRONMENT_INVALID")
		}
		seen[name] = true
		if strings.HasPrefix(value, `"`) {
			var decoded string
			if json.Unmarshal([]byte(value), &decoded) != nil {
				return errors.New("CONFIG_LICENSING_ENVIRONMENT_INVALID")
			}
			value = decoded
		} else if strings.HasPrefix(value, "'") {
			if len(value) < 2 || !strings.HasSuffix(value, "'") {
				return errors.New("CONFIG_LICENSING_ENVIRONMENT_INVALID")
			}
			value = value[1 : len(value)-1]
		}
		if value == "" || strings.ContainsAny(value, "\x00\r\n$`") {
			return errors.New("CONFIG_LICENSING_ENVIRONMENT_INVALID")
		}
		if name == "PROJECT_ONE_LICENSE_TRUST_FILE" {
			if !licensePath(value) {
				return errors.New("CONFIG_LICENSING_ENVIRONMENT_INVALID")
			}
			c.Licensing.TrustFile = value
			continue
		}
		n, parseError := strconv.ParseInt(value, 10, 32)
		if parseError != nil || strconv.FormatInt(n, 10) != value {
			return errors.New("CONFIG_LICENSING_ENVIRONMENT_INVALID")
		}
		switch name {
		case "PROJECT_ONE_LICENSE_MAXIMUM_IMPORTS":
			policy.MaximumImports = int(n)
		case "PROJECT_ONE_LICENSE_ROLLBACK_TOLERANCE_SECONDS":
			policy.RollbackToleranceSeconds = int(n)
		default:
			return errors.New("CONFIG_LICENSING_ENVIRONMENT_INVALID")
		}
		overridden = true
	}
	if policy.Validate() != nil {
		return errors.New("CONFIG_LICENSING_POLICY_INVALID")
	}
	if overridden {
		runtime := c.Policy()
		value := LicensePolicy(policy)
		runtime.Licensing = &value
		c.Runtime = &runtime
	}
	return nil
}

// LicenseTrust is mandatory at serving construction. Operators can still parse
// a legacy configuration without a license to perform explicit safe recovery.
func (c Config) LicenseTrust() (wire.Trust, error) {
	if c.Licensing == nil || !licensePath(c.Licensing.TrustFile) {
		return wire.Trust{}, errors.New("CONFIG_LICENSING_TRUST_REQUIRED")
	}
	raw, e := readLicenseFile(c.Licensing.TrustFile, wire.MaximumKeyBytes)
	if e != nil {
		return wire.Trust{}, errors.New("CONFIG_LICENSING_TRUST_REQUIRED")
	}
	trust, e := wire.ParseTrust(raw)
	if e != nil {
		return wire.Trust{}, errors.New("CONFIG_LICENSING_TRUST_INVALID")
	}
	return trust, nil
}
