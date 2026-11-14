package releases

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseViewProtectedConfigurationClosedRequiredAndNondefault(t *testing.T) {
	c := Config{Directory: t.TempDir(), ScopeID: strings.Repeat("a", 32), ManifestSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), PublisherUID: 0, PageSize: 7, OperationSeconds: 2}
	name := filepath.Join(t.TempDir(), "release-view.json")
	write := func(raw []byte) {
		t.Helper()
		if e := os.WriteFile(name, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	raw, _ := json.Marshal(c)
	write(raw)
	if got, e := Load(name); e != nil || got != c {
		t.Fatal("nondefault protected configuration", e)
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		t.Fatal(e)
	}
	for key, original := range fields {
		for _, invalid := range []json.RawMessage{nil, []byte("null"), []byte("false")} {
			if invalid == nil {
				delete(fields, key)
			} else {
				fields[key] = invalid
			}
			bad, _ := json.Marshal(fields)
			write(bad)
			if _, e := Load(name); e == nil {
				t.Fatalf("missing/wrong required field accepted: %s", key)
			}
			fields[key] = original
		}
	}
	fields["unknown"] = []byte("1")
	bad, _ := json.Marshal(fields)
	write(bad)
	if _, e := Load(name); e == nil {
		t.Fatal("unknown field accepted")
	}
	write(append([]byte(`{"page_size":7,`), raw[1:]...))
	if _, e := Load(name); e == nil {
		t.Fatal("duplicate field accepted")
	}
	write([]byte(strings.Replace(string(raw), "publisher_uid", "Publisher_uid", 1)))
	if _, e := Load(name); e == nil {
		t.Fatal("case-alias policy field accepted")
	}
	write(raw)
	if e := os.Chmod(name, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := Load(name); e == nil {
		t.Fatal("public configuration accepted")
	}
}
