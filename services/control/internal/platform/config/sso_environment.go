package config

import (
	"encoding/json"
	"errors"
	"go.yaml.in/yaml/v3"
	"reflect"
	"strings"
)

const maximumSSOProviderInventoryBytes int64 = 1 << 20

const ssoCleanupEnvironment = "PROJECT_ONE_SSO_CLEANUP_SECONDS"
const ssoCleanupTimeoutEnvironment = "PROJECT_ONE_SSO_CLEANUP_TIMEOUT_SECONDS"
const ssoCleanupBatchEnvironment = "PROJECT_ONE_SSO_CLEANUP_BATCH_SIZE"
const ssoFlowRetentionEnvironment = "PROJECT_ONE_SSO_FLOW_RETENTION_DAYS"

// The same typed policy owns YAML, explicit env-file overrides and validation.
func (p *SSOPolicy) environmentField(key string) *int {
	value := reflect.ValueOf(p).Elem()
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		if key == "PROJECT_ONE_SSO_"+strings.ToUpper(typ.Field(i).Tag.Get("yaml")) {
			return value.Field(i).Addr().Interface().(*int)
		}
	}
	return nil
}
func ssoMaintenanceEnvironment(key string) bool {
	p := SSOPolicy{}
	return p.environmentField(key) != nil
}
func (p *SSOPolicy) applyMaintenanceEnvironment(key string, value int) {
	if field := p.environmentField(key); field != nil {
		*field = value
	}
}
func ssoConfigurationEnvironment(key string) bool {
	return key == "PROJECT_ONE_SSO_ENABLED" || key == "PROJECT_ONE_SSO_KEYRING_FILE" || key == "PROJECT_ONE_SSO_PROVIDERS_FILE"
}
func (c *SSOConfiguration) applyEnvironment(key, value string) error {
	bad := errors.New("CONFIG_SSO_ENVIRONMENT_INVALID")
	switch key {
	case "PROJECT_ONE_SSO_ENABLED":
		if value != "true" && value != "false" {
			return bad
		}
		c.Enabled = value == "true"
	case "PROJECT_ONE_SSO_KEYRING_FILE":
		if !ssoPrivatePath(value) {
			return bad
		}
		c.KeyringFile = value
	case "PROJECT_ONE_SSO_PROVIDERS_FILE":
		raw, e := readPrivateFile(value, maximumSSOProviderInventoryBytes, "CONFIG_SSO_PROVIDER_PRIVATE_FILE_INVALID")
		if e != nil {
			return e
		}
		if !json.Valid(raw) {
			return bad
		}
		var node yaml.Node
		if yaml.Unmarshal(raw, &node) != nil || len(node.Content) != 1 || strictSSOYAML(node.Content[0], reflect.TypeOf([]SSOProvider{})) != nil {
			return bad
		}
		var providers []SSOProvider
		if node.Content[0].Decode(&providers) != nil || providers == nil {
			return bad
		}
		c.Providers = providers
	default:
		return bad
	}
	return nil
}
