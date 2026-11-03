package database

import (
	"regexp"
	"strings"
	"testing"
)

func TestM15CatalogPreservesExactM14PrefixAndPortalOnly(t *testing.T) {
	old, e := orderedProfile("plugins")
	if e != nil || len(old) != 20 {
		t.Fatal(e)
	}
	next, e := orderedProfile("portal")
	if e != nil || len(next) != 21 {
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
	if n := len(regexp.MustCompile(`(?m)^CREATE TABLE(?: IF NOT EXISTS)? [a-z]`).FindAllString(body, -1)) + 1; n != 86 {
		t.Fatal("exact catalog", n)
	}
	delta := string(next[20].SQL)
	if len(regexp.MustCompile(`(?m)^CREATE TABLE `).FindAllString(delta, -1)) != 2 {
		t.Fatal("six workflow owners required")
	}
	for _, value := range []string{"user_preferences_revision", "user_preferences_initialize", "terminal_notification_provenance", "workflow_terminal_notification", "terminal_notification_immutable", "workflow_run_recipient_identity"} {
		if !strings.Contains(delta, value) {
			t.Fatal("missing contract", value)
		}
	}
	for _, value := range []string{"CREATE TABLE application", "ALTER TABLE sessions", "DROP TABLE"} {
		if strings.Contains(delta, value) {
			t.Fatal("future owner", value)
		}
	}
}
