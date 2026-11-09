//go:build dataset_processor_integration

package datasets_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

func TestActualDatasetTLSContractsLargeUnicodeRowsCSRFExportAndReadiness(t *testing.T) {
	ctx := context.Background()
	account := fixture.New(t)
	password := fixture.Bootstrap(t, account)
	auth := fixture.Login(t, account, "admin@example.invalid", password, "admin")
	root := t.TempDir()
	os.Chmod(root, 0700)
	if e := datasets.InitializeStore(root, auth.Me.Workspace.TenantID, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	store, e := datasets.OpenStore(root, auth.Me.Workspace.TenantID, 4096)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	_, filename, _, _ := runtime.Caller(0)
	script := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../../runtimes/data-processing/process.py"))
	binary := os.Getenv("PROJECT_ONE_DATA_PYTHON")
	if binary == "" {
		t.Fatal("actual pinned native CPython required")
	}
	processor, e := datasets.NewProcessor(config.DatasetConfiguration{ArtifactRoot: root, PythonExecutable: binary, ProcessorFile: script}, config.DefaultDatasetPolicy())
	if e != nil {
		t.Fatal(e)
	}
	account.Config.Datasets = config.DatasetConfiguration{ArtifactRoot: root, PythonExecutable: binary, ProcessorFile: script}
	if account.Config.Models.ArtifactRoot != "" {
		if e = models.InitializeStore(account.Config.Models.ArtifactRoot, os.Geteuid(), os.Getegid()); e != nil {
			t.Fatal(e)
		}
	}
	handler := identityhttp.New(account)
	if handler.Notifications != nil {
		if e = handler.Notifications.Service.Start(ctx); e != nil {
			t.Fatal("start actual notification maintenance", e)
		}
		defer func() {
			stop, cancel := context.WithTimeout(context.Background(), config.Seconds(account.Config.Policy().Portal.CommitSeconds))
			defer cancel()
			if e := handler.Notifications.Service.Stop(stop); e != nil {
				t.Error("stop actual notification maintenance", e)
			}
		}()
	}
	handler.Datasets.Service.Store = store
	handler.Datasets.Service.Processor = processor
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/datasets/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	portal := httpfixture.New(t, server, "portal", schemas)
	for _, c := range []*httpfixture.Client{admin, portal} {
		c.Write("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, 200, "")
	}
	workspace := auth.Me.Workspace.ID
	base := "/api/v1/workspaces/" + workspace + "/datasets"
	adminBase := "/api/v1/admin/datasets"
	portal.Call("GET", base+"/configuration", nil, 200, "DatasetConfigurationResponse")
	admin.Call("GET", adminBase+"/configuration?workspace_id="+workspace, nil, 200, "DatasetConfigurationResponse")
	portal.Call("GET", adminBase+"?workspace_id="+workspace, nil, 404, "Problem")
	input := map[string]any{"name": "HTTP actual corpus", "origin": "Synthetic isolated local source", "license": "Synthetic CC0", "rights_asserted": true, "reason": "Actual HTTP acceptance"}
	portal.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	created := portal.Write("POST", base, input, 201, "DatasetResponse")
	dataset := created["id"].(string)
	path := base + "/" + dataset
	adminPath := adminBase + "/" + dataset
	portal.Write("POST", base, input, 201, "DatasetResponse")
	admin.Call("GET", adminBase+"?workspace_id="+workspace+"&limit=1", nil, 200, "DatasetPageResponse")
	admin.Call("GET", adminPath, nil, 200, "DatasetResponse")
	for _, body := range [][]byte{[]byte(`{"Name":"bad","origin":"x","license":"x","rights_asserted":true,"reason":"x"}`), []byte(`{"name":"bad","origin":"x","license":"x","rights_asserted":true,"reason":"\ud800"}`), []byte(`{"name":"bad","origin":"x","license":"x","rights_asserted":true,"reason":null}`), []byte(`{"name":"bad","origin":"x","license":"x","rights_asserted":true,"reason":"x","workspace_id":"foreign"}`)} {
		portal.Write("POST", base, body, 400, "Problem")
	}
	raw := []byte("{\"source_id\":\"a\",\"prompt\":\"Hello \u4e2d\u6587\",\"completion\":\"First\"}\n{bad}\n{\"source_id\":\"b\",\"prompt\":\"Other\",\"completion\":\"Second\"}\n")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	draft := portal.Write("POST", path+"/drafts", map[string]any{"source_name": "fixture.jsonl", "expected_bytes": len(raw), "source_sha256": digest, "reason": "Actual bounded HTTP source"}, 201, "DatasetDraftResponse")
	draftID := draft["id"].(string)
	draftPath := path + "/drafts/" + draftID
	if draft["total_rows"] != nil {
		t.Fatal("unvalidated counts misrepresented")
	}
	portal.Headers["Content-Type"] = "application/x-ndjson"
	portal.Headers["X-Content-SHA256"] = digest
	portal.Write("POST", draftPath+"/source", raw, 202, "DatasetDraftResponse")
	delete(portal.Headers, "Content-Type")
	delete(portal.Headers, "X-Content-SHA256")
	if worked, e := handler.Datasets.Service.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual HTTP source processor", e)
	}
	rows := portal.Call("GET", draftPath+"/rows?limit=2", nil, 200, "DatasetRowPageResponse")
	items := rows["items"].([]any)
	invalid := items[1].(map[string]any)
	draft = portal.Call("GET", draftPath, nil, 200, "DatasetDraftResponse")
	if invalid["line_number"] != float64(2) || invalid["error_code"] == nil || draft["invalid_rows"] != float64(1) {
		t.Fatal("line validation presentation")
	}
	body := map[string]any{"expected_revision": draft["revision"], "row_revision": invalid["revision"], "source_id": "c", "prompt": strings.Repeat("p", 12000), "completion": strings.Repeat("c", 12000), "reason": "Repair with an actual body larger than identity envelope"}
	portal.Write("PATCH", draftPath+"/rows/"+invalid["id"].(string), body, 200, "DatasetRowResponse")
	draft = portal.Call("GET", draftPath, nil, 200, "DatasetDraftResponse")
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	job := portal.Write("POST", draftPath+"/publications", map[string]any{"expected_revision": draft["revision"], "seed": "00112233445566778899aabbccddeeff", "validation_basis_points": 2000, "reason": "Publish actual HTTP dataset"}, 202, "DatasetJobResponse")
	if worked, e := handler.Datasets.Service.ProcessNext(ctx); e != nil || !worked {
		t.Fatal("actual HTTP publication", e)
	}
	portal.Call("GET", path+"/jobs/"+job["id"].(string), nil, 200, "DatasetJobResponse")
	versionID := job["version_id"].(string)
	versionPath := path + "/versions/" + versionID
	legacy := portal.Call("GET", versionPath, nil, 200, "DatasetVersionResponse")["manifest"].(map[string]any)
	if legacy["schema_version"] != float64(1) || legacy["test_basis_points"] != nil {
		t.Fatal("omitting the test ratio changed the legacy manifest")
	}
	portal.Call("GET", versionPath+"/files/test", nil, 409, "Problem")
	portal.Call("GET", path+"/versions", nil, 200, "DatasetVersionPageResponse")
	exported := portal.Call("GET", versionPath+"/files/train", nil, 200, "dataset-file")
	if exported["text"] == "" {
		t.Fatal("empty training export")
	}
	portal.Call("GET", "/health/ready", nil, 200, "")
	var attempt string
	if e = account.DB.QueryRow(ctx, "SELECT attempt_id::text FROM dataset_versions WHERE id=$1", versionID).Scan(&attempt); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(root, "versions", versionID, attempt, "train.jsonl")
	original, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, []byte("corruption"), 0600); e != nil {
		t.Fatal(e)
	}
	portal.Call("GET", versionPath+"/files/train", nil, 409, "Problem")
	portal.Call("GET", "/health/ready", nil, 503, "Problem")
	if e = os.WriteFile(file, original, 0600); e != nil {
		t.Fatal(e)
	}
	portal.Call("GET", "/health/ready", nil, 200, "")
	csrf := portal.CSRF
	portal.CSRF = ""
	portal.Call("POST", versionPath+"/retire", map[string]any{"reason": "Missing CSRF"}, 403, "Problem")
	portal.CSRF = csrf
	portal.Write("POST", versionPath+"/retire", map[string]any{"reason": "Retain immutable history"}, 200, "DatasetVersionResponse")
	admin.Call("GET", adminPath+"/versions/"+versionID, nil, 200, "DatasetVersionResponse")
	verifyThreeWayHTTPSPublication(t, handler.Datasets.Service, portal, admin, path, adminPath)
	verifyNearPromptHTTPSPublication(t, handler.Datasets.Service, portal, admin, path, adminPath)
}

