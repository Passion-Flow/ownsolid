package database

import (
	"regexp"
	"strings"
	"testing"
)

func TestM11CatalogPreservesExactM10PrefixAndScopedIssuance(t *testing.T) {
	old, e := orderedProfile("lifecycle")
	if e != nil || len(old) != 16 {
		t.Fatal(e)
	}
	next, e := orderedProfile("sso")
	if e != nil || len(next) != 17 {
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
	if len(tables) != 66 {
		t.Fatal("exact67-table catalog including ledger", len(tables)+1)
	}
	delta := string(next[16].SQL)
	if len(regexp.MustCompile(`(?m)^CREATE TABLE `).FindAllString(delta, -1)) != 4 || strings.Contains(delta, "ADD COLUMN sso_") {
		t.Fatal("duplicate source columns or unexpected table")
	}
	for _, required := range []string{"DROP CONSTRAINT account_local_session_only", "sessions_sso_source_complete", "sessions_sso_binding_fk", "sessions_sso_flow_fk", "issued_session_id", "sessions_sso_single_issuance", "SSO_FLOW_ALREADY_ISSUED"} {
		if !strings.Contains(delta, required) {
			t.Fatal("missing scope/retention boundary", required)
		}
	}
	for _, forbidden := range []string{"CREATE TABLE knowledge_", "CREATE TABLE workflow", "CREATE TABLE plugin", "public.vector("} {
		if strings.Contains(body, forbidden) {
			t.Fatal("future owner", forbidden)
		}
	}
}
