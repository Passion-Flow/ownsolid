//go:build localization_deployment_integration

package config

import (
	"context"
	"encoding/json"
	"go.yaml.in/yaml/v3"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestLocalizationActualRendererConfigurationFalseNondefaultAndUID(t *testing.T) {
	root, e := filepath.Abs("../../../../../")
	if e != nil {
		t.Fatal(e)
	}
	python := os.Getenv("PROJECT_ONE_LOCALIZATION_FACTORY_PYTHON")
	source := os.Getenv("PROJECT_ONE_LOCALE_PACKAGE_DIRECTORY")
	if python == "" || source == "" {
		t.Fatal("actual factory Python and original seed package required")
	}
	for _, cumulative := range []bool{false, true} {
		c := validConfig()
		if cumulative {
			raw, e := os.ReadFile(filepath.Join(root, "services/control/config/examples/control/localization-cumulative.example.yaml"))
			if e != nil {
				t.Fatal(e)
			}
			c, e = decodeWithEnvironment(raw, "")
			if e != nil {
				t.Fatal(e)
			}
			c.Localization = LocalizationConfiguration{}
		}

		policy := c.Policy()
		c.Runtime = &policy
		raw, e := yaml.Marshal(c)
		if e != nil {
			t.Fatal(e)
		}
		var base map[string]any
		if yaml.Unmarshal(raw, &base) != nil {
			t.Fatal("lowercase config projection")
		}
		raw, e = json.Marshal(base)
		if e != nil {
			t.Fatal(e)
		}
		for _, enabled := range []bool{false, true} {
			dir := t.TempDir()
			if os.Chmod(dir, 0700) != nil {
				t.Fatal("private operator output parent")
			}
			runtimeDirectory := filepath.Join(dir, "projection", "private")
			configPath := filepath.Join(dir, "base.json")
			if os.WriteFile(configPath, raw, 0600) != nil {
				t.Fatal("base")
			}
			environment := "PROJECT_ONE_LOCALIZATION_ENABLED=" + strconv.FormatBool(enabled) + "\nPROJECT_ONE_LOCALIZATION_CACHE_SECONDS=0\n"
			if enabled {
				environment += "PROJECT_ONE_LOCALIZATION_PACKAGE_HOST_DIRECTORY=" + source + "\nPROJECT_ONE_LOCALIZATION_PACKAGE_DIRECTORY=" + runtimeDirectory + "\nPROJECT_ONE_LOCALIZATION_REQUIRED_LOCALES='[\"en\",\"zh-CN\"]'\nPROJECT_ONE_LOCALIZATION_VOLUME=explicit-synthetic-not-created\nPROJECT_ONE_LOCALIZATION_SCOPE_ID=29dec352-2b20-4db6-94f7-a40b7ae5826f\nPROJECT_ONE_RUNTIME_UID=" + strconv.Itoa(os.Getuid()) + "\nPROJECT_ONE_RUNTIME_GID=" + strconv.Itoa(os.Getgid()) + "\n"
			}
			envPath := filepath.Join(dir, "component.env")
			if os.WriteFile(envPath, []byte(environment), 0600) != nil {
				t.Fatal("env")
			}
			out := filepath.Join(dir, "rendered")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			command := exec.CommandContext(ctx, python, "-B", filepath.Join(root, "tools/deploy/localization/render.py"), "--control-config-json", configPath, "--env-file", envPath, "--target", "compose", "--output-directory", out)
			result, err := command.CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("actual renderer: %v %s", err, result)
			}
			current, err := Load(filepath.Join(out, "control.yaml"))
			if err != nil {
				t.Fatal("renderer not accepted by actual Go loader", err)
			}
			if (current.Localization.PackageDirectory != "") != enabled || current.Policy().Localization.CacheSeconds != 0 || current.Server.PortalOrigin != c.Server.PortalOrigin {
				t.Fatal("false, nondefault or inherited authority changed")
			}
			if enabled && (current.Localization.PackageDirectory != runtimeDirectory || len(current.Localization.RequiredLocales) != 2) {
				t.Fatal("locale directory/required set changed")
			}
		}
	}
}
