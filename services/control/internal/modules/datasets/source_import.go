package datasets

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

// SourceFormat selects the file syntax independently of the file name.
type SourceFormat string

const (
	SourceJSONL   SourceFormat = "jsonl"
	SourceJSON    SourceFormat = "json"
	SourceCSV     SourceFormat = "csv"
	SourceParquet SourceFormat = "parquet"
)

// SourceImportLimits controls one import's disk, working set and run time.
type SourceImportLimits struct {
	SourceBytes int64
	OutputBytes int64
	RecordBytes int
	Fields      int
	Diagnostics int
	Timeout     time.Duration
}

const (
	importRecordCeiling  = 16 << 20
	importFieldCeiling   = 4096
	importFieldNameBytes = 1024
	importIssueCeiling   = 10000
	sourceReadFailedCode = "DATASET_IMPORT_SOURCE_READ_FAILED"
)

// SourceImportOptions controls recovery and optional binding to a source receipt.
// The empty CSV encoding selects UTF-8 and detects a leading UTF-8 BOM.
type SourceImportOptions struct {
	Format         SourceFormat
	TrainingFormat SourceTrainingFormat
	Cleaning       *SourceCleaningPolicy
	CSVEncoding    string
	SkipInvalid    bool
	ExpectedBytes  int64
	ExpectedSHA256 string
}

// SourceIssue locates a problem without including any values from the input.
// Line is a physical text line. Row is a one-based array or Parquet row.
type SourceIssue struct {
	Code  string `json:"code"`
	Field string `json:"field,omitempty"`
	Line  int64  `json:"line,omitempty"`
	Row   int64  `json:"row,omitempty"`
}

func (e SourceIssue) Error() string {
	message := e.Code
	if e.Line > 0 {
		message += fmt.Sprintf(" at line %d", e.Line)
	} else if e.Row > 0 {
		message += fmt.Sprintf(" at row %d", e.Row)
	}
	if e.Field != "" {
		message += " in field " + e.Field
	}
	return message
}

// SourceField describes a top-level field. Nulls include missing keys.
type SourceField struct {
	Types []string `json:"types"`
	Nulls int64    `json:"nulls"`
}

// SourceImportReport describes the original source and the accepted rows.
// OutputSHA256 identifies the exact normalized bytes once the destination
// link is created. A directory sync error can still leave that link visible.
type SourceImportReport struct {
	TrainingFormat SourceTrainingFormat   `json:"training_format,omitempty"`
	Cleaning       *SourceCleaningReport  `json:"cleaning,omitempty"`
	SourceBytes    int64                  `json:"source_bytes"`
	SourceSHA256   string                 `json:"source_sha256"`
	OutputBytes    int64                  `json:"output_bytes"`
	OutputSHA256   string                 `json:"output_sha256,omitempty"`
	Rows           int64                  `json:"rows"`
	RejectedRows   int64                  `json:"rejected_rows"`
	FieldCount     int                    `json:"field_count"`
	Fields         map[string]SourceField `json:"fields"`
	Issues         []SourceIssue          `json:"issues,omitempty"`
	Truncated      bool                   `json:"issues_truncated"`
}

// SourceObjectReader is the read side of the configured private object transport.
type SourceObjectReader interface {
	Open(context.Context, objectstore.Ref, string) (objectstore.Download, error)
}

// SourceObject is an already-authorized immutable object receipt. Resolve it
// using the object owner under the caller's current workspace authority.
type SourceObject struct {
	Ref    objectstore.Ref
	ETag   string
	Bytes  int64
	SHA256 string
}

// SourceImporter validates uploads and authorized object streams into private
// JSONL artifacts. It is safe for concurrent calls with independent inputs.
type SourceImporter struct {
	limits  SourceImportLimits
	parquet *ParquetImportConfig
}

