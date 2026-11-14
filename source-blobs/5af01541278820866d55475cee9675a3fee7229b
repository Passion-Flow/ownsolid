package localization

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type staticCatalogEntry struct {
	Locale     string     `json:"locale"`
	Aliases    []string   `json:"aliases"`
	Formatting Formatting `json:"formatting"`
}

func staticTargetCatalog(t *testing.T) ([]string, map[string]staticCatalogEntry) {
	t.Helper()
	raw, err := os.ReadFile("../../../../../api/localization/v1/release-language-set.json")
	if err != nil {
		t.Fatal(err)
	}
	var release struct {
		Locales []string `json:"documentation_target_locales"`
	}
	if json.Unmarshal(raw, &release) != nil || len(release.Locales) != 202 {
		t.Fatal("exact retained 202-locale contract required")
	}
	raw, err = os.ReadFile("../../../../../api/localization/v1/google-language-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Entries []staticCatalogEntry `json:"entries"`
	}
	if json.Unmarshal(raw, &catalog) != nil {
		t.Fatal("catalog")
	}
	entries := map[string]staticCatalogEntry{}
	for _, e := range catalog.Entries {
		entries[e.Locale] = e
	}
	return release.Locales, entries
}

// This is a structure-only fixture copied from English; it is not a translation,
// a real font inspection, or an assertion of target-language runtime quality.
func staticMetadataFixture(t *testing.T, locale string) (*Registry, LocalePackage) {
	t.Helper()
	r, err := New(config.LocalizationConfiguration{}, config.DefaultLocalizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r.locales["en"])
	var p LocalePackage
	if json.Unmarshal(raw, &p) != nil {
		t.Fatal("fixture clone")
	}
	_, catalog := staticTargetCatalog(t)
	entry, ok := catalog[locale]
	if !ok {
		t.Fatal("missing fixture catalog")
	}
	p.Locale, p.DisplayName, p.EnglishName = locale, "EXPLICIT METADATA FIXTURE", "EXPLICIT METADATA FIXTURE"
	p.Aliases, p.Formatting, p.Direction = entry.Aliases, entry.Formatting, entry.Formatting.Direction
	if len(p.Formatting.PluralCategories) == 0 {
		p.Formatting.PluralCategories = []string{"other"}
	}
	for key, plural := range p.Plurals {
		forms := map[string]string{}
		for _, cat := range p.Formatting.PluralCategories {
			forms[cat] = plural.Forms["other"]
		}
		p.Plurals[key] = Plural{Argument: "count", Forms: forms}
	}
	var lock struct {
		Fonts []struct {
			Family, Format, Weight, Style string
			Font                          struct {
				SHA256 string `json:"sha256"`
			} `json:"font"`
			License struct {
				SHA256 string `json:"sha256"`
			} `json:"license"`
		} `json:"fonts"`
	}
	if json.Unmarshal(fontAssets, &lock) != nil || len(lock.Fonts) == 0 {
		t.Fatal("font lock")
	}
	f := lock.Fonts[0]
	p.Fonts = []Font{{SHA256: f.Font.SHA256, LicenseSHA256: f.License.SHA256, Family: f.Family, Format: f.Format, Weight: f.Weight, Style: f.Style}}
	p.FontCoverage = &FontCoverage{RequiredCodepoints: 1, CoveredRequiredCodepoints: 1, MissingCodepoints: []string{}, Status: "complete_cmap_for_exact_product_strings_only"}
	p.CatalogSHA256 = strings.Repeat("a", 64)
	runtimeTranslation := false
	p.Translation = Translation{State: staticMachineState, Provider: staticMachineProvider, Review: map[string]string{"state": "pending"}, ManufacturingSHA256: strings.Repeat("b", 64), PlanSHA256: strings.Repeat("b", 64), CandidateSHA256: strings.Repeat("c", 64), RuntimeTranslation: &runtimeTranslation, UpstreamModelIdentity: unverifiedUpstreamModel}
	p.GoogleTranslatedComplete = false
	return r, p
}

