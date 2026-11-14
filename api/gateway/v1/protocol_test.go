package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanonicalOutcomeMatchesRustNullOrderAndDigest(t *testing.T) {
	code := "gateway_interrupted"
	out := Outcome{RequestID: "00000000-0000-0000-0000-000000000001", Status: "incomplete", ErrorCode: &code}
	const expected = `{"request_id":"00000000-0000-0000-0000-000000000001","status":"incomplete","error_code":"gateway_interrupted","usage":null,"elapsed_milliseconds":0,"output_started":false}`
	b, e := out.Canonical()
	if e != nil || string(b) != expected {
		t.Fatalf("canonical wire mismatch: %v", e)
	}
	decoded, e := DecodeOutcome(b)
	if e != nil {
		t.Fatal(e)
	}
	first, _ := decoded.Digest()
	changed := decoded
	changed.OutputStarted = true
	second, _ := changed.Digest()
	if len(first) != 64 || first == second {
		t.Fatal("receipt hash not bound")
	}
}
func TestOutcomeRejectsDuplicateMissingNullUnknownAndFabricatedUsage(t *testing.T) {
	code := "gateway_interrupted"
	base := Outcome{RequestID: "00000000-0000-0000-0000-000000000001", Status: "incomplete", ErrorCode: &code}
	b, _ := base.Canonical()
	for _, bad := range []string{string(b) + `{}`, strings.Replace(string(b), `"output_started":false`, `"output_started":null`, 1), strings.Replace(string(b), `"elapsed_milliseconds":0,`, ``, 1), strings.Replace(string(b), `"usage":null`, `"usage":null,"usage":null`, 1), strings.Replace(string(b), `"usage":null`, `"usage":{"prompt_tokens":7,"completion_tokens":null,"total_tokens":7}`, 1), strings.Replace(string(b), `"output_started":false`, `"output_started":false,"prompt":"must not persist"`, 1)} {
		if _, e := DecodeOutcome([]byte(bad)); e == nil {
			t.Fatal("unsafe outcome accepted")
		}
	}
	base.Status = "succeeded"
	base.ErrorCode = nil
	if base.Validate() == nil {
		t.Fatal("missing actual usage accepted")
	}
	base.Usage = &Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}
	b, _ = json.Marshal(base)
	if _, e := DecodeOutcome(b); e != nil {
		t.Fatal(e)
	}
	base.Usage.TotalTokens = 10
	if base.Validate() == nil {
		t.Fatal("inconsistent token totals accepted")
	}
}
func TestAdmissionScopeAndOutputBounds(t *testing.T) {
	request := AdmissionRequest{RequestID: "00000000-0000-0000-0000-000000000001", APIKey: "synthetic-key", Model: "public-alias", MaximumOutputTokens: 16}
	if request.Validate() != nil {
		t.Fatal("valid subset rejected")
	}
	request.Model = "http://127.0.0.1"
	if request.Validate() == nil {
		t.Fatal("unsafe alias accepted")
	}
	request.Model = "public-alias"
	request.MaximumOutputTokens = 513
	if request.Validate() == nil {
		t.Fatal("unbounded output accepted")
	}
}

func TestExactMachineFieldsRejectCaseVariantsMissingBooleansDuplicatesAndNull(t *testing.T) {
	valid := `{"request_id":"00000000-0000-0000-0000-000000000001","api_key":"synthetic-key","model":"public-alias","maximum_output_tokens":16,"stream":false}`
	if _, e := DecodeAdmissionRequest([]byte(valid)); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(valid, `"api_key"`, `"API_KEY"`, 1), strings.Replace(valid, `,"stream":false`, ``, 1), strings.Replace(valid, `"stream":false`, `"stream":null`, 1), strings.Replace(valid, `"model":"public-alias"`, `"model":"public-alias","MODEL":"another-alias"`, 1), strings.Replace(valid, `"stream":false`, `"stream":false,"stream":true`, 1)} {
		if _, e := DecodeAdmissionRequest([]byte(bad)); e == nil {
			t.Fatal("noncanonical or ambiguous machine request accepted")
		}
	}
	for _, bad := range []string{`{"API_KEY":"synthetic"}`, `{"api_key":null}`, `{"api_key":"synthetic","api_key":"other"}`, `{"api_key":"synthetic","prompt":"must not reach Go"}`} {
		if _, e := DecodeModelsRequest([]byte(bad)); e == nil {
			t.Fatal("invalid model authorization fields accepted")
		}
	}
}
