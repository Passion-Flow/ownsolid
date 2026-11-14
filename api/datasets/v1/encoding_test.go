package v1_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"testing"
)

func limits() wire.Limits {
	return wire.Limits{MaximumBytes: 1048576, MaximumRows: 100, MaximumLineBytes: 65536, MaximumTextBytes: 16384, MaximumSourceBytes: 512, MaximumErrors: 10}
}
func TestActualPythonCanonicalFixturesMatchUnicodeBytesAndContentHashes(t *testing.T) {
	raw, e := os.ReadFile("../../../tests/fixtures/datasets/canonical.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixtures []struct {
		Record        wire.Record `json:"record"`
		CanonicalHex  string      `json:"canonical_hex"`
		ContentSHA256 string      `json:"content_sha256"`
	}
	if e = json.Unmarshal(raw, &fixtures); e != nil {
		t.Fatal(e)
	}
	for _, fixture := range fixtures {
		b, e := fixture.Record.Canonical(limits())
		if e != nil || hex.EncodeToString(b) != fixture.CanonicalHex {
			t.Fatal("cross-language canonical Unicode bytes", e)
		}
		sha, e := fixture.Record.ContentDigest(limits())
		if e != nil || sha != fixture.ContentSHA256 {
			t.Fatal("cross-language exact content digest", e)
		}
	}
}
func TestUnpairedSurrogatesCannotBecomeReplacementCharacters(t *testing.T) {
	for _, body := range []string{`{"prompt":"\ud800"}`, `{"prompt":"\udc00"}`, `{"prompt":"\ud800x\udc00"}`, `{"prompt":"\ud800\u0041"}`} {
		if wire.ValidJSONUnicode([]byte(body)) {
			t.Fatal("unpaired surrogate accepted")
		}
	}
	for _, body := range []string{`{"prompt":"\ud83d\ude00"}`, `{"prompt":"literal \\ud800"}`, "{\"prompt\":\"\u4e2d\u6587 <>&\"}", `{"prompt":"escaped quote \"ok\""}`} {
		if !wire.ValidJSONUnicode([]byte(body)) {
			t.Fatal("valid Unicode/literal escape rejected")
		}
	}
}
func TestRecordLimitsAndNullControlPolicyDoNotNormalizeContent(t *testing.T) {
	for _, r := range []wire.Record{{SourceID: " s", Prompt: "p", Completion: "c"}, {SourceID: "s", Prompt: "\x00", Completion: "c"}, {SourceID: "s", Prompt: string([]byte{0xff}), Completion: "c"}} {
		if r.Validate(limits()) == nil {
			t.Fatal("unsafe record accepted")
		}
	}
	first := wire.Record{SourceID: "a", Prompt: "\u00e9", Completion: "c"}
	second := wire.Record{SourceID: "b", Prompt: "e\u0301", Completion: "c"}
	a, _ := first.ContentDigest(limits())
	b, _ := second.ContentDigest(limits())
	if a == b {
		t.Fatal("exact content silently normalized")
	}
}
