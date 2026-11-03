package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func TestPluginPolicyAndTrustedPublisherConfiguration(t *testing.T) {
	if e := DefaultRuntimePolicy().Validate(); e != nil {
		t.Fatal("plugin defaults broke existing policy", e)
	}
	public, _, e := ed25519.GenerateKey(nil)
	if e != nil {
		t.Fatal(e)
	}
	id, _ := wire.KeyID(public)
	trusted := PluginTrustKey{KeyID: id, PublicKey: base64.StdEncoding.EncodeToString(public)}
	c := validConfig()
	c.Plugins = PluginConfiguration{Enabled: true, TrustedKeys: []PluginTrustKey{trusted}}
	p := DefaultRuntimePolicy()
	c.Runtime = &p
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "control.yaml")
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("private config fixture")
	}
	loaded, e := Load(path)
	if e != nil || !loaded.Plugins.Enabled || loaded.Policy().Plugins.PackageLimits() != p.Plugins.PackageLimits() {
		t.Fatal("actual plugin config loader", e)
	}
	keys, e := loaded.Plugins.PublicKeys()
	if e != nil || len(keys) != 1 {
		t.Fatal("configured public trust", e)
	}
	keys[id][0] ^= 1
	again, e := loaded.Plugins.PublicKeys()
	if e != nil || again[id][0] != public[0] {
		t.Fatal("public trust retained caller mutation", e)
	}
	for _, bad := range []PluginConfiguration{{Enabled: true}, {TrustedKeys: []PluginTrustKey{trusted, trusted}}, {TrustedKeys: []PluginTrustKey{{KeyID: id, PublicKey: "not-base64"}}}, {TrustedKeys: []PluginTrustKey{{KeyID: strings.Repeat("0", 64), PublicKey: trusted.PublicKey}}}, {TrustedKeys: []PluginTrustKey{{KeyID: id, PublicKey: trusted.PublicKey + "\n"}}}} {
		if e := bad.Validate(); e == nil || strings.Contains(e.Error(), trusted.PublicKey) {
			t.Fatal("unsafe/ambiguous trust or key disclosure", e)
		}
	}
	configured := DefaultPluginPolicy()
	if e := yaml.Unmarshal([]byte("capsule_bytes: 65536\nmanifest_bytes: 4096\nresult_bytes: 8192\nmaximum_models: 7\nmaximum_versions: 9\nmaximum_grants: 12\nmaximum_references: 19\ncommit_seconds: 8\nmaintenance_batch: 13\nmaintenance_seconds: 23\n"), &configured); e != nil || configured.Validate() != nil || configured.MaximumModels != 7 || configured.MaximumGrants != 12 || configured.MaximumReferences != 19 || configured.CommitSeconds != 8 || configured.MaintenanceBatch != 13 || configured.MaintenanceSeconds != 23 {
		t.Fatal("nondefault plugin policy", e)
	}
	for _, change := range []func(*PluginPolicy){func(p *PluginPolicy) { p.MaximumReferences = 0 }, func(p *PluginPolicy) { p.MaximumReferences = 10001 }, func(p *PluginPolicy) { p.CommitSeconds = 0 }, func(p *PluginPolicy) { p.CommitSeconds = 31 }, func(p *PluginPolicy) { p.MaintenanceBatch = 0 }, func(p *PluginPolicy) { p.MaintenanceBatch = 1001 }, func(p *PluginPolicy) { p.MaintenanceSeconds = 0 }, func(p *PluginPolicy) { p.MaintenanceSeconds = 3601 }, func(p *PluginPolicy) { p.CapsuleBytes = 1 }, func(p *PluginPolicy) { p.ManifestBytes = p.CapsuleBytes }, func(p *PluginPolicy) { p.ResultBytes = 0 }, func(p *PluginPolicy) { p.MaximumModels = 101 }, func(p *PluginPolicy) { p.MaximumVersions = 0 }, func(p *PluginPolicy) { p.MaximumGrants = 10001 }} {
		bad := DefaultPluginPolicy()
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("unsafe plugin policy accepted")
		}
	}
	for _, value := range []string{"plugins: null", "plugins: {enabled: 'true'}", "plugins: {trusted_keys: null}", "plugins: {trusted_keys: [null]}", "plugins: {unknown: true}", "plugins: {enabled: false, enabled: true}", "runtime: {plugins: null}", "runtime: {plugins: {maximum_models: '7'}}", "runtime: {plugins: {result_bytes: null}}", "plugins: &p {enabled: false}\nruntime: {plugins: *p}"} {
		if validatePluginYAML([]byte(value)) == nil {
			t.Fatal("invalid owned YAML accepted", value)
		}
	}
}
