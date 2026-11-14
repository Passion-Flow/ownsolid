package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"reflect"

	"go.yaml.in/yaml/v3"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

const MaximumPluginTrustKeys = 32

type PluginTrustKey struct {
	KeyID     string `yaml:"key_id" json:"key_id"`
	PublicKey string `yaml:"public_key" json:"public_key"`
}
type PluginConfiguration struct {
	Enabled     bool             `yaml:"enabled"`
	TrustedKeys []PluginTrustKey `yaml:"trusted_keys"`
}
type PluginPolicy struct {
	MaximumReferences  int `yaml:"maximum_references"`
	CommitSeconds      int `yaml:"commit_seconds"`
	MaintenanceBatch   int `yaml:"maintenance_batch"`
	MaintenanceSeconds int `yaml:"maintenance_seconds"`
	CapsuleBytes       int `yaml:"capsule_bytes"`
	ManifestBytes      int `yaml:"manifest_bytes"`
	ResultBytes        int `yaml:"result_bytes"`
	MaximumModels      int `yaml:"maximum_models"`
	MaximumVersions    int `yaml:"maximum_versions"`
	MaximumGrants      int `yaml:"maximum_grants"`
}

func DefaultPluginPolicy() PluginPolicy {
	return PluginPolicy{MaximumReferences: 1000, CommitSeconds: 10, MaintenanceBatch: 100, MaintenanceSeconds: 60, CapsuleBytes: wire.DefaultCapsuleBytes, ManifestBytes: wire.DefaultManifestBytes, ResultBytes: 65536, MaximumModels: 100, MaximumVersions: 100, MaximumGrants: 1000}
}
func (p PluginPolicy) PackageLimits() wire.Limits {
	return wire.Limits{CapsuleBytes: p.CapsuleBytes, ManifestBytes: p.ManifestBytes}
}
func (p PluginPolicy) Validate() error {
	if p.MaximumReferences < 1 || p.MaximumReferences > 10000 || p.CommitSeconds < 1 || p.CommitSeconds > 30 || p.MaintenanceBatch < 1 || p.MaintenanceBatch > 1000 || p.MaintenanceSeconds < 1 || p.MaintenanceSeconds > 3600 || p.PackageLimits().Validate() != nil || p.ResultBytes < 1024 || p.ResultBytes > 1048576 || p.MaximumModels < 1 || p.MaximumModels > 100 || p.MaximumVersions < 1 || p.MaximumVersions > 1000 || p.MaximumGrants < 1 || p.MaximumGrants > 10000 {
		return errors.New("CONFIG_PLUGIN_POLICY_INVALID")
	}
	return nil
}
func (c PluginConfiguration) PublicKeys() (map[string]ed25519.PublicKey, error) {
	out := map[string]ed25519.PublicKey{}
	if len(c.TrustedKeys) > MaximumPluginTrustKeys || c.Enabled && len(c.TrustedKeys) == 0 {
		return nil, errors.New("CONFIG_PLUGIN_TRUST_INVALID")
	}
	for _, entry := range c.TrustedKeys {
		if len(entry.KeyID) != 64 || len(entry.PublicKey) != base64.StdEncoding.EncodedLen(ed25519.PublicKeySize) {
			return nil, errors.New("CONFIG_PLUGIN_TRUST_INVALID")
		}
		raw, e := base64.StdEncoding.Strict().DecodeString(entry.PublicKey)
		id, keyError := wire.KeyID(ed25519.PublicKey(raw))
		if e != nil || keyError != nil || base64.StdEncoding.EncodeToString(raw) != entry.PublicKey || id != entry.KeyID || len(out[id]) != 0 {
			return nil, errors.New("CONFIG_PLUGIN_TRUST_INVALID")
		}
		out[id] = append(ed25519.PublicKey(nil), raw...)
	}
	return out, nil
}
func (c PluginConfiguration) Validate() error { _, e := c.PublicKeys(); return e }
func (c *PluginConfiguration) UnmarshalYAML(node *yaml.Node) error {
	if e := strictKnowledgeYAML(node, reflect.TypeOf(*c)); e != nil {
		return errors.New("CONFIG_PLUGIN_INVALID")
	}
	type plain PluginConfiguration
	var value plain
	if node.Decode(&value) != nil {
		return errors.New("CONFIG_PLUGIN_INVALID")
	}
	*c = PluginConfiguration(value)
	return nil
}
func (p *PluginPolicy) UnmarshalYAML(node *yaml.Node) error {
	if e := strictKnowledgeYAML(node, reflect.TypeOf(*p)); e != nil {
		return errors.New("CONFIG_PLUGIN_POLICY_INVALID")
	}
	type plain PluginPolicy
	value := plain(*p)
	if node.Decode(&value) != nil {
		return errors.New("CONFIG_PLUGIN_POLICY_INVALID")
	}
	*p = PluginPolicy(value)
	return nil
}

func validatePluginYAML(raw []byte) error {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("CONFIG_PLUGIN_INVALID")
	}
	root := document.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		name, value := root.Content[i].Value, root.Content[i+1]
		if name == "plugins" {
			if e := strictKnowledgeYAML(value, reflect.TypeOf(PluginConfiguration{})); e != nil {
				return errors.New("CONFIG_PLUGIN_INVALID")
			}
		}
		if name == "runtime" && value.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(value.Content); j += 2 {
				if value.Content[j].Value == "plugins" {
					if e := strictKnowledgeYAML(value.Content[j+1], reflect.TypeOf(PluginPolicy{})); e != nil {
						return errors.New("CONFIG_PLUGIN_POLICY_INVALID")
					}
				}
			}
		}
	}
	return nil
}
