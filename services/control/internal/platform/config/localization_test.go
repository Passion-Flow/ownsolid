package config

import "testing"

func TestLocalizationConfigurationStrictFieldsNullDuplicatesAndTypes(t *testing.T) {
	for _, raw := range []string{"localization: null", "localization: {unknown: true}", "localization: {required_locales: null}", "localization: {required_locales: [null]}", "localization: {package_directory: /safe, package_directory: /other}", "runtime: {localization: null}", "runtime: {localization: {package_bytes: true}}", "runtime: {localization: {cache_seconds: null}}", "runtime: {localization: {Cache_seconds: 1}}"} {
		if validateLocalizationYAML([]byte(raw)) == nil {
			t.Fatal("ambiguous localization policy accepted")
		}
	}
}
func TestLocalizationPolicyDefaultNondefaultBoundsAndRequiredLocaleScopes(t *testing.T) {
	p := DefaultLocalizationPolicy()
	if p.Validate() != nil || p.PackageBytes != 1048576 || p.CacheSeconds != 3600 {
		t.Fatal("default policy")
	}
	p.PackageBytes = 4194304
	p.AggregateBytes = 268435456
	p.CacheSeconds = 0
	if p.Validate() != nil {
		t.Fatal("supported nondefault policy")
	}
	p.MaximumLocales = 1
	if p.Validate() == nil {
		t.Fatal("invalid lower bound")
	}
	p = DefaultLocalizationPolicy()
	p.FontBytes = 134217729
	if p.Validate() == nil {
		t.Fatal("font upper bound")
	}
	for _, c := range []LocalizationConfiguration{{PackageDirectory: "relative"}, {RequiredLocales: []string{"en", "en"}}, {RequiredLocales: []string{"../../other"}}} {
		if c.Validate() == nil {
			t.Fatal("invalid scope accepted")
		}
	}
}
