package security

import (
	"strings"
	"testing"
)

func TestM01EmailCanonicalBoundaries(t *testing.T) {
	long := strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	for _, v := range []struct{ name, raw, canonical, display string }{
		{"case_edges", " \t\r\nAlias@EXAMPLE.invalid \t\r\n", "alias@example.invalid", "Alias@EXAMPLE.invalid"},
		{"base", "Alias@example.invalid", "alias@example.invalid", "Alias@example.invalid"},
		{"dots", "Al.Ias@example.invalid", "al.ias@example.invalid", "Al.Ias@example.invalid"},
		{"tag", "Alias+tag@example.invalid", "alias+tag@example.invalid", "Alias+tag@example.invalid"},
		{"dot_tag", "Al.Ias+tag@example.invalid", "al.ias+tag@example.invalid", "Al.Ias+tag@example.invalid"},
		{"punctuation", "A!#$%&'*+/=?^_`{|}~-@example.invalid", "a!#$%&'*+/=?^_`{|}~-@example.invalid", "A!#$%&'*+/=?^_`{|}~-@example.invalid"},
		{"punycode", "Alias@XN--BCHER-KVA.invalid", "alias@xn--bcher-kva.invalid", "Alias@XN--BCHER-KVA.invalid"},
		{"canonical_254_local_64_labels_63", long, long, long},
		{"wire_320", strings.Repeat(" ", 320-len("a@example.invalid")) + "a@example.invalid", "a@example.invalid", "a@example.invalid"},
	} {
		t.Run(v.name, func(t *testing.T) {
			canonical, display, e := Email(v.raw)
			if e != nil || canonical != v.canonical || display != v.display {
				t.Fatal("EMAIL_SUPPORTED_CANONICAL_DISPLAY_MISMATCH")
			}
		})
	}
	for _, v := range []struct{ name, raw string }{
		{"wire_321", strings.Repeat(" ", 321-len("a@example.invalid")) + "a@example.invalid"},
		{"canonical_255", long + "d"},
		{"local_65", strings.Repeat("a", 65) + "@example.invalid"},
		{"label_64", "a@" + strings.Repeat("a", 64) + ".invalid"},
	} {
		t.Run(v.name, func(t *testing.T) {
			canonical, display, e := Email(v.raw)
			if e == nil || e.Error() != "VALIDATION_FAILED" || canonical != "" || display != "" {
				t.Fatal("EMAIL_INVALID_BOUNDARY_ACCEPTED")
			}
		})
	}
}

func TestM01EmailUnsupportedBeforeCaseFold(t *testing.T) {
	for _, v := range []struct{ name, raw string }{
		{"kelvin_local", "\u212Aate@example.invalid"},
		{"dotted_i_local", "\u0130@example.invalid"},
		{"kelvin_domain", "a@\u212A.example.invalid"},
		{"dotted_i_domain", "a@\u0130.example.invalid"},
		{"eai", "\u7528\u6237@example.invalid"},
		{"unicode_domain", "a@b\u00fccher.invalid"},
		{"unicode_edge", "\u00a0a@example.invalid"},
		{"invalid_utf8", "a\xff@example.invalid"},
		{"quoted", "\"a\"@example.invalid"},
		{"comment", "a(comment)@example.invalid"},
		{"internal_crlf", "a\r\nb@example.invalid"},
		{"internal_space", "a b@example.invalid"},
		{"repeated_dot", "a..b@example.invalid"},
		{"leading_dot", ".a@example.invalid"},
		{"trailing_dot", "a.@example.invalid"},
		{"single_domain", "a@localhost"},
		{"hyphen_domain", "a@-example.invalid"},
	} {
		t.Run(v.name, func(t *testing.T) {
			canonical, display, e := Email(v.raw)
			if e == nil || e.Error() != "VALIDATION_FAILED" || canonical != "" || display != "" {
				t.Fatal("EMAIL_UNSUPPORTED_SYNTAX_ACCEPTED")
			}
		})
	}
}
