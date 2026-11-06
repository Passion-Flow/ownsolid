package pagination

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ListSearchParameter is the query parameter every list searches with.
const ListSearchParameter = "q"

// SearchTerm checks the text of a list search: between one and maximum characters of valid UTF-8,
// with no control characters.
func SearchTerm(value string, maximum int) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximum {
		return false
	}
	return strings.IndexFunc(value, unicode.IsControl) < 0
}

// ContainsPattern is the LIKE pattern for text anywhere in a value. Use it with ESCAPE '!'.
func ContainsPattern(value string) string {
	return "%" + EscapeLike(value) + "%"
}

// EscapeLike makes LIKE's wildcards match themselves under ESCAPE '!'.
func EscapeLike(value string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(value)
}
