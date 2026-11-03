package config

import (
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"
)

const modelCacheEnvironmentPrefix = "PROJECT_ONE_MODEL_CACHE_"

type ModelCacheConfiguration struct {
	Enabled             bool   `yaml:"enabled"`
	Address             string `yaml:"address"`
	TLS                 bool   `yaml:"tls"`
	CAFile              string `yaml:"ca_file"`
	Username            string `yaml:"username"`
	PasswordFile        string `yaml:"password_file"`
	Namespace           string `yaml:"namespace"`
	TTLSeconds          int    `yaml:"ttl_seconds"`
	TimeoutMilliseconds int    `yaml:"timeout_milliseconds"`
	MaximumBytes        int    `yaml:"maximum_bytes"`
	MaximumConcurrent   int    `yaml:"maximum_concurrent"`
}

func DefaultModelCacheConfiguration() ModelCacheConfiguration {
	return ModelCacheConfiguration{TLS: true, Namespace: "projectone", TTLSeconds: 60, TimeoutMilliseconds: 100, MaximumBytes: 1 << 20, MaximumConcurrent: 4}
}
func (c ModelCacheConfiguration) Validate() error {
	bad := errors.New("CONFIG_MODEL_CACHE_INVALID")
	if c.TTLSeconds < 1 || c.TTLSeconds > 86400 || c.TimeoutMilliseconds < 1 || c.TimeoutMilliseconds > 1000 || c.MaximumBytes < 1024 || c.MaximumBytes > 16<<20 || c.MaximumConcurrent < 1 || c.MaximumConcurrent > 32 {
		return bad
	}
	if len(c.Namespace) < 1 || len(c.Namespace) > 64 {
		return bad
	}
	for _, r := range c.Namespace {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return bad
		}
	}
	if strings.IndexFunc(c.Username, unicode.IsControl) >= 0 || strings.TrimSpace(c.Username) != c.Username {
		return bad
	}
	for _, p := range []string{c.PasswordFile, c.CAFile} {
		if p != "" && (!filepath.IsAbs(p) || filepath.Clean(p) != p) {
			return bad
		}
	}
	if !c.TLS && c.CAFile != "" || (c.Username == "") != (c.PasswordFile == "") {
		return bad
	}
	if c.Enabled && (c.Address == "" || c.Username == "" || c.PasswordFile == "") {
		return bad
	}
	if c.Address != "" {
		host, port, e := net.SplitHostPort(c.Address)
		n, ne := strconv.Atoi(port)
		if e != nil || ne != nil || n < 1 || n > 65535 || host == "" || strings.ContainsAny(host, "/\\ \t\r\n") || host == "0.0.0.0" || host == "::" {
			return bad
		}
	}
	return nil
}
func (c *ModelCacheConfiguration) UnmarshalYAML(node *yaml.Node) error {
	if !modelPolicyAnchorsAbsent(node) || strictKnowledgeYAML(node, reflect.TypeOf(*c)) != nil {
		return errors.New("CONFIG_MODEL_CACHE_INVALID")
	}
	type plain ModelCacheConfiguration
	value := plain(*c)
	if node.Decode(&value) != nil {
		return errors.New("CONFIG_MODEL_CACHE_INVALID")
	}
	*c = ModelCacheConfiguration(value)
	return nil
}
func validateModelCacheYAML(raw []byte) error {
	var n yaml.Node
	if yaml.Unmarshal(raw, &n) != nil || len(n.Content) != 1 || n.Content[0].Kind != yaml.MappingNode {
		return errors.New("CONFIG_MODEL_CACHE_INVALID")
	}
	root := n.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "model_cache" && (!modelPolicyAnchorsAbsent(root.Content[i+1]) || strictKnowledgeYAML(root.Content[i+1], reflect.TypeOf(ModelCacheConfiguration{})) != nil) {
			return errors.New("CONFIG_MODEL_CACHE_INVALID")
		}
	}
	return nil
}
func modelCacheEnvironment(key string) bool {
	return strings.HasPrefix(key, modelCacheEnvironmentPrefix)
}
func (c *ModelCacheConfiguration) applyEnvironment(key, value string) error {
	bad := errors.New("CONFIG_MODEL_CACHE_ENVIRONMENT_INVALID")
	switch strings.TrimPrefix(key, modelCacheEnvironmentPrefix) {
	case "ENABLED", "TLS":
		if value != "true" && value != "false" {
			return bad
		}
		v := value == "true"
		if key == modelCacheEnvironmentPrefix+"ENABLED" {
			c.Enabled = v
		} else {
			c.TLS = v
		}
	case "ADDRESS":
		c.Address = value
	case "CA_FILE":
		c.CAFile = value
	case "USERNAME":
		c.Username = value
	case "PASSWORD_FILE":
		c.PasswordFile = value
	case "NAMESPACE":
		c.Namespace = value
	case "TTL_SECONDS", "TIMEOUT_MILLISECONDS", "MAXIMUM_BYTES", "MAXIMUM_CONCURRENT":
		if value == "" {
			return bad
		}
		for _, r := range value {
			if r < '0' || r > '9' {
				return bad
			}
		}
		v, e := strconv.Atoi(value)
		if e != nil {
			return bad
		}
		switch strings.TrimPrefix(key, modelCacheEnvironmentPrefix) {
		case "TTL_SECONDS":
			c.TTLSeconds = v
		case "TIMEOUT_MILLISECONDS":
			c.TimeoutMilliseconds = v
		case "MAXIMUM_BYTES":
			c.MaximumBytes = v
		case "MAXIMUM_CONCURRENT":
			c.MaximumConcurrent = v
		}
	default:
		return bad
	}
	return nil
}

// Password pins one bounded owner-only regular file inside its private directory.
func (c ModelCacheConfiguration) Password() ([]byte, error) {
	return readPrivateFile(c.PasswordFile, 4096, "CONFIG_MODEL_CACHE_SECRET_INVALID")
}
