package config

import (
	"encoding/json"
	"errors"
	"go.yaml.in/yaml/v3"
	"reflect"
	"strconv"
	"strings"
)

// Deployment overrides address the same typed policy and never read ambient env.
func (p *PluginPolicy) environmentField(key string) reflect.Value {
	v := reflect.ValueOf(p).Elem()
	for i := 0; i < v.NumField(); i++ {
		if key == "PROJECT_ONE_PLUGINS_"+strings.ToUpper(v.Type().Field(i).Tag.Get("yaml")) {
			return v.Field(i)
		}
	}
	return reflect.Value{}
}
func (p *PluginPolicy) applyEnvironment(key, raw string) error {
	v := p.environmentField(key)
	n, e := strconv.ParseInt(raw, 10, 64)
	if !v.IsValid() || e != nil || v.OverflowInt(n) {
		return errors.New("CONFIG_PLUGIN_ENVIRONMENT_INVALID")
	}
	v.SetInt(n)
	return nil
}
func pluginConfigurationEnvironment(key string) bool {
	return key == "PROJECT_ONE_PLUGINS_ENABLED" || key == "PROJECT_ONE_PLUGINS_TRUSTED_KEYS_FILE"
}
func (c *PluginConfiguration) applyEnvironment(key, raw string) error {
	bad := errors.New("CONFIG_PLUGIN_ENVIRONMENT_INVALID")
	if key == "PROJECT_ONE_PLUGINS_ENABLED" {
		if raw != "true" && raw != "false" {
			return bad
		}
		c.Enabled = raw == "true"
		return nil
	}
	if key != "PROJECT_ONE_PLUGINS_TRUSTED_KEYS_FILE" {
		return bad
	}
	data, e := readPrivateFile(raw, 16384, "CONFIG_PLUGIN_TRUST_PRIVATE_FILE_INVALID")
	if e != nil {
		return e
	}
	var node yaml.Node
	if !json.Valid(data) || yaml.Unmarshal(data, &node) != nil || len(node.Content) != 1 || strictKnowledgeYAML(node.Content[0], reflect.TypeOf([]PluginTrustKey{})) != nil {
		return bad
	}
	var keys []PluginTrustKey
	if node.Content[0].Decode(&keys) != nil || keys == nil {
		return bad
	}
	c.TrustedKeys = keys
	return nil
}
