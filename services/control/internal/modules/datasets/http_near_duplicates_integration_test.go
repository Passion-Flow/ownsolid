//go:build dataset_processor_integration

package datasets_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

func verifyNearPromptHTTPSPublication(t *testing.T, service *datasets.Service, portal, admin *httpfixture.Client, path, adminPath string) {
	t.Helper()
	// Two successive spelling changes produce a transitive leakage edge.
	base := "Before scheduling any planned maintenance, verify the configured source and store the new temporary reference in the independent workspace archive."
	typo := strings.Replace(base, "configured", "configurad", 1)
	drift := strings.Replace(typo, "temporary", "temporery", 1)
	var raw bytes.Buffer
	rows := []wire.Record{
		{SourceID: "a", Prompt: base, Completion: "ready"},
		{SourceID: "b", Prompt: typo, Completion: "ready"},
		{SourceID: "c", Prompt: typo, Completion: "ready"},
		{SourceID: "d", Prompt: base, Completion: "denied"},
		{SourceID: "e", Prompt: drift, Completion: "ready"},
		{SourceID: "f", Prompt: "yes", Completion: "ready"},
		{SourceID: "g", Prompt: "yet", Completion: "ready"},
		{SourceID: "a", Prompt: "Name the largest ocean", Completion: "Pacific."},
	}
	encoder := json.NewEncoder(&raw)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	draft := prepareHTTPSPublicationDraft(t, service, portal, path, raw.Bytes())
	input := map[string]any{
		"expected_revision": draft["revision"], "seed": "00112233445566778899aabbccddeeff",
		"validation_basis_points": 5000, "test_basis_points": 4000, "reason": "Publish with an explicit deduplication policy",
	}
	publication := adminPath + "/drafts/" + draft["id"].(string) + "/publications"
	admin.Headers["Idempotency-Key"] = uuid.NewString()
	required, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimSuffix(string(required), "}")
	for _, suffix := range []string{
		`,"deduplication":""`, `,"deduplication":null`, `,"deduplication":"exact"`, `,"deduplication":"unrecognized"`,
		`,"deduplication":1`, `,"deduplication":"near_prompt_v1","deduplication":"near_prompt_v1"`,
	} {
		problem := admin.Write("POST", publication, []byte(prefix+suffix+"}"), 400, "Problem")
		if problem["code"] != "VALIDATION_FAILED" {
			t.Fatalf("invalid deduplication strategy returned %v", problem["code"])
		}
	}
	input["deduplication"] = datasets.NearPromptV1
	job := admin.Write("POST", publication, input, 202, "DatasetJobResponse")
	replay := admin.Write("POST", publication, input, 202, "DatasetJobResponse")
	if replay["id"] != job["id"] || replay["version_id"] != job["version_id"] {
		t.Fatal("deduplication replay changed the job or version")
	}
	delete(input, "deduplication")
	problem := admin.Write("POST", publication, input, 409, "Problem")
	if problem["code"] != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("replacing near deduplication with the default returned %v", problem["code"])
	}
	retry := retryHTTPSDatasetJobAfterProcessorExit(t, service, portal, path, job)
	nearPath := path + "/versions/" + retry["version_id"].(string)
	near := portal.Call("GET", nearPath, nil, 200, "DatasetVersionResponse")
	nearManifest := readHTTPSDatasetManifest(t, near["manifest"])
	checkHTTPSDeduplicationManifest(t, nearManifest, 2)
	_, nearSplits, nearRows := checkHTTPSPartitionAssets(t, portal, nearPath, nearManifest)
	checkHTTPSNearLeakage(t, nearSplits, nearRows)
	if nearRows["b"] != 0 || nearRows["c"] != 0 {
		t.Fatalf("near duplicates survived publication: %v", nearRows)
	}
	checkHTTPSDuplicateAsset(t, portal, nearPath, nearManifest, datasets.NearPromptV1)

	// Clone the same effective rows and exercise the omitted strategy.
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	clone := portal.Write("POST", nearPath+"/drafts", map[string]any{"reason": "Compare the default exact policy"}, 202, "DatasetDraftResponse")
	if worked, err := service.ProcessNext(t.Context()); err != nil || !worked {
		t.Fatal("clone HTTPS deduplication source", err)
	}
	clonePath := path + "/drafts/" + clone["id"].(string)
	clone = portal.Call("GET", clonePath, nil, 200, "DatasetDraftResponse")
	input["expected_revision"] = clone["revision"]
	portal.Headers["Idempotency-Key"] = uuid.NewString()
	exactJob := portal.Write("POST", clonePath+"/publications", input, 202, "DatasetJobResponse")
	if worked, err := service.ProcessNext(t.Context()); err != nil || !worked {
		t.Fatal("publish the default deduplication policy", err)
	}
	exactPath := path + "/versions/" + exactJob["version_id"].(string)
	exact := portal.Call("GET", exactPath, nil, 200, "DatasetVersionResponse")
	exactManifest := readHTTPSDatasetManifest(t, exact["manifest"])
	checkHTTPSDeduplicationManifest(t, exactManifest, 1)
	if exact["effective_source_sha256"] != near["effective_source_sha256"] {
		t.Fatal("switching policies changed the effective input")
	}
	_, exactSplits, exactRows := checkHTTPSPartitionAssets(t, portal, exactPath, exactManifest)
	checkHTTPSNearLeakage(t, exactSplits, exactRows)
	if exactRows["b"] != 1 || exactRows["c"] != 0 || exactSplits["b"] != exactSplits["a"] {
		t.Fatalf("default exact policy lost or leaked the spelling variant: %v", exactRows)
	}
	checkHTTPSDuplicateAsset(t, portal, exactPath, exactManifest, "")
}

func checkHTTPSDeduplicationManifest(t *testing.T, manifest wire.Manifest, duplicates int64) {
	t.Helper()
	if manifest.SchemaVersion != 2 || manifest.TestBasisPoints != 4000 || manifest.ValidationBasisPoints != 5000 ||
		manifest.TotalRows != 8 || manifest.UniqueRows != 8-duplicates || manifest.DuplicateRows != duplicates ||
		manifest.SourceGroups != 3 || manifest.ValidationGroups != 1 || manifest.TestGroups != 1 {
		t.Fatalf("unexpected deduplication manifest: %+v", manifest)
	}
}

func checkHTTPSNearLeakage(t *testing.T, splits map[string]string, rows map[string]int) {
	t.Helper()
	if rows["a"] != 2 || rows["d"] != 1 || rows["e"] != 1 || rows["f"] != 1 || rows["g"] != 1 ||
		splits["a"] != splits["d"] || splits["a"] != splits["e"] {
		t.Fatalf("near leakage component did not stay together: %v, %v", splits, rows)
	}
}

func checkHTTPSDuplicateAsset(t *testing.T, client *httpfixture.Client, versionPath string, manifest wire.Manifest, rule string) {
	t.Helper()
	raw, _ := readHTTPSDatasetAsset(t, client, versionPath, "duplicates", manifest)
	decoder := json.NewDecoder(strings.NewReader(raw))
	var count int64
	for {
		var entry struct {
			MatchRule *string `json:"match_rule"`
		}
		if err := decoder.Decode(&entry); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if rule == "" && entry.MatchRule != nil || rule != "" && (entry.MatchRule == nil || *entry.MatchRule != rule) {
			t.Fatalf("unexpected duplicate rule: %v", entry.MatchRule)
		}
		count++
	}
	if count != manifest.DuplicateRows {
		t.Fatalf("duplicate export has %d rows, manifest has %d", count, manifest.DuplicateRows)
	}
}