func TestStaticProductRetained202LocaleGrammarAliasesAndHonestIntlMetadata(t *testing.T) {
	locales, entries := staticTargetCatalog(t)
	aliases := map[string]string{}
	policy := config.DefaultLocalizationPolicy()
	policy.MaximumLocales = len(locales)
	if policy.Validate() != nil {
		t.Fatal("explicit 202-locale policy")
	}
	for _, locale := range locales {
		entry, ok := entries[locale]
		if !ok || !CanonicalLocale(locale) {
			t.Errorf("retained canonical locale rejected: %s", locale)
			continue
		}
		own := false
		for _, alias := range entry.Aliases {
			if old := aliases[alias]; old != "" && old != locale {
				t.Errorf("alias conflict %s", alias)
			}
			aliases[alias] = locale
			own = own || alias == locale
		}
		if !own {
			t.Errorf("missing self alias %s", locale)
		}
		if locale == "en" {
			continue
		}
		p := LocalePackage{Locale: locale, Direction: entry.Formatting.Direction, Formatting: entry.Formatting}
		if len(p.Formatting.PluralCategories) == 0 {
			p.Formatting.PluralCategories = []string{"other"}
		}
		if !validStaticFormatting(p) {
			t.Errorf("actual formatting rejected %s", locale)
		}
	}
	for _, locale := range []string{"EN", "../fr", "fr/../../key", "fr?x", "fr-", "en_US"} {
		if CanonicalLocale(locale) {
			t.Errorf("invalid canonical locale accepted: %s", locale)
		}
	}
}

func TestStaticProductMachineProvenanceAndStructuralNegativeCases(t *testing.T) {
	cases := map[string]func(*LocalePackage){
		"wrong_source":               func(p *LocalePackage) { p.SourceSHA256 = strings.Repeat("d", 64) },
		"canonical_source_overwrite": func(p *LocalePackage) { p.Locale = "en"; p.Aliases = []string{"en"} },
		"wrong_provider":             func(p *LocalePackage) { p.Translation.Provider = "Google Translate" },
		"forged_review":              func(p *LocalePackage) { p.Translation.Review["state"] = "approved" },
		"extra_review_claim":         func(p *LocalePackage) { p.Translation.Review["reviewer_sha256"] = strings.Repeat("e", 64) },
		"forged_google":              func(p *LocalePackage) { p.GoogleTranslatedComplete = true },
		"missing_manufacturing":      func(p *LocalePackage) { p.Translation.ManufacturingSHA256 = "" },
		"plan_source_mismatch":       func(p *LocalePackage) { p.Translation.PlanSHA256 = strings.Repeat("d", 64) },
		"candidate_hash":             func(p *LocalePackage) { p.Translation.CandidateSHA256 = "invalid" },
		"missing_catalog":            func(p *LocalePackage) { p.CatalogSHA256 = "" },
		"missing_runtime_policy":     func(p *LocalePackage) { p.Translation.RuntimeTranslation = nil },
		"runtime_provider":           func(p *LocalePackage) { v := true; p.Translation.RuntimeTranslation = &v },
		"forged_upstream":            func(p *LocalePackage) { p.Translation.UpstreamModelIdentity = "verified" },
		"claimed_model":              func(p *LocalePackage) { p.Translation.Model = "general/nmt" },
		"claimed_model_resource":     func(p *LocalePackage) { p.Translation.ModelResourceSHA256 = strings.Repeat("e", 64) },
		"claimed_review_hash":        func(p *LocalePackage) { p.Translation.ReviewedSHA256 = strings.Repeat("e", 64) },
		"attribution": func(p *LocalePackage) {
			p.Translation.Attribution = &Attribution{Link: "https://translate.google.com/"}
		},
		"missing_message":   func(p *LocalePackage) { delete(p.Messages, "login") },
		"extra_message":     func(p *LocalePackage) { p.Messages["injected"] = "Injected" },
		"added_placeholder": func(p *LocalePackage) { p.Messages["login"] = "Login {injected}" },
		"html_injection": func(p *LocalePackage) {
			m := p.Mail["password_reset"]
			m.HTML = "<script>bad</script>" + m.HTML
			p.Mail["password_reset"] = m
		},
		"mail_action": func(p *LocalePackage) {
			m := p.Mail["password_reset"]
			m.Text += "{{if true}}bad{{end}}"
			p.Mail["password_reset"] = m
		},
		"mail_subject_newline": func(p *LocalePackage) {
			m := p.Mail["password_reset"]
			m.Subject += "\r\nInjected"
			p.Mail["password_reset"] = m
		},
		"plural_argument": func(p *LocalePackage) {
			for k, v := range p.Plurals {
				v.Argument = "other"
				p.Plurals[k] = v
				break
			}
		},
		"plural_placeholder": func(p *LocalePackage) {
			for k, v := range p.Plurals {
				v.Forms["other"] = "none"
				p.Plurals[k] = v
				break
			}
		},
		"missing_plural_category": func(p *LocalePackage) {
			for k, v := range p.Plurals {
				delete(v.Forms, "other")
				p.Plurals[k] = v
				break
			}
		},
		"format_locale":    func(p *LocalePackage) { p.Formatting.Locale = "de" },
		"format_direction": func(p *LocalePackage) { p.Formatting.Direction = "rtl" },
		"format_resolved":  func(p *LocalePackage) { p.Formatting.ResolvedDateLocale = "../fr" },
		"format_status":    func(p *LocalePackage) { p.Formatting.MetadataStatus = "human_reviewed" },
		"missing_font":     func(p *LocalePackage) { p.Fonts = nil },
		"unknown_font":     func(p *LocalePackage) { p.Fonts[0].SHA256 = strings.Repeat("e", 64) },
		"font_license":     func(p *LocalePackage) { p.Fonts[0].LicenseSHA256 = strings.Repeat("e", 64) },
		"cmap_missing":     func(p *LocalePackage) { p.FontCoverage.MissingCodepoints = []string{"U+1234"} },
		"cmap_count":       func(p *LocalePackage) { p.FontCoverage.CoveredRequiredCodepoints = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, p := staticMetadataFixture(t, "fr")
			if err := r.validate(p, false); err != nil {
				t.Fatal("baseline metadata fixture", err)
			}
			mutate(&p)
			if r.validate(p, false) == nil {
				t.Fatal("invalid static artifact accepted")
			}
		})
	}
}

