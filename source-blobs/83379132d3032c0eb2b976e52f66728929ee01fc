package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const InspectOperation = "inspect"
const PublishOperation = "publish"
const MaxRequestBytes = 16384
const MaximumDiagnosticBytes = 16384

// NearDuplicatePlanFile is the protected publication plan in the attempt scope.
const NearDuplicatePlanFile = "near-duplicates.json"

// SourceRowFormat selects how the protected inspector reads uploaded JSONL rows.
type SourceRowFormat string

// SingleTurnMessagesV1 accepts one textual user turn followed by one assistant reply.
const SingleTurnMessagesV1 SourceRowFormat = "single_turn_messages_v1"

// Valid accepts the legacy default and the supported single-turn format.
func (f SourceRowFormat) Valid() bool { return f == "" || f == SingleTurnMessagesV1 }

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var seedPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func ValidateSplit(seed string, basisPoints int) error {
	if !seedPattern.MatchString(seed) || basisPoints < 100 || basisPoints > 5000 {
		return errors.New("DATASET_SPLIT_CONFIGURATION_INVALID")
	}
	return nil
}

// ValidateThreeWaySplit checks held-out group ratios for a three-way publication.
func ValidateThreeWaySplit(seed string, validation, test int) error {
	if ValidateSplit(seed, validation) != nil || test < 100 || test > 5000 || validation+test > 9000 {
		return errors.New("DATASET_SPLIT_CONFIGURATION_INVALID")
	}
	return nil
}

func ValidID(value string) bool {
	return idPattern.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}
func ValidDigest(value string) bool { return hashPattern.MatchString(value) }

type ProcessorRequest struct {
	SchemaVersion           int             `json:"schema_version"`
	Operation               string          `json:"operation"`
	JobID                   string          `json:"job_id"`
	AttemptID               string          `json:"attempt_id"`
	ScopeDirectory          string          `json:"scope_directory"`
	InputFile               string          `json:"input_file"`
	InputSHA256             string          `json:"input_sha256"`
	SourceRowFormat         SourceRowFormat `json:"source_row_format,omitempty"`
	OutputDirectory         string          `json:"output_directory"`
	Limits                  Limits          `json:"limits"`
	Resources               Resources       `json:"resources"`
	Seed                    *string         `json:"seed,omitempty"`
	ValidationBasisPoints   *int            `json:"validation_basis_points,omitempty"`
	TestBasisPoints         int             `json:"test_basis_points,omitempty"`
	NearDuplicatePlanSHA256 string          `json:"near_duplicate_plan_sha256,omitempty"`
}

func (r ProcessorRequest) Validate() error {
	if r.SchemaVersion != 1 || !ValidID(r.JobID) || !ValidID(r.AttemptID) || !ValidDigest(r.InputSHA256) || r.Limits.Validate() != nil || r.Resources.Validate() != nil || !r.SourceRowFormat.Valid() {
		return errors.New("DATASET_PROCESSOR_REQUEST_INVALID")
	}
	for _, path := range []string{r.ScopeDirectory, r.InputFile, r.OutputDirectory} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n") {
			return errors.New("DATASET_PROCESSOR_PATH_INVALID")
		}
	}
	inside := func(path string) bool { return strings.HasPrefix(path, r.ScopeDirectory+string(filepath.Separator)) }
	if r.ScopeDirectory == string(filepath.Separator) || !inside(r.InputFile) || !inside(r.OutputDirectory) || r.InputFile == r.OutputDirectory || strings.HasPrefix(r.InputFile, r.OutputDirectory+string(filepath.Separator)) {
		return errors.New("DATASET_PROCESSOR_PATH_INVALID")
	}
	switch r.Operation {
	case InspectOperation:
		if r.Seed != nil || r.ValidationBasisPoints != nil || r.TestBasisPoints != 0 || r.NearDuplicatePlanSHA256 != "" {
			return errors.New("DATASET_PROCESSOR_REQUEST_INVALID")
		}
	case PublishOperation:
		if r.SourceRowFormat != "" {
			return errors.New("DATASET_PROCESSOR_REQUEST_INVALID")
		}
		if r.NearDuplicatePlanSHA256 != "" && !ValidDigest(r.NearDuplicatePlanSHA256) {
			return errors.New("DATASET_PROCESSOR_REQUEST_INVALID")
		}
		if r.Seed == nil || r.ValidationBasisPoints == nil || ValidateSplit(*r.Seed, *r.ValidationBasisPoints) != nil {
			return errors.New("DATASET_SPLIT_CONFIGURATION_INVALID")
		}
		if r.TestBasisPoints != 0 {
			if err := ValidateThreeWaySplit(*r.Seed, *r.ValidationBasisPoints, r.TestBasisPoints); err != nil {
				return err
			}
			if r.NearDuplicatePlanSHA256 == "" {
				return errors.New("DATASET_PROCESSOR_REQUEST_INVALID")
			}
		}
	default:
		return errors.New("DATASET_PROCESSOR_REQUEST_INVALID")
	}
	return nil
}

