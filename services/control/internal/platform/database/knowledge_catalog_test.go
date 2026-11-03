package database

import (
	"regexp"
	"strings"
	"testing"
)

func TestM12CatalogPreservesExactM11PrefixAndKnowledgeScope(t *testing.T) {
	old, e := orderedProfile("sso")
	if e != nil || len(old) != 17 {
		t.Fatal(e)
	}
	next, e := orderedProfile("knowledge")
	if e != nil || len(next) != 18 {
		t.Fatal(e)
	}
	for i, m := range old {
		if next[i].Checksum != m.Checksum || string(next[i].SQL) != string(m.SQL) {
			t.Fatal("applied migration changed", i)
		}
	}
	body := ""
	for _, m := range next {
		body += string(m.SQL)
	}
	tables := regexp.MustCompile(`(?m)^CREATE TABLE(?: IF NOT EXISTS)? ([a-z][a-z0-9_]*)`).FindAllStringSubmatch(body, -1)
	if len(tables) != 73 {
		t.Fatal("exact74-table catalog including ledger", len(tables)+1)
	}
	delta := string(next[17].SQL)
	if len(regexp.MustCompile(`(?m)^CREATE TABLE `).FindAllString(delta, -1)) != 7 || strings.Contains(delta, "ALTER TABLE sessions") {
		t.Fatal("duplicate source columns or unexpected table")
	}
	for _, required := range []string{"knowledge_bases", "knowledge_source_versions", "public.vector(384)", "DEFERRABLE INITIALLY DEFERRED", "KNOWLEDGE_APPROVED_VECTOR_EXTENSION_REQUIRED", "CREATE TRIGGER"} {
		if !strings.Contains(delta, required) {
			t.Fatal("missing scope/integrity boundary", required)
		}
	}
	for _, forbidden := range []string{"CREATE TABLE workflow", "CREATE TABLE plugin", "CREATE EXTENSION", "DROP TABLE"} {
		if strings.Contains(delta, forbidden) {
			t.Fatal("future owner or privilege mutation", forbidden)
		}
	}
}