func TestStaticProductArtifactContractKeepsMachineAndReviewedProvenanceSeparate(t *testing.T) {
	_, p := staticMetadataFixture(t, "fr")
	p.FontCoverage.Qualification = "Explicit synthetic shape fixture; not actual font or translation acceptance."
	schema := localizationArtifactSchema(t, "Locale")
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		t.Fatal("fixture JSON")
	}
	if err := schema.Validate(body); err != nil {
		t.Fatal("machine shape", err)
	}
	for _, key := range []string{"provider", "plan_sha256", "candidate_sha256", "manufacturing_sha256", "runtime_translation", "upstream_model_identity"} {
		translation := body["translation"].(map[string]any)
		previous := translation[key]
		delete(translation, key)
		if schema.Validate(body) == nil {
			t.Errorf("missing machine field accepted: %s", key)
		}
		translation[key] = previous
	}
	for name, change := range map[string]func(map[string]any){
		"english overwrite": func(v map[string]any) { v["locale"] = "en" },
		"Google claim":      func(v map[string]any) { v["google_translated_complete"] = true },
		"human claim": func(v map[string]any) {
			v["translation"].(map[string]any)["review"] = map[string]any{"state": "approved"}
		},
		"invented model":       func(v map[string]any) { v["translation"].(map[string]any)["model"] = "general/nmt" },
		"runtime API":          func(v map[string]any) { v["translation"].(map[string]any)["runtime_translation"] = true },
		"runtime false string": func(v map[string]any) { v["translation"].(map[string]any)["runtime_translation"] = "false" },
		"missing catalog":      func(v map[string]any) { delete(v, "catalog_sha256") },
		"uncovered glyph":      func(v map[string]any) { v["font_coverage"].(map[string]any)["missing_codepoints"] = []string{"U+1234"} },
		"seed carrying machine provenance": func(v map[string]any) {
			v["translation"].(map[string]any)["state"] = "first_party_seed_pending_Google_review"
		},
	} {
		t.Run(name, func(t *testing.T) {
			var altered map[string]any
			if json.Unmarshal(raw, &altered) != nil {
				t.Fatal("fixture JSON")
			}
			change(altered)
			if schema.Validate(altered) == nil {
				t.Fatal("invalid machine contract accepted")
			}
		})
	}
}

func TestStaticProductTranslatesWorkspaceWithoutChangingBrandTerms(t *testing.T) {
	r, p := staticMetadataFixture(t, "zh-CN")
	p.Messages["workspace"] = "\u5de5\u4f5c\u533a"
	p.Messages["workspace_name"] = "\u5de5\u4f5c\u533a\u540d\u79f0"
	if err := r.validate(p, false); err != nil {
		t.Fatal("translatable product concept rejected", err)
	}
	p.Messages["brand"] = "Another product"
	if r.validate(p, false) == nil {
		t.Fatal("substituted product name accepted")
	}
	if sameTokens(fixedTerms, "\u5de5\u4f5c\u533a", "Workspace") {
		t.Fatal("historical reviewed-term policy weakened")
	}
}

