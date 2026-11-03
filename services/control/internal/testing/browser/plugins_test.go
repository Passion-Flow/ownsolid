package browser_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/plugins/v1"
)

func TestM14PluginBrowser(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			t.Setenv("PROJECT_ONE_BROWSER_LOCALE", locale)
			testProductionBrowser(t, "plugins/plugin-flow.mjs", "plugin-browser-"+locale, true)
		})
	}
}

func TestM14PluginToolOnlyBrowser(t *testing.T) {
	t.Setenv("PROJECT_ONE_PLUGIN_BROWSER_TOOL_ONLY", "1")
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			t.Setenv("PROJECT_ONE_BROWSER_LOCALE", locale)
			testProductionBrowser(t, "plugins/plugin-flow.mjs", "plugin-tool-only-browser-"+locale, true)
		})
	}
}

func configurePluginBrowser(t *testing.T, s *identity.Service, directory string) []string {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := wire.KeyID(public)
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Plugins = config.PluginConfiguration{Enabled: true, TrustedKeys: []config.PluginTrustKey{{KeyID: id, PublicKey: base64.StdEncoding.EncodeToString(public)}}}
	s.Config.Workflows.Enabled = true
	paths := []string{}
	for _, version := range []string{"1.0.0", "1.1.0"} {
		raw, err := wire.Sign(wire.Manifest{PluginID: wire.PluginID, Version: version, ABI: wire.ABI, Executor: wire.Executor, Capabilities: []string{wire.Capability}, License: wire.License}, private, s.Config.Policy().Plugins.PackageLimits())
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "synthetic-plugin-"+version+".json")
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}
