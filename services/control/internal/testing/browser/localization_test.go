package browser_test

import "testing"

func TestM16LocalizationBrowser(t *testing.T) {
	testProductionBrowser(t, "localization/localization-flow.mjs", "localization-browser", true)
}

func TestM16InstalledStaticLocaleBrowser(t *testing.T) {
	testProductionBrowser(t, "localization/static-flow.mjs", "localization-static-browser", true)
}
