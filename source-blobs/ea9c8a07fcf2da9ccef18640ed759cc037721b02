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

func TestThreeWayManifestCannotMixSplitVersions(t *testing.T) {
	request := wireRequest()
	request.Operation = PublishOperation
	seed := strings.Repeat("1", 32)
	validation := 5000
	request.Seed, request.ValidationBasisPoints = &seed, &validation
	request.TestBasisPoints = 4000
	if request.Validate() == nil {
		t.Fatal("three-way request accepted without the sealed grouping plan")
	}
	request.NearDuplicatePlanSHA256 = strings.Repeat("b", 64)
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	invalidRequest := request
	invalidRequest.TestBasisPoints++
	if invalidRequest.Validate() == nil {
		t.Fatal("held-out fractions exceed the combined ceiling")
	}
	invalidRequest = request
	invalidRequest.Operation = InspectOperation
	if invalidRequest.Validate() == nil {
		t.Fatal("inspection accepted a test split")
	}
	m := Manifest{
		SchemaVersion: 2, RowSchema: RowSchema, ProcessorVersion: ProcessorVersion, SplitAlgorithm: ThreeWaySplitAlgorithm,
		SourceSHA256: request.InputSHA256, Seed: seed, ValidationBasisPoints: validation, TestBasisPoints: 4000,
		TotalRows: 4, UniqueRows: 3, DuplicateRows: 1, SourceGroups: 3, ValidationGroups: 1, TestGroups: 1,
		TrainRows: 1, ValidationRows: 1, TestRows: 1,
		ManifestFile: Asset{Path: "manifest.json", SizeBytes: 1, SHA256: request.InputSHA256},
	}
	for _, name := range []string{"samples.jsonl", "train.jsonl", "validation.jsonl", "assignments.jsonl", "duplicates.jsonl", "test.jsonl"} {
		m.Files = append(m.Files, Asset{Path: name, SizeBytes: 1, SHA256: request.InputSHA256})
	}
	if err := m.Validate(request); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Manifest){
		"legacy schema":     func(m *Manifest) { m.SchemaVersion = 1 },
		"legacy ranking":    func(m *Manifest) { m.SplitAlgorithm = SplitAlgorithm },
		"missing asset":     func(m *Manifest) { m.Files = m.Files[:5] },
		"empty asset":       func(m *Manifest) { m.Files[5].SizeBytes = 0 },
		"zero test rows":    func(m *Manifest) { m.TestRows = 0 },
		"zero test groups":  func(m *Manifest) { m.TestGroups = 0 },
		"no training group": func(m *Manifest) { m.TestGroups = 2 },
		"ratio mismatch":    func(m *Manifest) { m.TestBasisPoints = 3000 },
		"unassigned row":    func(m *Manifest) { m.TotalRows++; m.UniqueRows++ },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			invalid := m
			invalid.Files = append([]Asset{}, m.Files...)
			change(&invalid)
			if invalid.Validate(request) == nil {
				t.Fatal("accepted a mixed or incomplete three-way manifest")
			}
		})
	}
	legacy := m
	legacy.SchemaVersion, legacy.SplitAlgorithm, legacy.TestBasisPoints = 1, SplitAlgorithm, 0
	legacy.TrainRows, legacy.TestRows, legacy.TestGroups = 2, 0, 0
	legacy.Files = legacy.Files[:5]
	request.TestBasisPoints = 0
	request.NearDuplicatePlanSHA256 = ""
	if err := legacy.Validate(request); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil || strings.Contains(string(encoded), `"test_`) {
		t.Fatalf("default manifest gained test metadata: %s, %v", encoded, err)
	}
}

func TestInspectionSourceRowFormatBindsProtectedReceipt(t *testing.T) {
	request := wireRequest()
	request.SourceRowFormat = SingleTurnMessagesV1
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	result := ProcessorResult{
		SchemaVersion: 1, ProcessorVersion: ProcessorVersion, JobID: request.JobID, AttemptID: request.AttemptID,
		Operation: request.Operation, Status: "failed", SourceRowFormat: request.SourceRowFormat,
		ErrorCode: "DATASET_MESSAGE_SEQUENCE_INVALID",
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeResult(raw, request); err != nil {
		t.Fatal(err)
	}
	withoutFormat := request
	withoutFormat.SourceRowFormat = ""
	if _, err := DecodeResult(raw, withoutFormat); err == nil {
		t.Fatal("default inspection accepted a receipt from the opted-in parser")
	}
	result.SourceRowFormat = ""
	raw, _ = json.Marshal(result)
	if _, err := DecodeResult(raw, request); err == nil {
		t.Fatal("opted-in inspection accepted a receipt without its source format")
	}
	request.SourceRowFormat = "preference"
	if request.Validate() == nil {
		t.Fatal("unsupported source shape reached the processor")
	}
	request.SourceRowFormat = SingleTurnMessagesV1
	request.Operation = PublishOperation
	seed := strings.Repeat("a", 32)
	validation := 2000
	request.Seed, request.ValidationBasisPoints = &seed, &validation
	if request.Validate() == nil {
		t.Fatal("published artifacts accepted a noncanonical input format")
	}
}
