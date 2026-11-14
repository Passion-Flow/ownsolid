package localization

import (
	"encoding/json"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"strings"
	"testing"
)

func localizationArtifactSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	raw, e := os.ReadFile("../../../../../api/localization/v1/artifacts.schema.json")
	if e != nil {
		t.Fatal(e)
	}
	var document map[string]any
	if json.Unmarshal(raw, &document) != nil {
		t.Fatal("invalid artifact contract")
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	const id = "https://projectone.invalid/contracts/localization/artifacts-v1"
	if e = compiler.AddResource(id, document); e != nil {
		t.Fatal(e)
	}
	schema, e := compiler.Compile(id + "#/$defs/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return schema
}
func TestLocalizationArtifactBuildConfigurationExampleAndInvalidPolicies(t *testing.T) {
	raw, e := os.ReadFile("../../../config/examples/localization/build.example.json")
	if e != nil {
		t.Fatal(e)
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		t.Fatal("factory example shape")
	}
	schema := localizationArtifactSchema(t, "BuildConfiguration")
	if e = schema.Validate(value); e != nil {
		t.Fatal("example structural contract", e)
	}
	for _, change := range []struct {
		field string
		value any
	}{{"batch_items", false}, {"batch_items", 0}, {"request_timeout_seconds", 121}, {"target_locale", "en"}, {"unknown", true}, {"token_file", nil}} {
		previous, present := value[change.field]
		value[change.field] = change.value
		if schema.Validate(value) == nil {
			t.Fatal("invalid factory field accepted", change.field)
		}
		if present {
			value[change.field] = previous
		} else {
			delete(value, change.field)
		}
	}
}
func TestLocalizationArtifactRealSeedShapeRequiredFieldParityAndNoPrivateProvenance(t *testing.T) {
	r, e := Default()
	if e != nil {
		t.Fatal(e)
	}
	schema := localizationArtifactSchema(t, "Locale")
	for _, locale := range []string{"en", "zh-CN"} {
		value := *r.locales[locale]
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		var body map[string]any
		if json.Unmarshal(raw, &body) != nil {
			t.Fatal("invalid real seed")
		}
		if e = schema.Validate(body); e != nil {
			t.Fatal("real seed contract", e)
		}
		for _, name := range artifactRequiredFields[value.Kind] {
			saved := body[name]
			delete(body, name)
			changed, _ := json.Marshal(body)
			if requiredArtifactObject(changed, value.Kind) || schema.Validate(body) == nil {
				t.Fatal("missing field became runtime default", name)
			}
			body[name] = saved
		}
		value.Translation.Provider = "synthetic-private-project"
		if sameSeedText(*r.locales[locale], value) {
			t.Fatal("seed accepted substituted private provenance")
		}
	}
}
func TestLocalizationArtifactReviewBudgetAreOnlyShapesAndPublicModelNeverContainsPrivateProject(t *testing.T) {
	hash := strings.Repeat("0", 64)
	checks := map[string]any{}
	for _, key := range []string{"linguistic_quality", "terminology", "placeholders", "mail_links_and_templates", "plural_categories", "direction_and_long_labels", "font_rendering", "offline_browser_and_mail"} {
		checks[key] = false
	}
	review := map[string]any{"schema_version": 1, "kind": "projectone-human-localization-review-v1", "state": "pending", "source_sha256": hash, "plan_sha256": hash, "candidate_sha256": hash, "target_locale": "fr", "reviewer": "", "reference": "", "reviewed_at": "", "display_name": "", "checks": checks, "reviewed_entries": map[string]any{"ui.example": "EXPLICIT SHAPE FIXTURE"}}
	schema := localizationArtifactSchema(t, "HumanReview")
	if e := schema.Validate(review); e != nil {
		t.Fatal("pending draft shape", e)
	}
	review["state"] = "approved"
	if schema.Validate(review) == nil {
		t.Fatal("shape allowed empty approval")
	}
	budget := map[string]any{"schema_version": 1, "kind": "projectone-translation-budget-approval-v1", "approval_id": "11111111-1111-4111-8111-111111111111", "reference": "EXPLICIT TEST ONLY", "approver": "EXPLICIT SYNTHETIC IDENTITY", "created_at": "2026-01-01T00:00:00Z", "expires_at": "2026-01-02T00:00:00Z", "plan_sha256": hash, "source_sha256": hash, "project_id": "synthetic-project", "location": "global", "model": "general/nmt", "target_locale": "fr", "maximum_codepoints": 1, "maximum_cost_minor_units": 1, "price_per_million_codepoints_minor_units": 1, "currency": "USD", "shutdown_plan": "TEST ONLY; no provider call"}
	schema = localizationArtifactSchema(t, "BudgetApproval")
	if e := schema.Validate(budget); e != nil {
		t.Fatal("explicit budget shape", e)
	}
	budget["maximum_cost_minor_units"] = false
	if schema.Validate(budget) == nil {
		t.Fatal("false became numeric budget")
	}
	r, e := Default()
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(*r.locales["en"])
	var body map[string]any
	json.Unmarshal(raw, &body)
	body["translation"].(map[string]any)["model"] = "projects/private-maker/locations/global/models/general/nmt"
	if localizationArtifactSchema(t, "Locale").Validate(body) == nil {
		t.Fatal("private model resource exposed")
	}
}

func TestLocalizationArtifactExplicitMetadataFixturesRejectChangedBadgeAndInvalidReviewTime(t *testing.T) {
	// Metadata-only negative fixtures. They are never written as a locale or
	// counted as a provider result, human approval, French support or real font.
	r, e := New(config.LocalizationConfiguration{}, config.DefaultLocalizationPolicy())
	if e != nil {
		t.Fatal(e)
	}
	p := *r.locales["en"]
	p.Locale = "fr"
	p.Aliases = []string{"fr"}
	p.DisplayName = "EXPLICIT METADATA FIXTURE"
	p.EnglishName = p.DisplayName
	p.GoogleTranslatedComplete = true
	p.CatalogSHA256 = strings.Repeat("0", 64)
	var lock struct {
		Fonts []struct {
			Family, Format, Weight, Style string
			Font                          struct {
				SHA256 string `json:"sha256"`
			}
			License struct {
				SHA256 string `json:"sha256"`
			}
		}
	}
	if e = json.Unmarshal(fontAssets, &lock); e != nil || len(lock.Fonts) == 0 {
		t.Fatal("pinned font contract", e)
	}
	f := lock.Fonts[0]
	p.Fonts = []Font{{SHA256: f.Font.SHA256, LicenseSHA256: f.License.SHA256, Family: f.Family, Format: f.Format, Weight: f.Weight, Style: f.Style}}
	p.FontCoverage = &FontCoverage{Status: "complete_cmap_for_exact_product_strings_only", RequiredCodepoints: 1, CoveredRequiredCodepoints: 1, MissingCodepoints: []string{}}
	hash := strings.Repeat("0", 64)
	badges := []Badge{}
	for name, h := range attributionBadgeDigests {
		badges = append(badges, Badge{Variant: name, SHA256: h})
		r.blobs[h] = blob{bytes: []byte("EXPLICIT PNG METADATA TRANSPORT FIXTURE"), mediaType: "image/png"}
	}
	p.Translation = Translation{State: "google_generated_human_reviewed", Provider: "Google Translate", Model: "general/nmt", ModelResourceSHA256: hash, PlanSHA256: hash, CandidateSHA256: hash, ReviewedSHA256: hash, Review: map[string]string{"state": "approved", "review_sha256": hash, "reviewed_at": "2026-01-01T00:00:00Z", "reference_sha256": hash, "reviewer_sha256": hash}, Attribution: &Attribution{Link: "https://translate.google.com/", DocumentationURL: "https://docs.cloud.google.com/translate/attribution", Badges: badges}}
	if e = r.validate(p, false); e != nil {
		t.Fatal("explicit baseline metadata fixture", e)
	}
	bad := p
	copyAttribution := *p.Translation.Attribution
	copyAttribution.Badges = append([]Badge{}, badges...)
	copyAttribution.Badges[0].Variant = "unknown.png"
	bad.Translation.Attribution = &copyAttribution
	if r.validate(bad, false) == nil {
		t.Fatal("unknown badge variant accepted")
	}
	copyAttribution.Badges[0] = badges[1]
	if r.validate(bad, false) == nil {
		t.Fatal("duplicate badge accepted")
	}
	bad = p
	bad.Translation.Review = map[string]string{}
	for k, v := range p.Translation.Review {
		bad.Translation.Review[k] = v
	}
	bad.Translation.Review["reviewed_at"] = "not-a-time"
	if r.validate(bad, false) == nil {
		t.Fatal("invalid review timestamp accepted")
	}
}
