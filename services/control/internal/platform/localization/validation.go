package localization

import (
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maximumMessageBytes = 65536

var placeholders = regexp.MustCompile(`\{\{\.[A-Za-z][A-Za-z0-9_]*\}\}|\{\{?[A-Za-z_][A-Za-z0-9_]*\}?\}`)
var templateActions = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
var htmlTags = regexp.MustCompile(`(?s)<[^>]*>`)
var fixedTerms = regexp.MustCompile(`\b(?:Project-one|Workspace|NVIDIA|CUDA|LoRA|pgvector|vLLM|OAuth2|JSONL|TypeScript|React)\b`)
var allowedCategories = map[string]bool{"zero": true, "one": true, "two": true, "few": true, "many": true, "other": true}

func frequencies(values []string) map[string]int {
	out := map[string]int{}
	for _, value := range values {
		out[value]++
	}
	return out
}
func sameTokens(pattern *regexp.Regexp, a, b string) bool {
	return reflect.DeepEqual(frequencies(pattern.FindAllString(a, -1)), frequencies(pattern.FindAllString(b, -1)))
}
func textValid(value string) bool {
	return value != "" && strings.TrimSpace(value) != "" && len(value) <= maximumMessageBytes && utf8.ValidString(value)
}
func labelValid(value string) bool {
	if !textValid(value) || len(value) > 600 {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func sameKeys[T any, U any](a map[string]T, b map[string]U) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if _, ok := b[key]; !ok {
			return false
		}
	}
	return true
}
func sameSeedText(a, b LocalePackage) bool {
	return a.SourceSHA256 == b.SourceSHA256 && a.Locale == b.Locale && a.Direction == b.Direction && reflect.DeepEqual(a.Aliases, b.Aliases) && reflect.DeepEqual(a.Messages, b.Messages) && reflect.DeepEqual(a.Mail, b.Mail) && reflect.DeepEqual(a.Plurals, b.Plurals) && reflect.DeepEqual(a.Translation, b.Translation) && b.Translation.Review["state"] == "pending" && !b.GoogleTranslatedComplete
}
func safeTemplate(value, source string) bool {
	if !textValid(value) || !sameTokens(placeholders, value, source) || !sameTokens(templateActions, value, source) {
		return false
	}
	remainder := templateActions.ReplaceAllString(value, "")
	return !strings.Contains(remainder, "{{") && !strings.Contains(remainder, "}}")
}
func safeHTML(value, source string) bool {
	tags := htmlTags.FindAllString(value, -1)
	expected := htmlTags.FindAllString(source, -1)
	if !reflect.DeepEqual(tags, expected) {
		return false
	}
	plain := htmlTags.ReplaceAllString(value, "")
	return !strings.ContainsAny(plain, "<>")
}
func (r *Registry) validate(p LocalePackage, seed bool) error {
	machine := !seed && staticMachine(p)
	terms := fixedTerms
	if machine {
		terms = staticProductTerms
	}
	if p.SchemaVersion != 1 || p.Kind != "projectone-offline-locale-v1" || !CanonicalLocale(p.Locale) || p.SourceSHA256 != r.source || !labelValid(p.DisplayName) || !labelValid(p.EnglishName) || (p.Direction != "ltr" && p.Direction != "rtl") || len(p.Aliases) < 1 || len(p.Aliases) > 4 || len(p.Messages) < 1 || len(p.Messages) > r.policy.MaximumMessages || len(p.Fonts) > r.policy.MaximumFonts {
		return unavailable()
	}
	ownAlias := false
	seen := map[string]bool{}
	for _, alias := range p.Aliases {
		if len(alias) < 2 || len(alias) > 35 || seen[alias] || strings.ContainsAny(alias, "/\\?# \t\r\n") {
			return unavailable()
		}
		seen[alias] = true
		if alias == p.Locale {
			ownAlias = true
		}
	}
	if !ownAlias || !machine && (!p.Formatting.PluralRulesSupported || !p.Formatting.NumberFormatSupported || !p.Formatting.DateFormatSupported) || len(p.Formatting.PluralCategories) < 1 || len(p.Formatting.PluralCategories) > 6 {
		return unavailable()
	}
	categories := map[string]bool{}
	for _, category := range p.Formatting.PluralCategories {
		if !allowedCategories[category] || categories[category] {
			return unavailable()
		}
		categories[category] = true
	}
	if !categories["other"] {
		return unavailable()
	}
	base := r.locales["en"]
	for key, text := range p.Messages {
		if !textValid(text) {
			return unavailable()
		}
		if !seed && base != nil {
			original, ok := base.Messages[key]
			if !ok || !sameTokens(placeholders, text, original) || !sameTokens(terms, text, original) {
				return unavailable()
			}
		}
	}
	if base != nil && (!sameKeys(p.Messages, base.Messages) || !sameKeys(p.Mail, base.Mail) || !sameKeys(p.Plurals, base.Plurals)) {
		return unavailable()
	}
	if len(p.Mail) != 3 || len(p.Plurals) < 1 {
		return unavailable()
	}
	for _, purpose := range []string{"invitation", "password_reset", "password_changed"} {
		current, ok := p.Mail[purpose]
		if !ok || !textValid(current.Subject) || strings.ContainsAny(current.Subject, "\r\n") || !textValid(current.Text) || !textValid(current.HTML) {
			return unavailable()
		}
		if !seed && base != nil {
			original := base.Mail[purpose]
			if !safeTemplate(current.Subject, original.Subject) || !safeTemplate(current.Text, original.Text) || !safeTemplate(current.HTML, original.HTML) || !safeHTML(current.HTML, original.HTML) {
				return unavailable()
			}
		}
	}
	for _, plural := range p.Plurals {
		if plural.Argument != "count" || len(plural.Forms) != len(categories) {
			return unavailable()
		}
		for category, text := range plural.Forms {
			if !categories[category] || !textValid(text) || !reflect.DeepEqual(placeholders.FindAllString(text, -1), []string{"{count}"}) {
				return unavailable()
			}
		}
	}
	fontSeen := map[string]bool{}
	for _, font := range p.Fonts {
		if !digestPattern.MatchString(font.SHA256) || !digestPattern.MatchString(font.LicenseSHA256) || fontSeen[font.SHA256] || !labelValid(font.Family) || font.Format != "truetype" || (font.Weight != "100 900" && font.Weight != "400") || font.Style != "normal" {
			return unavailable()
		}
		if !knownFont(font) {
			return unavailable()
		}
		fontSeen[font.SHA256] = true
	}
	if seed {
		if p.Locale != "en" && p.Locale != "zh-CN" || p.GoogleTranslatedComplete || p.Translation.Review["state"] != "pending" || staticMachine(p) || hasMachineMetadata(p.Translation) {
			return unavailable()
		}
		return nil
	}
	if machine {
		return validateStaticMachine(p)
	}
	if hasMachineMetadata(p.Translation) {
		return unavailable()
	}
	if p.Translation.Model != "general/nmt" || !digestPattern.MatchString(p.Translation.ModelResourceSHA256) || !p.GoogleTranslatedComplete || p.Translation.State != "google_generated_human_reviewed" || p.Translation.Provider != "Google Translate" || p.Translation.Review["state"] != "approved" || !digestPattern.MatchString(p.CatalogSHA256) || !digestPattern.MatchString(p.Translation.PlanSHA256) || !digestPattern.MatchString(p.Translation.CandidateSHA256) || !digestPattern.MatchString(p.Translation.ReviewedSHA256) || len(p.Fonts) < 1 || p.FontCoverage == nil || p.FontCoverage.Status != "complete_cmap_for_exact_product_strings_only" || len(p.FontCoverage.MissingCodepoints) != 0 || p.FontCoverage.RequiredCodepoints < 1 || p.FontCoverage.RequiredCodepoints != p.FontCoverage.CoveredRequiredCodepoints {
		return unavailable()
	}
	if len(p.Translation.Review) != 5 {
		return unavailable()
	}
	for _, key := range []string{"review_sha256", "reference_sha256", "reviewer_sha256"} {
		if !digestPattern.MatchString(p.Translation.Review[key]) {
			return unavailable()
		}
	}
	attribution := p.Translation.Attribution
	if attribution == nil || attribution.Link != "https://translate.google.com/" || attribution.DocumentationURL != "https://docs.cloud.google.com/translate/attribution" || len(attribution.Badges) != 2 {
		return unavailable()
	}
	if _, e := time.Parse(time.RFC3339, p.Translation.Review["reviewed_at"]); e != nil {
		return unavailable()
	}
	seenBadges := map[string]bool{}
	for _, badge := range attribution.Badges {
		if seenBadges[badge.Variant] || attributionBadgeDigests[badge.Variant] != badge.SHA256 || !digestPattern.MatchString(badge.SHA256) || r.blobs[badge.SHA256].mediaType != "image/png" {
			return unavailable()
		}
		seenBadges[badge.Variant] = true
	}
	return nil
}
