package v1

import (
	"strconv"
	"unicode/utf8"
)

// Reject malformed UTF-16 escapes before encoding/json can silently substitute
// U+FFFD. Syntax, closed fields and byte bounds remain the caller's authority.
func validJSONUnicode(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	inside := false
	unit := func(start int) (uint64, bool) {
		if start+6 > len(body) || body[start] != '\\' || body[start+1] != 'u' {
			return 0, false
		}
		value, e := strconv.ParseUint(string(body[start+2:start+6]), 16, 16)
		return value, e == nil
	}
	for i := 0; i < len(body); {
		if body[i] == '"' {
			inside = !inside
			i++
			continue
		}
		if inside && body[i] == '\\' {
			if i+1 >= len(body) {
				return false
			}
			if body[i+1] == 'u' {
				first, ok := unit(i)
				if !ok {
					return false
				}
				if first >= 0xD800 && first <= 0xDBFF {
					second, ok := unit(i + 6)
					if !ok || second < 0xDC00 || second > 0xDFFF {
						return false
					}
					i += 12
					continue
				}
				if first >= 0xDC00 && first <= 0xDFFF {
					return false
				}
				i += 6
				continue
			}
			i += 2
			continue
		}
		i++
	}
	return !inside
}