func prepareHTTPSPublicationDraft(t *testing.T, service *datasets.Service, client *httpfixture.Client, path string, raw []byte) map[string]any {
	t.Helper()
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	client.Headers["Idempotency-Key"] = uuid.NewString()
	draft := client.Write("POST", path+"/drafts", map[string]any{
		"source_name": "splits.jsonl", "expected_bytes": len(raw), "source_sha256": digest, "reason": "Validate split source",
	}, 201, "DatasetDraftResponse")
	draftPath := path + "/drafts/" + draft["id"].(string)
	client.Headers["Content-Type"] = "application/x-ndjson"
	client.Headers["X-Content-SHA256"] = digest
	client.Write("POST", draftPath+"/source", raw, 202, "DatasetDraftResponse")
	delete(client.Headers, "Content-Type")
	delete(client.Headers, "X-Content-SHA256")
	if worked, err := service.ProcessNext(t.Context()); err != nil || !worked {
		t.Fatal("inspect HTTPS split source", err)
	}
	return client.Call("GET", draftPath, nil, 200, "DatasetDraftResponse")
}

func verifyThreeWayHTTPSPublication(t *testing.T, service *datasets.Service, portal, admin *httpfixture.Client, path, adminPath string) {
	t.Helper()
	raw := []byte(`{"source_id":"a","prompt":"Identify the prime numbers","completion":"Two and three."}
{"source_id":"a","prompt":"Explain why trees lose their leaves","completion":"Day length changes."}
{"source_id":"b","prompt":"Translate a greeting into Spanish","completion":"Hola."}
{"source_id":"c","prompt":"Name the largest ocean","completion":"Pacific."}
`)
	draft := prepareHTTPSPublicationDraft(t, service, portal, path, raw)
	input := map[string]any{
		"expected_revision": draft["revision"], "seed": "00112233445566778899aabbccddeeff",
		"validation_basis_points": 5000, "reason": "Reserve independent test groups",
	}
	publication := adminPath + "/drafts/" + draft["id"].(string) + "/publications"
	admin.Headers = map[string]string{"Idempotency-Key": uuid.NewString()}
	required, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(string(required), "}")
	for _, suffix := range []string{
		`,"test_basis_points":0`, `,"test_basis_points":null`, `,"test_basis_points":99`, `,"test_basis_points":5001`,
		`,"test_basis_points":4001`, `,"test_basis_points":-100`, `,"test_basis_points":100.5`, `,"test_basis_points":"100"`,
		`,"test_basis_points":100,"test_basis_points":100`, `,"Test_basis_points":100`,
	} {
		problem := admin.Write("POST", publication, []byte(prefix+suffix+"}"), 400, "Problem")
		if problem["code"] != "VALIDATION_FAILED" {
			t.Fatalf("invalid split input returned %v", problem["code"])
		}
	}
	input["test_basis_points"] = 4000
	job := admin.Write("POST", publication, input, 202, "DatasetJobResponse")
	replay := admin.Write("POST", publication, input, 202, "DatasetJobResponse")
	if replay["id"] != job["id"] || replay["version_id"] != job["version_id"] {
		t.Fatal("publication replay changed the job or version")
	}
	input["test_basis_points"] = 3999
	problem := admin.Write("POST", publication, input, 409, "Problem")
	if problem["code"] != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed test ratio returned %v", problem["code"])
	}
	delete(input, "test_basis_points")
	problem = admin.Write("POST", publication, input, 409, "Problem")
	if problem["code"] != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("omitting the queued test ratio returned %v", problem["code"])
	}

	retry := retryHTTPSPublicationAfterProcessorExit(t, service, portal, path, job)
	versionID := retry["version_id"].(string)
	versionPath := path + "/versions/" + versionID
	version := portal.Call("GET", versionPath, nil, 200, "DatasetVersionResponse")
	admin.Call("GET", adminPath+"/versions/"+versionID, nil, 200, "DatasetVersionResponse")
	portal.Call("GET", path+"/versions", nil, 200, "DatasetVersionPageResponse")
	manifest := readHTTPSDatasetManifest(t, version["manifest"])
	if manifest.SchemaVersion != 2 || manifest.TestBasisPoints != 4000 || manifest.ValidationBasisPoints != 5000 ||
		manifest.SourceGroups != 3 || manifest.ValidationGroups != 1 || manifest.TestGroups != 1 || manifest.UniqueRows != 4 {
		t.Fatalf("unexpected three-way manifest: %+v", manifest)
	}
	testDigest, sourceSplits, sourceRows := checkHTTPSPartitionAssets(t, portal, versionPath, manifest)
	if len(sourceSplits) != 3 || sourceRows["a"] != 2 || sourceRows["b"] != 1 || sourceRows["c"] != 1 {
		t.Fatalf("unexpected source counts: %v", sourceRows)
	}
	adminFile := admin.Call("GET", adminPath+"/versions/"+versionID+"/files/test", nil, 200, "dataset-file")
	if adminFile["sha256"] != testDigest {
		t.Fatal("Admin and Portal returned different test files")
	}
	foreignPath := "/api/v1/workspaces/" + uuid.NewString() + "/datasets/" + version["dataset_id"].(string)
	portal.Call("GET", foreignPath+"/versions/"+versionID+"/files/test", nil, 404, "Problem")

	tooFew := prepareHTTPSPublicationDraft(t, service, portal, path, []byte(`{"source_id":"a","prompt":"Identify the prime numbers","completion":"Two and three."}
{"source_id":"b","prompt":"Name the largest ocean","completion":"Pacific."}
`))
	input["expected_revision"] = tooFew["revision"]
	input["test_basis_points"] = 100
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	shortJob := portal.Write("POST", path+"/drafts/"+tooFew["id"].(string)+"/publications", input, 202, "DatasetJobResponse")
	if worked, err := service.ProcessNext(t.Context()); err != nil || !worked {
		t.Fatal("process insufficient-groups publication", err)
	}
	shortResult := portal.Call("GET", path+"/jobs/"+shortJob["id"].(string), nil, 200, "DatasetJobResponse")
	if shortResult["state"] != "failed" || shortResult["error_code"] != "DATASET_SPLIT_INSUFFICIENT_GROUPS" || shortResult["retryable"] != false {
		t.Fatalf("insufficient groups produced an unexpected result: %v", shortResult["error_code"])
	}
	portal.Call("GET", path+"/versions/"+shortJob["version_id"].(string), nil, 404, "Problem")
}

