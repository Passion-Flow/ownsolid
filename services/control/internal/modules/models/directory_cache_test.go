package models

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func directoryFixture(t *testing.T) ([]Version, []directoryEntry) {
	t.Helper()
	raw, e := json.MarshalIndent(agentwire.ApprovedModel(), "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	v := Version{ID: "00000000-0000-4000-8000-000000000001", WorkspaceID: "00000000-0000-4000-8000-000000000002", Name: "Synthetic \u4e16\u754c", ModelKey: "qwen3-0.6b", CatalogDigest: agentwire.ModelCatalogDigest(), Kind: "approved_base", Manifest: raw, State: "ready", CreatedAt: time.Now().UTC()}
	sum := sha256.Sum256(raw)
	header := v
	header.Manifest = nil
	return []Version{v}, []directoryEntry{{Version: header, ManifestSHA256: hex.EncodeToString(sum[:])}}
}
func TestDirectoryCacheExactSourceBinding(t *testing.T) {
	values, entries := directoryFixture(t)
	raw, e := encodeDirectory(values)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := decodeDirectory(raw)
	if e != nil || !bytes.Equal(decoded[0].Manifest, values[0].Manifest) || !matchesDirectory(decoded, entries, "tenant") {
		t.Fatal("raw manifest/hash lost", e)
	}
	for _, mutate := range []func(*Version){func(v *Version) { v.WorkspaceID = "foreign" }, func(v *Version) { v.ID = "foreign" }, func(v *Version) { v.Name = "stale" }, func(v *Version) { v.ModelKey = "foreign" }, func(v *Version) { v.CatalogDigest = "forged" }, func(v *Version) { v.State = "deleted" }, func(v *Version) { v.Kind = "merged_lora" }, func(v *Version) { v.CreatedAt = v.CreatedAt.Add(time.Second) }, func(v *Version) { v.Manifest = []byte(`{}`) }} {
		altered := append([]Version(nil), decoded...)
		mutate(&altered[0])
		if matchesDirectory(altered, entries, "tenant") {
			t.Fatal("untrusted cache accepted")
		}
	}
	if matchesDirectory(nil, nil, "tenant") || matchesDirectory([]Version{}, entries, "tenant") {
		t.Fatal("truncated page")
	}
	if !matchesDirectory([]Version{}, []directoryEntry{}, "tenant") {
		t.Fatal("empty directory")
	}
	for _, raw := range [][]byte{[]byte("null"), []byte("{}"), []byte(`[{"unknown":1}]`)} {
		if _, e := decodeDirectory(raw); e == nil {
			t.Fatal("malformed cache accepted")
		}
	}
}
