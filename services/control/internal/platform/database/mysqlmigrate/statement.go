package mysqlmigrate

import "strings"

// singleStatementTerminator checks boundaries, not SQL syntax. The catalog still
// requires the exact owned source hash, statement kind and measured definition.
// Quoting follows mysqlstore's fixed session modes (backslash escapes enabled;
// ANSI_QUOTES disabled). Executable comments and optimizer hints are not admitted.
func singleStatementTerminator(statement string) bool {
	if statement == "" || strings.ContainsRune(statement, '\x00') {
		return false
	}
	var quote byte
	lineComment, blockComment, terminated := false, false, false
	for i := 0; i < len(statement); i++ {
		c := statement[i]
		if quote != 0 {
			if c == '\\' && quote != '`' {
				if i+1 == len(statement) {
					return false
				}
				i++
			} else if c == quote {
				if i+1 < len(statement) && statement[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if lineComment {
			if c == '\n' || c == '\r' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if c == '*' && i+1 < len(statement) && statement[i+1] == '/' {
				blockComment = false
				i++
			} else if c == '/' && i+1 < len(statement) && statement[i+1] == '*' {
				return false
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '#':
			lineComment = true
		case '-':
			if i+2 < len(statement) && statement[i+1] == '-' && (statement[i+2] <= ' ' || statement[i+2] == 0x7f) {
				lineComment = true
				i++
			}
		case '/':
			if i+1 < len(statement) && statement[i+1] == '*' {
				if i+2 == len(statement) || statement[i+2] == '!' || statement[i+2] == '+' {
					return false
				}
				blockComment = true
				i++
			}
		case ';':
			if i != len(statement)-1 {
				return false
			}
			terminated = true
		}
	}
	return terminated && quote == 0 && !lineComment && !blockComment
}