// NewSourceImporter validates operator-provided limits and optional Parquet runtime.
func NewSourceImporter(limits SourceImportLimits, parquet *ParquetImportConfig) (*SourceImporter, error) {
	if limits.SourceBytes < 1 || limits.SourceBytes > math.MaxInt64-importRecordCeiling-2 || limits.OutputBytes < 1 ||
		limits.RecordBytes < 1 || limits.RecordBytes > importRecordCeiling ||
		limits.Fields < 1 || limits.Fields > importFieldCeiling ||
		limits.Diagnostics < 0 || limits.Diagnostics > importIssueCeiling || limits.Timeout <= 0 {
		return nil, failure("DATASET_IMPORT_CONFIGURATION_INVALID")
	}
	importer := &SourceImporter{limits: limits}
	if parquet != nil {
		cfg := *parquet
		if err := cfg.validate(); err != nil {
			return nil, err
		}
		importer.parquet = &cfg
	}
	return importer, nil
}

// ImportUpload consumes an open upload body. The destination must be a new file
// in a private, existing directory owned by the service. The caller owns the
// body and must cancel or close any blocking network reader on cancellation.
func (i *SourceImporter) ImportUpload(ctx context.Context, body io.Reader, destination string, opts SourceImportOptions) (SourceImportReport, error) {
	ctx, cancel := context.WithTimeout(ctx, i.limits.Timeout)
	defer cancel()
	return i.importSource(ctx, body, destination, opts)
}

