package datasets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testImportLimits() SourceImportLimits {
	return SourceImportLimits{SourceBytes: 512 << 20, OutputBytes: 512 << 20, RecordBytes: 64 << 10, Fields: 64, Diagnostics: 8, Timeout: time.Minute}
}

func testImporter(t *testing.T, limits SourceImportLimits, cfg *ParquetImportConfig) *SourceImporter {
	t.Helper()
	importer, err := NewSourceImporter(limits, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return importer
}

func privateImportDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func readImported(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSourceImportFormats(t *testing.T) {
	importer := testImporter(t, testImportLimits(), nil)
	for _, tc := range []struct {
		name     string
		opts     SourceImportOptions
		input    string
		expected string
		types    string
	}{
		{"jsonl", SourceImportOptions{Format: SourceJSONL},
			"{\"id\":1,\"text\":null}\r\n{\"id\":2,\"text\":\"ok\"}", "{\"id\":1,\"text\":null}\n{\"id\":2,\"text\":\"ok\"}\n", "number"},
		{"array", SourceImportOptions{Format: SourceJSON},
			" [\n {\"text\":null,\"id\":1},\n {\"id\":2,\"text\":\"ok\"}\n ]\n", "{\"id\":1,\"text\":null}\n{\"id\":2,\"text\":\"ok\"}\n", "number"},
		{"bom", SourceImportOptions{Format: SourceCSV},
			"\xef\xbb\xbfid,text\r\n01,\r\n02,\"quoted, \"\"value\"\"\r\nnext\"\r\n", "{\"id\":\"01\",\"text\":null}\n{\"id\":\"02\",\"text\":\"quoted, \\\"value\\\"\\nnext\"}\n", "string"},
		{"gbk", SourceImportOptions{Format: SourceCSV, CSVEncoding: "gbk"},
			"id,text\n01,\n02,\xd6\xd0\xce\xc4\n", "{\"id\":\"01\",\"text\":null}\n{\"id\":\"02\",\"text\":\"\u4e2d\u6587\"}\n", "string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(privateImportDir(t), "rows.jsonl")
			report, err := importer.ImportUpload(t.Context(), strings.NewReader(tc.input), path, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if report.Rows != 2 || report.FieldCount != 2 || report.RejectedRows != 0 ||
				report.SourceBytes != int64(len(tc.input)) || report.SourceSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(tc.input))) ||
				report.OutputBytes != int64(len(tc.expected)) || report.Fields["text"].Nulls != 1 ||
				report.Fields["id"].Nulls != 0 || strings.Join(report.Fields["id"].Types, ",") != tc.types {
				t.Fatalf("unexpected report: %+v", report)
			}
			if got := readImported(t, path); got != tc.expected {
				t.Fatalf("got %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestSourceImportRecovery(t *testing.T) {
	const input = "{\"id\":1,\"late\":null}\n{broken}\n{\"id\":\"two\"}\n{\"id\":3,\"late\":true,\"extra\":[]}"
	importer := testImporter(t, testImportLimits(), nil)
	dir := privateImportDir(t)
	path := filepath.Join(dir, "rows.jsonl")
	opts := SourceImportOptions{Format: SourceJSONL}
	report, err := importer.ImportUpload(t.Context(), strings.NewReader(input), path, opts)
	if err != (SourceIssue{Code: "DATASET_JSON_INVALID", Line: 2}) || report.RejectedRows != 1 {
		t.Fatalf("report %+v, error %v", report, err)
	}
	assertNoImports(t, dir)

	opts.SkipInvalid = true
	report, err = importer.ImportUpload(t.Context(), strings.NewReader(input), path, opts)
	if err != nil || report.Rows != 3 || report.RejectedRows != 1 || report.FieldCount != 3 ||
		report.Fields["extra"].Nulls != 2 || report.Fields["late"].Nulls != 2 ||
		strings.Join(report.Fields["id"].Types, ",") != "number,string" ||
		len(report.Issues) != 1 || report.Issues[0].Line != 2 {
		t.Fatalf("report %+v, error %v", report, err)
	}
	before := readImported(t, path)
	_, err = importer.ImportUpload(t.Context(), strings.NewReader("{\"id\":4}\n"), path, opts)
	if err == nil || err.Error() != "DATASET_OUTPUT_CONFLICT" || readImported(t, path) != before {
		t.Fatalf("existing import changed: %v", err)
	}
}

func assertNoImports(t *testing.T, directory string) {
	t.Helper()
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatalf("attempt files remain: %v, %v", files, err)
	}
}

func TestSourceImportMalformedInputs(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		opts        SourceImportOptions
		want        SourceIssue
	}{
		{"invalid-array", "[{\"id\":1},{oops}]", SourceImportOptions{Format: SourceJSON, SkipInvalid: true}, SourceIssue{Code: "DATASET_JSON_ARRAY_INVALID", Row: 2}},
		{"trailing-array", "[{\"id\":1},]", SourceImportOptions{Format: SourceJSON, SkipInvalid: true}, SourceIssue{Code: "DATASET_JSON_ARRAY_INVALID", Row: 2}},
		{"array-tail", "[{\"id\":1}] true", SourceImportOptions{Format: SourceJSON}, SourceIssue{Code: "DATASET_JSON_ARRAY_INVALID", Row: 2}},
		{"duplicate-json", "{\"id\":{\"x\":1,\"x\":2}}\n", SourceImportOptions{Format: SourceJSONL}, SourceIssue{Code: "DATASET_JSON_INVALID", Line: 1}},
		{"invalid-json-unicode", "{\"id\":\"\\ud800\"}\n", SourceImportOptions{Format: SourceJSONL}, SourceIssue{Code: "DATASET_JSON_INVALID", Line: 1}},
		{"broken-quotes", "id,text\n01,\"first\nsecond\"\n02,\"broken\n03,end\n", SourceImportOptions{Format: SourceCSV, SkipInvalid: true}, SourceIssue{Code: "DATASET_CSV_INVALID", Line: 4}},
		{"duplicate-header", "id,id\n", SourceImportOptions{Format: SourceCSV}, SourceIssue{Code: "DATASET_CSV_HEADER_INVALID", Line: 1}},
		{"gbk-undeclared", "id,text\n01,\xd6\xd0\xce\xc4\n", SourceImportOptions{Format: SourceCSV, SkipInvalid: true}, SourceIssue{Code: "DATASET_CSV_UTF8_INVALID_SELECT_ENCODING", Line: 2}},
		{"gbk-invalid", "id,text\n01,\x81\n", SourceImportOptions{Format: SourceCSV, CSVEncoding: "gbk"}, SourceIssue{Code: "DATASET_CSV_GBK_INVALID", Line: 2}},
		{"utf16", "\xff\xfei\x00d\x00", SourceImportOptions{Format: SourceCSV}, SourceIssue{Code: "DATASET_IMPORT_ENCODING_UNSUPPORTED", Line: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := privateImportDir(t)
			importer := testImporter(t, testImportLimits(), nil)
			_, err := importer.ImportUpload(t.Context(), strings.NewReader(tc.input), filepath.Join(dir, "rows.jsonl"), tc.opts)
			if err != tc.want {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			assertNoImports(t, dir)
		})
	}
}

func TestSourceImportSkipsOnlyRecoverableRecords(t *testing.T) {
	limits := testImportLimits()
	limits.RecordBytes = 64
	limits.Diagnostics = 1
	importer := testImporter(t, limits, nil)
	for _, tc := range []struct {
		format       SourceFormat
		input        string
		firstIssue   SourceIssue
		rejectedRows int64
	}{
		{SourceJSONL, "{\"id\":1}\n" + strings.Repeat("x", 1<<20) + "\n{bad}\n{\"id\":2}\n", SourceIssue{Code: "DATASET_IMPORT_RECORD_LIMIT", Line: 2}, 2},
		{SourceJSON, "[{\"id\":1},null,{\"id\":2}]", SourceIssue{Code: "DATASET_JSON_INVALID", Row: 2}, 1},
		{SourceCSV, "id,text\n1,\"ok\nmore\"\n2\n3,end\n", SourceIssue{Code: "DATASET_CSV_FIELD_COUNT", Line: 4}, 1},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			path := filepath.Join(privateImportDir(t), "rows.jsonl")
			report, err := importer.ImportUpload(t.Context(), strings.NewReader(tc.input), path, SourceImportOptions{Format: tc.format, SkipInvalid: true})
			if err != nil || report.Rows != 2 || report.RejectedRows != tc.rejectedRows || len(report.Issues) != 1 ||
				report.Issues[0] != tc.firstIssue || report.Truncated != (tc.rejectedRows > 1) {
				t.Fatalf("report %+v, error %v", report, err)
			}
		})
	}
}

func TestSourceImportLimitsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, input, code string
		format            SourceFormat
		setLimits         func(*SourceImportLimits)
	}{
		{"source", "{\"id\":1}\n{\"id\":2}", "DATASET_FILE_TOO_LARGE", SourceJSONL, func(l *SourceImportLimits) { l.SourceBytes = 10 }},
		{"output", "{\"id\":1}\n", "DATASET_IMPORT_OUTPUT_LIMIT at line 1", SourceJSONL, func(l *SourceImportLimits) { l.OutputBytes = 2 }},
		{"fields", "{\"id\":1}\n{\"other\":2}\n", "DATASET_IMPORT_FIELD_LIMIT at line 2", SourceJSONL, func(l *SourceImportLimits) { l.Fields = 1 }},
		{"json-array-element", "[{\"id\":\"" + strings.Repeat("x", 1<<20) + "\"}]", "DATASET_IMPORT_RECORD_LIMIT at row 1", SourceJSON, func(l *SourceImportLimits) { l.RecordBytes = 64 }},
		{"csv-record", "id\n\"" + strings.Repeat("x\n", 1<<19) + "\"\n", "DATASET_IMPORT_RECORD_LIMIT at line 2", SourceCSV, func(l *SourceImportLimits) { l.RecordBytes = 64 }},
		{"all-invalid", "{bad}\n", "DATASET_IMPORT_NO_VALID_ROWS", SourceJSONL, func(l *SourceImportLimits) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := privateImportDir(t)
			limits := testImportLimits()
			tc.setLimits(&limits)
			importer := testImporter(t, limits, nil)
			_, err := importer.ImportUpload(t.Context(), strings.NewReader(tc.input), filepath.Join(dir, "rows.jsonl"), SourceImportOptions{Format: tc.format, SkipInvalid: true})
			if err == nil || err.Error() != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
			assertNoImports(t, dir)
		})
	}
	dir := privateImportDir(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := testImporter(t, testImportLimits(), nil).ImportUpload(ctx, strings.NewReader("{\"id\":1}"), filepath.Join(dir, "rows.jsonl"), SourceImportOptions{Format: SourceJSONL})
	if err != context.Canceled {
		t.Fatal(err)
	}
	assertNoImports(t, dir)
}

