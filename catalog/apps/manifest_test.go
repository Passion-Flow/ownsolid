package apps

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fixtureManifest = `{
  "schema_version": 1,
  "id": "example-app",
  "version": "1.2.0",
  "platform": {"minimum": "1.10.0", "before": "2.0.0"},
  "artifact": {"engine": "compose", "sha256": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "images": ["sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],
  "form": [
    {"id": "display-name", "type": "text", "required": true},
    {"id": "replicas", "type": "integer", "required": false},
    {"id": "enabled", "type": "boolean", "required": false},
    {"id": "mode", "type": "choice", "required": true, "choices": ["strict", "basic"]}
  ]
}`

const fixtureEnglish = `{
  "title": "Example app",
  "description": "A local application",
  "fields": {
    "display-name": {"label": "Name", "description": "The displayed name"},
    "replicas": {"label": "Replicas", "description": "How many instances to run"},
    "enabled": {"label": "Enabled", "description": "Whether the app is enabled"},
    "mode": {"label": "Mode", "description": "The operating mode", "choices": {"strict": "Strict", "basic": "Basic"}}
  }
}`

const fixtureChinese = `{
  "title": "\u793a\u4f8b\u5e94\u7528",
  "description": "\u672c\u5730\u5e94\u7528",
  "fields": {
    "display-name": {"label": "\u540d\u79f0", "description": "\u663e\u793a\u540d\u79f0"},
    "replicas": {"label": "\u526f\u672c", "description": "\u526f\u672c\u6570\u91cf"},
    "enabled": {"label": "\u542f\u7528", "description": "\u662f\u5426\u542f\u7528"},
    "mode": {"label": "\u6a21\u5f0f", "description": "\u8fd0\u884c\u6a21\u5f0f", "choices": {"strict": "\u4e25\u683c", "basic": "\u57fa\u672c"}}
  }
}`