type ProcessedRow struct {
	LineNumber int64  `json:"line_number"`
	SourceID   string `json:"source_id"`
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

func (r ProcessedRow) Record() Record {
	return Record{SourceID: r.SourceID, Prompt: r.Prompt, Completion: r.Completion}
}

type LineError struct {
	LineNumber      int64  `json:"line_number"`
	Code            string `json:"code"`
	Preview         string `json:"preview"`
	PreviewReplaced bool   `json:"preview_replaced"`
}
type Asset struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}
type Manifest struct {
	SchemaVersion         int     `json:"schema_version"`
	RowSchema             string  `json:"row_schema"`
	ProcessorVersion      string  `json:"processor_version"`
	SplitAlgorithm        string  `json:"split_algorithm"`
	SourceSHA256          string  `json:"source_sha256"`
	Seed                  string  `json:"seed"`
	ValidationBasisPoints int     `json:"validation_basis_points"`
	TestBasisPoints       int     `json:"test_basis_points,omitempty"`
	TotalRows             int64   `json:"total_rows"`
	UniqueRows            int64   `json:"unique_rows"`
	DuplicateRows         int64   `json:"duplicate_rows"`
	SourceGroups          int64   `json:"source_groups"`
	ValidationGroups      int64   `json:"validation_groups"`
	TestGroups            int64   `json:"test_groups,omitempty"`
	TrainRows             int64   `json:"train_rows"`
	ValidationRows        int64   `json:"validation_rows"`
	TestRows              int64   `json:"test_rows,omitempty"`
	Files                 []Asset `json:"files"`
	ManifestFile          Asset   `json:"manifest_file"`
}
type ProcessorResult struct {
	FatalSourceLineNumber *int64          `json:"fatal_source_line_number,omitempty"`
	SchemaVersion         int             `json:"schema_version"`
	ProcessorVersion      string          `json:"processor_version"`
	JobID                 string          `json:"job_id"`
	AttemptID             string          `json:"attempt_id"`
	Operation             string          `json:"operation"`
	Status                string          `json:"status"`
	SourceSHA256          string          `json:"source_sha256,omitempty"`
	SourceRowFormat       SourceRowFormat `json:"source_row_format,omitempty"`
	SourceBytes           int64           `json:"source_bytes,omitempty"`
	MemoryEnforcement     string          `json:"memory_enforcement,omitempty"`
	TotalRows             int64           `json:"total_rows,omitempty"`
	InvalidRows           int64           `json:"invalid_rows,omitempty"`
	ErrorsTruncated       bool            `json:"errors_truncated,omitempty"`
	FatalSourceError      bool            `json:"fatal_source_error,omitempty"`
	Rows                  []ProcessedRow  `json:"rows,omitempty"`
	Errors                []LineError     `json:"errors,omitempty"`
	Manifest              *Manifest       `json:"manifest,omitempty"`
	ErrorCode             string          `json:"error_code,omitempty"`
	ErrorLineNumber       *int64          `json:"error_line_number,omitempty"`
}

