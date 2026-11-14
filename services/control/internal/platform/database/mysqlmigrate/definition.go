package mysqlmigrate

import (
	"regexp"
	"strings"
)

// MySQL preserves whether a column charset was explicit. A mysqldump roundtrip
// can render CHARACTER SET utf8mb4 before an identical binary COLLATE clause.
// Normalize only that declaration prefix; no literals,CHECK expressions,types,
// nullability,other charsets/collations or table options are rewritten.
var redundantUTF8ColumnCharset = regexp.MustCompile("(?m)^(  `[a-z][a-z0-9_]*` (?:varchar\\([0-9]+\\)|char\\([0-9]+\\)|tinytext|mediumtext|longtext|text)) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin([ ,].*)$")

func canonicalDefinition(definition string) string {
	return redundantUTF8ColumnCharset.ReplaceAllString(strings.TrimSpace(definition), "${1} COLLATE utf8mb4_0900_bin${2}")
}
