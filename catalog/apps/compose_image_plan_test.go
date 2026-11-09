package apps

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestVerifyComposeImagePlan(t *testing.T) {
	image := newImageFixture(t, "amd64", false)
	fixture := signOCI(t, ociTar(t, image.files, ""), image.image)
	verified, err := VerifyInstallBundle(t.Context(), fixture.root, fixture.key, fixture.limit)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{name: "signed"},
		{name: "unsigned image", field: "image", value: "sha256:" + strings.Repeat("1", 64)},
		{name: "registry reference", field: "image", value: "example.invalid/app@" + image.image},
		{name: "mutable tag", field: "image", value: "example.invalid/app:current"},
		{name: "missing pull policy", field: "pull_policy", value: ""},
		{name: "pull always", field: "pull_policy", value: "always"},
		{name: "build", field: "build", value: map[string]string{"context": "."}},
		{name: "provider", field: "provider", value: map[string]string{"type": "external"}},
		{name: "init image", field: "pre_start", value: []map[string]string{{"image": "example.invalid/init:current"}}},
		{name: "models", field: "models", value: []string{"external"}},
		{name: "develop", field: "develop", value: map[string]any{}},
		{name: "extends", field: "extends", value: map[string]string{"file": "other.yaml"}},
		{name: "image mount", field: "volumes", value: []map[string]string{{"type": "image", "source": image.image}}},
		{name: "foreign platform", field: "platform", value: "linux/arm64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := map[string]any{
				"image": image.image, "pull_policy": "never", "platform": "linux/amd64",
				"volumes": []map[string]string{{"type": "volume", "source": "data", "target": "/data"}},
			}
			if tc.field != "" {
				api[tc.field] = tc.value
			}
			raw := ociJSON(t, map[string]any{
				"services": map[string]any{
					"worker": map[string]any{"image": image.image, "pull_policy": "never", "profiles": []string{"batch"}},
					"api":    api,
				},
			})
			plan, err := verified.VerifyComposeImagePlan(RenderedCompose{ArtifactSHA256: verified.Manifest.Artifact.SHA256, JSON: raw})
			if tc.field != "" {
				if !errors.Is(err, ErrComposeImagePlan) || len(plan.Services) != 0 {
					t.Fatalf("unsafe model returned an image plan: %v", err)
				}
				return
			}
			want := []ComposeServiceImage{{Service: "api", ManifestDigest: image.image}, {Service: "worker", ManifestDigest: image.image}}
			if err != nil || plan.Pin != verified.Pin || plan.ModelSHA256 != digestBytes(raw) || !slices.Equal(plan.Services, want) {
				t.Fatalf("unexpected signed image plan %+v, %v", plan, err)
			}
		})
	}
	services := `{"api":{"image":"` + image.image + `","pull_policy":"never"}}`
	for _, raw := range []string{
		`{"services":` + services + `,"services":` + services + `}`,
		`{"services":` + services + `,"include":["other.yaml"]}`,
		`{"services":` + services + `,"models":{"external":{"model":"other"}}}`,
		`{"services":{}}`,
		strings.Repeat(" ", maxComposeModelBytes+1),
	} {
		if _, err := verified.VerifyComposeImagePlan(RenderedCompose{ArtifactSHA256: verified.Manifest.Artifact.SHA256, JSON: []byte(raw)}); !errors.Is(err, ErrComposeImagePlan) {
			t.Fatalf("invalid model was accepted: %v", err)
		}
	}
}

func TestImportRefusesRenderedComposeFromAnotherArtifact(t *testing.T) {
	root, key, private := transitionCatalog(t)
	pin := writeTransitionRelease(t, root, private, catalogReleaseFixture{version: "1.0.0", engine: "compose"})
	artifact, err := root.ReadFile(pin.Version + "/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"sha256:" + strings.Repeat("1", 64), digestBytes(artifact)} {
		result, err := ImportInstallImage(t.Context(), filepath.Join(root.Name(), pin.Version), key, ImageImportOptions{
			MaximumPayloadBytes: testTransitionBudget, MaximumExpandedBytes: testTransitionBudget, Timeout: time.Second,
			ExpectedPin: &pin, RenderedCompose: &RenderedCompose{
				ArtifactSHA256: source, JSON: []byte(`{"services":{"api":{"image":"example.invalid/unsigned:current","pull_policy":"never"}}}`),
			},
		})
		if !errors.Is(err, ErrComposeImagePlan) || result.Pin != (ReleasePin{}) {
			t.Fatalf("import passed the rendered image gate: %v", err)
		}
	}
}