func invalidResult() error { return errors.New("DATASET_PROCESSOR_RESULT_INVALID") }

// DecodeStrict checks exact case-sensitive JSON fields, duplicate/null values and Unicode before decoding typed wire objects.
func DecodeStrict(raw []byte, out any) error {
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer || !utf8.Valid(raw) || !ValidJSONUnicode(raw) {
		return invalidResult()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if unique(d) != nil {
		return invalidResult()
	}
	if _, e := d.Token(); e != io.EOF {
		return invalidResult()
	}
	if strictKeys(raw, t.Elem()) != nil {
		return invalidResult()
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return invalidResult()
	}
	return nil
}
func strictKeys(value json.RawMessage, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	// The standard timestamp contract is a JSON string despite its Go struct
	// representation. Do not admit arbitrary custom unmarshallers here. The outer
	// syntax pass still rejects duplicate keys, null and invalid Unicode.
	if t == reflect.TypeFor[time.Time]() {
		var timestamp time.Time
		if json.Unmarshal(value, &timestamp) != nil {
			return invalidResult()
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(value, &fields) != nil || fields == nil {
			return invalidResult()
		}
		allowed := map[string]reflect.StructField{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				allowed[name] = field
				if !strings.Contains(field.Tag.Get("json"), ",omitempty") {
					if _, ok := fields[name]; !ok {
						return invalidResult()
					}
				}
			}
		}
		for name, raw := range fields {
			field, ok := allowed[name]
			if !ok {
				return invalidResult()
			}
			if e := strictKeys(raw, field.Type); e != nil {
				return e
			}
		}
	case reflect.Slice:
		var items []json.RawMessage
		if json.Unmarshal(value, &items) != nil {
			return invalidResult()
		}
		for _, item := range items {
			if e := strictKeys(item, t.Elem()); e != nil {
				return e
			}
		}
	}
	return nil
}
func unique(d *json.Decoder) error {
	token, e := d.Token()
	if e != nil || token == nil {
		return invalidResult()
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return invalidResult()
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return invalidResult()
			}
			seen[name] = true
			if e = unique(d); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = unique(d); e != nil {
				return e
			}
		}
	default:
		return invalidResult()
	}
	_, e = d.Token()
	return e
}
func DecodeResult(raw []byte, request ProcessorRequest) (ProcessorResult, error) {
	var out ProcessorResult
	if int64(len(raw)) > request.Resources.MaximumResultBytes || !utf8.Valid(raw) || !ValidJSONUnicode(raw) {
		return out, invalidResult()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if unique(d) != nil {
		return out, invalidResult()
	}
	if _, e := d.Token(); e != io.EOF {
		return out, invalidResult()
	}
	if strictKeys(raw, reflect.TypeFor[ProcessorResult]()) != nil {
		return out, invalidResult()
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil {
		return out, invalidResult()
	}
	if out.SchemaVersion != 1 || out.ProcessorVersion != ProcessorVersion || out.JobID != request.JobID || out.AttemptID != request.AttemptID || out.Operation != request.Operation || out.SourceRowFormat != request.SourceRowFormat {
		return out, invalidResult()
	}
	required := []string{"schema_version", "processor_version", "job_id", "attempt_id", "operation", "status"}
	if request.SourceRowFormat != "" {
		required = append(required, "source_row_format")
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	switch out.Status {
	case "failed":
		required = append(required, "error_code")
		if out.ErrorLineNumber != nil {
			required = append(required, "error_line_number")
			if *out.ErrorLineNumber < 0 || *out.ErrorLineNumber > request.Limits.MaximumRows+1 {
				return out, invalidResult()
			}
		}
		if !KnownError(out.ErrorCode) {
			return out, invalidResult()
		}
	case "valid", "invalid", "prepared":
		required = append(required, "source_sha256", "source_bytes", "memory_enforcement")
		if out.SourceSHA256 != request.InputSHA256 || out.SourceBytes < 0 || out.SourceBytes > request.Limits.MaximumBytes || out.MemoryEnforcement != "linux_rlimit_as_cpu_fsize_wall" && out.MemoryEnforcement != "native_cpu_fsize_wall_input_output_bounds_parent_RSS_required" {
			return out, invalidResult()
		}
		if request.Operation == InspectOperation && out.Status != "prepared" {
			required = append(required, "total_rows", "invalid_rows", "errors_truncated", "fatal_source_error", "rows", "errors")
			if out.FatalSourceError != (out.FatalSourceLineNumber != nil) {
				return out, invalidResult()
			}
			if out.FatalSourceLineNumber != nil {
				required = append(required, "fatal_source_line_number")
				if *out.FatalSourceLineNumber < 1 || *out.FatalSourceLineNumber > out.TotalRows {
					return out, invalidResult()
				}
			}
			if out.TotalRows < 0 || out.TotalRows > request.Limits.MaximumRows || out.InvalidRows < 0 || out.InvalidRows > out.TotalRows+1 || int64(len(out.Rows))+out.InvalidRows != out.TotalRows && (out.TotalRows != 0 || out.InvalidRows != 1) || int64(len(out.Errors)) > request.Limits.MaximumErrors || out.ErrorsTruncated != (out.InvalidRows > int64(len(out.Errors))) || out.Status == "valid" != (out.InvalidRows == 0 && out.TotalRows > 0) {
				return out, invalidResult()
			}
			seen := map[int64]bool{}
			for _, row := range out.Rows {
				if row.LineNumber < 1 || row.LineNumber > out.TotalRows || seen[row.LineNumber] || row.Record().Validate(request.Limits) != nil {
					return out, invalidResult()
				}
				seen[row.LineNumber] = true
			}
			for _, line := range out.Errors {
				if line.LineNumber < 0 || line.LineNumber > out.TotalRows || line.LineNumber == 0 && out.TotalRows != 0 || seen[line.LineNumber] || !KnownError(line.Code) || len(line.Preview) > 4096 {
					return out, invalidResult()
				}
				seen[line.LineNumber] = true
			}
		} else if request.Operation == PublishOperation && out.Status == "prepared" {
			required = append(required, "manifest")
			if out.Manifest == nil || out.Manifest.Validate(request) != nil {
				return out, invalidResult()
			}
		} else {
			return out, invalidResult()
		}
	default:
		return out, invalidResult()
	}
	if len(fields) != len(required) {
		return out, invalidResult()
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return out, invalidResult()
		}
	}
	return out, nil
}

// Validate binds a prepared manifest to the selected processor request.
func (m Manifest) Validate(request ProcessorRequest) error {
	if request.Seed == nil || m.Seed != *request.Seed || request.ValidationBasisPoints == nil ||
		m.ValidationBasisPoints != *request.ValidationBasisPoints || m.TestBasisPoints != request.TestBasisPoints {
		return invalidResult()
	}
	return m.ValidateStored(request.InputSHA256, request.Limits.MaximumRows, request.Resources.MaximumResultBytes)
}

// ValidateStored checks the shape of a retained manifest under storage limits.
func (m Manifest) ValidateStored(sourceSHA string, maximumRows, maximumFileBytes int64) error {
	if m.RowSchema != RowSchema || m.ProcessorVersion != ProcessorVersion || m.SourceSHA256 != sourceSHA ||
		m.TotalRows < 2 || m.TotalRows > maximumRows || m.UniqueRows < 2 || m.UniqueRows > m.TotalRows ||
		m.DuplicateRows != m.TotalRows-m.UniqueRows || m.SourceGroups < 2 || m.SourceGroups > m.UniqueRows ||
		m.ValidationGroups < 1 || m.ValidationGroups >= m.SourceGroups ||
		m.TrainRows < 1 || m.TrainRows > m.UniqueRows || m.ValidationRows < 1 || m.ValidationRows > m.UniqueRows {
		return invalidResult()
	}
	names := map[string]bool{"samples.jsonl": true, "train.jsonl": true, "validation.jsonl": true, "assignments.jsonl": true, "duplicates.jsonl": true}
	switch m.SchemaVersion {
	case 1:
		if m.SplitAlgorithm != SplitAlgorithm || ValidateSplit(m.Seed, m.ValidationBasisPoints) != nil ||
			m.TestBasisPoints != 0 || m.TestRows != 0 || m.TestGroups != 0 {
			return invalidResult()
		}
	case 2:
		if m.SplitAlgorithm != ThreeWaySplitAlgorithm || ValidateThreeWaySplit(m.Seed, m.ValidationBasisPoints, m.TestBasisPoints) != nil ||
			m.TestGroups < 1 || m.TestGroups >= m.SourceGroups-m.ValidationGroups ||
			m.TestRows < m.TestGroups || m.TestRows > m.UniqueRows || m.ValidationRows < m.ValidationGroups ||
			m.TrainRows < m.SourceGroups-m.ValidationGroups-m.TestGroups {
			return invalidResult()
		}
		names["test.jsonl"] = true
	default:
		return invalidResult()
	}
	if m.TestRows != m.UniqueRows-m.TrainRows-m.ValidationRows || len(m.Files) != len(names) {
		return invalidResult()
	}
	for _, file := range m.Files {
		if !names[file.Path] || file.SizeBytes < 0 || file.SizeBytes > maximumFileBytes || !ValidDigest(file.SHA256) ||
			file.Path == "test.jsonl" && file.SizeBytes == 0 {
			return invalidResult()
		}
		delete(names, file.Path)
	}
	if m.ManifestFile.Path != "manifest.json" || m.ManifestFile.SizeBytes < 1 ||
		m.ManifestFile.SizeBytes > maximumFileBytes || !ValidDigest(m.ManifestFile.SHA256) {
		return invalidResult()
	}
	return nil
}

func KnownError(code string) bool {
	switch code {
	case "DATASET_JSON_INVALID", "DATASET_FIELDS_INVALID", "DATASET_MESSAGE_SEQUENCE_INVALID", "DATASET_TEXT_INVALID", "DATASET_SOURCE_INVALID", "DATASET_LINE_TOO_LARGE", "DATASET_ENCODING_INVALID", "DATASET_BOM_FORBIDDEN", "DATASET_EMPTY_LINE", "DATASET_ROW_LIMIT", "DATASET_EMPTY_SOURCE", "DATASET_FILE_TOO_LARGE", "DATASET_DUPLICATE_FIELD", "DATASET_SOURCE_CHANGED", "DATASET_SOURCE_DIGEST_INVALID", "DATASET_PROCESSOR_LIMITS_INVALID", "DATASET_PROCESSOR_RESOURCES_INVALID", "DATASET_PROCESSOR_REQUEST_INVALID", "DATASET_PROCESSOR_PATH_INVALID", "DATASET_PROCESSOR_TIMEOUT", "DATASET_PROCESSOR_FAILED", "DATASET_PROCESSOR_MEMORY_EXCEEDED", "DATASET_PROCESSOR_RESULT_TOO_LARGE", "DATASET_OUTPUT_CONFLICT", "DATASET_SPLIT_CONFIGURATION_INVALID", "DATASET_SPLIT_INSUFFICIENT_GROUPS", "DATASET_VALIDATION_FAILED", "DATASET_NEAR_DUPLICATE_BUDGET_EXCEEDED":
		return true
	}
	return false
}