func TestStaticProductPinnedMathFontUsesActualStaticWeight(t *testing.T) {
	r, p := staticMetadataFixture(t, "fr")
	var lock struct {
		Fonts []struct {
			Family, Format, Weight, Style string
			Font                          struct {
				SHA256 string `json:"sha256"`
			} `json:"font"`
			License struct {
				SHA256 string `json:"sha256"`
			} `json:"license"`
		}
	}
	if json.Unmarshal(fontAssets, &lock) != nil {
		t.Fatal("font lock")
	}
	found := false
	for _, item := range lock.Fonts {
		if item.Family != "Noto Sans Math" {
			continue
		}
		found = true
		p.Fonts = []Font{{SHA256: item.Font.SHA256, LicenseSHA256: item.License.SHA256, Family: item.Family, Format: item.Format, Weight: item.Weight, Style: item.Style}}
		if item.Weight != "400" || r.validate(p, false) != nil {
			t.Fatal("pinned static font rejected")
		}
		for _, weight := range []string{"100 900", "bold", "400; src: url(https://untrusted.invalid)", ""} {
			p.Fonts[0].Weight = weight
			if r.validate(p, false) == nil {
				t.Fatal("mismatched pinned font weight accepted", weight)
			}
		}
	}
	if !found {
		t.Fatal("original math font missing")
	}
}

func TestStaticProductUnsupportedIntlDoesNotPretendSupportOrRelaxLegacy(t *testing.T) {
	r, p := staticMetadataFixture(t, "aa")
	if p.Formatting.PluralRulesSupported || p.Formatting.NumberFormatSupported || p.Formatting.DateFormatSupported {
		t.Fatal("test must retain actual unsupported flags")
	}
	if r.validate(p, false) != nil {
		t.Fatal("explicit static other fallback refused")
	}
	for k, v := range p.Plurals {
		v.Forms["one"] = v.Forms["other"]
		p.Plurals[k] = v
	}
	p.Formatting.PluralCategories = []string{"one", "other"}
	if r.validate(p, false) == nil {
		t.Fatal("invented unsupported plural categories accepted")
	}
	seed := *r.locales["zh-CN"]
	seed.Formatting.PluralRulesSupported = false
	if r.validate(seed, true) == nil {
		t.Fatal("legacy seed Intl invariant weakened")
	}
	_, p = staticMetadataFixture(t, "zh-CN")
	if r.validate(p, true) == nil {
		t.Fatal("machine relabeled seed")
	}
}

func TestStaticProductUnknownAndMissingMachineJSONFieldsFailClosed(t *testing.T) {
	r, p := staticMetadataFixture(t, "fr")
	raw, _ := json.Marshal(p)
	var body map[string]any
	json.Unmarshal(raw, &body)
	for _, key := range []string{"runtime_translation", "manufacturing_sha256", "upstream_model_identity"} {
		t.Run(key, func(t *testing.T) {
			translation := body["translation"].(map[string]any)
			value := translation[key]
			delete(translation, key)
			changed, _ := json.Marshal(body)
			var q LocalePackage
			if strict.DecodeStrict(changed, &q) == nil && r.validate(q, false) == nil {
				t.Fatal("missing machine field accepted")
			}
			translation[key] = value
		})
	}
	body["translation"].(map[string]any)["api_key"] = "SYNTHETIC NEVER A CREDENTIAL"
	changed, _ := json.Marshal(body)
	var q LocalePackage
	if strict.DecodeStrict(changed, &q) == nil {
		t.Fatal("unknown secret-bearing field accepted")
	}
}