// ImportObject fetches exactly the configured transport object bound by the
// authorized receipt. It verifies size and SHA-256 before making rows visible.
func (i *SourceImporter) ImportObject(ctx context.Context, storage SourceObjectReader, object SourceObject, destination string, opts SourceImportOptions) (SourceImportReport, error) {
	opts.ExpectedBytes, opts.ExpectedSHA256 = object.Bytes, object.SHA256
	if storage == nil || object.ETag == "" || object.Bytes < 1 || !wire.ValidDigest(object.SHA256) {
		return SourceImportReport{}, failure("DATASET_IMPORT_OBJECT_INVALID")
	}
	if _, err := object.Ref.Key(); err != nil {
		return SourceImportReport{}, failure("DATASET_IMPORT_OBJECT_INVALID")
	}
	if err := i.validateOptions(opts); err != nil {
		return SourceImportReport{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, i.limits.Timeout)
	defer cancel()
	download, err := storage.Open(ctx, object.Ref, object.ETag)
	if err != nil {
		return SourceImportReport{}, err
	}
	if download.Body == nil {
		return SourceImportReport{}, failure("DATASET_IMPORT_OBJECT_INVALID")
	}
	defer download.Body.Close()
	if download.Info.Bytes != object.Bytes || download.Info.ETag != object.ETag {
		return SourceImportReport{}, failure("DATASET_SOURCE_CHANGED")
	}
	return i.importSource(ctx, download.Body, destination, opts)
}

func (i *SourceImporter) validateOptions(opts SourceImportOptions) error {
	switch opts.Format {
	case SourceJSON, SourceJSONL, SourceCSV:
	case SourceParquet:
		if i.parquet == nil {
			return failure("DATASET_PARQUET_RUNTIME_UNAVAILABLE")
		}
	default:
		return failure("DATASET_IMPORT_FORMAT_INVALID")
	}
	if opts.CSVEncoding != "" && (opts.Format != SourceCSV || opts.CSVEncoding != "utf-8" && opts.CSVEncoding != "gbk") {
		return failure("DATASET_IMPORT_ENCODING_UNSUPPORTED")
	}
	if !opts.Cleaning.valid(i.limits.Fields) {
		return failure("DATASET_CLEANING_POLICY_INVALID")
	}
	if !opts.TrainingFormat.valid() {
		return failure("DATASET_TRAINING_FORMAT_INVALID")
	}
	if opts.ExpectedBytes < 0 || opts.ExpectedSHA256 != "" && !wire.ValidDigest(opts.ExpectedSHA256) {
		return failure("DATASET_IMPORT_SOURCE_INVALID")
	}
	if opts.ExpectedBytes > i.limits.SourceBytes {
		return failure("DATASET_FILE_TOO_LARGE")
	}
	return nil
}

func (i *SourceImporter) importSource(ctx context.Context, body io.Reader, destination string, opts SourceImportOptions) (SourceImportReport, error) {
	if err := i.validateOptions(opts); err != nil {
		return SourceImportReport{}, err
	}
	if body == nil || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return SourceImportReport{}, failure("DATASET_IMPORT_SOURCE_INVALID")
	}
	directory := filepath.Dir(destination)
	if _, err := privatePath(directory, "", true, 0); err != nil {
		return SourceImportReport{}, failure("DATASET_STORAGE_PATH_INVALID")
	}
	output, err := os.CreateTemp(directory, ".dataset-import-")
	if err != nil {
		return SourceImportReport{}, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer os.Remove(output.Name())
	defer output.Close()

	limit := i.limits.SourceBytes
	if opts.ExpectedBytes > 0 {
		limit = opts.ExpectedBytes
	}
	input := sourceReadObserver{Reader: boundedContextReader{ctx, body}}
	limited := &io.LimitedReader{R: &input, N: limit + 1}
	sourceHash := sha256.New()
	outputHash := sha256.New()
	source := io.TeeReader(limited, sourceHash)
	state := sourceImportState{
		ctx: ctx, limits: i.limits, opts: opts, output: bufio.NewWriter(io.MultiWriter(output, outputHash)),
		fields: make(map[string]*sourceFieldCount), cleaning: newSourceCleaning(opts.Cleaning),
	}
	switch opts.Format {
	case SourceJSONL:
		err = state.readJSONL(source)
	case SourceJSON:
		err = state.readJSONArray(source)
	case SourceCSV:
		err = state.readCSV(source)
	case SourceParquet:
		err = state.readParquet(source, directory, *i.parquet)
	}

	report := state.report()
	report.SourceBytes = limit + 1 - limited.N
	if ctx.Err() != nil {
		return report, ctx.Err()
	}
	if report.SourceBytes > i.limits.SourceBytes {
		return report, failure("DATASET_FILE_TOO_LARGE")
	}
	if opts.ExpectedBytes > 0 && report.SourceBytes > opts.ExpectedBytes {
		return report, failure("DATASET_SOURCE_CHANGED")
	}
	if input.interrupted {
		var issue SourceIssue
		if errors.As(err, &issue) && issue.Code == sourceReadFailedCode {
			return report, issue
		}
		return report, failure(sourceReadFailedCode)
	}
	if err != nil {
		return report, err
	}
	report.SourceSHA256 = hex.EncodeToString(sourceHash.Sum(nil))
	if opts.ExpectedBytes > 0 && report.SourceBytes != opts.ExpectedBytes ||
		opts.ExpectedSHA256 != "" && report.SourceSHA256 != opts.ExpectedSHA256 {
		return report, failure("DATASET_SOURCE_CHANGED")
	}
	if report.Rows == 0 {
		return report, failure("DATASET_IMPORT_NO_VALID_ROWS")
	}
	if state.output.Flush() != nil || output.Sync() != nil {
		return report, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if output.Close() != nil {
		return report, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	dir, err := os.Open(directory)
	if err != nil {
		return report, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer dir.Close()
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if err := os.Link(output.Name(), destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return report, failure("DATASET_OUTPUT_CONFLICT")
		}
		return report, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	report.OutputSHA256 = hex.EncodeToString(outputHash.Sum(nil))
	if dir.Sync() != nil {
		return report, failure("DATASET_IMPORT_COMMIT_UNCERTAIN")
	}
	if state.cleaning != nil {
		report.Cleaning.Samples = state.cleaning.report.Samples
	}
	return report, nil
}

// sourceReadObserver records body failures before buffered parsers can
// reclassify an interrupted transfer as a format error.
type sourceReadObserver struct {
	io.Reader
	interrupted bool
}

func (s *sourceReadObserver) Read(p []byte) (int, error) {
	n, err := s.Reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		s.interrupted = true
	}
	return n, err
}

type sourceFieldCount struct {
	types   []string
	nonNull int64
}

type sourceImportState struct {
	ctx      context.Context
	limits   SourceImportLimits
	opts     SourceImportOptions
	output   *bufio.Writer
	fields   map[string]*sourceFieldCount
	cleaning *sourceCleaningState
	schema   bool
	rows     int64
	rejected int64
	bytes    int64
	issues   []SourceIssue
}

func (s *sourceImportState) reject(issue SourceIssue) error {
	s.rejected++
	if len(s.issues) < s.limits.Diagnostics {
		s.issues = append(s.issues, issue)
	}
	if !s.opts.SkipInvalid {
		return issue
	}
	return nil
}

func (s *sourceImportState) report() SourceImportReport {
	fields := make(map[string]SourceField, len(s.fields))
	for name, count := range s.fields {
		types := count.types
		if len(types) == 0 {
			types = []string{"null"}
		}
		slices.Sort(types)
		fields[name] = SourceField{Types: types, Nulls: s.rows - count.nonNull}
	}
	var cleaning *SourceCleaningReport
	if s.cleaning != nil {
		report := s.cleaning.report
		report.Samples = nil
		cleaning = &report
	}
	return SourceImportReport{
		Cleaning: cleaning, TrainingFormat: s.opts.TrainingFormat, Rows: s.rows, RejectedRows: s.rejected, OutputBytes: s.bytes,
		FieldCount: len(fields), Fields: fields,
		Issues: s.issues, Truncated: s.rejected > int64(len(s.issues)),
	}
}

func (s *sourceImportState) accept(row map[string]jsontext.Value, position SourceIssue) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	var before map[string]jsontext.Value
	if s.cleaning != nil {
		keep, err := s.cleaning.keep(row)
		if err != nil {
			position.Code = "DATASET_IMPORT_ROW_INVALID"
			return s.reject(position)
		}
		if !keep {
			s.cleaning.report.FilteredRows++
			s.cleaning.sample(position, row, nil)
			return nil
		}
		cleaned, err := s.cleaning.trim(row)
		if err != nil {
			position.Code = "DATASET_IMPORT_ROW_INVALID"
			return s.reject(position)
		}
		if cleaned != nil {
			before, row = row, cleaned
		}
	}
	if field := s.opts.TrainingFormat.invalidField(row); field != "" {
		position.Code = "DATASET_TRAINING_ROW_INVALID"
		position.Field = field
		return s.reject(position)
	}
	newFields := 0
	for name := range row {
		if len(name) > importFieldNameBytes {
			position.Code = "DATASET_IMPORT_FIELD_NAME_LIMIT"
			return s.reject(position)
		}
		if s.fields[name] == nil {
			newFields++
		}
	}
	if len(s.fields)+newFields > s.limits.Fields {
		position.Code = "DATASET_IMPORT_FIELD_LIMIT"
		return position
	}
	raw, err := json.Marshal(row, json.Deterministic(true))
	if err != nil {
		position.Code = "DATASET_IMPORT_ROW_INVALID"
		return s.reject(position)
	}
	needed := int64(len(raw)) + 1
	if needed > s.limits.OutputBytes-s.bytes {
		position.Code = "DATASET_IMPORT_OUTPUT_LIMIT"
		return position
	}
	if _, err = s.output.Write(raw); err != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if err = s.output.WriteByte('\n'); err != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	for name, value := range row {
		count := s.fields[name]
		if count == nil {
			count = &sourceFieldCount{}
			s.fields[name] = count
		}
		if value.Kind() != jsontext.KindNull {
			count.nonNull++
			if !s.schema {
				kind := jsonFieldType(value)
				if !slices.Contains(count.types, kind) {
					count.types = append(count.types, kind)
				}
			}
		}
	}
	s.rows++
	s.bytes += needed
	if before != nil {
		s.cleaning.report.ChangedRows++
		s.cleaning.sample(position, before, raw)
	}
	return nil
}

func jsonFieldType(value jsontext.Value) string {
	switch value.Kind() {
	case jsontext.KindBeginObject:
		return "object"
	case jsontext.KindBeginArray:
		return "array"
	case jsontext.KindString:
		return "string"
	case jsontext.KindTrue, jsontext.KindFalse:
		return "boolean"
	default:
		return "number"
	}
}