func releaseDirectory(t *testing.T, manifest, chinese string) *os.Root {
	t.Helper()
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "locales"), 0700); err != nil {
		t.Fatal(err)
	}
	for file, data := range map[string]string{
		"manifest.json": manifest, "locales/en.json": fixtureEnglish, "locales/zh-cn.json": chinese,
	} {
		if err := os.WriteFile(filepath.Join(directory, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func TestLoadLocalRelease(t *testing.T) {
	for engine, filename := range map[string]string{"compose": "compose.yaml", "helm": "chart.tgz"} {
		t.Run(engine, func(t *testing.T) {
			manifest := strings.Replace(fixtureManifest, `"compose"`, `"`+engine+`"`, 1)
			release, err := Load(releaseDirectory(t, manifest, fixtureChinese))
			if err != nil {
				t.Fatal(err)
			}
			if release.Manifest.Artifact.Filename() != filename || release.Manifest.ID != "example-app" ||
				release.English.Fields["mode"].Choices["strict"] != "Strict" ||
				release.SimplifiedChinese.Fields["mode"].Choices["strict"] != "\u4e25\u683c" {
				t.Fatal("release did not preserve its artifact or both locales")
			}
			for platform, want := range map[string]bool{
				"1.9.99": false, "1.10.0": true, "1.99.0": true, "2.0.0": false, "2.0.1": false,
			} {
				got, err := release.Manifest.Platform.Supports(platform)
				if got != want || err != nil {
					t.Fatalf("platform %s: got %t, %v; want %t", platform, got, err, want)
				}
			}
			if _, err := release.Manifest.Platform.Supports("1.10.0-rc.1"); !errors.Is(err, ErrPlatformVersion) {
				t.Fatalf("invalid platform version: %v", err)
			}

			values, err := release.Manifest.DecodeValues([]byte(`{"display-name":"\u793a\u4f8b","replicas":0,"enabled":false,"mode":"strict"}`))
			want := map[string]any{"display-name": "\u793a\u4f8b", "replicas": int64(0), "enabled": false, "mode": "strict"}
			if err != nil || !reflect.DeepEqual(values, want) {
				t.Fatalf("typed form values: %v, %v", values, err)
			}
			values, err = release.Manifest.DecodeValues([]byte(`{"display-name":"Example","mode":"basic"}`))
			if err != nil || len(values) != 2 {
				t.Fatalf("omitted values should stay absent: %v, %v", values, err)
			}
		})
	}
}

func TestRejectInvalidReleaseDocuments(t *testing.T) {
	replace := func(old, new string) string { return strings.Replace(fixtureManifest, old, new, 1) }
	for name, manifest := range map[string]string{
		"format":           replace(`"schema_version": 1`, `"schema_version": 2`),
		"unknown field":    replace(`"schema_version": 1`, `"schema_version": 1, "remote_schema": "https://example.invalid/"`),
		"duplicate":        replace(`"schema_version": 1`, `"schema_version": 1, "schema_version": 1`),
		"case variant":     replace(`"schema_version"`, `"Schema_Version"`),
		"identifier":       replace(`"example-app"`, `"../example-app"`),
		"version":          replace(`"1.2.0"`, `"1.02.0"`),
		"empty range":      replace(`"2.0.0"`, `"1.10.0"`),
		"reversed range":   replace(`"2.0.0"`, `"1.9.9"`),
		"engine":           replace(`"compose"`, `"script"`),
		"mutable image":    replace(`"sha256:`+strings.Repeat("b", 64)+`"`, `"example.invalid/image:latest"`),
		"duplicate ID":     replace(`"replicas"`, `"display-name"`),
		"duplicate choice": replace(`["strict", "basic"]`, `["strict", "strict"]`),
		"missing required": replace(`, "required": false`, ``),
		"null required":    replace(`"required": false`, `"required": null`),
		"secret field":     replace(`"type": "text"`, `"type": "secret"`),
		"invalid UTF-8":    replace("example-app", "\xff"),
		"trailing":         fixtureManifest + " {}",
		"too large":        fixtureManifest + strings.Repeat(" ", documentBytes),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Load(releaseDirectory(t, manifest, fixtureChinese))
			if !errors.Is(err, ErrManifest) || !reflect.DeepEqual(got, Release{}) {
				t.Fatalf("expected manifest refusal, got %v", err)
			}
		})
	}
	for name, chinese := range map[string]string{
		"unknown locale key": strings.Replace(fixtureChinese, `"replicas"`, `"other"`, 1),
		"missing choice":     strings.Replace(fixtureChinese, `"strict"`, `"other"`, 1),
		"empty":              "null",
		"non-text":           strings.Replace(fixtureChinese, `"\u540d\u79f0"`, `true`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(releaseDirectory(t, fixtureManifest, chinese)); !errors.Is(err, ErrLocale) {
				t.Fatalf("expected locale refusal, got %v", err)
			}
		})
	}
}

func TestRejectLocaleSymlink(t *testing.T) {
	root := releaseDirectory(t, fixtureManifest, fixtureChinese)
	if err := root.Remove("locales/zh-cn.json"); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("en.json", "locales/zh-cn.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); !errors.Is(err, ErrLocale) {
		t.Fatalf("symlink accepted as a locale file: %v", err)
	}
}

func TestRejectInvalidFormValues(t *testing.T) {
	release, err := Load(releaseDirectory(t, fixtureManifest, fixtureChinese))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`null`, `[]`, `{}`, `{"display-name":"Example","mode":"other"}`,
		`{"display-name":"Example","mode":"basic","other":"value"}`,
		`{"display-name":"Example","mode":"basic","enabled":"false"}`,
		`{"display-name":"Example","mode":"basic","enabled":null}`,
		`{"display-name":"Example","mode":"basic","replicas":null}`,
		`{"display-name":"Example","mode":"basic","replicas":1.5}`,
		`{"display-name":"Example","mode":"basic","replicas":9223372036854775808}`,
		`{"display-name":"Example","mode":"basic","mode":"strict"}`,
		`{"display-name":"Example\n","mode":"basic"}`,
		`{"display-name":"Example","mode":"basic"} {}`,
	} {
		if values, err := release.Manifest.DecodeValues([]byte(input)); !errors.Is(err, ErrValues) || values != nil {
			t.Errorf("expected rejection for %s, got %v, %v", input, values, err)
		}
	}
}
