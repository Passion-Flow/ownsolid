package compose_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"testing"
	"time"
)

func exerciseComposeDataset(t *testing.T, client *http.Client, origin, password string, restart func(), verifyProcessLimits func(string)) {
	t.Helper()
	var csrf string
	call := func(method, path, media string, body io.Reader, headers map[string]string, status int, out any) []byte {
		t.Helper()
		request, e := http.NewRequest(method, origin+path, body)
		if e != nil {
			t.Fatal(e)
		}
		if method != "GET" {
			request.Header.Set("Origin", origin)
			request.Header.Set("X-CSRF-Token", csrf)
			request.Header.Set("Content-Type", media)
		}
		for k, v := range headers {
			request.Header.Set(k, v)
		}
		response, e := client.Do(request)
		if e != nil {
			t.Fatal("dataset Compose transport", e)
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if e != nil || response.StatusCode != status {
			var problem struct{ Code string }
			json.Unmarshal(raw, &problem)
			t.Fatalf("dataset Compose %s %s status=%d code=%s expected=%d", method, path, response.StatusCode, problem.Code, status)
		}
		if out != nil && json.Unmarshal(raw, out) != nil {
			t.Fatal("dataset Compose response invalid")
		}
		if response.Header.Get("X-Artifact-SHA256") != "" {
			sum := sha256.Sum256(raw)
			if response.Header.Get("X-Artifact-SHA256") != hex.EncodeToString(sum[:]) {
				t.Fatal("dataset Compose export digest")
			}
		}
		return raw
	}
	encode := func(value any) io.Reader { raw, _ := json.Marshal(value); return bytes.NewReader(raw) }
	refresh := func() {
		var envelope struct{ Data struct{ Token string } }
		call("GET", "/api/v1/auth/csrf", "", nil, nil, 200, &envelope)
		csrf = envelope.Data.Token
		if csrf == "" {
			t.Fatal("dataset Compose CSRF missing")
		}
	}
	refresh()
	call("POST", "/api/v1/auth/login", "application/json", encode(map[string]string{"email": "admin@example.invalid", "password": password}), nil, 200, nil)
	refresh()
	var me struct {
		Data struct{ Workspace struct{ ID string } }
	}
	call("GET", "/api/v1/me", "", nil, nil, 200, &me)
	base := "/api/v1/workspaces/" + me.Data.Workspace.ID + "/datasets"
	var configuration struct{ Data datasets.PublicConfiguration }
	call("GET", base+"/configuration", "", nil, nil, 200, &configuration)
	if !configuration.Data.StorageAvailable {
		t.Fatal("Linux dataset runtime unavailable")
	}
	var data struct{ Data datasets.Dataset }
	call("POST", base, "application/json", encode(map[string]any{"name": "Actual Linux CPU corpus", "origin": "Synthetic isolated Compose fixture", "license": "Synthetic CC0", "rights_asserted": true, "reason": "Actual Linux data worker"}), map[string]string{"Idempotency-Key": uuid.NewString()}, 201, &data)
	path := base + "/" + data.Data.ID
	raw := []byte("{\"source_id\":\"A\",\"prompt\":\"duplicate\",\"completion\":\"answer\"}\n{\"source_id\":\"B\",\"prompt\":\"duplicate\",\"completion\":\"answer\"}\n{\"source_id\":\"C\",\"prompt\":\"independent\",\"completion\":\"second\"}\n")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	var draft struct{ Data datasets.Draft }
	call("POST", path+"/drafts", "application/json", encode(datasets.DraftRequest{SourceName: "fixture.jsonl", ExpectedBytes: int64(len(raw)), SourceSHA256: digest, Reason: "Actual immutable Linux source"}), map[string]string{"Idempotency-Key": uuid.NewString()}, 201, &draft)
	draftPath := path + "/drafts/" + draft.Data.ID
	call("POST", draftPath+"/source", "application/x-ndjson", bytes.NewReader(raw), map[string]string{"X-Content-SHA256": digest}, 202, &draft)
	wait := func(expected string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			call("GET", draftPath, "", nil, nil, 200, &draft)
			if draft.Data.State == expected {
				return
			}
			if draft.Data.State == "failed" {
				t.Fatalf("actual Linux processor failed: %+v", draft.Data.ErrorCode)
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("dataset Linux job timeout")
	}
	wait("editing")
	var job struct{ Data datasets.Job }
	call("POST", draftPath+"/publications", "application/json", encode(datasets.PublicationRequest{ExpectedRevision: draft.Data.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Actual Linux immutable publication"}), map[string]string{"Idempotency-Key": uuid.NewString()}, 202, &job)
	wait("published")
	verifyProcessLimits(job.Data.ID)
	if job.Data.VersionID == nil {
		t.Fatal("Linux publication ID missing")
	}
	versionPath := path + "/versions/" + *job.Data.VersionID
	var version struct{ Data datasets.Version }
	call("GET", versionPath, "", nil, nil, 200, &version)
	if version.Data.Manifest.UniqueRows != 2 || version.Data.Manifest.DuplicateRows != 1 || version.Data.Manifest.SourceGroups != 2 {
		t.Fatal("Linux split/dedup counts")
	}
	before := call("GET", versionPath+"/files/train", "", nil, nil, 200, nil)
	original := version.Data
	call("POST", versionPath+"/drafts", "application/json", encode(map[string]string{"reason": "Clone complete Linux effective source"}), map[string]string{"Idempotency-Key": uuid.NewString()}, 202, &draft)
	draftPath = path + "/drafts/" + draft.Data.ID
	wait("editing")
	if draft.Data.TotalRows == nil || *draft.Data.TotalRows != 3 || draft.Data.SourceSHA256 != original.EffectiveSourceSHA256 {
		t.Fatal("Linux clone lost pre-dedup source")
	}
	call("POST", draftPath+"/publications", "application/json", encode(datasets.PublicationRequest{ExpectedRevision: draft.Data.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Republish preserved Linux source grouping"}), map[string]string{"Idempotency-Key": uuid.NewString()}, 202, &job)
	wait("published")
	call("GET", path+"/versions/"+*job.Data.VersionID, "", nil, nil, 200, &version)
	if version.Data.Ordinal != 2 || version.Data.ParentVersionID == nil || *version.Data.ParentVersionID != original.ID || version.Data.Manifest.SourceGroups != original.Manifest.SourceGroups || version.Data.Manifest.DuplicateRows != original.Manifest.DuplicateRows {
		t.Fatal("Linux cloned publication lineage")
	}

	restart()
	after := call("GET", versionPath+"/files/train", "", nil, nil, 200, nil)
	if !bytes.Equal(before, after) {
		t.Fatal("Linux restart changed immutable files")
	}
	t.Log("Actual declared Docker Desktop Linux arm64 control/Go/PostgreSQL/native CPython worker, restricted runtime-role COPY/DML, private source/version volume, exact dedup/grouped split/export and restart passed. Linux host bind/physical architecture qualification remains separate.")
}
