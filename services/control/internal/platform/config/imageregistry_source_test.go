package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestImageRegistrySiteSourceSelection(t *testing.T) {
	registry, _ := registryConfigurationFixture(t)
	source := filepath.Join(filepath.Dir(registry), "site-source.json")
	t.Setenv(registrySiteSourceEnvironment, "/ambient-must-not-be-read")
	base := registryParentYAML(t, registry)
	c, err := Decode(base)
	if err != nil || c.ImageRegistrySiteSourceFile != "" {
		t.Fatal("omitted source selected ambient configuration", err)
	}
	selected := append(base, []byte("\nimage_registry_site_source_file: "+source+"\n")...)
	c, err = Decode(selected)
	if err != nil || c.ImageRegistrySiteSourceFile != source || c.ImageRegistry == nil {
		t.Fatal("explicit source was not selected", err)
	}
	raw, err := json.Marshal(c)
	if err != nil || strings.Contains(string(raw), "site-source.json") {
		t.Fatal("private source reference entered diagnostics", err)
	}
	other := filepath.Join(filepath.Dir(registry), "override.json")
	c, err = decodeWithEnvironment(selected, workspaceEnvironmentFixture(t, registrySiteSourceEnvironment+"="+other+"\n"))
	if err != nil || c.ImageRegistrySiteSourceFile != other {
		t.Fatal("explicit protected override was not applied", err)
	}
	withoutRegistry := append(registryParentYAML(t, ""), []byte("\nimage_registry_site_source_file: "+source+"\n")...)
	if _, err := Decode(withoutRegistry); err == nil {
		t.Fatal("site source enabled without a writable registry")
	}
}

func TestImageRegistrySiteSourceRefusesMalformedSelection(t *testing.T) {
	registry, _ := registryConfigurationFixture(t)
	source := filepath.Join(filepath.Dir(registry), "site-source.json")
	environment := workspaceEnvironmentFixture(t, registrySiteSourceEnvironment+"="+source+"\n")
	for _, value := range []string{"null", "false", "123", "[]", "{}", "''", "relative", "'/run/../private/source'"} {
		raw := append(registryParentYAML(t, registry), []byte("\nimage_registry_site_source_file: "+value+"\n")...)
		if _, err := decodeWithEnvironment(raw, environment); err == nil {
			t.Fatal("malformed YAML was repaired by override", value)
		}
	}
	base := append(registryParentYAML(t, registry), []byte("\nimage_registry_site_source_file: "+source+"\n")...)
	duplicate := append(append([]byte{}, base...), []byte("image_registry_site_source_file: "+source+"\n")...)
	if _, err := Decode(duplicate); err == nil {
		t.Fatal("duplicate source selector accepted")
	}
	for _, body := range []string{
		registrySiteSourceEnvironment + "=\n",
		registrySiteSourceEnvironment + "=relative\n",
		registrySiteSourceEnvironment + "=/run/../private/source\n",
		registrySiteSourceEnvironment + "=" + source + "\n" + registrySiteSourceEnvironment + "=" + source + "\n",
		"PROJECT_ONE_REGISTRY_SITE_SOURCE_UNKNOWN=false\n",
	} {
		if _, err := decodeWithEnvironment(base, workspaceEnvironmentFixture(t, body)); err == nil {
			t.Fatal("invalid source environment accepted")
		}
	}
}
