package datasets

import (
	"encoding/json"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"testing"
)

func TestMongoImportRequestStrictMapping(t *testing.T) {
	raw := []byte(`{"source_id":"source","prompt_field":"question","completion_field":"answer","conversion":"string","reason":"Import approved examples"}`)
	var input MongoDBRequest
	if e := wire.DecodeStrict(raw, &input); e != nil {
		t.Fatal(e)
	}
	if input.mapping().Prompt != "question" || input.mapping().Completion != "answer" {
		t.Fatal("mapping lost")
	}
	raw = append(raw[:len(raw)-1], []byte(`,"uri":"mongodb://attacker"}`)...)
	if wire.DecodeStrict(raw, &input) == nil {
		t.Fatal("request URI admitted")
	}
}
func TestMongoImportPrivateReceiptFieldsDoNotLeak(t *testing.T) {
	raw, e := json.Marshal(MongoDBImport{objectID: "private-object", attemptID: "private-attempt", intent: []byte("private-intent")})
	if e != nil {
		t.Fatal(e)
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		t.Fatal("bad output")
	}
	for _, key := range []string{"objectID", "object_id", "attempt_id", "intent"} {
		if _, ok := body[key]; ok {
			t.Fatal("private receipt field exposed")
		}
	}
}
