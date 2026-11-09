package datasets

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

func TestSourceCleaningPipeline(t *testing.T) {
	const input = "{\"text\":\"  private-fixture-alpha  \",\"tag\":\" x \"}\n" +
		"{\"text\":\"filtered null\",\"tag\":null}\n" +
		"broken\n" +
		"{\"text\":\"unchanged\",\"tag\":false}\n" +
		"{\"text\":\"filtered missing\"}\n" +
		"{\"text\":\"   \",\"tag\":\"present\"}\n" +
		"{\"text\":\"\\u3000trimmed\\u3000\",\"tag\":0}\n" +
		"{\"text\":\"filtered blank\",\"tag\":\"\\u00a0 \"}\n"
	const expected = "{\"tag\":\"x\",\"text\":\"private-fixture-alpha\"}\n" +
		"{\"tag\":false,\"text\":\"unchanged\"}\n" +
		"{\"tag\":0,\"text\":\"trimmed\"}\n"
	dir := privateImportDir(t)
	path := filepath.Join(dir, "rows.jsonl")
	importer := testImporter(t, testImportLimits(), nil)
	options := SourceImportOptions{Format: SourceJSONL, TrainingFormat: SourceTrainingText, SkipInvalid: true,
		Cleaning: &SourceCleaningPolicy{TrimFields: []string{"text", "tag"}, KeepNonBlankFields: []string{"tag"}, PreviewRows: 8, PreviewBytes: 2048}}
	report, err := importer.ImportUpload(t.Context(), strings.NewReader(input), path, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.Rows != 3 || report.RejectedRows != 2 || report.FieldCount != 2 || report.OutputBytes != int64(len(expected)) ||
		report.SourceSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(input))) ||
		report.OutputSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(expected))) ||
		len(report.Issues) != 2 || report.Issues[0].Line != 3 || report.Issues[1] != (SourceIssue{Code: "DATASET_TRAINING_ROW_INVALID", Line: 6, Field: "text"}) ||
		readImported(t, path) != expected {
		t.Fatalf("unexpected import result: %+v", report)
	}
	cleaning := report.Cleaning
	if cleaning == nil || cleaning.ChangedRows != 2 || cleaning.FilteredRows != 3 || cleaning.SamplesTruncated || len(cleaning.Samples) != 5 {
		t.Fatalf("unexpected cleaning counts: %+v", cleaning)
	}
	first := cleaning.Samples[0]
	if first.Line != 1 || first.Row != 0 ||
		string(first.Before) != "{\"tag\":\" x \",\"text\":\"  private-fixture-alpha  \"}" ||
		string(first.After) != "{\"tag\":\"x\",\"text\":\"private-fixture-alpha\"}" {
		t.Fatalf("first sample: %+v", first)
	}
	for index, line := range []int64{2, 5, 7, 8} {
		sample := cleaning.Samples[index+1]
		if sample.Line != line || len(sample.Before) == 0 || (len(sample.After) > 0) != (line == 7) {
			t.Fatalf("sample for line %d: %+v", line, sample)
		}
	}
	serialized, err := json.Marshal(report)
	if err != nil || strings.Contains(string(serialized), "private-fixture-alpha") || strings.Contains(string(serialized), `"samples":`) {
		t.Fatalf("preview leaked through the statistics report: %s, %v", serialized, err)
	}
}

func TestSourceCleaningPreviewBudgets(t *testing.T) {
	input := "[{\"text\":\" " + strings.Repeat("x", 128) + " \"},{\"text\":\" a \"},{\"text\":\" b \"}]"
	for _, tc := range []struct {
		name          string
		rows, bytes   int
		wantSampleRow int64
		truncated     bool
	}{
		{"bytes", 2, 32, 2, true},
		{"rows", 1, 1024, 1, true},
		{"disabled", 0, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(privateImportDir(t), "rows.jsonl")
			opts := SourceImportOptions{Format: SourceJSON,
				Cleaning: &SourceCleaningPolicy{TrimFields: []string{"text"}, PreviewRows: tc.rows, PreviewBytes: tc.bytes}}
			report, err := testImporter(t, testImportLimits(), nil).ImportUpload(t.Context(), strings.NewReader(input), path, opts)
			cleaning := report.Cleaning
			if err != nil || report.Rows != 3 || cleaning == nil || cleaning.ChangedRows != 3 || cleaning.SamplesTruncated != tc.truncated {
				t.Fatalf("report %+v, cleaning %+v, error %v", report, cleaning, err)
			}
			if tc.wantSampleRow == 0 {
				if len(cleaning.Samples) != 0 {
					t.Fatal("disabled preview returned samples")
				}
				return
			}
			if len(cleaning.Samples) != 1 {
				t.Fatalf("wrong sample count: %d", len(cleaning.Samples))
			}
			sample := cleaning.Samples[0]
			if sample.Row != tc.wantSampleRow || sample.Line != 0 || len(sample.Before)+len(sample.After) > tc.bytes {
				t.Fatalf("unexpected sample: %+v", sample)
			}
		})
	}
}

func TestSourceCleaningInvalidPolicy(t *testing.T) {
	for _, policy := range []SourceCleaningPolicy{
		{},
		{TrimFields: []string{"text", "text"}},
		{KeepNonBlankFields: []string{""}},
		{TrimFields: []string{"\xff"}},
		{TrimFields: []string{"text"}, PreviewRows: 1},
		{TrimFields: []string{"text"}, PreviewRows: -1, PreviewBytes: 1},
		{TrimFields: []string{"text"}, PreviewRows: 1, PreviewBytes: cleaningPreviewByteCeiling + 1},
	} {
		dir := privateImportDir(t)
		opts := SourceImportOptions{Format: SourceJSONL, Cleaning: &policy}
		_, err := testImporter(t, testImportLimits(), nil).ImportUpload(t.Context(), strings.NewReader("{\"text\":\"ok\"}"), filepath.Join(dir, "rows.jsonl"), opts)
		if err == nil || err.Error() != "DATASET_CLEANING_POLICY_INVALID" {
			t.Fatalf("invalid cleaning policy admitted: %+v, %v", policy, err)
		}
		assertNoImports(t, dir)
	}
}

