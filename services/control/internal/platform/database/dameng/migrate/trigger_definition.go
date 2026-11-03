package migrate

import (
	"reflect"
	"strings"
)

// Metadata comparison only. Tokens retain literal bytes, punctuation and token
// boundaries; no comments or extra clauses are erased. It never rewrites SQL.
func definitionTokens(s string) ([]string, bool) {
	var out []string
	for i := 0; i < len(s); {
		b := s[i]
		if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
			i++
			continue
		}
		if i+1 < len(s) && (s[i:i+2] == "--" || s[i:i+2] == "/*") {
			return nil, false
		}
		if b == '\'' || b == '"' {
			start := i
			i++
			closed := false
			for i < len(s) {
				if s[i] == b {
					if i+1 < len(s) && s[i+1] == b {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, false
			}
			token := s[start:i]
			if b == '"' && identifier.MatchString(token[1:len(token)-1]) {
				token = token[1 : len(token)-1]
			}
			out = append(out, token)
			continue
		}
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_' || b >= '0' && b <= '9' {
			start := i
			i++
			for i < len(s) {
				v := s[i]
				if !(v >= 'a' && v <= 'z' || v >= 'A' && v <= 'Z' || v >= '0' && v <= '9' || v == '_' || v == '$' || v == '#') {
					break
				}
				i++
			}
			out = append(out, strings.ToUpper(s[start:i]))
			continue
		}
		if !strings.ContainsRune(".,:;()+-*/%=<>!?|", rune(b)) {
			return nil, false
		}
		out = append(out, string(b))
		i++
	}
	return out, true
}
func triggerDefinitionMatches(actual, schema string, t trigger) bool {
	got, ok := definitionTokens(actual)
	if !ok {
		return false
	}
	// GET_DDL can include the selected schema and CREATE OR REPLACE. Enumerate
	// only these exact declaration variants. WHEN/UPDATE OF/REFERENCING clauses,
	// altered aliases, AUTHID, ordering and appended statements all remain unequal.
	for _, create := range []string{"CREATE TRIGGER ", "CREATE OR REPLACE TRIGGER "} {
		for _, name := range []string{quoted(t.Name), objectName(schema, t.Name)} {
			for _, table := range []string{quoted(t.Table), objectName(schema, t.Table)} {
				want := create + name + " " + strings.TrimSuffix(t.Timing, " EACH ROW") + " " + t.Event + " ON " + table + " FOR EACH ROW " + t.Body
				tokens, ok := definitionTokens(want)
				if ok && reflect.DeepEqual(got, tokens) {
					return true
				}
			}
		}
	}
	return false
}
func objectName(schema, name string) string { return quoted(schema) + "." + quoted(name) }
