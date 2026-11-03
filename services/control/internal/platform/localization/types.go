// Package localization owns immutable offline product resources, never user content.
package localization

type Plural struct {
	Argument string            `json:"argument"`
	Forms    map[string]string `json:"forms"`
}
type MailTemplate struct {
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}
type Formatting struct {
	PluralCategories      []string `json:"plural_categories"`
	PluralRulesSupported  bool     `json:"plural_rules_supported"`
	NumberFormatSupported bool     `json:"number_format_supported"`
	DateFormatSupported   bool     `json:"date_format_supported"`
	GoogleCode            string   `json:"google_code,omitempty"`
	Locale                string   `json:"locale,omitempty"`
	LikelyScript          string   `json:"likely_script,omitempty"`
	Direction             string   `json:"direction,omitempty"`
	ResolvedPluralLocale  string   `json:"resolved_plural_locale,omitempty"`
	ResolvedNumberLocale  string   `json:"resolved_number_locale,omitempty"`
	ResolvedDateLocale    string   `json:"resolved_date_locale,omitempty"`
	MetadataStatus        string   `json:"metadata_status,omitempty"`
}
type Font struct {
	SHA256        string `json:"sha256"`
	LicenseSHA256 string `json:"license_sha256"`
	Family        string `json:"family"`
	Format        string `json:"format"`
	Weight        string `json:"weight"`
	Style         string `json:"style"`
}
type FontCoverage struct {
	RequiredCodepoints        int      `json:"required_codepoints"`
	CoveredRequiredCodepoints int      `json:"covered_required_codepoints"`
	MissingCodepoints         []string `json:"missing_codepoints"`
	Status                    string   `json:"status"`
	Qualification             string   `json:"qualification"`
}
type Badge struct {
	Variant string `json:"variant"`
	SHA256  string `json:"sha256"`
}
type Attribution struct {
	Link             string  `json:"link"`
	Badges           []Badge `json:"badges"`
	DocumentationURL string  `json:"documentation_url"`
}
type Translation struct {
	ManufacturingSHA256   string            `json:"manufacturing_sha256,omitempty"`
	RuntimeTranslation    *bool             `json:"runtime_translation,omitempty"`
	UpstreamModelIdentity string            `json:"upstream_model_identity,omitempty"`
	ModelResourceSHA256   string            `json:"model_resource_sha256,omitempty"`
	State                 string            `json:"state"`
	Review                map[string]string `json:"review"`
	Provider              string            `json:"provider,omitempty"`
	Model                 string            `json:"model,omitempty"`
	PlanSHA256            string            `json:"plan_sha256,omitempty"`
	CandidateSHA256       string            `json:"candidate_sha256,omitempty"`
	ReviewedSHA256        string            `json:"reviewed_sha256,omitempty"`
	Attribution           *Attribution      `json:"attribution,omitempty"`
}
type LocalePackage struct {
	SchemaVersion            int                     `json:"schema_version"`
	Kind                     string                  `json:"kind"`
	Locale                   string                  `json:"locale"`
	DisplayName              string                  `json:"display_name"`
	EnglishName              string                  `json:"english_name"`
	Aliases                  []string                `json:"aliases"`
	Direction                string                  `json:"direction"`
	SourceSHA256             string                  `json:"source_sha256"`
	CatalogSHA256            string                  `json:"catalog_sha256,omitempty"`
	Formatting               Formatting              `json:"formatting"`
	Messages                 map[string]string       `json:"messages"`
	Mail                     map[string]MailTemplate `json:"mail"`
	Plurals                  map[string]Plural       `json:"plurals"`
	Fonts                    []Font                  `json:"fonts"`
	FontCoverage             *FontCoverage           `json:"font_coverage,omitempty"`
	Translation              Translation             `json:"translation"`
	GoogleTranslatedComplete bool                    `json:"google_translated_complete"`
}
type Inventory struct {
	SchemaVersion  int          `json:"schema_version"`
	Kind           string       `json:"kind"`
	SourceSHA256   string       `json:"source_sha256"`
	CatalogSHA256  string       `json:"catalog_sha256"`
	Locales        []LocaleFile `json:"locales"`
	Assets         []Asset      `json:"assets"`
	DeliveryStatus string       `json:"delivery_status"`
}
type LocaleFile struct {
	Locale string `json:"locale"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}
type Asset struct {
	File      string `json:"file"`
	SHA256    string `json:"sha256"`
	SizeBytes int    `json:"size_bytes"`
	MediaType string `json:"media_type"`
}
type Choice struct {
	Locale                   string     `json:"locale"`
	DisplayName              string     `json:"display_name"`
	EnglishName              string     `json:"english_name"`
	Aliases                  []string   `json:"aliases"`
	Direction                string     `json:"direction"`
	PackageSHA256            string     `json:"package_sha256"`
	PackageURL               string     `json:"package_url"`
	TranslationState         string     `json:"translation_state"`
	ReviewState              string     `json:"review_state"`
	GoogleTranslatedComplete bool       `json:"google_translated_complete"`
	Fonts                    []Font     `json:"fonts"`
	Formatting               Formatting `json:"formatting"`
}
