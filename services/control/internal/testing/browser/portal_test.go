package browser_test

import "testing"

func TestM15PortalBrowser(t *testing.T) {
	for _, toolOnly := range []bool{false, true} {
		mode := "mixed"
		if toolOnly {
			mode = "tool-only"
		}
		t.Run(mode, func(t *testing.T) {
			if toolOnly {
				t.Setenv("PROJECT_ONE_PLUGIN_BROWSER_TOOL_ONLY", "1")
			}
			for _, locale := range []string{"en", "zh-CN"} {
				t.Run(locale, func(t *testing.T) {
					t.Setenv("PROJECT_ONE_BROWSER_LOCALE", locale)
					testProductionBrowser(t, "portal/portal-flow.mjs", "portal-browser-"+mode+"-"+locale, true)
				})
			}
		})
	}
}
