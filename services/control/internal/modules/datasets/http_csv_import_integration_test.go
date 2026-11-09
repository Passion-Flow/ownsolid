//go:build dataset_processor_integration

package datasets_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	datasethttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func verifyCSVImportHTTPS(t *testing.T, handler *datasethttp.Handler, portal, admin *httpfixture.Client, root, workspace, dataset, password string) {
	t.Helper()
	ctx := t.Context()
	service := handler.Service
	// The candidate table is outside the published catalog fingerprint.
	// Install it only after the released readiness checks have run.
	schema, err := os.ReadFile("testdata/csv_import_candidate.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.DB.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	auth := fixture.Login(t, service.Identity, "admin@example.invalid", password, "portal")
	path := "/api/v1/workspaces/" + workspace + "/datasets/" + dataset
	upload := path + "/drafts/from-csv"
	raw := []byte("\xef\xbb\xbfsource_id,prompt,completion\r\nfirst,\"Who, me?\",\"Yes.\r\nAgain.\"\r\nsecond,Color,Blue\r\n")
	sourceDigest := sha256.Sum256(raw)
	input := datasets.CSVImportRequest{Name: "original.csv", SHA256: hex.EncodeToString(sourceDigest[:]), Bytes: int64(len(raw))}
	intent := uuid.NewString()
	uploadHeaders := map[string]string{
		"Idempotency-Key": intent, "Content-Type": "text/csv; charset=UTF-8",
		"X-Content-SHA256": input.SHA256, "X-Dataset-Source-Name": input.Name,
	}
	snapshot := func() [5]int {
		t.Helper()
		var counts [5]int
		err := service.DB.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM dataset_drafts WHERE dataset_id=$1),
			       (SELECT count(*) FROM dataset_jobs WHERE dataset_id=$1),
			       (SELECT count(*) FROM dataset_csv_import_candidates WHERE dataset_id=$1),
			       (SELECT count(*) FROM audit_events WHERE workspace_id=$2 AND action IN ('dataset.draft_created','dataset.source_received'))`,
			dataset, workspace).Scan(&counts[0], &counts[1], &counts[2], &counts[3])
		if err != nil {
			t.Fatal(err)
		}
		objects, err := os.ReadDir(filepath.Join(root, "objects"))
		if err != nil {
			t.Fatal(err)
		}
		counts[4] = len(objects)
		return counts
	}
	baseline := snapshot()
	portal.Headers = uploadHeaders
	defer func() { portal.Headers = map[string]string{} }()
	csrf := portal.CSRF
	portal.CSRF = ""
	portal.Call("POST", upload, raw, 403, "Problem")
	portal.CSRF = csrf
	disabled := datasethttp.Handler{Service: service}
	request := httptest.NewRequest(http.MethodPost, upload, bytes.NewReader(raw))
	for name, value := range uploadHeaders {
		request.Header.Set(name, value)
	}
	if _, _, err := disabled.Handle(request, auth, uuid.NewString()); err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatal("the isolated route was registered by default", err)
	}

	foreign := admin.Write("POST", "/api/v1/admin/workspaces", map[string]any{
		"name": "Other CSV workspace", "owner_user_id": auth.Me.User.ID,
	}, 201, "")["id"].(string)
	foreignUpload := strings.Replace(upload, "/workspaces/"+workspace+"/", "/workspaces/"+foreign+"/", 1)
	portal.Call("POST", foreignUpload, raw, 404, "Problem")
	foreignReader := &observedCSVReader{err: io.ErrUnexpectedEOF}
	if _, err := handler.CSVImportCandidate.Import(ctx, auth, foreign, dataset, input, intent, foreignReader, uuid.NewString()); err == nil || foreignReader.read {
		t.Fatal("the foreign workspace request consumed the source", err)
	}

	prefix := raw[:bytes.Index(raw, []byte("second,"))]
	broken := &observedCSVReader{data: prefix, err: io.ErrUnexpectedEOF}
	out, err := handler.CSVImportCandidate.Import(ctx, auth, workspace, dataset, input, intent, broken, uuid.NewString())
	if err == nil || err.Error() != "DATASET_UPLOAD_INCOMPLETE" || !broken.read || !reflect.DeepEqual(out, datasets.CSVImportResult{}) {
		t.Fatal("the interrupted source returned a draft or report", err)
	}
	if counts := snapshot(); counts != baseline {
		t.Fatalf("interrupted stream changed the database or store: %v -> %v", baseline, counts)
	}

	if _, err = service.DB.Exec(ctx, "ALTER TABLE dataset_csv_import_candidates ADD CONSTRAINT fixture_refuse_report CHECK (false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	out, err = handler.CSVImportCandidate.Import(ctx, auth, workspace, dataset, input, intent, bytes.NewReader(raw), uuid.NewString())
	if err == nil || err.Error() != "STORAGE_CHECK_CONFLICT" || !reflect.DeepEqual(out, datasets.CSVImportResult{}) {
		t.Fatal("report insertion failure returned a draft or report", err)
	}
	if counts := snapshot(); counts != baseline {
		t.Fatalf("failed transaction changed the database or store: %v -> %v", baseline, counts)
	}
	if _, err = service.DB.Exec(ctx, "ALTER TABLE dataset_csv_import_candidates DROP CONSTRAINT fixture_refuse_report"); err != nil {
		t.Fatal(err)
	}
	invalid := []byte("source_id,prompt,completion\na,,b\n")
	invalidDigest := sha256.Sum256(invalid)
	uploadHeaders["X-Content-SHA256"] = hex.EncodeToString(invalidDigest[:])
	rejected := portal.Call("POST", upload, invalid, 422, "Problem")
	if rejected["code"] != "DATASET_VALIDATION_FAILED" || snapshot() != baseline {
		t.Fatal("invalid legacy row became visible")
	}
	uploadHeaders["X-Content-SHA256"] = input.SHA256
	// Keep the first request between authorization and commit while HTTPS
	// completes the same intent. The slower attempt must discard its files.
	paused := &pausedCSVReader{
		Reader: bytes.NewReader(raw), started: make(chan struct{}), release: make(chan struct{}),
	}
	unblock := sync.OnceFunc(func() { close(paused.release) })
	defer unblock()
	type outcome struct {
		result datasets.CSVImportResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := handler.CSVImportCandidate.Import(ctx, auth, workspace, dataset, input, intent, paused, uuid.NewString())
		finished <- outcome{result, err}
	}()
	select {
	case <-paused.started:
	case early := <-finished:
		t.Fatal("first attempt never started reading", early.err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first CSV attempt")
	}
	result := portal.Call("POST", upload, raw, 202, "")
	draft, report := result["draft"].(map[string]any), result["report"].(map[string]any)
	draftID := draft["id"].(string)
	if report["source_sha256"] != input.SHA256 || report["source_bytes"] != float64(len(raw)) ||
		report["rows"] != float64(2) || report["rejected_rows"] != float64(0) ||
		report["output_sha256"] != draft["source_sha256"] || report["output_bytes"] != draft["expected_bytes"] ||
		draft["workspace_id"] != workspace || draft["dataset_id"] != dataset || draft["state"] != "validating" {
		t.Fatal("draft and report do not bind the same source")
	}
	expected := baseline
	for i, delta := range [5]int{1, 1, 1, 2, 1} {
		expected[i] += delta
	}
	unblock()
	select {
	case replay := <-finished:
		if replay.err != nil || replay.result.Draft.ID != draftID || replay.result.Report.SourceSHA256 != input.SHA256 {
			t.Fatal("the concurrent attempt failed to replay the winner", replay.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the concurrent CSV attempt did not finish")
	}
	if counts := snapshot(); counts != expected {
		t.Fatalf("the concurrent attempt committed or kept a second source: %v", counts)
	}
	var object, jobDigest string
	if err = service.DB.QueryRow(ctx, `SELECT d.source_object_id::text,j.input_sha256
		FROM dataset_drafts d JOIN dataset_jobs j ON j.id=d.current_job_id WHERE d.id=$1`, draftID).Scan(&object, &jobDigest); err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join(root, "objects", object, "source.csv")
	stored, err := os.ReadFile(originalPath)
	if err != nil || !bytes.Equal(stored, raw) || jobDigest != report["output_sha256"] {
		t.Fatal("committed object lost the exact source bytes or inspection digest", err)
	}
	replay := portal.Call("POST", upload, raw, 202, "")
	if !reflect.DeepEqual(replay, result) || snapshot() != expected {
		t.Fatal("HTTPS replay changed the committed import")
	}
	uploadHeaders["X-Dataset-Source-Name"] = "different.csv"
	conflict := portal.Call("POST", upload, raw, 409, "Problem")
	if conflict["code"] != "IDEMPOTENCY_CONFLICT" || snapshot() != expected {
		t.Fatal("a changed declaration reused a committed import key")
	}
	uploadHeaders["X-Dataset-Source-Name"] = input.Name
	portal.Call("POST", foreignUpload, raw, 404, "Problem")

	worked, err := service.ProcessNext(ctx)
	if err != nil || !worked {
		t.Fatal("run the real CSV draft inspection", err)
	}
	draftPath := path + "/drafts/" + draftID
	draft = portal.Call("GET", draftPath, nil, 200, "DatasetDraftResponse")
	if draft["state"] != "editing" || draft["total_rows"] != float64(2) || draft["invalid_rows"] != float64(0) {
		t.Fatal("legacy inspection did not open a usable CSV draft", draft["state"], draft["error_code"])
	}
	rows := portal.Call("GET", draftPath+"/rows?limit=2", nil, 200, "DatasetRowPageResponse")["items"].([]any)
	first := rows[0].(map[string]any)
	if first["prompt"] != "Who, me?" || first["completion"] != "Yes.\nAgain." {
		t.Fatal("CSV quoting or multiline field did not survive inspection")
	}

	changed := bytes.Clone(raw)
	changed[len(changed)-3] ^= 1
	if err = os.WriteFile(originalPath, changed, 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := portal.Call("POST", upload, raw, 409, "Problem")
	if corrupt["code"] != "DATASET_FILE_INVALID" {
		t.Fatal("replay returned a report for altered original bytes")
	}
	if err = os.WriteFile(originalPath, raw, 0600); err != nil {
		t.Fatal(err)
	}

	reopened, err := datasets.NewCSVImportCandidate(service)
	if err != nil {
		t.Fatal(err)
	}
	unread := &observedCSVReader{err: iotest.ErrTimeout}
	out, err = reopened.Import(ctx, auth, workspace, dataset, input, intent, unread, uuid.NewString())
	if err != nil || unread.read || out.Draft.ID != draftID || out.Draft.State != "editing" || out.Report.SourceSHA256 != input.SHA256 {
		t.Fatal("the report could not be reloaded independently of the upload body", err)
	}
}

type observedCSVReader struct {
	data []byte
	err  error
	read bool
}

func (r *observedCSVReader) Read(p []byte) (int, error) {
	r.read = true
	return copy(p, r.data), r.err
}

type pausedCSVReader struct {
	io.Reader
	started, release chan struct{}
	once             sync.Once
}

func (r *pausedCSVReader) Read(p []byte) (int, error) {
	r.once.Do(func() {
		close(r.started)
		<-r.release
	})
	return r.Reader.Read(p)
}
