package mysqlmigrate

import (
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func TestMySQLStatementBoundaryRetainsNativeRegistrySource(t *testing.T) {
	for _, mode := range strings.Split(mysqlstore.SessionSQLModes, ",") {
		if mode == "ANSI_QUOTES" || mode == "NO_BACKSLASH_ESCAPES" {
			t.Fatal("statement boundary must be reconciled with changed session quoting", mode)
		}
	}
	raw, e := assets.Files.ReadFile("000248_image_registry_grants.sql")
	if e != nil || hash(string(raw)) != "e855e2cb9c41b0a0b9c2292eb1e39122c265c415710fa5892beca7262ab3bb0c" {
		t.Fatal("measured registry source changed", e)
	}
	if !singleStatementTerminator(string(raw)) {
		t.Fatal("ordinary source comment semicolon is not a second statement")
	}
}

func TestMySQLStatementBoundaryRejectsExtraExecutionAndAmbiguousComments(t *testing.T) {
	for _, fixture := range []struct {
		name, source string
		valid        bool
	}{
		{"plain", "SELECT 1;", true},
		{"line_comment", "SELECT -- ordinary; comment\n1;", true},
		{"hash_comment", "SELECT # ordinary; comment\n1;", true},
		{"block_comment", "SELECT /* ordinary; comment */ 1;", true},
		{"multiline_comment", "SELECT /* ordinary;\n'quoted; text'\n*/ 1;", true},
		{"single_quote", "SELECT 'ordinary; -- # /* text';", true},
		{"double_quote", `SELECT "ordinary; -- # /* text";`, true},
		{"doubled_quote", "SELECT 'it''s; ordinary';", true},
		{"escaped_quote", `SELECT 'it\'s; ordinary';`, true},
		{"escaped_double_quote", `SELECT "it\"s; ordinary";`, true},
		{"quoted_identifier", "SELECT `name;with``quote`;", true},
		{"two_statements", "SELECT 1;SELECT 2;", false},
		{"extra_terminator", "SELECT 1;;", false},
		{"trailing_line_comment", "SELECT 1; -- ordinary comment", false},
		{"trailing_block_comment", "SELECT 1; /* ordinary comment */", false},
		{"trailing_tokens", "SELECT 1; SELECT 2", false},
		{"comment_hides_second", "SELECT 1; -- hidden;\nSELECT 2;", false},
		{"block_hides_second", "SELECT 1; /* hidden; */ SELECT 2;", false},
		{"dash_requires_whitespace", "SELECT 1--not_a_comment;SELECT 2;", false},
		{"executable_comment", "SELECT /*!80400 1;SELECT 2 */ 3;", false},
		{"optimizer_hint", "SELECT /*+ SET_VAR(sql_mode='') */ 1;", false},
		{"nested_comment", "SELECT /* outer /* inner */ 1;", false},
		{"unterminated_block", "SELECT /* ordinary;", false},
		{"unterminated_line", "SELECT 1 -- ordinary;", false},
		{"unterminated_quote", "SELECT 'ordinary;", false},
		{"unterminated_identifier", "SELECT `ordinary;", false},
		{"unterminated_escape", "SELECT 'ordinary\\", false},
		{"nul", "SELECT 1\x00;", false},
		{"missing_terminator", "SELECT 1", false},
		{"empty", "", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			if actual := singleStatementTerminator(fixture.source); actual != fixture.valid {
				t.Fatalf("statement boundary got %v, want %v", actual, fixture.valid)
			}
		})
	}
}