func TestSourceImportParquet(t *testing.T) {
	python := os.Getenv("OWNSOLID_TEST_PARQUET_PYTHON")
	if python == "" {
		t.Skip("set OWNSOLID_TEST_PARQUET_PYTHON to a protected Python 3.12 executable with the pinned Parquet extra")
	}
	script, err := filepath.Abs("../../../../../runtimes/data-processing/import_parquet.py")
	if err != nil {
		t.Fatal(err)
	}
	cfg := ParquetImportConfig{
		PythonExecutable: python, DecoderFile: script, BatchRows: 1,
		MetadataBytes: 1 << 20, RowGroupBytes: 8 << 20, MemoryBytes: 512 << 20,
	}
	importer := testImporter(t, testImportLimits(), &cfg)
	dir := privateImportDir(t)
	input := filepath.Join(dir, "input.parquet")
	fixture := `
import sys
import pyarrow as p
import pyarrow.parquet as pq
from decimal import Decimal
table = p.table({
    "id": [1, 2, 3],
    "answer": ["ok", "bad", None],
    "price": p.array([Decimal("1.25"), None, Decimal("2.00")], type=p.decimal128(6, 2)),
    "score": [0.5, float("nan"), 0.25],
    "data": [b"abc", None, b"def"],
    "times": p.array([[1], [2], [3]], type=p.list_(p.timestamp("ns"))),
})
pq.write_table(table, sys.argv[1], row_group_size=2, write_page_checksum=True)
`
	if out, err := exec.CommandContext(t.Context(), python, "-I", "-B", "-c", fixture, input).CombinedOutput(); err != nil {
		t.Fatalf("write synthetic Parquet: %v %s", err, out)
	}
	raw, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	outDir := privateImportDir(t)
	output := filepath.Join(outDir, "rows.jsonl")
	opts := SourceImportOptions{Format: SourceParquet}
	_, err = importer.ImportUpload(t.Context(), bytes.NewReader(raw), output, opts)
	if err != (SourceIssue{Code: "DATASET_PARQUET_ROW_INVALID", Row: 2}) {
		t.Fatal(err)
	}
	assertNoImports(t, outDir)

	opts.SkipInvalid = true
	report, err := importer.ImportUpload(t.Context(), bytes.NewReader(raw), output, opts)
	if err != nil || report.Rows != 2 || report.RejectedRows != 1 || report.FieldCount != 6 || report.Fields["answer"].Nulls != 1 || report.SourceBytes != int64(len(raw)) ||
		report.Fields["id"].Types[0] != "int64" || report.Fields["price"].Types[0] != "decimal128(6, 2)" ||
		len(report.Issues) != 1 || report.Issues[0].Row != 2 {
		t.Fatalf("report %+v, error %v", report, err)
	}
	if got := readImported(t, output); got != "{\"answer\":\"ok\",\"data\":\"YWJj\",\"id\":1,\"price\":\"1.25\",\"score\":0.5,\"times\":[1]}\n{\"answer\":null,\"data\":\"ZGVm\",\"id\":3,\"price\":\"2.00\",\"score\":0.25,\"times\":[3]}\n" {
		t.Fatalf("unexpected Parquet rows %q", got)
	}
	_, err = importer.ImportUpload(t.Context(), bytes.NewReader(raw[:len(raw)-4]), filepath.Join(outDir, "broken"), opts)
	if err != (SourceIssue{Code: "DATASET_PARQUET_INVALID"}) {
		t.Fatalf("truncated footer: %v", err)
	}
	cfg.RowGroupBytes = 1
	_, err = testImporter(t, testImportLimits(), &cfg).ImportUpload(t.Context(), bytes.NewReader(raw), filepath.Join(outDir, "large-group"), opts)
	if err != (SourceIssue{Code: "DATASET_PARQUET_RESOURCE_LIMIT", Row: 1}) {
		t.Fatalf("row group budget: %v", err)
	}
	if files, err := os.ReadDir(outDir); err != nil || len(files) != 1 || files[0].Name() != "rows.jsonl" {
		t.Fatalf("failed attempt files remain: %v, %v", files, err)
	}
}