// The transport uses an explicitly substituted, test-only font lock. It proves
// immutable package, permission and labeling behavior, not font cmap correctness.
func staticTransportFixture(t *testing.T, locale string) string {
	t.Helper()
	font := []byte("EXPLICIT SYNTHETIC FONT TRANSPORT FIXTURE")
	license := []byte("SIL OPEN FONT LICENSE Version 1.1 EXPLICIT TEST FIXTURE")
	old := fontAssets
	lock := map[string]any{"fonts": []any{map[string]any{"family": "EXPLICIT FIXTURE", "format": "truetype", "weight": "100 900", "style": "normal", "font": map[string]any{"sha256": hash(font)}, "license": map[string]any{"sha256": hash(license)}}}}
	fontAssets, _ = json.Marshal(lock)
	t.Cleanup(func() { fontAssets = old })
	_, p := staticMetadataFixture(t, locale)
	raw, _ := json.Marshal(p)
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	assets := []Asset{{File: hash(font) + ".ttf", SHA256: hash(font), SizeBytes: len(font), MediaType: "font/ttf"}, {File: hash(license) + ".txt", SHA256: hash(license), SizeBytes: len(license), MediaType: "text/plain; charset=utf-8"}, {File: hash(raw) + ".json", SHA256: hash(raw), SizeBytes: len(raw), MediaType: "application/json"}}
	inventory := Inventory{SchemaVersion: 1, Kind: "projectone-offline-localization-inventory-v1", SourceSHA256: p.SourceSHA256, CatalogSHA256: p.CatalogSHA256, Locales: []LocaleFile{{Locale: locale, File: hash(raw) + ".json", SHA256: hash(raw)}}, Assets: assets, DeliveryStatus: "EXPLICIT SYNTHETIC TRANSPORT TEST ONLY"}
	inventoryRaw, _ := json.Marshal(inventory)
	for name, data := range map[string][]byte{"inventory.json": inventoryRaw, assets[0].File: font, assets[1].File: license, assets[2].File: raw} {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func TestStaticProductPrivateLoadAndExplicitChineseReplacementWithoutGoogleLabel(t *testing.T) {
	for _, locale := range []string{"fr", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			directory := staticTransportFixture(t, locale)
			r, err := New(config.LocalizationConfiguration{PackageDirectory: directory, RequiredLocales: []string{locale}}, config.DefaultLocalizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			p := r.locales[locale]
			if p.Translation.State != staticMachineState || p.GoogleTranslatedComplete || !reflect.DeepEqual(p.Translation.Review, map[string]string{"state": "pending"}) {
				t.Fatal("misleading provenance")
			}
			presentation, err := r.MailPresentation(locale)
			if err != nil || presentation.TranslationLabel != "" || presentation.TranslationLink != "" || len(presentation.TranslationBadge) != 0 {
				t.Fatal("machine incorrectly receives Google review attribution")
			}
			found := false
			for _, choice := range r.Catalog() {
				if choice.Locale == locale {
					found = true
					if choice.TranslationState != staticMachineState || choice.ReviewState != "pending" || choice.GoogleTranslatedComplete {
						t.Fatal("wrong public metadata")
					}
				}
			}
			if !found {
				t.Fatal("missing installed locale")
			}
			if r.locales["en"].Translation.State != "canonical_English_source" {
				t.Fatal("canonical source replaced")
			}
		})
	}
}

func TestStaticProductPrivateTransportStillRejectsIntegrityPermissionsAndAliasCollision(t *testing.T) {
	for _, failure := range []string{"file_hash", "file_permission", "inventory_catalog", "locale_alias", "duplicate_locale"} {
		t.Run(failure, func(t *testing.T) {
			directory := staticTransportFixture(t, "fr")
			raw, _ := os.ReadFile(filepath.Join(directory, "inventory.json"))
			var inv Inventory
			json.Unmarshal(raw, &inv)
			item := inv.Locales[0]
			switch failure {
			case "file_hash":
				os.WriteFile(filepath.Join(directory, item.File), []byte("{}"), 0600)
			case "file_permission":
				os.Chmod(filepath.Join(directory, item.File), 0644)
			case "inventory_catalog":
				inv.CatalogSHA256 = strings.Repeat("f", 64)
			case "duplicate_locale":
				inv.Locales = append(inv.Locales, item)
			case "locale_alias":
				raw, _ := os.ReadFile(filepath.Join(directory, item.File))
				var p LocalePackage
				json.Unmarshal(raw, &p)
				p.Aliases = append(p.Aliases, "en")
				raw, _ = json.Marshal(p)
				os.Remove(filepath.Join(directory, item.File))
				name := hash(raw) + ".json"
				os.WriteFile(filepath.Join(directory, name), raw, 0600)
				inv.Locales[0] = LocaleFile{Locale: "fr", File: name, SHA256: hash(raw)}
				for i := range inv.Assets {
					if inv.Assets[i].File == item.File {
						inv.Assets[i] = Asset{File: name, SHA256: hash(raw), SizeBytes: len(raw), MediaType: "application/json"}
					}
				}
			}
			raw, _ = json.Marshal(inv)
			os.WriteFile(filepath.Join(directory, "inventory.json"), raw, 0600)
			if _, err := New(config.LocalizationConfiguration{PackageDirectory: directory}, config.DefaultLocalizationPolicy()); err == nil {
				t.Fatal("invalid package accepted")
			}
		})
	}
}
