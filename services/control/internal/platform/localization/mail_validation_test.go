package localization

import "testing"

// Mirror the static manufacturer controls at the existing runtime boundary.
// These are synthetic template fixtures, not translation or delivery evidence.
func TestStaticProductMailManufacturingAdmissionParity(t *testing.T) {
	cases := []struct {
		name       string
		source     string
		translated string
		html       bool
		valid      bool
	}{
		{"added action", "Open {{.Link}}", `Ouvrir {{.Link}} {{printf "added"}}`, false, false},
		{"added comment", "Open {{.Link}}", "Ouvrir {{.Link}} {{/* added comment */}}", false, false},
		{"multiline action", "Open {{.Link}}", "Ouvrir {{.Link}} {{printf\n\"added\"}}", false, false},
		{"trimmed comment", "Open {{.Link}}", "Ouvrir {{.Link}} {{- /* added comment */ -}}", false, false},
		{"unclosed action", "Open {{.Link}}", "Ouvrir {{.Link}} {{", false, false},
		{"unmatched close", "Open {{.Link}}", "Ouvrir {{.Link}} }}", false, false},
		{"bare html opening angle", "<p>Open {{.Link}} {{.Name}}</p>", "<p>Ouvrir {{.Link}} {{.Name}}</p> <", true, false},
		{"bare html closing angle", "<p>Open {{.Link}} {{.Name}}</p>", "<p>Ouvrir {{.Link}} {{.Name}} > 5</p>", true, false},
		{"encoded angles and reordered actions", "<p>Open {{.Link}} {{.Name}}</p>", "<p>{{.Name}} \u0627\u0641\u062a\u062d {{.Link}} &lt; 5 &gt; 1</p>", true, true},
		{"plain text angles", "Open {{.Link}} < 5 > 1", "Ouvrir {{.Link}} < 5 > 1", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			valid := safeTemplate(tc.translated, tc.source) && (!tc.html || safeHTML(tc.translated, tc.source))
			if valid != tc.valid {
				t.Fatalf("mail admission = %v, want %v", valid, tc.valid)
			}
		})
	}
}
