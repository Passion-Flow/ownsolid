package localization

import (
	"encoding/json"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"testing"
)

func TestLocalizationRealEmbeddedSeedsExactMessageMailPluralAndDetachedMetadata(t *testing.T) {
	r, e := New(config.LocalizationConfiguration{}, config.DefaultLocalizationPolicy())
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Catalog()) != 2 || len(r.MailTemplates()) != 6 || !digestPattern.MatchString(r.SourceSHA256()) {
		t.Fatal("incomplete real seeds")
	}
	for _, locale := range []string{"en", "zh-CN"} {
		if value, ok := r.Resolve(locale); !ok || value != locale {
			t.Fatal("seed alias missing")
		}
		p := r.locales[locale]
		if len(p.Messages) < 1400 || p.GoogleTranslatedComplete || p.Translation.Review["state"] != "pending" {
			t.Fatal("seed provenance incorrect")
		}
	}
	catalog := r.Catalog()
	catalog[0].Aliases[0] = "changed"
	catalog[0].Formatting.PluralCategories[0] = "invalid"
	if r.Catalog()[0].Aliases[0] == "changed" || r.Catalog()[0].Formatting.PluralCategories[0] == "invalid" {
		t.Fatal("shared inventory mutated")
	}
	templates := r.MailTemplates()
	delete(templates, "en/invitation")
	if len(r.MailTemplates()) != 6 {
		t.Fatal("shared mail mutated")
	}
	if _, ok := r.Resolve("fr"); ok {
		t.Fatal("uninstalled French claimed available")
	}
}
func TestLocalizationRequiredMissingCorruptUnknownAndSubstitutedDirectoriesRefuse(t *testing.T) {
	p := config.DefaultLocalizationPolicy()
	if _, e := New(config.LocalizationConfiguration{RequiredLocales: []string{"fr"}}, p); e == nil {
		t.Fatal("missing required locale accepted")
	}
	for name, raw := range map[string][]byte{"invalid_json": []byte("{"), "unknown_inventory": []byte(`{"schema_version":1,"unexpected":true}`), "duplicate": []byte(`{"schema_version":1,"schema_version":1}`), "null": []byte(`null`)} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			if e := os.Chmod(directory, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(directory, "inventory.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e := New(config.LocalizationConfiguration{PackageDirectory: directory}, p); e == nil {
				t.Fatal("corrupt package accepted")
			}
		})
	}
	directory := t.TempDir()
	link := filepath.Join(t.TempDir(), "substitution")
	if e := os.Symlink(directory, link); e != nil {
		t.Fatal(e)
	}
	if _, e := New(config.LocalizationConfiguration{PackageDirectory: link}, p); e == nil {
		t.Fatal("substituted directory accepted")
	}
}
func TestLocalizationValidationRejectsSeedTextDriftInjectedTemplateAndFalseProvider(t *testing.T) {
	r, e := Default()
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(*r.locales["en"])
	if e != nil {
		t.Fatal(e)
	}
	var copy LocalePackage
	if e = json.Unmarshal(raw, &copy); e != nil {
		t.Fatal(e)
	}
	copy.Messages["brand"] = "Modified seed"
	if sameSeedText(*r.locales["en"], copy) {
		t.Fatal("modified seed accepted")
	}
	if safeTemplate("Use {{.Link}} {{if true}}bad{{end}}", "Use {{.Link}}") || safeHTML(`<p><a href="https://foreign.invalid">link</a></p>`, `<p><a href="{{.Link}}">link</a></p>`) {
		t.Fatal("injected template accepted")
	}
	if e = r.validate(copy, false); e == nil {
		t.Fatal("seed pretended to have actual provider and review")
	}
	if CanonicalLocale("../en") || CanonicalLocale("EN") || !CanonicalLocale("fr") || !CanonicalLocale("zh-CN") {
		t.Fatal("locale grammar")
	}
}
func TestLocalizationPinnedFontLockRejectsChangedLicenseFamilyOrHash(t *testing.T) {
	font := Font{SHA256: "bfb7bb691513f12e734dc346c03a03f784912432d7e3fa8e56efcf906fe86b3d", LicenseSHA256: "cee9892f9f0cc8fe882c9e9537ee6a89621d86ee7ceaf70b02e2b2b1c25c061a", Family: "Noto Sans", Format: "truetype", Weight: "100 900", Style: "normal"}
	if !knownFont(font) {
		t.Fatal("actual frozen font declaration refused")
	}
	font.Family = "Foreign"
	if knownFont(font) {
		t.Fatal("changed font family accepted")
	}
	font.Family = "Noto Sans"
	font.LicenseSHA256 = font.SHA256
	if knownFont(font) {
		t.Fatal("changed license accepted")
	}
}
