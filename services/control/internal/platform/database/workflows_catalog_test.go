package database

import (
	"regexp"
	"strings"
	"testing"
)

func TestM13CatalogPreservesExactM12PrefixAndSchema1(t *testing.T) {
	old, e := orderedProfile("knowledge")
	if e != nil || len(old) != 18 {
		t.Fatal(e)
	}
	next, e := orderedProfile("workflows")
	if e != nil || len(next) != 19 {
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
	if n := len(regexp.MustCompile(`(?m)^CREATE TABLE(?: IF NOT EXISTS)? [a-z]`).FindAllString(body, -1)) + 1; n != 80 {
		t.Fatal("exact catalog", n)
	}
	delta := string(next[18].SQL)
	if len(regexp.MustCompile(`(?m)^CREATE TABLE `).FindAllString(delta, -1)) != 6 {
		t.Fatal("six workflow owners required")
	}
	for _, s := range []string{"workflow_one_active_turn_idx", "workflow_publication_immutable", "workflow_run_identity_guard", "workflow_schema1_draft", "workflow_schema1_version", "workflow_schema1_run", "membership_authority uuid NOT NULL", "PRIMARY KEY(tenant_id,workspace_id,actor_id,idempotency_key)"} {
		if !strings.Contains(delta, s) {
			t.Fatal("missing contract", s)
		}
	}
	for _, s := range []string{"CREATE TABLE plugin", "CREATE TABLE notification", "result_kind", "tool_proof", "ALTER TABLE sessions", "DROP TABLE"} {
		if strings.Contains(delta, s) {
			t.Fatal("future owner", s)
		}
	}
}
