//go:build localization_assets_integration

package localization

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"strings"
	"testing"
)

func actualSeedPackage(t *testing.T) string {
	t.Helper()
	path := os.Getenv("PROJECT_ONE_LOCALE_PACKAGE_DIRECTORY")
	if path == "" {
		t.Fatal("actual owned seed/font package required")
	}
	return path
}
func copyActualPackage(t *testing.T, source string) string {
	t.Helper()
	out := t.TempDir()
	if e := os.Chmod(out, 0700); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(source)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatal("unexpected nested artifact")
		}
		input, e := os.Open(filepath.Join(source, entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		output, e := os.OpenFile(filepath.Join(out, entry.Name()), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			input.Close()
			t.Fatal(e)
		}
		_, e = io.Copy(output, input)
		input.Close()
		closeError := output.Close()
		if e != nil || closeError != nil {
			t.Fatal("owned fixture copy failed")
		}
	}
	return out
}
func TestLocalizationActualOriginalFontPackageRestartPublicHashesAndPendingSeedProvenance(t *testing.T) {
	directory := actualSeedPackage(t)
	raw, e := os.ReadFile(filepath.Join(directory, "inventory.json"))
	if e != nil {
		t.Fatal(e)
	}
	var inventory any
	if json.Unmarshal(raw, &inventory) != nil {
		t.Fatal("invalid actual inventory")
	}
	if e = localizationArtifactSchema(t, "Inventory").Validate(inventory); e != nil {
		t.Fatal("actual inventory artifact contract", e)
	}
	configuration := config.LocalizationConfiguration{PackageDirectory: directory, RequiredLocales: []string{"en", "zh-CN"}}
	var first []Choice
	for restart := 0; restart < 2; restart++ {
		r, e := New(configuration, config.DefaultLocalizationPolicy())
		if e != nil {
			t.Fatal("actual package startup", e)
		}
		choices := r.Catalog()
		if restart == 0 {
			first = choices
		} else if first[0].PackageSHA256 != choices[0].PackageSHA256 {
			t.Fatal("restart changed immutable identity")
		}
		for _, choice := range choices {
			if choice.GoogleTranslatedComplete || choice.ReviewState != "pending" || len(choice.Fonts) != 2 {
				t.Fatal("seed claimed reviewed Google provenance or missed fonts")
			}
			for _, font := range choice.Fonts {
				response := httptest.NewRecorder()
				r.ServeHTTP(response, httptest.NewRequest("GET", publicPrefix+"fonts/"+font.SHA256, nil))
				if response.Code != 200 || hash(response.Body.Bytes()) != font.SHA256 || response.Header().Get("Content-Type") != "font/ttf" {
					t.Fatal("real original font integrity")
				}
				response = httptest.NewRecorder()
				r.ServeHTTP(response, httptest.NewRequest("GET", publicPrefix+"assets/"+font.LicenseSHA256, nil))
				if response.Code != 200 || !strings.Contains(response.Body.String(), "SIL OPEN FONT LICENSE") {
					t.Fatal("original font license unavailable")
				}
			}
		}
	}
}
func TestLocalizationActualPackageUnknownMissingCorruptSymlinkAndPermissionFailures(t *testing.T) {
	source := actualSeedPackage(t)
	for _, failure := range []string{"unknown", "missing", "corrupt", "symlink", "permission", "missing_status"} {
		t.Run(failure, func(t *testing.T) {
			directory := copyActualPackage(t, source)
			entries, e := os.ReadDir(directory)
			if e != nil {
				t.Fatal(e)
			}
			var font string
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".ttf") {
					font = filepath.Join(directory, entry.Name())
					break
				}
			}
			if font == "" {
				t.Fatal("actual font required")
			}
			switch failure {
			case "missing_status":
				path := filepath.Join(directory, "inventory.json")
				raw, readError := os.ReadFile(path)
				if readError != nil {
					t.Fatal(readError)
				}
				var body map[string]any
				if json.Unmarshal(raw, &body) != nil {
					t.Fatal("inventory")
				}
				delete(body, "delivery_status")
				raw, e = json.Marshal(body)
				if e == nil {
					e = os.WriteFile(path, raw, 0600)
				}
			case "unknown":
				e = os.WriteFile(filepath.Join(directory, "unknown"), []byte("PRESERVE UNRECOGNIZED DATA"), 0600)
			case "missing":
				e = os.Remove(font)
			case "corrupt":
				e = os.WriteFile(font, []byte("CORRUPTED OWNED TEST COPY"), 0600)
			case "permission":
				e = os.Chmod(font, 0644)
			case "symlink":
				e = os.Remove(font)
				if e == nil {
					e = os.Symlink(filepath.Join(source, filepath.Base(font)), font)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = New(config.LocalizationConfiguration{PackageDirectory: directory}, config.DefaultLocalizationPolicy()); e == nil {
				t.Fatal("invalid actual package accepted")
			}
			if failure == "unknown" {
				raw, e := os.ReadFile(filepath.Join(directory, "unknown"))
				if e != nil || string(raw) != "PRESERVE UNRECOGNIZED DATA" {
					t.Fatal("unknown data modified")
				}
			}
		})
	}
}
