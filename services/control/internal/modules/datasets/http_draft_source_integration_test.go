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
	"os/exec"
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

func verifyDraftSourceHTTPS(t *testing.T, handler *datasethttp.Handler, portal, admin *httpfixture.Client, root, workspace, dataset, password string, parquet *datasets.ParquetImportConfig) {
	t.Helper()
	ctx := t.Context()
	service := handler.Service
	// The candidate table is outside the published catalog fingerprint.
	// Install it only after the released readiness checks have run.
	schema, err := os.ReadFile("testdata/draft_import_candidate.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.DB.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	csv := draftSourceFixture{
		format: datasets.SourceCSV, media: "text/csv; charset=UTF-8",
		raw:     []byte("\xef\xbb\xbfsource_id,prompt,completion\r\nfirst,\"Who, me?\",\"Yes.\r\nAgain.\"\r\nsecond,Color,Blue\r\n"),
		invalid: []byte("source_id,prompt,completion\na,,b\n"),
	}
	verifyDraftSourceFormat(t, handler, portal, admin, root, workspace, dataset, password, parquet, csv)
	if parquet != nil {
		binary := draftSourceFixture{
			format: datasets.SourceParquet, media: "application/vnd.apache.parquet",
			raw:     writeParquetDraftFixture(t, parquet.PythonExecutable, false),
			invalid: writeParquetDraftFixture(t, parquet.PythonExecutable, true),
		}
		verifyDraftSourceFormat(t, handler, portal, admin, root, workspace, dataset, password, parquet, binary)
	}
}

type draftSourceFixture struct {
	format       datasets.SourceFormat
	media        string
	raw, invalid []byte
}

