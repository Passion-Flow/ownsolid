package pagination

import (
	"strings"
	"testing"
)

func TestSearchTermBoundsTheText(t *testing.T) {
	for _, valid := range []string{"a", "gpu-a01", "\u5f20\u4e09", strings.Repeat("\u5f20", 8)} {
		if !SearchTerm(valid, 8) {
			t.Fatal(valid)
		}
	}
	for _, invalid := range []string{"", strings.Repeat("a", 9), "a\x00b", "a\nb", "\xff"} {
		if SearchTerm(invalid, 8) {
			t.Fatalf("%q", invalid)
		}
	}
}

func TestContainsPatternEscapesWildcards(t *testing.T) {
	if got := ContainsPattern("50%_a!"); got != "%50!%!_a!!%" {
		t.Fatal(got)
	}
}
