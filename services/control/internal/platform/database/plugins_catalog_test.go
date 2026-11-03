package database

import (
	"regexp"
	"strings"
	"testing"
)

func TestM14CatalogPreservesExactM13PrefixAndSchema2(t *testing.T) {
	old, e := orderedProfile("workflows")
	if e != nil || len(old) != 19 {
		t.Fatal(e)
	}
	next, e := orderedProfile("plugins")
	if e != nil || len(next) != 20 {
		t.Fatal(e)
	}
	for i, m := range old {
		if next[i].Checksum != m.Checksum || string(next[i].SQL) != string(m.SQL) {
			t.Fatal("applied prefix changed", i)
		}
	}
	body := ""
	for _, m := range next {
		body += string(m.SQL)
	}
	if n := len(regexp.MustCompile(`(?m)^CREATE TABLE(?: IF NOT EXISTS)? [a-z]`).FindAllString(body, -1)) + 1; n != 84 {
		t.Fatal("exact catalog", n)
	}
	delta := string(next[19].SQL)
	if len(regexp.MustCompile(`(?m)^CREATE TABLE `).FindAllString(delta, -1)) != 4 {
		t.Fatal("six workflow owners required")
	}
	for _, value := range []string{"plugin_provenance_immutable", "workflow_plugin_reference_immutable", "result_kind", "tool_proof", "workflow_supported_draft", "workflow_supported_version", "workflow_result_graph_kind"} {
		if !strings.Contains(delta, value) {
			t.Fatal("missing contract", value)
		}
	}
	for _, value := range []string{"CREATE TABLE notification", "CREATE TABLE application", "ALTER TABLE sessions", "DROP TABLE"} {
		if strings.Contains(delta, value) {
			t.Fatal("future owner", value)
		}
	}
}
