package localization

import (
	"reflect"
	"regexp"
)

var scriptPattern = regexp.MustCompile(`^[A-Z][a-z]{3}$`)

// Workspace is a translatable product concept. Brands and protocol names remain
// exact terms; the historical Google-reviewed admission policy is unchanged.
var staticProductTerms = regexp.MustCompile(`\b(?:ownsolid|Project-one|NVIDIA|CUDA|LoRA|pgvector|vLLM|OAuth2|JSONL|TypeScript|React)\b`)

const staticMachineState = "machine_generated_structure_checked"
const staticMachineProvider = "Jayogo"
const unverifiedUpstreamModel = "not_independently_verified"
const pinnedFormattingStatus = "derived_from_pinned_Intl_not_human_or_font_review"

// Static manufacture is a separate admission path, never a Google or human
// review claim. The runtime consumes immutable local bytes and no API settings.
func staticMachine(p LocalePackage) bool {
	return p.Translation.State == staticMachineState
}

func hasMachineMetadata(t Translation) bool {
	return t.ManufacturingSHA256 != "" || t.RuntimeTranslation != nil || t.UpstreamModelIdentity != ""
}

func validFontCoverage(p LocalePackage) bool {
	return len(p.Fonts) > 0 && p.FontCoverage != nil &&
		p.FontCoverage.Status == "complete_cmap_for_exact_product_strings_only" &&
		len(p.FontCoverage.MissingCodepoints) == 0 &&
		p.FontCoverage.RequiredCodepoints > 0 &&
		p.FontCoverage.RequiredCodepoints == p.FontCoverage.CoveredRequiredCodepoints
}

func validStaticFormatting(p LocalePackage) bool {
	f := p.Formatting
	// Unsupported Intl data is retained honestly. The manufactured plural
	// resource has only its existing other fallback, not invented CLDR forms.
	if !f.PluralRulesSupported && !reflect.DeepEqual(f.PluralCategories, []string{"other"}) {
		return false
	}
	return f.Locale == p.Locale && f.Direction == p.Direction &&
		labelValid(f.GoogleCode) && len(f.GoogleCode) <= 35 &&
		scriptPattern.MatchString(f.LikelyScript) &&
		CanonicalLocale(f.ResolvedPluralLocale) &&
		CanonicalLocale(f.ResolvedNumberLocale) &&
		CanonicalLocale(f.ResolvedDateLocale) &&
		f.MetadataStatus == pinnedFormattingStatus
}

func validateStaticMachine(p LocalePackage) error {
	t := p.Translation
	if p.Locale == "en" || !staticMachine(p) ||
		t.Provider != staticMachineProvider || p.GoogleTranslatedComplete ||
		!reflect.DeepEqual(t.Review, map[string]string{"state": "pending"}) ||
		!digestPattern.MatchString(t.ManufacturingSHA256) ||
		t.PlanSHA256 != t.ManufacturingSHA256 ||
		!digestPattern.MatchString(t.CandidateSHA256) ||
		!digestPattern.MatchString(p.CatalogSHA256) ||
		t.RuntimeTranslation == nil || *t.RuntimeTranslation ||
		t.UpstreamModelIdentity != unverifiedUpstreamModel ||
		t.Model != "" || t.ModelResourceSHA256 != "" ||
		t.ReviewedSHA256 != "" || t.Attribution != nil ||
		!validFontCoverage(p) || !validStaticFormatting(p) {
		return unavailable()
	}
	return nil
}
