package datasets

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSourceTrainingFormats(t *testing.T) {
	importer := testImporter(t, testImportLimits(), nil)
	for _, tc := range []struct {
		schema       SourceTrainingFormat
		valid        string
		invalid      string
		invalidField string
	}{
		{SourceTrainingText, `{"text":"Line 1\nLine 2","trace_id":"record-1"}`, `{"text":" "}`, "text"},
		{SourceTrainingMessages, `{"messages":[{"role":"system","content":"Be concise"},{"role":"user","content":"Hello"},{"role":"assistant","content":"Hi"}]}`,
			`{"messages":[{"role":"user","content":"Hello"},{"role":"tool","content":"Hi"}]}`, "messages[1].role"},
		{SourceTrainingMessages, `{"messages":[{"role":"user","content":"Hello"},{"role":"assistant","content":"Hi"}]}`,
			`{"messages":[{"role":"user","content":"Hello"},{"role":"assistant","content":null}]}`, "messages[1].content"},
		{SourceTrainingMessages, `{"messages":[{"role":"user","content":"Hello"},{"role":"assistant","content":"Hi"}]}`,
			`{"messages":[{"role":"user","content":"Hello"}]}`, "messages"},
		{SourceTrainingInstruction, `{"instruction":"Translate","input":null,"output":"\u4f60\u597d"}`, `{"instruction":"Translate","input":42,"output":"hello"}`, "input"},
		{SourceTrainingPreference, `{"prompt":"Is it ready?","chosen":" Yes","rejected":" Maybe"}`, `{"prompt":"Is it ready?","chosen":"Yes","rejected":"Yes"}`, "rejected"},
	} {
		t.Run(string(tc.schema)+"/"+tc.invalidField, func(t *testing.T) {
			dir := privateImportDir(t)
			destination := filepath.Join(dir, "rows.jsonl")
			input := tc.valid + "\n" + tc.invalid + "\n" + tc.valid + "\n"
			opts := SourceImportOptions{Format: SourceJSONL, TrainingFormat: tc.schema}
			want := SourceIssue{Code: "DATASET_TRAINING_ROW_INVALID", Line: 2, Field: tc.invalidField}
			_, err := importer.ImportUpload(t.Context(), strings.NewReader(input), destination, opts)
			if err != want {
				t.Fatalf("got %v, want %v", err, want)
			}
			assertNoImports(t, dir)
			opts.SkipInvalid = true
			report, err := importer.ImportUpload(t.Context(), strings.NewReader(input), destination, opts)
			if err != nil || report.TrainingFormat != tc.schema || report.Rows != 2 || report.RejectedRows != 1 ||
				len(report.Issues) != 1 || report.Issues[0] != want {
				t.Fatalf("report %+v, error %v", report, err)
			}
			var expected any
			if err := json.Unmarshal([]byte(tc.valid), &expected); err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(strings.TrimSuffix(readImported(t, destination), "\n"), "\n") {
				var got any
				if err := json.Unmarshal([]byte(line), &got); err != nil || !reflect.DeepEqual(got, expected) {
					t.Fatalf("accepted row changed: %q (%v)", line, err)
				}
			}
		})
	}
}

func TestSourceTrainingFormatSelectionIsIndependentOfFileSyntax(t *testing.T) {
	dir := privateImportDir(t)
	importer := testImporter(t, testImportLimits(), nil)
	path := filepath.Join(dir, "rows.jsonl")
	_, err := importer.ImportUpload(t.Context(), strings.NewReader("text\nhello\n"), path, SourceImportOptions{Format: SourceCSV, TrainingFormat: "guessed"})
	if err == nil || err.Error() != "DATASET_TRAINING_FORMAT_INVALID" {
		t.Fatalf("accepted unsupported format: %v", err)
	}
	assertNoImports(t, dir)
	report, err := importer.ImportUpload(t.Context(), strings.NewReader("text\nhello\n"), path, SourceImportOptions{Format: SourceCSV, TrainingFormat: SourceTrainingText})
	if err != nil || report.Rows != 1 || report.TrainingFormat != SourceTrainingText || readImported(t, path) != "{\"text\":\"hello\"}\n" {
		t.Fatalf("report %+v, error %v", report, err)
	}
}