func TestSourceCleaningFailureWithholdsPreview(t *testing.T) {
	for _, tc := range []struct {
		name, input, digest, code string
		filtered                  int64
	}{
		{"wrong-source", "{\"text\":\" private-fixture \"}\n", strings.Repeat("a", 64), "DATASET_SOURCE_CHANGED", 0},
		{"all-filtered", "{\"text\":null}\n", "", "DATASET_IMPORT_NO_VALID_ROWS", 1},
		{"invalid-row", "{\"text\":\" private-fixture \"}\nbroken\n", "", "DATASET_JSON_INVALID at line 2", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := privateImportDir(t)
			opts := SourceImportOptions{Format: SourceJSONL, ExpectedSHA256: tc.digest,
				Cleaning: &SourceCleaningPolicy{TrimFields: []string{"text"}, KeepNonBlankFields: []string{"text"}, PreviewRows: 3, PreviewBytes: 1024}}
			report, err := testImporter(t, testImportLimits(), nil).ImportUpload(t.Context(), strings.NewReader(tc.input), filepath.Join(dir, "rows.jsonl"), opts)
			if err == nil || err.Error() != tc.code || report.Cleaning == nil ||
				len(report.Cleaning.Samples) != 0 || report.Cleaning.FilteredRows != tc.filtered || report.OutputSHA256 != "" {
				t.Fatalf("report %+v, error %v", report, err)
			}
			assertNoImports(t, dir)
		})
	}
}

func TestSourceCleaningDryRun(t *testing.T) {
	const input = "{\"text\":\"private-filtered\",\"tag\":null}\n{\"text\":\" before \",\"tag\":\"keep\"}\n"
	dir := privateImportDir(t)
	importer := testImporter(t, testImportLimits(), nil)
	opts := SourceImportOptions{
		Format: SourceJSONL, TrainingFormat: SourceTrainingText, ExpectedBytes: int64(len(input)),
		ExpectedSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(input))),
		Cleaning:       &SourceCleaningPolicy{TrimFields: []string{"text"}, KeepNonBlankFields: []string{"tag"}, PreviewRows: 4, PreviewBytes: 1024},
	}
	preview, err := importer.PreviewUpload(t.Context(), strings.NewReader(input), dir, opts)
	if err != nil || preview.Rows != 1 || preview.Cleaning == nil || preview.Cleaning.ChangedRows != 1 ||
		preview.Cleaning.FilteredRows != 1 || len(preview.Cleaning.Samples) != 2 || preview.OutputSHA256 != "" {
		t.Fatal("invalid dry-run result", err)
	}
	assertNoImports(t, dir)

	applied, err := importer.ImportUpload(t.Context(), strings.NewReader(input), filepath.Join(dir, "rows.jsonl"), opts)
	if err != nil || applied.OutputSHA256 == "" {
		t.Fatal("apply failed after the same preview", err)
	}
	applied.OutputSHA256 = ""
	if !reflect.DeepEqual(preview, applied) {
		t.Fatal("preview differs from application of the same policy")
	}
}

func TestSourceCleaningDryRunFiltersEveryRow(t *testing.T) {
	const input = "{\"text\":\"private-filtered\",\"tag\":null}\n"
	dir := privateImportDir(t)
	importer := testImporter(t, testImportLimits(), nil)
	opts := SourceImportOptions{
		Format: SourceJSONL, ExpectedBytes: int64(len(input)), ExpectedSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(input))),
		Cleaning: &SourceCleaningPolicy{KeepNonBlankFields: []string{"tag"}, PreviewRows: 2, PreviewBytes: 1024},
	}
	preview, err := importer.PreviewUpload(t.Context(), strings.NewReader(input), dir, opts)
	if err != nil || preview.Rows != 0 || preview.OutputBytes != 0 || preview.OutputSHA256 != "" ||
		preview.SourceSHA256 != opts.ExpectedSHA256 || preview.Cleaning == nil || preview.Cleaning.FilteredRows != 1 ||
		len(preview.Cleaning.Samples) != 1 || preview.Cleaning.Samples[0].After != nil {
		t.Fatal("all-filtered dry run did not retain a bounded before sample", err)
	}
	raw, err := json.Marshal(preview)
	if err != nil || strings.Contains(string(raw), "private-filtered") {
		t.Fatal("dry run serialized private sample values", err)
	}
	assertNoImports(t, dir)

	broken := io.MultiReader(strings.NewReader(input), iotest.ErrReader(io.ErrUnexpectedEOF))
	result, err := importer.PreviewUpload(t.Context(), broken, dir, opts)
	if err == nil || !strings.HasPrefix(err.Error(), sourceReadFailedCode) || !reflect.DeepEqual(result, SourceImportReport{}) {
		t.Fatal("dry run returned output after an interrupted transfer", err)
	}
	opts.ExpectedSHA256 = strings.Repeat("a", 64)
	result, err = importer.PreviewUpload(t.Context(), strings.NewReader(input), dir, opts)
	if err == nil || err.Error() != "DATASET_SOURCE_CHANGED" || !reflect.DeepEqual(result, SourceImportReport{}) {
		t.Fatal("dry run returned output after a digest mismatch", err)
	}
	assertNoImports(t, dir)
}