func retryHTTPSPublicationAfterProcessorExit(t *testing.T, service *datasets.Service, portal *httpfixture.Client, path string, job map[string]any) map[string]any {
	t.Helper()
	// Fail one worker execution after the HTTPS request has committed the job.
	configured := service.Identity.Config.Datasets
	configured.ProcessorFile = filepath.Join(t.TempDir(), "fail.py")
	if err := os.WriteFile(configured.ProcessorFile, []byte("raise SystemExit(9)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	failing, err := datasets.NewProcessor(configured, service.Identity.Config.Policy().Datasets)
	if err != nil {
		t.Fatal(err)
	}
	actual := service.Processor
	service.Processor = failing
	worked, err := service.ProcessNext(t.Context())
	service.Processor = actual
	if err != nil || !worked {
		t.Fatal("run interrupted publication", err)
	}
	failedPath := path + "/jobs/" + job["id"].(string)
	failed := portal.Call("GET", failedPath, nil, 200, "DatasetJobResponse")
	if failed["state"] != "failed" || failed["retryable"] != true {
		t.Fatalf("worker failure did not leave a retryable job: %v", failed["error_code"])
	}
	portal.Call("GET", path+"/versions/"+job["version_id"].(string), nil, 404, "Problem")

	portal.Headers["Idempotency-Key"] = uuid.NewString()
	retryInput := map[string]any{"reason": "Retry the frozen publication"}
	retry := portal.Write("POST", failedPath+"/retry", retryInput, 202, "DatasetJobResponse")
	retryReplay := portal.Write("POST", failedPath+"/retry", retryInput, 202, "DatasetJobResponse")
	if retry["id"] == job["id"] || retryReplay["id"] != retry["id"] || retry["version_id"] == job["version_id"] {
		t.Fatal("retry did not create and replay one new job")
	}
	var retryOf string
	if err := service.DB.QueryRow(t.Context(), "UPDATE dataset_jobs SET not_before=clock_timestamp() WHERE id=$1 RETURNING retry_of::text", retry["id"]).Scan(&retryOf); err != nil || retryOf != job["id"] {
		t.Fatal("retry lost its original job", err)
	}
	if worked, err := service.ProcessNext(t.Context()); err != nil || !worked {
		t.Fatal("process retried HTTPS publication", err)
	}
	completed := portal.Call("GET", path+"/jobs/"+retry["id"].(string), nil, 200, "DatasetJobResponse")
	if completed["state"] != "succeeded" {
		t.Fatalf("retried publication failed: %v", completed["error_code"])
	}
	return retry
}

func readHTTPSDatasetManifest(t *testing.T, value any) wire.Manifest {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var manifest wire.Manifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func readHTTPSDatasetAsset(t *testing.T, client *httpfixture.Client, versionPath, name string, manifest wire.Manifest) (string, string) {
	t.Helper()
	file := client.Call("GET", versionPath+"/files/"+name, nil, 200, "dataset-file")
	raw, digest := file["text"].(string), file["sha256"].(string)
	for _, asset := range manifest.Files {
		if asset.Path == name+".jsonl" && asset.SHA256 == digest && asset.SizeBytes == int64(len(raw)) {
			return raw, digest
		}
	}
	t.Fatalf("%s export differs from its manifest digest or length", name)
	return "", ""
}

func checkHTTPSPartitionAssets(t *testing.T, client *httpfixture.Client, versionPath string, manifest wire.Manifest) (string, map[string]string, map[string]int) {
	t.Helper()
	sourceSplits := make(map[string]string)
	sourceRows := make(map[string]int)
	var testDigest string
	for _, partition := range []struct {
		name string
		rows int64
	}{{"train", manifest.TrainRows}, {"validation", manifest.ValidationRows}, {"test", manifest.TestRows}} {
		raw, digest := readHTTPSDatasetAsset(t, client, versionPath, partition.name, manifest)
		if partition.name == "test" {
			testDigest = digest
		}
		decoder := json.NewDecoder(strings.NewReader(raw))
		var rows int64
		for {
			var row wire.Record
			err := decoder.Decode(&row)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if prior := sourceSplits[row.SourceID]; prior != "" && prior != partition.name {
				t.Fatalf("source %s crossed partitions", row.SourceID)
			}
			sourceSplits[row.SourceID] = partition.name
			sourceRows[row.SourceID]++
			rows++
		}
		if rows == 0 || rows != partition.rows {
			t.Fatalf("%s partition has %d rows, manifest has %d", partition.name, rows, partition.rows)
		}
	}
	return testDigest, sourceSplits, sourceRows
}
