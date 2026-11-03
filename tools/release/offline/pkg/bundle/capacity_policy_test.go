package bundle

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestVerificationFixedReserveMappingPreservesLegacyBytes(t *testing.T) {
	p := Defaults()
	raw, e := json.Marshal(p)
	if e != nil || bytes.Contains(raw, []byte("minimum_free_bytes")) {
		t.Fatal("legacy verification spelling changed", e)
	}
	p.MinimumFreeBytes = 32 << 30
	raw, e = json.Marshal(p)
	var reopened Verification
	if e != nil || json.Unmarshal(raw, &reopened) != nil {
		t.Fatal("protected verification roundtrip", e)
	}
	c, e := reopened.config()
	if e != nil || c.MinimumFreeBytes != 32<<30 {
		t.Fatal("bundle preparation dropped fixed reserve", e)
	}
	p.MinimumFreeBytes = -1
	if p.Validate() == nil {
		t.Fatal("invalid public reserve accepted")
	}
	for _, raw := range []string{`null`, `{"minimum_free_bytes":null}`, `{"Minimum_Free_Bytes":1}`, "{\"minimum_free_byte\u017f\":1}", `{"minimum_free_bytes":-1}`, `{"minimum_free_bytes":1125899906842625}`, `{"minimum_free_bytes":0,"minimum_free_bytes":1}`, `{"timeout_seconds":1}`, `{"allow_test_fixture":null}`} {
		v := Defaults()
		if json.Unmarshal([]byte(raw), &v) == nil {
			t.Fatal("invalid public protected input accepted", raw)
		}
	}
}