func TestSourceImportMemory(t *testing.T) {
	const childInput = "OWNSOLID_TEST_IMPORT_MEMORY_INPUT"
	if path := os.Getenv(childInput); path != "" {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		importer := testImporter(t, testImportLimits(), nil)
		report, err := importer.ImportUpload(t.Context(), file, path+".jsonl", SourceImportOptions{Format: SourceJSONL})
		if err != nil || report.SourceBytes != info.Size() || report.Rows != info.Size()/4096 || report.OutputBytes != info.Size() {
			t.Fatalf("report %+v, error %v", report, err)
		}
		return
	}
	if testing.Short() {
		t.Skip("writes and imports 144 MiB of synthetic files")
	}
	dir := privateImportDir(t)
	record := []byte("{\"text\":\"" + strings.Repeat("x", 4084) + "\"}\n")
	chunk := bytes.Repeat(record, 256)
	var peak [2]int64
	for index, size := range []int64{16 << 20, 128 << 20} {
		path := filepath.Join(dir, fmt.Sprintf("sample-%d", size))
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		for offset := int64(0); offset < size; offset += int64(len(chunk)) {
			if _, err = file.Write(chunk); err != nil {
				file.Close()
				t.Fatal(err)
			}
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSourceImportMemory$")
		// Fix CPU parallelism and GC settings so the comparison does not
		// measure host-wide scheduling or a caller-supplied heap limit.
		command.Env = append(os.Environ(), childInput+"="+path, "GOMAXPROCS=2", "GOGC=100", "GOMEMLIMIT=off")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("import %d MiB: %v\n%s", size>>20, err, out)
		}
		usage := command.ProcessState.SysUsage().(*syscall.Rusage)
		peak[index] = int64(usage.Maxrss)
		if runtime.GOOS == "linux" {
			peak[index] *= 1024
		}
		if peak[index] <= 0 {
			t.Fatal("peak RSS unavailable")
		}
		t.Logf("input=%d bytes, accepted=%d rows, peak RSS=%d bytes (%.2f MiB)", size, size/4096, peak[index], float64(peak[index])/(1<<20))
	}
	if peak[1] > peak[0]+(24<<20) || peak[1] > 64<<20 {
		t.Fatalf("peak RSS grew with input: %d -> %d bytes", peak[0], peak[1])
	}
}
