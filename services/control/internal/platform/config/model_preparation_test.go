package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preparationConfiguration() ModelPreparationConfiguration {
	return ModelPreparationConfiguration{ArtifactRoot: "/synthetic/preparations", ImageReference: "projectone/model-lifecycle@sha256:" + strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("b", 64), WorkerSourceSHA256: strings.Repeat("c", 64)}
}
func TestModelPreparationReleaseIdentityPathAndDependencyConfiguration(t *testing.T) {
	roots := []string{"/synthetic/models", "/synthetic/datasets", "/synthetic/training"}
	c := preparationConfiguration()
	if !c.Enabled() || c.Validate(roots[0], roots[1], roots[2]) != nil || (ModelPreparationConfiguration{}).Enabled() || (ModelPreparationConfiguration{}).Validate("", "", "") != nil {
		t.Fatal("complete release group and disabled default")
	}
	for name, change := range map[string]func(*ModelPreparationConfiguration){
		"relative":          func(c *ModelPreparationConfiguration) { c.ArtifactRoot = "relative" },
		"control_character": func(c *ModelPreparationConfiguration) { c.ArtifactRoot = "/synthetic/\tpreparations" },
		"root":              func(c *ModelPreparationConfiguration) { c.ArtifactRoot = "/" },
		"nested":            func(c *ModelPreparationConfiguration) { c.ArtifactRoot = "/synthetic/models/preparations" },
		"ancestor":          func(c *ModelPreparationConfiguration) { c.ArtifactRoot = "/synthetic" },
		"floating_image":    func(c *ModelPreparationConfiguration) { c.ImageReference = "projectone/model-lifecycle:latest" },
		"foreign_image": func(c *ModelPreparationConfiguration) {
			c.ImageReference = "projectone/training@sha256:" + strings.Repeat("a", 64)
		},
		"missing_id": func(c *ModelPreparationConfiguration) { c.ImageID = "" },
		"source":     func(c *ModelPreparationConfiguration) { c.WorkerSourceSHA256 = "synthetic-sensitive-value" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := c
			change(&candidate)
			e := candidate.Validate(roots[0], roots[1], roots[2])
			if e == nil || strings.Contains(e.Error(), "synthetic-sensitive-value") {
				t.Fatal("invalid group accepted or leaked", e)
			}
		})
	}
	if c.Validate(roots[0], roots[1], "") == nil {
		t.Fatal("missing training store accepted")
	}
}

func TestModelPreparationYAMLStrictScalarsAndNestedResourceOverrides(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/control/control.example.yaml")
	if e != nil {
		t.Fatal(e)
	}
	load := func(value string) (Config, error) {
		path := filepath.Join(t.TempDir(), "control.yaml")
		if e := os.WriteFile(path, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
		return Load(path)
	}
	base := string(raw)
	for name, fragment := range map[string]string{"null": "null", "number": "1", "null_field": "{artifact_root: null}", "numeric_field": "{artifact_root: 15}", "unknown": "{unknown_secret: synthetic-sensitive-value}", "merge": "{<<: {artifact_root: null}}"} {
		t.Run(name, func(t *testing.T) {
			_, e := load(strings.Replace(base, "model_preparation: {}", "model_preparation: "+fragment, 1))
			if e == nil || strings.Contains(e.Error(), "synthetic-sensitive-value") {
				t.Fatal("invalid group accepted or leaked", e)
			}
		})
	}
	for name, value := range map[string]string{"null": "null", "fraction": "5.5", "quoted": "\"512\"", "boolean": "true", "mapping": "{}"} {
		t.Run("resource_"+name, func(t *testing.T) {
			_, e := load(strings.Replace(base, "memory_bytes: 6442450944", "memory_bytes: "+value, 1))
			if e == nil {
				t.Fatal("invalid resource type accepted")
			}
		})
	}
	changed := strings.Replace(base, "max_preparations_per_workspace: 1000", "max_preparations_per_workspace: 4", 1)
	changed = strings.Replace(changed, "max_active_per_workspace: 10", "max_active_per_workspace: 2", 1)
	changed = strings.Replace(changed, "memory_bytes: 6442450944", "memory_bytes: 1073741824", 1)
	c, e := load(changed)
	if e != nil || c.Policy().ModelLifecycle.MaxPreparationsPerWorkspace != 4 || c.Policy().ModelLifecycle.MaxActivePerWorkspace != 2 || c.Policy().ModelLifecycle.Resources.MemoryBytes != 1073741824 {
		t.Fatal("nondefault nested policy", e)
	}
	if _, e = load(strings.Replace(base, "max_active_per_workspace: 10", "max_active_per_workspace: 1001", 1)); e == nil {
		t.Fatal("invalid active limit accepted")
	}
	if _, e = load(strings.Replace(base, "minimum_gpu_memory_mib: 8192", "minimum_gpu_memory_mib: 4096", 1)); e == nil {
		t.Fatal("unsafe declared GPU floor accepted")
	}
	resourceStart := strings.Index(base, "    resources:\n")
	resourceEnd := strings.Index(base[resourceStart:], "  training:\n") + resourceStart
	if resourceStart < 0 || resourceEnd <= resourceStart {
		t.Fatal("owned example resource boundaries")
	}
	if _, e = load(base[:resourceStart] + "    resources: null\n" + base[resourceEnd:]); e == nil {
		t.Fatal("null nested resources accepted")
	}
}
