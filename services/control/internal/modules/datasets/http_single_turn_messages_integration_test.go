//go:build dataset_processor_integration

package datasets_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

const sourceRowFormatHeader = "Dataset-Source-Row-Format"

func verifySingleTurnHTTPSUpload(t *testing.T, service *datasets.Service, portal, admin *httpfixture.Client, path, adminPath string) {
	t.Helper()
	records := []wire.Record{
		{SourceID: "source-a", Prompt: "  Inspect \u4e2d\u6587\n", Completion: "\tAccepted.  "},
		{SourceID: "source-b", Prompt: "Name the largest ocean.", Completion: "Pacific."},
		{SourceID: "source-c", Prompt: "Which bus stops here?", Completion: "Check the posted timetable."},
	}
	var raw, effective bytes.Buffer
	for i, row := range records {
		line, err := json.Marshal(map[string]any{
			"source_id": row.SourceID,
			"messages": []map[string]string{
				{"role": "user", "content": row.Prompt},
				{"role": "assistant", "content": row.Completion},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 {
			raw.WriteString("\r\n")
		}
		raw.WriteString(" \t")
		raw.Write(line)
		line, err = row.Canonical(service.Identity.Config.Policy().Datasets.Limits)
		if err != nil {
			t.Fatal(err)
		}
		effective.Write(line)
		effective.WriteByte('\n')
	}
	draft := createHTTPSDatasetDraft(t, admin, adminPath, raw.Bytes())
	draftID := draft["id"].(string)
	sourcePath := adminPath + "/drafts/" + draftID + "/source"
	sourceDigest := sha256.Sum256(raw.Bytes())
	admin.Headers["Content-Type"] = "application/x-ndjson"
	admin.Headers["X-Content-SHA256"] = hex.EncodeToString(sourceDigest[:])
	format := string(wire.SingleTurnMessagesV1)
	for _, invalid := range []string{"", "preference", "Single_Turn_Messages_V1", format + ", " + format} {
		admin.Headers[sourceRowFormatHeader] = invalid
		problem := admin.Call("POST", sourcePath, raw.Bytes(), 400, "Problem")
		if problem["code"] != "VALIDATION_FAILED" {
			t.Fatalf("unsupported source format returned %v", problem["code"])
		}
	}
	admin.Headers[sourceRowFormatHeader] = format
	attached := admin.Call("POST", sourcePath, raw.Bytes(), 202, "DatasetDraftResponse")
	replay := admin.Call("POST", sourcePath, raw.Bytes(), 202, "DatasetDraftResponse")
	if attached["state"] != "validating" || replay["current_job_id"] != attached["current_job_id"] {
		t.Fatal("replaying a single-turn attachment did not retain the original inspection")
	}
	delete(admin.Headers, sourceRowFormatHeader)
	problem := admin.Call("POST", sourcePath, raw.Bytes(), 409, "Problem")
	if problem["code"] != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("omitting the frozen source format returned %v", problem["code"])
	}
	delete(admin.Headers, "Content-Type")
	delete(admin.Headers, "X-Content-SHA256")

	job := portal.Call("GET", path+"/jobs/"+attached["current_job_id"].(string), nil, 200, "DatasetJobResponse")
	retryHTTPSDatasetJobAfterProcessorExit(t, service, portal, path, job)
	draftPath := path + "/drafts/" + draftID
	draft = portal.Call("GET", draftPath, nil, 200, "DatasetDraftResponse")
	if draft["state"] != "editing" || draft["invalid_rows"] != float64(0) ||
		draft["source_sha256"] != hex.EncodeToString(sourceDigest[:]) {
		t.Fatal("retry changed the selected source interpretation")
	}
	rows := portal.Call("GET", draftPath+"/rows", nil, 200, "DatasetRowPageResponse")["items"].([]any)
	if len(rows) != len(records) {
		t.Fatalf("inspection returned %d rows, expected %d", len(rows), len(records))
	}
	for i, row := range rows {
		value := row.(map[string]any)
		if value["line_number"] != float64(i+1) || value["source_id"] != records[i].SourceID ||
			value["prompt"] != records[i].Prompt || value["completion"] != records[i].Completion {
			t.Fatalf("single-turn row %d changed during inspection", i+1)
		}
	}
	input := map[string]any{
		"expected_revision": draft["revision"], "seed": "00112233445566778899aabbccddeeff",
		"validation_basis_points": 2000, "test_basis_points": 2000, "reason": "Publish the canonical training rows",
	}
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	publication := portal.Call("POST", draftPath+"/publications", input, 202, "DatasetJobResponse")
	if worked, err := service.ProcessNext(t.Context()); err != nil || !worked {
		t.Fatal("publish the canonical single-turn rows", err)
	}
	versionPath := path + "/versions/" + publication["version_id"].(string)
	version := portal.Call("GET", versionPath, nil, 200, "DatasetVersionResponse")
	effectiveDigest := sha256.Sum256(effective.Bytes())
	if version["raw_source_sha256"] != hex.EncodeToString(sourceDigest[:]) ||
		version["effective_source_sha256"] != hex.EncodeToString(effectiveDigest[:]) {
		t.Fatal("single-turn publication did not bind both source forms")
	}
	manifest := readHTTPSDatasetManifest(t, version["manifest"])
	samples, _ := readHTTPSDatasetAsset(t, portal, versionPath, "samples", manifest)
	if samples != effective.String() {
		t.Fatal("published samples differ from the canonical prompt/completion rows")
	}
	checkHTTPSPartitionAssets(t, portal, versionPath, manifest)
	var object string
	if err := service.DB.QueryRow(t.Context(), "SELECT source_object_id::text FROM dataset_drafts WHERE id=$1", draftID).Scan(&object); err != nil {
		t.Fatal(err)
	}
	source, err := service.Store.Source(object, hex.EncodeToString(sourceDigest[:]), int64(raw.Len()))
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(original, raw.Bytes()) {
		t.Fatal("the original uploaded bytes changed", err)
	}

	legacy := prepareHTTPSPublicationDraft(t, service, portal, path, raw.Bytes())
	legacyPath := path + "/drafts/" + legacy["id"].(string)
	legacyRows := portal.Call("GET", legacyPath+"/rows", nil, 200, "DatasetRowPageResponse")["items"].([]any)
	if len(legacyRows) != len(records) || legacyRows[0].(map[string]any)["error_code"] != "DATASET_FIELDS_INVALID" {
		t.Fatal("omitting the header inferred a single-turn source")
	}

	bad := []byte(`{"source_id":"ok","messages":[{"role":"user","content":"Hello"},{"role":"assistant","content":"Hi"}]}
{"source_id":"bad","messages":[{"role":"assistant","content":"Hi"},{"role":"user","content":"Hello"}]}
`)
	portal.Headers[sourceRowFormatHeader] = format
	malformed := prepareHTTPSPublicationDraft(t, service, portal, path, bad)
	delete(portal.Headers, sourceRowFormatHeader)
	badPath := path + "/drafts/" + malformed["id"].(string)
	badRows := portal.Call("GET", badPath+"/rows", nil, 200, "DatasetRowPageResponse")["items"].([]any)
	if len(badRows) != 2 || badRows[1].(map[string]any)["line_number"] != float64(2) ||
		badRows[1].(map[string]any)["error_code"] != "DATASET_MESSAGE_SEQUENCE_INVALID" {
		t.Fatal("an invalid message sequence lost its physical line error")
	}
	input["expected_revision"] = malformed["revision"]
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	problem = portal.Call("POST", badPath+"/publications", input, 422, "Problem")
	if problem["code"] != "DATASET_VALIDATION_FAILED" {
		t.Fatalf("publishing invalid message rows returned %v", problem["code"])
	}
}
