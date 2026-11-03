package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func wireRequest() ProcessorRequest {
	return ProcessorRequest{SchemaVersion: 1, Operation: InspectOperation, JobID: "11111111-1111-4111-8111-111111111111", AttemptID: "22222222-2222-4222-8222-222222222222", ScopeDirectory: "/private/store/attempt", InputFile: "/private/store/attempt/input.jsonl", OutputDirectory: "/private/store/attempt/output", InputSHA256: strings.Repeat("a", 64), Limits: Limits{MaximumBytes: 1048576, MaximumRows: 100, MaximumLineBytes: 65536, MaximumTextBytes: 16384, MaximumSourceBytes: 512, MaximumErrors: 10}, Resources: Resources{WallSeconds: 5, CpuSeconds: 3, MemoryBytes: 268435456, MaximumResultBytes: 1048576}}
}
func TestResultDecoderRejectsAmbiguousOrForeignPreparation(t *testing.T) {
	request := wireRequest()
	good := map[string]any{"schema_version": 1, "processor_version": ProcessorVersion, "job_id": request.JobID, "attempt_id": request.AttemptID, "operation": "inspect", "status": "valid", "source_sha256": request.InputSHA256, "source_bytes": 10, "memory_enforcement": "native_cpu_fsize_wall_input_output_bounds_parent_RSS_required", "total_rows": 1, "invalid_rows": 0, "errors_truncated": false, "fatal_source_error": false, "rows": []ProcessedRow{{1, "a", "p", "c"}}, "errors": []LineError{}}
	encoded, _ := json.Marshal(good)
	if _, e := DecodeResult(encoded, request); e != nil {
		t.Fatal(e)
	}
	cases := map[string][]byte{"duplicate": append(append([]byte{}, encoded[:len(encoded)-1]...), []byte(`,"status":"valid"}`)...), "null": []byte(strings.Replace(string(encoded), `"errors":[]`, `"errors":null`, 1)), "case": []byte(strings.Replace(string(encoded), `"status"`, `"Status"`, 1)), "surrogate": []byte(strings.Replace(string(encoded), `"prompt":"p"`, `"prompt":"\ud800"`, 1)), "foreign": []byte(strings.Replace(string(encoded), request.AttemptID, request.JobID, 1)), "extra": append(append([]byte{}, encoded[:len(encoded)-1]...), []byte(`,"unexpected":true}`)...), "incomplete": []byte(`{"status":"valid"}`)}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, e := DecodeResult(raw, request); e == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
	good["status"] = "prepared"
	encoded, _ = json.Marshal(good)
	if _, e := DecodeResult(encoded, request); e == nil {
		t.Fatal("inspect accepted publication")
	}
}
func TestRequestDecoderBoundsAndStrictRecordUnicode(t *testing.T) {
	request := wireRequest()
	if e := request.Validate(); e != nil {
		t.Fatal(e)
	}
	seed := strings.Repeat("1", 32)
	request.Seed = &seed
	if request.Validate() == nil {
		t.Fatal("inspect accepted split fields")
	}
	request.Seed = nil
	request.OutputDirectory = request.ScopeDirectory + "/../foreign"
	if request.Validate() == nil {
		t.Fatal("path traversal")
	}
	limits := wireRequest().Limits
	for _, raw := range []string{`{"source_id":"a","prompt":"p","completion":"c","prompt":"q"}`, `{"source_id":"a","Prompt":"p","completion":"c"}`, `{"source_id":"a","prompt":null,"completion":"c"}`, `{"source_id":"a","prompt":"\udc00","completion":"c"}`} {
		if _, e := DecodeRecord([]byte(raw), limits); e == nil {
			t.Fatal("invalid row accepted")
		}
	}
}
