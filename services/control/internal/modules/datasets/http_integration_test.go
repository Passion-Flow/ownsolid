//go:build dataset_processor_integration

package datasets_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"runtime"
	"strings"
	"testing"
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
	portal.Call("GET", versionPath, nil, 200, "DatasetVersionResponse")
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
}