func verifyDraftSourceFormat(t *testing.T, handler *datasethttp.Handler, portal, admin *httpfixture.Client, root, workspace, dataset, password string, parquet *datasets.ParquetImportConfig, source draftSourceFixture) {
	t.Helper()
	t.Logf("verify %s source custody and workspace admission", source.format)
	ctx := t.Context()
	service := handler.Service
	auth := fixture.Login(t, service.Identity, "admin@example.invalid", password, "portal")
	path := "/api/v1/workspaces/" + workspace + "/datasets/" + dataset
	uploadSuffix := "/drafts/from-" + string(source.format)
	upload := path + uploadSuffix
	raw := source.raw
	extension := "." + string(source.format)
	sourceDigest := sha256.Sum256(raw)
	input := datasets.DraftSourceRequest{Name: "original" + extension, SHA256: hex.EncodeToString(sourceDigest[:]), Bytes: int64(len(raw))}
	intent := uuid.NewString()
	uploadHeaders := map[string]string{
		"Idempotency-Key": intent, "Content-Type": source.media,
		"X-Content-SHA256": input.SHA256, "X-Dataset-Source-Name": input.Name,
	}
	snapshot := func() [5]int {
		t.Helper()
		var counts [5]int
		err := service.DB.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM dataset_drafts WHERE dataset_id=$1),
			       (SELECT count(*) FROM dataset_jobs WHERE dataset_id=$1),
			       (SELECT count(*) FROM dataset_draft_import_candidates WHERE dataset_id=$1),
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
		"name": "Other " + string(source.format) + " workspace", "owner_user_id": auth.Me.User.ID,
	}, 201, "")["id"].(string)
	foreignUpload := strings.Replace(upload, "/workspaces/"+workspace+"/", "/workspaces/"+foreign+"/", 1)
	portal.Call("POST", foreignUpload, raw, 404, "Problem")
	foreignReader := &observedDraftSourceReader{err: io.ErrUnexpectedEOF}
	if _, err := handler.DraftSourceCandidate.Import(ctx, auth, foreign, dataset, source.format, input, intent, foreignReader, uuid.NewString()); err == nil || foreignReader.read {
		t.Fatal("the foreign workspace request consumed the source", err)
	}

	prefix := raw[:len(raw)-8]
	broken := &observedDraftSourceReader{data: prefix, err: io.ErrUnexpectedEOF}
	out, err := handler.DraftSourceCandidate.Import(ctx, auth, workspace, dataset, source.format, input, intent, broken, uuid.NewString())
	if err == nil || err.Error() != "DATASET_UPLOAD_INCOMPLETE" || !broken.read || !reflect.DeepEqual(out, datasets.DraftSourceResult{}) {
		t.Fatal("the interrupted source returned a draft or report", err)
	}
	if counts := snapshot(); counts != baseline {
		t.Fatalf("interrupted stream changed the database or store: %v -> %v", baseline, counts)
	}

	if _, err = service.DB.Exec(ctx, "ALTER TABLE dataset_draft_import_candidates ADD CONSTRAINT fixture_refuse_report CHECK (false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	out, err = handler.DraftSourceCandidate.Import(ctx, auth, workspace, dataset, source.format, input, intent, bytes.NewReader(raw), uuid.NewString())
	if err == nil || err.Error() != "STORAGE_CHECK_CONFLICT" || !reflect.DeepEqual(out, datasets.DraftSourceResult{}) {
		t.Fatal("report insertion failure returned a draft or report", err)
	}
	if counts := snapshot(); counts != baseline {
		t.Fatalf("failed transaction changed the database or store: %v -> %v", baseline, counts)
	}
	if _, err = service.DB.Exec(ctx, "ALTER TABLE dataset_draft_import_candidates DROP CONSTRAINT fixture_refuse_report"); err != nil {
		t.Fatal(err)
	}
	invalid := source.invalid
	invalidDigest := sha256.Sum256(invalid)
	uploadHeaders["X-Content-SHA256"] = hex.EncodeToString(invalidDigest[:])
	rejected := portal.Call("POST", upload, invalid, 422, "Problem")
	if rejected["code"] != "DATASET_VALIDATION_FAILED" || snapshot() != baseline {
		t.Fatal("invalid legacy row became visible")
	}
	uploadHeaders["X-Content-SHA256"] = input.SHA256
	// Keep the first request between authorization and commit while HTTPS
	// completes the same intent. The slower attempt must discard its files.
	paused := &pausedDraftSourceReader{
		Reader: bytes.NewReader(raw), started: make(chan struct{}), release: make(chan struct{}),
	}
	unblock := sync.OnceFunc(func() { close(paused.release) })
	type outcome struct {
		result datasets.DraftSourceResult
		err    error
	}
	finished := make(chan outcome, 1)
	var attempt sync.WaitGroup
	attempt.Go(func() {
		result, err := handler.DraftSourceCandidate.Import(ctx, auth, workspace, dataset, source.format, input, intent, paused, uuid.NewString())
		finished <- outcome{result, err}
	})
	defer func() {
		unblock()
		attempt.Wait()
	}()
	select {
	case <-paused.started:
	case early := <-finished:
		t.Fatal("first attempt never started reading", early.err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first source attempt")
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
		t.Fatal("the concurrent source attempt did not finish")
	}
	if counts := snapshot(); counts != expected {
		t.Fatalf("the concurrent attempt committed or kept a second source: %v", counts)
	}
	var object, jobDigest, storedFormat string
	if err = service.DB.QueryRow(ctx, `SELECT d.source_object_id::text,j.input_sha256,c.source_format
		FROM dataset_drafts d JOIN dataset_jobs j ON j.id=d.current_job_id
		JOIN dataset_draft_import_candidates c ON c.draft_id=d.id WHERE d.id=$1`, draftID).Scan(&object, &jobDigest, &storedFormat); err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join(root, "objects", object, "source"+extension)
	stored, err := os.ReadFile(originalPath)
	if err != nil || !bytes.Equal(stored, raw) || jobDigest != report["output_sha256"] || storedFormat != string(source.format) {
		t.Fatal("committed object lost the exact source bytes or inspection digest", err)
	}
	replay := portal.Call("POST", upload, raw, 202, "")
	if !reflect.DeepEqual(replay, result) || snapshot() != expected {
		t.Fatal("HTTPS replay changed the committed import")
	}
	uploadHeaders["X-Dataset-Source-Name"] = "different" + extension
	conflict := portal.Call("POST", upload, raw, 409, "Problem")
	if conflict["code"] != "IDEMPOTENCY_CONFLICT" || snapshot() != expected {
		t.Fatal("a changed declaration reused a committed import key")
	}
	uploadHeaders["X-Dataset-Source-Name"] = input.Name
	portal.Call("POST", foreignUpload, raw, 404, "Problem")

	worked, err := service.ProcessNext(ctx)
	if err != nil || !worked {
		t.Fatal("run the real source draft inspection", err)
	}
	draftPath := path + "/drafts/" + draftID
	draft = portal.Call("GET", draftPath, nil, 200, "DatasetDraftResponse")
	if draft["state"] != "editing" || draft["total_rows"] != float64(2) || draft["invalid_rows"] != float64(0) {
		t.Fatal("legacy inspection did not open a usable source draft", draft["state"], draft["error_code"])
	}
	rows := portal.Call("GET", draftPath+"/rows?limit=2", nil, 200, "DatasetRowPageResponse")["items"].([]any)
	first := rows[0].(map[string]any)
	if first["prompt"] != "Who, me?" || first["completion"] != "Yes.\nAgain." {
		t.Fatal("multiline field did not survive inspection")
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

	reopened, err := datasets.NewDraftSourceCandidate(service, parquet)
	if err != nil {
		t.Fatal(err)
	}
	unread := &observedDraftSourceReader{err: iotest.ErrTimeout}
	out, err = reopened.Import(ctx, auth, workspace, dataset, source.format, input, intent, unread, uuid.NewString())
	if err != nil || unread.read || out.Draft.ID != draftID || out.Draft.State != "editing" || out.Report.SourceSHA256 != input.SHA256 {
		t.Fatal("the report could not be reloaded independently of the upload body", err)
	}

	// The same owner, body, digest and key can create an independent draft
	// when the dataset is actually in the second authorized workspace.
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	foreignBase := "/api/v1/workspaces/" + foreign + "/datasets"
	foreignDataset := portal.Write("POST", foreignBase, map[string]any{
		"name": "Independent " + string(source.format) + " source", "origin": "Synthetic fixture",
		"license": "Synthetic CC0", "rights_asserted": true, "reason": "Verify workspace-local import keys",
	}, 201, "DatasetResponse")["id"].(string)
	portal.Headers = uploadHeaders
	otherUpload := foreignBase + "/" + foreignDataset + uploadSuffix
	other := portal.Call("POST", otherUpload, raw, 202, "")
	otherDraft := other["draft"].(map[string]any)
	if otherDraft["id"] == draftID || otherDraft["workspace_id"] != foreign || otherDraft["dataset_id"] != foreignDataset ||
		!reflect.DeepEqual(other["report"], report) {
		t.Fatal("identical cross-workspace requests did not create independent scoped drafts")
	}
	if repeated := portal.Call("POST", otherUpload, raw, 202, ""); !reflect.DeepEqual(other, repeated) {
		t.Fatal("the second workspace did not replay its own draft")
	}
	var otherObject string
	if err = service.DB.QueryRow(ctx, `SELECT source_object_id::text FROM dataset_drafts WHERE id=$1 AND workspace_id=$2`,
		otherDraft["id"], foreign).Scan(&otherObject); err != nil || otherObject == object {
		t.Fatal("workspace drafts shared source custody", err)
	}
	worked, err = service.ProcessNext(ctx)
	if err != nil || !worked {
		t.Fatal("run inspection for the second workspace draft", err)
	}

}

type observedDraftSourceReader struct {
	data []byte
	err  error
	read bool
}

func (r *observedDraftSourceReader) Read(p []byte) (int, error) {
	r.read = true
	return copy(p, r.data), r.err
}

type pausedDraftSourceReader struct {
	io.Reader
	started, release chan struct{}
	once             sync.Once
}

func (r *pausedDraftSourceReader) Read(p []byte) (int, error) {
	r.once.Do(func() {
		close(r.started)
		<-r.release
	})
	return r.Reader.Read(p)
}

func parquetDraftFixtureConfig(t *testing.T, decoderDirectory string) *datasets.ParquetImportConfig {
	t.Helper()
	python := os.Getenv("OWNSOLID_TEST_PARQUET_PYTHON")
	if python == "" {
		return nil
	}
	return &datasets.ParquetImportConfig{
		PythonExecutable: python, DecoderFile: filepath.Join(decoderDirectory, "import_parquet.py"),
		BatchRows: 1, MetadataBytes: 1 << 20, RowGroupBytes: 8 << 20, MemoryBytes: 512 << 20,
	}
}

func writeParquetDraftFixture(t *testing.T, python string, invalid bool) []byte {
	t.Helper()
	script, err := filepath.Abs("testdata/draft_source_fixture.py")
	if err != nil {
		t.Fatal(err)
	}
	mode := "valid"
	if invalid {
		mode = "invalid"
	}
	output, err := exec.CommandContext(t.Context(), python, "-I", "-B", script, mode).Output()
	if err != nil {
		t.Fatalf("write real Parquet fixture: %v", err)
	}
	return output
}
