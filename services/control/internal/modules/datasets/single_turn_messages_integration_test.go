//go:build dataset_processor_integration

package datasets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/google/uuid"
)

func messagesLine(t *testing.T, row wire.Record) string {
	t.Helper()
	raw, err := json.Marshal(struct {
		SourceID string              `json:"source_id"`
		Messages []map[string]string `json:"messages"`
	}{
		SourceID: row.SourceID,
		Messages: []map[string]string{
			{"role": "user", "content": row.Prompt},
			{"role": "assistant", "content": row.Completion},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestActualProcessorSingleTurnMessagesShapeAndBounds(t *testing.T) {
	p, _ := nativeProcessor(t)
	first := wire.Record{SourceID: "raw-a", Prompt: "  Ask \u4e2d\u6587\n", Completion: "\t Preserve this answer  "}
	last := wire.Record{SourceID: "raw-b", Prompt: "Last valid prompt", Completion: "Last answer"}
	overlong := wire.Record{SourceID: "text-limit", Prompt: strings.Repeat("x", int(p.policy.Limits.MaximumTextBytes)+1), Completion: "reply"}
	lines := []struct{ raw, code string }{
		{messagesLine(t, first), ""},
		{`{"source_id":"reversed","messages":[{"role":"assistant","content":"reply"},{"role":"user","content":"prompt"}]}`, "DATASET_MESSAGE_SEQUENCE_INVALID"},
		{`{"source_id":"system","messages":[{"role":"system","content":"instruction"},{"role":"user","content":"prompt"},{"role":"assistant","content":"reply"}]}`, "DATASET_MESSAGE_SEQUENCE_INVALID"},
		{`{"source_id":"multi","messages":[{"role":"user","content":"one"},{"role":"assistant","content":"first"},{"role":"user","content":"two"},{"role":"assistant","content":"second"}]}`, "DATASET_MESSAGE_SEQUENCE_INVALID"},
		{`{"source_id":"tool","messages":[{"role":"user","content":"prompt"},{"role":"tool","content":"reply"}]}`, "DATASET_MESSAGE_SEQUENCE_INVALID"},
		{`{"source_id":"image","messages":[{"role":"user","content":[{"type":"text","text":"prompt"}]},{"role":"assistant","content":"reply"}]}`, "DATASET_TEXT_INVALID"},
		{`{"source_id":"preference","prompt":"prompt","chosen":"one","rejected":"two"}`, "DATASET_FIELDS_INVALID"},
		{`{"source_id":"extra","messages":[{"role":"user","content":"prompt"},{"role":"assistant","content":"reply","tool_calls":[]}]}`, "DATASET_FIELDS_INVALID"},
		{messagesLine(t, overlong), "DATASET_TEXT_INVALID"},
		{strings.Repeat("x", int(p.policy.Limits.MaximumLineBytes)+32), "DATASET_LINE_TOO_LARGE"},
		{messagesLine(t, last), ""},
		{`{"source_id":"truncated","messages":[{"role":"user","content":"partial"}`, "DATASET_JSON_INVALID"},
	}
	var source strings.Builder
	wantErrors := make(map[int64]string)
	for i, line := range lines {
		if i > 0 {
			source.WriteString("\r\n")
		}
		source.WriteString(line.raw)
		if line.code != "" {
			wantErrors[int64(i+1)] = line.code
		}
	}
	request := attempt(t, p, source.String(), false)
	request.SourceRowFormat = wire.SingleTurnMessagesV1
	result, err := p.Execute(t.Context(), request)
	if err != nil || result.Status != "invalid" || result.SourceRowFormat != wire.SingleTurnMessagesV1 ||
		result.SourceBytes != int64(source.Len()) || result.SourceSHA256 != request.InputSHA256 ||
		result.TotalRows != int64(len(lines)) || result.InvalidRows != int64(len(wantErrors)) || result.ErrorsTruncated || result.FatalSourceError {
		t.Fatalf("invalid inspection or source identity: %+v, %v", result, err)
	}
	if len(result.Rows) != 2 || result.Rows[0].LineNumber != 1 || result.Rows[0].Record() != first ||
		result.Rows[1].LineNumber != 11 || result.Rows[1].Record() != last {
		t.Fatalf("content changed or a row was lost after the oversized line: %+v", result.Rows)
	}
	if len(result.Errors) != len(wantErrors) {
		t.Fatalf("wrong diagnostics count: %+v", result.Errors)
	}
	for _, issue := range result.Errors {
		if wantErrors[issue.LineNumber] != issue.Code {
			t.Fatalf("line %d returned %s, wanted %s", issue.LineNumber, issue.Code, wantErrors[issue.LineNumber])
		}
	}

	legacy := attempt(t, p, row(first.SourceID, first.Prompt, first.Completion), false)
	legacyResult, err := p.Execute(t.Context(), legacy)
	if err != nil || legacyResult.Status != "valid" || legacyResult.SourceRowFormat != "" ||
		len(legacyResult.Rows) != 1 || legacyResult.Rows[0].Record() != first {
		t.Fatalf("legacy inspection changed: %+v, %v", legacyResult, err)
	}
	withoutSelection := attempt(t, p, messagesLine(t, first), false)
	unselected, err := p.Execute(t.Context(), withoutSelection)
	if err != nil || unselected.Status != "invalid" || len(unselected.Errors) != 1 ||
		unselected.Errors[0].Code != "DATASET_FIELDS_INVALID" {
		t.Fatalf("messages were inferred without an explicit selection: %+v, %v", unselected, err)
	}
}

func TestActualSingleTurnMessagesRetryPublishAndSourceTrace(t *testing.T) {
	s, _, actor := workerScenario(t)
	ctx := t.Context()
	workspace := actor.Me.Workspace.ID
	data, err := s.Create(ctx, actor, CreateRequest{
		WorkspaceID: workspace, Name: "Single-turn fixture", Origin: "Synthetic local messages",
		License: "Synthetic CC0", RightsAsserted: true, Reason: "Exercise bounded messages inspection",
	}, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}

	records := []wire.Record{
		{SourceID: "raw-a", Prompt: "  First message \u4e2d\u6587\n", Completion: "\tFirst reply  "},
		{SourceID: "raw-b", Prompt: "Where does the ocean end?", Completion: "At the shore."},
		{SourceID: "raw-c", Prompt: "Is the sidewalk wet?", Completion: "Check the weather."},
		{SourceID: "raw-d", Prompt: "Which bus stops here?", Completion: "Look at the posted timetable."},
	}
	var raw, effective strings.Builder
	for i, record := range records {
		if i > 0 {
			raw.WriteString("\r\n")
		}
		raw.WriteString(" \t" + messagesLine(t, record))
		canonical, err := record.Canonical(s.policy().Limits)
		if err != nil {
			t.Fatal(err)
		}
		effective.Write(canonical)
		effective.WriteByte('\n')
	}
	digest := sha256.Sum256([]byte(raw.String()))
	sourceSHA := hex.EncodeToString(digest[:])
	digest = sha256.Sum256([]byte(effective.String()))
	effectiveSHA := hex.EncodeToString(digest[:])

	for _, testPoints := range []int{0, 2000} {
		draft, err := s.CreateDraft(ctx, actor, workspace, data.ID, DraftRequest{
			SourceName: "messages.jsonl", ExpectedBytes: int64(raw.Len()), SourceSHA256: sourceSHA, Reason: "Retain exact uploaded messages",
		}, uuid.NewString(), uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.UploadWithSourceRowFormat(ctx, actor, workspace, data.ID, draft.ID, sourceSHA, int64(raw.Len()), nil, "preference", uuid.NewString()); err == nil || err.Error() != "VALIDATION_FAILED" {
			t.Fatal("unsupported training choice reached the upload path", err)
		}
		draft, err = s.UploadWithSourceRowFormat(ctx, actor, workspace, data.ID, draft.ID, sourceSHA, int64(raw.Len()), strings.NewReader(raw.String()), wire.SingleTurnMessagesV1, uuid.NewString())
		if err != nil || draft.CurrentJobID == nil || draft.State != "validating" {
			t.Fatalf("upload failed to queue protected inspection: %+v, %v", draft, err)
		}
		jobID := *draft.CurrentJobID
		if testPoints == 0 {
			processor := s.Processor.configuration.ProcessorFile
			fixtureProgram(t, s.Processor, "raise SystemExit(9)\n")
			process(t, s)
			failed, err := s.GetJob(ctx, actor, workspace, data.ID, jobID)
			if err != nil || failed.State != "failed" || !failed.Retryable {
				t.Fatal("the child failure did not leave a retryable import", err)
			}
			s.Processor.configuration.ProcessorFile = processor
			retry, err := s.RetryJob(ctx, actor, workspace, data.ID, jobID, "Retry the frozen source format", uuid.NewString(), uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec(ctx, "UPDATE dataset_jobs SET not_before=clock_timestamp() WHERE id=$1", retry.ID); err != nil {
				t.Fatal(err)
			}
		}
		process(t, s)
		draft, err = s.GetDraft(ctx, actor, workspace, data.ID, draft.ID)
		if err != nil || draft.State != "editing" || draft.InvalidRows == nil || *draft.InvalidRows != 0 || draft.SourceSHA256 != sourceSHA {
			t.Fatalf("inspection or retry lost the selected source format: %+v, %v", draft, err)
		}
		rows, err := s.RowPage(ctx, actor, workspace, data.ID, draft.ID, url.Values{})
		if err != nil || len(rows.Items) != len(records) {
			t.Fatal("missing inspected rows", err)
		}
		for i, observed := range rows.Items {
			record := records[i]
			if observed.LineNumber != int64(i+1) || observed.SourceID == nil || *observed.SourceID != record.SourceID ||
				observed.Prompt == nil || *observed.Prompt != record.Prompt || observed.Completion == nil || *observed.Completion != record.Completion {
				t.Fatalf("draft row %d changed: %+v", i+1, observed)
			}
		}

		input := PublicationRequest{ExpectedRevision: draft.Revision, Seed: threeWaySeed, ValidationBasisPoints: 2000, Reason: "Publish canonical training rows"}
		publication, err := s.PublishWithOptions(ctx, actor, workspace, data.ID, draft.ID, input, PublicationOptions{TestBasisPoints: testPoints}, uuid.NewString(), uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		process(t, s)
		version, err := s.GetVersion(ctx, actor, workspace, data.ID, *publication.VersionID)
		if err != nil || version.RawSourceSHA256 != sourceSHA || version.EffectiveSourceSHA256 != effectiveSHA ||
			version.Manifest.TestBasisPoints != testPoints || version.Manifest.TotalRows != int64(len(records)) {
			t.Fatalf("publication changed source provenance or split mode: %+v, %v", version, err)
		}
		if replay, err := s.UploadWithSourceRowFormat(ctx, actor, workspace, data.ID, draft.ID, sourceSHA, int64(raw.Len()), nil, wire.SingleTurnMessagesV1, uuid.NewString()); err != nil || replay.ID != draft.ID || replay.State != "published" {
			t.Fatal("exact replay failed after publication", err)
		}
		if _, err := s.Upload(ctx, actor, workspace, data.ID, draft.ID, sourceSHA, int64(raw.Len()), nil, uuid.NewString()); err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
			t.Fatal("legacy replay reinterpreted the attached bytes", err)
		}
		if got := threeWayAsset(t, s, actor, version, "samples.jsonl"); !bytes.Equal(got, []byte(effective.String())) {
			t.Fatal("the published rows do not match the frozen training schema")
		}
		inputs, err := trainingInputsFromVersion(version)
		if err != nil || s.VerifyTrainingInputSnapshot(ctx, inputs) != nil || inputs.Train.Path != "train.jsonl" || inputs.Validation.Path != "validation.jsonl" {
			t.Fatal("the existing training consumer could not verify the converted data", err)
		}
		if testPoints != 0 {
			threeWayAsset(t, s, actor, version, "test.jsonl")
		}

		var object string
		if err = s.DB.QueryRow(ctx, "SELECT source_object_id::text FROM dataset_drafts WHERE id=$1", draft.ID).Scan(&object); err != nil {
			t.Fatal(err)
		}
		source, err := s.Store.Source(object, sourceSHA, int64(raw.Len()))
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(source)
		if err != nil || !bytes.Equal(original, []byte(raw.String())) {
			t.Fatal("original uploaded bytes were rewritten", err)
		}
		mapping, err := os.ReadFile(filepath.Join(s.Store.path, versionDirectory(version.ID, version.AttemptID), "mapping.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		mappingDigest := sha256.Sum256(mapping)
		if hex.EncodeToString(mappingDigest[:]) != version.RowMappingSHA256 {
			t.Fatal("published row mapping digest did not bind the saved map")
		}
		decoder := json.NewDecoder(bytes.NewReader(mapping))
		for i, inspected := range rows.Items {
			var entry rowMapping
			err := decoder.Decode(&entry)
			if err != nil || entry.EffectiveLineNumber != int64(i+1) || entry.OriginalLineNumber != inspected.LineNumber ||
				entry.RowID != inspected.ID || entry.RowRevision != inspected.Revision {
				t.Fatalf("effective line %d lost its original row and line: %+v, %v", i+1, entry, err)
			}
		}
		var extra rowMapping
		if err := decoder.Decode(&extra); err != io.EOF {
			t.Fatal("row mapping had extra entries", err)
		}
	}
}
